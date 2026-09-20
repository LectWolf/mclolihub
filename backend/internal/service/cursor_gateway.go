package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursorproxy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// CursorGatewayService reverse-proxies OpenAI Chat Completions onto two
// distinct Cursor backends:
//
//   - cursor_sand: InferenceService/Stream + SAND_INFERENCE_RENEWAL_CREDENTIAL
//     (Grok Bot / sand quota). client-type=sand.
//   - cursor: InferenceService/RunInference + Cursor CLI login session JWT
//     (Cursor IDE quota). client-type=ide.
type CursorGatewayService struct {
	tokens *cursorproxy.TokenCache
	client *http.Client
	h2     *http.Client
}

func NewCursorGatewayService() *CursorGatewayService {
	return &CursorGatewayService{
		tokens: cursorproxy.NewTokenCache(),
		client: &http.Client{Timeout: 3 * time.Minute},
		h2:     nil, // lazily built inside cursorproxy.RunInference
	}
}

func shouldUseCursorProxy(account *Account) bool {
	return account != nil && (account.IsCursorSand() || account.IsCursor())
}

func (s *CursorGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	_ *ParsedRequest,
) (*ForwardResult, error) {
	if account == nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "account is required")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}
	stream := gjson.GetBytes(body, "stream").Bool()
	messages, err := parseCursorChatMessages(body)
	if err != nil {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	if len(messages) == 0 {
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "messages required")
	}

	switch {
	case account.IsCursorSand():
		return s.forwardSand(ctx, c, account, model, messages, stream)
	case account.IsCursor():
		return s.forwardIDE(ctx, c, account, model, messages, stream)
	default:
		return nil, s.writeError(c, http.StatusBadRequest, "invalid_request_error", "unsupported cursor platform")
	}
}

func (s *CursorGatewayService) forwardSand(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	model string,
	messages []cursorproxy.ChatMessage,
	stream bool,
) (*ForwardResult, error) {
	creds := parseSandCredentials(account)
	token, err := cursorproxy.ResolveSandToken(ctx, s.client, creds, s.tokens)
	if err != nil {
		return nil, s.writeError(c, http.StatusUnauthorized, "authentication_error", "sand auth failed: "+err.Error())
	}
	payload := cursorproxy.NewStreamPayload(model, messages)
	resp, err := cursorproxy.Stream(ctx, s.client, token, creds, payload, uuid.NewString())
	if err != nil {
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", fmt.Sprintf("Stream HTTP %d: %s", resp.StatusCode, truncateRunes(string(raw), 240)))
	}
	return s.pumpConnectStream(c, resp.Body, model, stream, false)
}

func (s *CursorGatewayService) forwardIDE(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	model string,
	messages []cursorproxy.ChatMessage,
	stream bool,
) (*ForwardResult, error) {
	creds := parseIDECredentials(account)
	pr, pw := io.Pipe()
	conversationID, invocationID := cursorproxy.NewIDs()
	runEnv, err := cursorproxy.Envelope(cursorproxy.RunInferenceRunRequest(
		conversationID, model, cursorproxy.ConcatUserText(messages),
	))
	if err != nil {
		_ = pr.Close()
		return nil, s.writeError(c, http.StatusInternalServerError, "api_error", err.Error())
	}
	invokeEnv, err := cursorproxy.Envelope(cursorproxy.RunInferenceInvoke(
		invocationID, conversationID, model, cursorproxy.OpenAIMessages(messages),
	))
	if err != nil {
		_ = pr.Close()
		return nil, s.writeError(c, http.StatusInternalServerError, "api_error", err.Error())
	}

	writeErr := make(chan error, 1)
	go func() {
		_, werr := pw.Write(runEnv)
		writeErr <- werr
	}()

	resp, err := cursorproxy.RunInference(ctx, s.h2, creds, pr, uuid.NewString())
	if err != nil {
		_ = pw.Close()
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", err.Error())
	}
	defer resp.Body.Close()
	if werr := <-writeErr; werr != nil {
		_ = pw.Close()
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", werr.Error())
	}

	leftover, usedModel, readyErr := readUntilRunReady(resp.Body, 30*time.Second)
	if readyErr != nil {
		_ = pw.Close()
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", readyErr.Error())
	}
	if usedModel == "" {
		usedModel = model
	}
	if _, werr := pw.Write(invokeEnv); werr != nil {
		_ = pw.Close()
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", werr.Error())
	}
	go func() {
		<-ctx.Done()
		_ = pw.Close()
	}()
	body := io.MultiReader(bytes.NewReader(leftover), resp.Body)
	return s.pumpConnectStream(c, body, usedModel, stream, true)
}

