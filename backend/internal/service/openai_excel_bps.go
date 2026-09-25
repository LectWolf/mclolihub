package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// excelBPSReplay keeps native tool items so a later turn can replay them.
// It is process-local: a restart or another instance cannot resume an old thread.
var excelBPSReplay basispoints.ReplayCache

func excelBPSAccountID(account *Account, accessToken string) string {
	if accountID := strings.TrimSpace(account.GetChatGPTAccountID()); accountID != "" {
		return accountID
	}
	claims, err := openai.DecodeIDToken(accessToken)
	if err != nil || claims.OpenAIAuth == nil {
		return ""
	}
	return strings.TrimSpace(claims.OpenAIAuth.ChatGPTAccountID)
}

func newExcelBPSRequest(ctx context.Context, body []byte, token, accountID string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = http.Header{
		"Authorization":           {"Bearer " + token},
		"Chatgpt-Account-Id":      {accountID},
		"X-Openai-Account-Id":     {accountID},
		"X-Basispoints-Auth-Mode": {"chatgpt"},
		"Content-Type":            {"application/json"},
		"Accept":                  {"text/event-stream"},
		"Origin":                  {"https://bps.openai.com"},
		"User-Agent":              {"Mozilla/5.0"},
		"X-Openai-Internal-Basispoints-Client-Product":       {"basispoints-excel-plugin"},
		"X-Openai-Internal-Basispoints-Client-Agent-Profile": {"excel"},
	}
	return req, nil
}