func readUntilRunReady(body io.Reader, timeout time.Duration) (leftover []byte, model string, err error) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 8192)
	for time.Now().Before(deadline) {
		n, rerr := body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			frames, rest, derr := cursorproxy.DecodeFrames(buf)
			if derr != nil {
				return nil, "", derr
			}
			buf = rest
			var pending []map[string]any
			sawReady := false
			readyModel := ""
			for _, frame := range frames {
				if sawReady {
					pending = append(pending, frame)
					continue
				}
				if code, msg := cursorproxy.FrameError(frame); msg != "" || code != "" {
					return nil, "", errors.New(strings.TrimSpace(code + " " + msg))
				}
				if m, ok := cursorproxy.FrameRunReady(frame); ok {
					sawReady = true
					readyModel = m
					continue
				}
			}
			if sawReady {
				extra, eerr := cursorproxy.EncodeFrames(pending)
				if eerr != nil {
					return nil, "", eerr
				}
				return append(extra, rest...), readyModel, nil
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil, "", errors.New("RunInference closed before run_ready")
			}
			return nil, "", rerr
		}
	}
	return nil, "", errors.New("RunInference timed out waiting for run_ready")
}

func (s *CursorGatewayService) pumpConnectStream(
	c *gin.Context,
	body io.Reader,
	model string,
	stream bool,
	stopOnTurnEnd bool,
) (*ForwardResult, error) {
	chatID := "chatcmpl-" + uuid.NewString()[:24]
	created := time.Now().Unix()
	usedModel := model
	var textBuf strings.Builder
	var lastErr string

	emit := func(delta string, finish string) {
		if !stream {
			return
		}
		chunk := gin.H{
			"id":      chatID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   usedModel,
			"choices": []gin.H{{
				"index": 0,
				"delta": gin.H{"content": delta},
			}},
		}
		if finish != "" {
			chunk["choices"] = []gin.H{{
				"index":         0,
				"delta":         gin.H{},
				"finish_reason": finish,
			}}
		}
		raw, _ := json.Marshal(chunk)
		_, _ = c.Writer.Write([]byte("data: " + string(raw) + "\n\n"))
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	if stream {
		MarkResponseCommitted(c)
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Status(http.StatusOK)
	}

	reader := bufio.NewReader(body)
	var buf []byte
	tmp := make([]byte, 8192)
	for {
		n, rerr := reader.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			var frames []map[string]any
			var err error
			frames, buf, err = cursorproxy.DecodeFrames(buf)
			if err != nil {
				lastErr = err.Error()
				break
			}
			for _, frame := range frames {
				if code, msg := cursorproxy.FrameError(frame); msg != "" || code != "" {
					lastErr = strings.TrimSpace(code + " " + msg)
					continue
				}
				if m := cursorproxy.FrameModel(frame); m != "" {
					usedModel = m
				}
				delta := cursorproxy.FrameText(frame)
				if delta != "" {
					_, _ = textBuf.WriteString(delta)
					emit(delta, "")
				}
				if stopOnTurnEnd && (cursorproxy.FrameTurnEnded(frame) || cursorproxy.FrameInvocationEnded(frame)) {
					rerr = io.EOF
				}
			}
		}
		if rerr != nil {
			if rerr != io.EOF && lastErr == "" {
				lastErr = rerr.Error()
			}
			break
		}
	}

	if lastErr != "" && textBuf.Len() == 0 {
		if stream {
			errPayload, _ := json.Marshal(gin.H{"error": gin.H{"message": lastErr, "type": "api_error"}})
			_, _ = c.Writer.Write([]byte("data: " + string(errPayload) + "\n\n"))
			_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return &ForwardResult{Model: usedModel, Stream: true}, errors.New(lastErr)
		}
		return nil, s.writeError(c, http.StatusBadGateway, "api_error", lastErr)
	}

	if stream {
		emit("", "stop")
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return &ForwardResult{Model: usedModel, Stream: true}, nil
	}

	text := textBuf.String()
	MarkResponseCommitted(c)
	c.JSON(http.StatusOK, gin.H{
		"id":      chatID,
		"object":  "chat.completion",
		"created": created,
		"model":   usedModel,
		"choices": []gin.H{{
			"index": 0,
			"message": gin.H{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": "stop",
		}},
		"usage": gin.H{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
	return &ForwardResult{Model: usedModel, Stream: false}, nil
}

func (s *CursorGatewayService) writeError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    nil,
		},
	})
	return errors.New(message)
}