// forwardExcelBPS sends one Responses or compact request through bps.openai.com
// using the account's existing ChatGPT access token. It does not inject a Codex
// ticket and does not hand the request to an OAuth transport plugin.
func (s *OpenAIGatewayService) forwardExcelBPS(ctx context.Context, c *gin.Context, account *Account, body []byte, start time.Time) (*OpenAIForwardResult, error) {
	fail := func(status int, code, message string) (*OpenAIForwardResult, error) {
		committed := StopOpenAICompactSSEKeepaliveCommitted(c)
		MarkResponseCommitted(c)
		if committed {
			writeOpenAICompactSSEFailureMessage(c, status, code, message)
		} else if c != nil {
			c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "code": code, "message": message}})
		}
		return nil, fmt.Errorf("excel BPS: %s", code)
	}
	originalModel := gjson.GetBytes(body, "model").String()
	model := account.GetMappedModel(originalModel)
	stream := gjson.GetBytes(body, "stream").Bool()
	var err error
	body, err = sjson.SetBytes(body, "model", model)
	if err != nil {
		return fail(http.StatusBadRequest, "basispoints_request_invalid", "Invalid model request")
	}
	_, threadID := resolveOpenAIWSExecutionScope(c, body, getAPIKeyIDFromContext(c))
	if threadID != "" {
		body, err = sjson.SetBytes(body, "prompt_cache_key", threadID)
		if err != nil {
			return nil, err
		}
	}
	if isOpenAIResponsesCompactPath(c) {
		var request map[string]any
		if err = json.Unmarshal(body, &request); err != nil {
			return fail(http.StatusBadRequest, "basispoints_request_invalid", "Invalid compact request")
		}
		var input []any
		switch v := request["input"].(type) {
		case []any:
			input = v
		case string:
			input = []any{map[string]any{"role": "user", "content": v}}
		default:
			return fail(http.StatusBadRequest, "basispoints_request_invalid", "Compact requires input")
		}
		request["input"] = append(input, map[string]any{"type": "compaction_trigger"})
		request["tool_choice"] = "none"
		body, err = json.Marshal(request)
		if err != nil {
			return nil, err
		}
	}
	scope := fmt.Sprintf("account:%d/key:%d/thread:%s", account.ID, getAPIKeyIDFromContext(c), threadID)
	upstreamBody, bridge, err := basispoints.Prepare(body, scope, &excelBPSReplay)
	if err != nil {
		return fail(http.StatusBadRequest, "basispoints_request_invalid", err.Error())
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return fail(http.StatusBadGateway, "basispoints_auth_unavailable", "Account OAuth credential is unavailable")
	}
	accountID := excelBPSAccountID(account, token)
	if accountID == "" {
		return fail(http.StatusBadRequest, "basispoints_account_id_missing", "Excel BPS requires chatgpt_account_id")
	}
	if s.httpUpstream == nil {
		return fail(http.StatusBadGateway, "basispoints_transport_error", "Excel BPS transport is unavailable")
	}
	requestCtx := WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileLongStream))
	req, err := newExcelBPSRequest(requestCtx, upstreamBody, token, accountID)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	SetActualOpenAIUpstreamEndpoint(c, "/basispoints/api/responses")
	SetOpsUpstreamModel(c, model)
	sent := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(sent).Milliseconds())
	if err != nil {
		return fail(http.StatusBadGateway, "basispoints_transport_error", "Excel BPS connection failed; request was not replayed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
		upstreamMessage := fmt.Sprintf("Excel BPS returned HTTP %d", resp.StatusCode)
		upstreamDetail := ""
		if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
			safeBody := excelBPSSanitizeErrorBody(string(raw), token, account)
			maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail, _ = sanitizeErrorBodyForStorage(safeBody, maxBytes)
			if message := strings.TrimSpace(extractUpstreamErrorMessage([]byte(safeBody))); message != "" {
				upstreamMessage = truncateString(message, 2048)
			}
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMessage, upstreamDetail)
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
			UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"),
			UpstreamURL: basispoints.ResponsesURL, Kind: "http_error",
			Message: upstreamMessage, Detail: upstreamDetail, UpstreamResponseBody: upstreamDetail,
		})
		code := gjson.GetBytes(raw, "error.code").String()
		if code == "basispoints_model_access_changed" {
			return fail(resp.StatusCode, code, "This model is not available on the account's Excel BPS endpoint")
		}
		return fail(resp.StatusCode, "basispoints_upstream_error", "Excel BPS rejected this request; account scheduling was not changed")
	}
	converted := bridge.Stream(resp.Body)
	defer func() { _ = converted.Close() }()
	requestedEffort := coalesceRequestedReasoningEffort(RequestedReasoningEffortFromContext(ctx), &bridge.RequestedEffort)
	result := &OpenAIForwardResult{
		Model: originalModel, UpstreamModel: model, UpstreamEndpoint: "/basispoints/api/responses",
		Stream: stream, ReasoningEffort: &bridge.Effort, RequestedReasoningEffort: requestedEffort,
		RequestID: resp.Header.Get("x-request-id"),
	}
	if stream && c != nil {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
	}
	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(converted)
		scanner.Buffer(make([]byte, 64<<10), 16<<20)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case lines <- scanner.Text():
			}
		}
		if err := scanner.Err(); err != nil {
			readErr <- err
		}
	}()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	keepalive := func() {
		if stream && c != nil && ctx.Err() == nil {
			_, _ = c.Writer.WriteString(": keepalive\n\n")
			c.Writer.Flush()
		}
	}
	var completed []byte
	terminal := ""