func parseSandCredentials(account *Account) cursorproxy.SandCredentials {
	token := strings.TrimSpace(account.GetCredential("grok_bot_token"))
	if token == "" {
		token = strings.TrimSpace(account.GetCredential("access_token"))
	}
	return cursorproxy.SandCredentials{
		RenewalCredential: strings.TrimSpace(account.GetCredential("sand_inference_renewal_credential")),
		GrokBotToken:      token,
		MachineID:         strings.TrimSpace(account.GetCredential("machine_id")),
		ClientOS:          strings.TrimSpace(account.GetCredential("client_os")),
	}
}

func parseIDECredentials(account *Account) cursorproxy.IDECredentials {
	token := strings.TrimSpace(account.GetCredential("session_token"))
	if token == "" {
		token = strings.TrimSpace(account.GetCredential("access_token"))
	}
	if token == "" {
		token = strings.TrimSpace(account.GetCredential("cli_access_token"))
	}
	if token == "" {
		token = strings.TrimSpace(account.GetCredential("api_key"))
	}
	return cursorproxy.IDECredentials{
		SessionToken:  token,
		ClientVersion: strings.TrimSpace(account.GetCredential("client_version")),
		MachineID:     strings.TrimSpace(account.GetCredential("machine_id")),
	}
}

func parseCursorChatMessages(body []byte) ([]cursorproxy.ChatMessage, error) {
	arr := gjson.GetBytes(body, "messages")
	if !arr.IsArray() {
		return nil, errors.New("messages must be an array")
	}
	out := make([]cursorproxy.ChatMessage, 0)
	arr.ForEach(func(_, value gjson.Result) bool {
		role := value.Get("role").String()
		content := value.Get("content")
		text := ""
		switch {
		case content.Type == gjson.String:
			text = content.String()
		case content.IsArray():
			var parts []string
			content.ForEach(func(_, part gjson.Result) bool {
				if part.Get("type").String() == "text" || part.Get("text").Exists() {
					parts = append(parts, part.Get("text").String())
				}
				return true
			})
			text = strings.Join(parts, "\n")
		}
		out = append(out, cursorproxy.ChatMessage{Role: role, Content: text})
		return true
	})
	if gjson.GetBytes(body, "system").Type == gjson.String {
		sys := strings.TrimSpace(gjson.GetBytes(body, "system").String())
		if sys != "" {
			out = append([]cursorproxy.ChatMessage{{Role: "system", Content: sys}}, out...)
		}
	}
	return out, nil
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