readLoop:
	for {
		select {
		case <-ctx.Done():
			result.ClientDisconnect = true
			result.Duration = time.Since(start)
			return result, ctx.Err()
		case <-heartbeat.C:
			keepalive()
		case err, ok := <-readErr:
			if ok && err != nil {
				result.Duration = time.Since(start)
				return excelBPSIncomplete(c, result, stream)
			}
		case line, ok := <-lines:
			if !ok {
				break readLoop
			}
			if strings.HasPrefix(line, "data: ") {
				payload := []byte(strings.TrimPrefix(line, "data: "))
				kind := gjson.GetBytes(payload, "type").String()
				s.parseSSEUsageBytes(payload, &result.Usage)
				if result.FirstTokenMs == nil && (kind == "response.output_text.delta" || kind == "response.output_item.added") {
					ms := int(time.Since(start).Milliseconds())
					result.FirstTokenMs = &ms
				}
				switch kind {
				case "response.completed", "response.failed", "response.incomplete", "error":
					terminal = kind
					completed = []byte(gjson.GetBytes(payload, "response").Raw)
					result.ResponseID = gjson.GetBytes(payload, "response.id").String()
					result.UpstreamResponseModel = gjson.GetBytes(payload, "response.model").String()
				}
			}
			if stream && c != nil {
				if _, err = c.Writer.WriteString(line + "\n"); err != nil {
					result.ClientDisconnect = true
					result.Duration = time.Since(start)
					return result, err
				}
				if line == "" {
					c.Writer.Flush()
				}
			}
		}
	}
	result.Duration = time.Since(start)
	result.UpstreamTerminalEvent = terminal
	select {
	case err := <-readErr:
		if err != nil && ctx.Err() == nil {
			return excelBPSIncomplete(c, result, stream)
		}
	default:
	}
	if terminal == "" {
		if ctx.Err() != nil {
			result.ClientDisconnect = true
			return result, ctx.Err()
		}
		return excelBPSIncomplete(c, result, stream)
	}
	if terminal != "response.completed" {
		MarkResponseCommitted(c)
	}
	if !stream && c != nil {
		if terminal != "response.completed" {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "basispoints_protocol_error", "message": "Excel BPS did not complete the response"}})
		} else {
			c.Data(http.StatusOK, "application/json", completed)
		}
	}
	if terminal != "response.completed" {
		return result, fmt.Errorf("excel BPS terminal: %s", terminal)
	}
	s.bindHTTPResponseAccount(ctx, c, account, result.ResponseID)
	return result, nil
}

func excelBPSIncomplete(c *gin.Context, result *OpenAIForwardResult, stream bool) (*OpenAIForwardResult, error) {
	MarkResponseCommitted(c)
	if stream && c != nil {
		_, _ = c.Writer.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"basispoints_stream_incomplete\",\"message\":\"Upstream stream ended before completion\"}}}\n\n")
		c.Writer.Flush()
	} else if c != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "basispoints_stream_incomplete", "message": "Excel BPS stream ended before completion"}})
	}
	return result, fmt.Errorf("excel BPS stream incomplete")
}

var (
	excelBPSBearerPattern         = regexp.MustCompile(`(?i)\bBearer\s+\S+`)
	excelBPSURLCredentialsPattern = regexp.MustCompile(`(https?://)[^/\s@]+@`)
)

func excelBPSSanitizeErrorBody(raw, token string, account *Account) string {
	if !json.Valid([]byte(raw)) {
		return ""
	}
	secrets := append([]string{token}, excelBPSAccountSecrets(account)...)
	fields := make(map[string]string)
	for _, key := range []string{"message", "code", "type", "param"} {
		value := gjson.Get(raw, "error."+key)
		if value.Type != gjson.String {
			continue
		}
		clean := value.String()
		for _, secret := range secrets {
			if secret != "" {
				clean = strings.ReplaceAll(clean, secret, "[redacted]")
			}
		}
		clean = excelBPSBearerPattern.ReplaceAllString(clean, "Bearer [redacted]")
		clean = excelBPSURLCredentialsPattern.ReplaceAllString(clean, "${1}[redacted]@")
		clean = sanitizeUpstreamErrorMessage(clean)
		fields[key] = truncateString(logredact.RedactText(clean, "authorization", "api_key", "apikey", "token", "secret", "key", "cookie", "ticket", "recovery_ticket"), 2048)
	}
	encoded, _ := json.Marshal(map[string]any{"error": fields})
	return string(encoded)
}

func excelBPSAccountSecrets(account *Account) []string {
	if account == nil {
		return nil
	}
	var secrets []string
	for _, key := range []string{"access_token", "refresh_token", "id_token", "api_key", "session_key", "cookie"} {
		if value := account.GetCredential(key); value != "" {
			secrets = append(secrets, value)
		}
	}
	if account.Proxy != nil && account.Proxy.Password != "" {
		secrets = append(secrets, account.Proxy.Password)
	}
	return secrets
}
