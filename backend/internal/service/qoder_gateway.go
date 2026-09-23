package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoderproxy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// qoderTokenRefreshSkew refreshes device tokens this long before they expire.
	qoderTokenRefreshSkew = 5 * time.Minute
	// qoderRefreshLockWait bounds how long a request waits for another worker
	// that holds the device-token refresh lock.
	qoderRefreshLockWait = 3 * time.Second

	// QoderCredentialRejectedReason marks failovers caused by a Qoder
	// credential that the upstream refused (revoked PAT, dead refresh token).
	QoderCredentialRejectedReason GatewayFailureReason = "qoder_credential_rejected"
	// QoderCredentialRejectedClientMessage is safe to show to API clients.
	QoderCredentialRejectedClientMessage = "Qoder rejected the account credential; sign in again or update the personal access token"
)

var (
	errQoderNoCredential       = errors.New("qoder account has no personal access token or device login")
	errQoderDeviceLoginExpired = errors.New("qoder device token expired and no refresh token is stored")
	errQoderRefreshInProgress  = errors.New("qoder device token refresh is in progress on another worker")
)

// QoderGatewayService reverse-proxies Chat Completions, Anthropic Messages and
// OpenAI Responses onto Qoder's agent SSE endpoint. Chat Completions is the
// canonical shape: the other protocols are bridged through apicompat on the
// way in and on the way out.
//
// Credentials are either a personal access token (exchanged for a cached job
// token) or a device login (access + refresh token, refreshed under the shared
// OAuth refresh lock). Every upstream call goes through the account proxy.
type QoderGatewayService struct {
	accountRepo      AccountRepository
	httpUpstream     HTTPUpstream
	rateLimitService *RateLimitService
	refreshAPI       *OAuthRefreshAPI
	cfg              *config.Config
	tokens           *qoderproxy.TokenCache
	// profiles caches the user behind a device token, keyed by account ID.
	profiles sync.Map
	// doer replaces the proxy-aware upstream client in tests.
	doer qoderproxy.Doer
}

type qoderCachedProfile struct {
	token   string
	profile qoderproxy.Profile
}

func NewQoderGatewayService(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	rateLimitService *RateLimitService,
	refreshAPI *OAuthRefreshAPI,
	cfg *config.Config,
) *QoderGatewayService {
	return &QoderGatewayService{
		accountRepo:      accountRepo,
		httpUpstream:     httpUpstream,
		rateLimitService: rateLimitService,
		refreshAPI:       refreshAPI,
		cfg:              cfg,
		tokens:           qoderproxy.NewTokenCache(),
	}
}

type qoderProtocol uint8

const (
	qoderProtocolChat qoderProtocol = iota
	qoderProtocolAnthropic
	qoderProtocolResponses
)

// qoderRequest is one inbound request normalized to Chat Completions.
type qoderRequest struct {
	protocol        qoderProtocol
	chat            *apicompat.ChatCompletionsRequest
	clientModel     string
	stream          bool
	includeUsage    bool
	reasoningEffort *string
	// Responses tool bookkeeping restores custom / namespaced tools on the way back.
	customTools    map[string]bool
	functionTools  map[string]bool
	toolSearch     bool
	namespaceTools map[string]apicompat.NamespacedToolName
}

// ForwardAsChatCompletions serves POST /v1/chat/completions.
func (s *QoderGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	_ *ParsedRequest,
) (*ForwardResult, error) {
	var req apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, qoderClientError(c, qoderProtocolChat, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	return s.forward(ctx, c, account, &qoderRequest{
		protocol:        qoderProtocolChat,
		chat:            &req,
		clientModel:     req.Model,
		stream:          req.Stream,
		includeUsage:    req.StreamOptions != nil && req.StreamOptions.IncludeUsage,
		reasoningEffort: extractCCReasoningEffortFromBody(body),
	})
}

// ForwardAsAnthropic serves POST /v1/messages (Claude Code and other
// Anthropic clients).
func (s *QoderGatewayService) ForwardAsAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*ForwardResult, error) {
	var req apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, qoderClientError(c, qoderProtocolAnthropic, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	chatReq, err := apicompat.AnthropicToChatCompletionsRequest(&req)
	if err != nil {
		return nil, qoderClientError(c, qoderProtocolAnthropic, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	return s.forward(ctx, c, account, &qoderRequest{
		protocol:    qoderProtocolAnthropic,
		chat:        chatReq,
		clientModel: req.Model,
		stream:      req.Stream,
	})
}

// ForwardAsResponses serves POST /v1/responses (Codex and other Responses
// clients).
func (s *QoderGatewayService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*ForwardResult, error) {
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, qoderClientError(c, qoderProtocolResponses, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	effectiveTools, err := apicompat.EffectiveResponsesTools(&req)
	if err != nil {
		return nil, qoderClientError(c, qoderProtocolResponses, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&req)
	if err != nil {
		return nil, qoderClientError(c, qoderProtocolResponses, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	return s.forward(ctx, c, account, &qoderRequest{
		protocol:        qoderProtocolResponses,
		chat:            chatReq,
		clientModel:     req.Model,
		stream:          req.Stream,
		reasoningEffort: ExtractResponsesReasoningEffortFromBody(body),
		customTools:     apicompat.CustomToolNames(effectiveTools),
		functionTools:   apicompat.FunctionToolNames(effectiveTools),
		toolSearch:      apicompat.HasToolSearchTool(effectiveTools),
		namespaceTools:  apicompat.NamespaceToolNames(effectiveTools),
	})
}

func (s *QoderGatewayService) forward(ctx context.Context, c *gin.Context, account *Account, req *qoderRequest) (*ForwardResult, error) {
	start := time.Now()
	if account == nil {
		return nil, qoderClientError(c, req.protocol, http.StatusBadRequest, "invalid_request_error", "account is required")
	}
	clientModel := strings.TrimSpace(req.clientModel)
	if clientModel == "" {
		clientModel = "auto"
	}
	upstreamModel := strings.TrimSpace(account.GetMappedModel(clientModel))
	if upstreamModel == "" {
		upstreamModel = clientModel
	}
	input, err := qoderChatInput(req.chat)
	if err != nil {
		return nil, qoderClientError(c, req.protocol, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	if len(input.Messages) == 0 {
		return nil, qoderClientError(c, req.protocol, http.StatusBadRequest, "invalid_request_error", "messages required")
	}
	input.Model = upstreamModel

	doer := s.doerFor(account)
	var rejected *qoderSession
	for attempt := 0; ; attempt++ {
		session, err := s.resolveSession(ctx, account, doer, rejected)
		if err != nil {
			return nil, s.credentialFailure(ctx, c, account, req, upstreamModel, err)
		}
		input.UserID = session.identity.UserID
		input.RequestID = uuid.NewString()
		input.BusinessID = uuid.NewString()
		input.BeginAtUnixMilli = time.Now().UnixMilli()
		payload, err := qoderproxy.BuildChatBody(input)
		if err != nil {
			return nil, qoderClientError(c, req.protocol, http.StatusBadRequest, "invalid_request_error", err.Error())
		}

		SetOpsUpstreamModel(c, upstreamModel)
		upstreamCtx, cancel := context.WithCancel(ctx)
		resp, err := qoderproxy.OpenChat(upstreamCtx, doer, session.identity, upstreamModel, payload, session.chatURL)
		if err != nil {
			cancel()
			return nil, s.transportFailure(ctx, c, account, err)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			body := []byte(qoderproxy.ReadErrorBody(resp.Body))
			_ = resp.Body.Close()
			cancel()
			if attempt == 0 {
				// A cached job token or device token may have been revoked
				// early; retry once with a freshly exchanged/refreshed one.
				logger.FromContext(ctx).Info("qoder.chat_credential_retry",
					zap.Int64("account_id", account.ID),
					zap.Int("status", resp.StatusCode),
					zap.Bool("device_login", session.device),
				)
				rejected = session
				continue
			}
			return nil, s.upstreamStatusFailure(ctx, c, account, req, upstreamModel, resp.StatusCode, resp.Header, body)
		}
		if resp.StatusCode != http.StatusOK {
			body := []byte(qoderproxy.ReadErrorBody(resp.Body))
			_ = resp.Body.Close()
			cancel()
			return nil, s.upstreamStatusFailure(ctx, c, account, req, upstreamModel, resp.StatusCode, resp.Header, body)
		}
		return s.relay(ctx, c, account, req, resp, cancel, input, clientModel, upstreamModel, start)
	}
}

// qoderChatInput converts a Chat Completions request into the upstream input.
// Text parts are kept; image and file parts are replaced by a short marker
// because the agent endpoint only accepts images uploaded to Qoder storage.
func qoderChatInput(req *apicompat.ChatCompletionsRequest) (qoderproxy.ChatInput, error) {
	if req == nil {
		return qoderproxy.ChatInput{}, errors.New("request body is required")
	}
	in := qoderproxy.ChatInput{
		ThinkingEffort: strings.TrimSpace(req.ReasoningEffort),
	}
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		in.MaxTokens = *req.MaxCompletionTokens
	} else if req.MaxTokens != nil && *req.MaxTokens > 0 {
		in.MaxTokens = *req.MaxTokens
	}
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		in.Messages = append(in.Messages, qoderproxy.Message{Role: "system", Content: instructions})
	}
	for _, msg := range req.Messages {
		converted, err := qoderMessage(msg)
		if err != nil {
			return qoderproxy.ChatInput{}, err
		}
		if converted.Content == "" && len(converted.ToolCalls) == 0 && converted.Role != "tool" {
			continue
		}
		in.Messages = append(in.Messages, converted)
	}
	for _, tool := range req.Tools {
		if raw, ok := qoderFunctionTool(tool.Type, tool.Function); ok {
			in.Tools = append(in.Tools, raw)
		}
	}
	for i := range req.Functions {
		if raw, ok := qoderFunctionTool("function", &req.Functions[i]); ok {
			in.Tools = append(in.Tools, raw)
		}
	}
	return in, nil
}

func qoderMessage(msg apicompat.ChatMessage) (qoderproxy.Message, error) {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	content, err := qoderContentText(msg.Content)
	if err != nil {
		return qoderproxy.Message{}, fmt.Errorf("invalid content for %s message: %w", role, err)
	}
	out := qoderproxy.Message{Role: role, Content: content, ToolCallID: msg.ToolCallID, Name: msg.Name}
	if role == "function" {
		// Legacy function results carry the function name instead of a call id.
		out.Role = "tool"
	}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, qoderproxy.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	if msg.FunctionCall != nil && msg.FunctionCall.Name != "" {
		out.ToolCalls = append(out.ToolCalls, qoderproxy.ToolCall{
			Name:      msg.FunctionCall.Name,
			Arguments: msg.FunctionCall.Arguments,
		})
	}
	return out, nil
}

func qoderContentText(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	var parts []apicompat.ChatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, part := range parts {
		var text string
		switch part.Type {
		case "image_url", "input_image":
			text = "[image omitted]"
		case "file", "input_file":
			text = "[file omitted]"
		default:
			text = part.Text
		}
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			_, _ = b.WriteString("\n")
		}
		_, _ = b.WriteString(text)
	}
	return b.String(), nil
}

func qoderFunctionTool(toolType string, fn *apicompat.ChatFunction) (json.RawMessage, bool) {
	if fn == nil || strings.TrimSpace(fn.Name) == "" {
		return nil, false
	}
	if toolType != "" && toolType != "function" {
		// Provider built-ins (web_search, x_search, ...) have no Qoder equivalent.
		return nil, false
	}
	function := map[string]any{"name": fn.Name}
	if fn.Description != "" {
		function["description"] = fn.Description
	}
	if len(fn.Parameters) > 0 && strings.TrimSpace(string(fn.Parameters)) != "null" {
		function["parameters"] = fn.Parameters
	} else {
		function["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	raw, err := json.Marshal(map[string]any{"type": "function", "function": function})
	if err != nil {
		return nil, false
	}
	return raw, true
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

type qoderSession struct {
	identity qoderproxy.Identity
	chatURL  string
	// device is true when the identity is a device-login access token;
	// otherwise it is a job token exchanged from the personal access token.
	device bool
}

type qoderUpstreamDoer struct {
	upstream    HTTPUpstream
	proxyURL    string
	accountID   int64
	concurrency int
}

func (d qoderUpstreamDoer) Do(req *http.Request) (*http.Response, error) {
	return d.upstream.Do(req, d.proxyURL, d.accountID, d.concurrency)
}

// doerFor routes Qoder calls through the account proxy.
func (s *QoderGatewayService) doerFor(account *Account) qoderproxy.Doer {
	if s.doer != nil {
		return s.doer
	}
	if s.httpUpstream == nil || account == nil {
		return http.DefaultClient
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	return qoderUpstreamDoer{
		upstream:    s.httpUpstream,
		proxyURL:    proxyURL,
		accountID:   account.ID,
		concurrency: account.Concurrency,
	}
}

func qoderAccountRegion(account *Account) qoderproxy.Region {
	if account == nil {
		return qoderproxy.RegionGlobal
	}
	return qoderproxy.AccountRegion(account.GetCredential("qoder_region"))
}

func qoderPersonalToken(account *Account) string {
	if account == nil {
		return ""
	}
	for _, key := range []string{"personal_token", "pat", "api_key"} {
		if token := strings.TrimSpace(account.GetCredential(key)); token != "" {
			return token
		}
	}
	return ""
}

func qoderHasDeviceLogin(account *Account) bool {
	return account != nil && (strings.TrimSpace(account.GetCredential("access_token")) != "" ||
		strings.TrimSpace(account.GetCredential("refresh_token")) != "")
}

// qoderMachineID keeps the COSY machine id stable per account: the stored
// value when set, otherwise one derived from the account id.
func qoderMachineID(account *Account) string {
	if account == nil {
		return ""
	}
	if id := strings.TrimSpace(account.GetCredential("machine_id")); id != "" {
		return id
	}
	if account.ID <= 0 {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("sub2api:qoder:%d", account.ID))).String()
}

// isQoderCredentialDead reports whether the stored credential itself was
// refused, as opposed to a transient network or upstream failure.
func isQoderCredentialDead(err error) bool {
	return errors.Is(err, errQoderNoCredential) ||
		errors.Is(err, errQoderDeviceLoginExpired) ||
		qoderproxy.IsRejected(err)
}

// resolveSession picks the credential for one attempt. rejected is the
// session the upstream just refused; its token is invalidated or refreshed.
func (s *QoderGatewayService) resolveSession(ctx context.Context, account *Account, doer qoderproxy.Doer, rejected *qoderSession) (*qoderSession, error) {
	region := qoderAccountRegion(account)
	machineID := qoderMachineID(account)
	pat := qoderPersonalToken(account)

	if qoderHasDeviceLogin(account) {
		rejectedAccess := ""
		if rejected != nil && rejected.device {
			rejectedAccess = rejected.identity.JobToken
		}
		session, err := s.deviceSession(ctx, account, doer, region, machineID, rejectedAccess)
		if err == nil {
			return session, nil
		}
		if pat == "" || !isQoderCredentialDead(err) {
			return nil, err
		}
		logger.FromContext(ctx).Warn("qoder.device_login_unusable_fallback_to_pat",
			zap.Int64("account_id", account.ID),
			zap.Error(err),
		)
	}
	if pat == "" {
		return nil, errQoderNoCredential
	}
	if rejected != nil && !rejected.device {
		s.tokens.Invalidate(pat)
	}
	identity, err := s.tokens.ResolveRegion(ctx, doer, region, pat, machineID)
	if err != nil {
		return nil, err
	}
	return &qoderSession{identity: identity, chatURL: region.ChatEndpoint()}, nil
}

func (s *QoderGatewayService) deviceSession(
	ctx context.Context,
	account *Account,
	doer qoderproxy.Doer,
	region qoderproxy.Region,
	machineID string,
	rejectedAccess string,
) (*qoderSession, error) {
	refresher := &qoderTokenRefresher{svc: s, rejectedAccessToken: rejectedAccess}
	if refresher.NeedsRefresh(account, qoderTokenRefreshSkew) {
		if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
			return nil, errQoderDeviceLoginExpired
		}
		fresh, err := s.refreshDeviceLogin(ctx, account, refresher)
		if err != nil {
			return nil, err
		}
		account = fresh
	}
	access := strings.TrimSpace(account.GetCredential("access_token"))
	if access == "" {
		return nil, errQoderDeviceLoginExpired
	}
	profile := qoderproxy.Profile{
		UserID: strings.TrimSpace(account.GetCredential("user_id")),
		Name:   strings.TrimSpace(account.GetCredential("name")),
		Email:  strings.TrimSpace(account.GetCredential("email")),
	}
	if profile.UserID == "" {
		fetched, err := s.deviceProfile(ctx, account, doer, region, access)
		if err != nil {
			return nil, err
		}
		profile = fetched
	}
	return &qoderSession{
		identity: qoderproxy.Identity{
			UserID:    profile.UserID,
			Name:      profile.Name,
			Email:     profile.Email,
			JobToken:  access,
			MachineID: machineID,
		},
		chatURL: region.ChatEndpoint(),
		device:  true,
	}, nil
}

// deviceProfile loads (and caches per account) the user behind a device
// token when the login did not store user_id.
func (s *QoderGatewayService) deviceProfile(ctx context.Context, account *Account, doer qoderproxy.Doer, region qoderproxy.Region, access string) (qoderproxy.Profile, error) {
	if cached, ok := s.profiles.Load(account.ID); ok {
		if entry, ok := cached.(qoderCachedProfile); ok && entry.token == access {
			return entry.profile, nil
		}
	}
	profile, err := qoderproxy.FetchProfile(ctx, doer, region, access)
	if err != nil {
		return qoderproxy.Profile{}, err
	}
	s.profiles.Store(account.ID, qoderCachedProfile{token: access, profile: profile})
	return profile, nil
}

// refreshDeviceLogin refreshes the device token under the shared OAuth refresh
// lock (process mutex + Redis lock + DB re-read), so concurrent requests and
// other instances never race on a rotating refresh token.
func (s *QoderGatewayService) refreshDeviceLogin(ctx context.Context, account *Account, refresher *qoderTokenRefresher) (*Account, error) {
	if s.refreshAPI == nil {
		creds, err := refresher.Refresh(ctx, account)
		if err != nil {
			return nil, err
		}
		if s.accountRepo != nil && account.ID > 0 {
			if err := persistAccountCredentials(ctx, s.accountRepo, account, creds); err != nil {
				return nil, fmt.Errorf("persist qoder device token: %w", err)
			}
		} else {
			account.Credentials = creds
		}
		return account, nil
	}

	result, err := s.refreshAPI.RefreshIfNeeded(ctx, account, refresher, qoderTokenRefreshSkew)
	if err != nil {
		// Another instance may have rotated the refresh token while this one
		// used the old copy; a changed token in the DB means we lost a race.
		if qoderproxy.IsRejected(err) {
			if fresh := s.rotatedAccount(ctx, account); fresh != nil {
				return fresh, nil
			}
		}
		return nil, err
	}
	if result != nil && result.LockHeld {
		return s.waitForRefresh(ctx, account, refresher)
	}
	if result != nil && result.Account != nil {
		return result.Account, nil
	}
	return account, nil
}

func (s *QoderGatewayService) rotatedAccount(ctx context.Context, used *Account) *Account {
	if s.accountRepo == nil || used == nil || used.ID <= 0 {
		return nil
	}
	current, err := s.accountRepo.GetByID(ctx, used.ID)
	if err != nil || current == nil {
		return nil
	}
	if strings.TrimSpace(current.GetCredential("refresh_token")) == strings.TrimSpace(used.GetCredential("refresh_token")) {
		return nil
	}
	return current
}

// waitForRefresh polls the account while another worker holds the refresh lock.
func (s *QoderGatewayService) waitForRefresh(ctx context.Context, account *Account, refresher *qoderTokenRefresher) (*Account, error) {
	if s.accountRepo == nil || account.ID <= 0 {
		return nil, errQoderRefreshInProgress
	}
	deadline := time.Now().Add(qoderRefreshLockWait)
	for time.Now().Before(deadline) {
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		current, err := s.accountRepo.GetByID(ctx, account.ID)
		if err == nil && current != nil && !refresher.NeedsRefresh(current, qoderTokenRefreshSkew) {
			return current, nil
		}
	}
	return nil, errQoderRefreshInProgress
}

// qoderTokenRefresher is the OAuthRefreshExecutor for Qoder device logins.
type qoderTokenRefresher struct {
	svc *QoderGatewayService
	// rejectedAccessToken forces a refresh when the upstream refused this token
	// even though it has not expired yet.
	rejectedAccessToken string
}

func (r *qoderTokenRefresher) CacheKey(account *Account) string {
	return fmt.Sprintf("qoder:device:%d", account.ID)
}

func (r *qoderTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.IsQoder() && strings.TrimSpace(account.GetCredential("refresh_token")) != ""
}

func (r *qoderTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	access := strings.TrimSpace(account.GetCredential("access_token"))
	if access == "" {
		return true
	}
	if r.rejectedAccessToken != "" && access == r.rejectedAccessToken {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	return expiresAt != nil && time.Until(*expiresAt) < refreshWindow
}

func (r *qoderTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	tok, err := qoderproxy.RefreshLogin(ctx, r.svc.doerFor(account), qoderAccountRegion(account), account.GetCredential("refresh_token"))
	if err != nil {
		return nil, err
	}
	creds := shallowCopyMap(account.Credentials)
	if creds == nil {
		creds = map[string]any{}
	}
	creds["access_token"] = tok.AccessToken
	if tok.RefreshToken != "" {
		creds["refresh_token"] = tok.RefreshToken
	}
	if !tok.ExpiresAt.IsZero() {
		creds["expires_at"] = tok.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if tok.UserID != "" {
		creds["user_id"] = tok.UserID
	}
	return creds, nil
}

// ---------------------------------------------------------------------------
// Failures
// ---------------------------------------------------------------------------

// qoderErrorStatus maps an upstream error status onto an HTTP error status.
// SSE statusCodeValue may carry business codes outside the HTTP range, and
// handlers reuse the status for the client response.
func qoderErrorStatus(status int) int {
	if status < http.StatusBadRequest || status > 599 {
		return http.StatusBadGateway
	}
	return status
}

// qoderShouldFailover reports whether another account may succeed.
func qoderShouldFailover(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests, 529:
		return true
	default:
		return status >= http.StatusInternalServerError
	}
}

func qoderOpsEvent(account *Account, status int, kind, message string) OpsUpstreamErrorEvent {
	return OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: status,
		Kind:               kind,
		Message:            message,
	}
}

func qoderErrorBody(message string) []byte {
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "upstream_error", "message": message}})
	return raw
}

func qoderCredentialFailoverError(status int, body []byte, headers http.Header) *UpstreamFailoverError {
	var cloned http.Header
	if headers != nil {
		cloned = headers.Clone()
	}
	return &UpstreamFailoverError{
		StatusCode:        status,
		ResponseBody:      body,
		ResponseHeaders:   cloned,
		Stage:             GatewayFailureStageAccountAuth,
		Scope:             GatewayFailureScopeAccount,
		Reason:            QoderCredentialRejectedReason,
		NextAccountAction: NextAccountRetry,
		ClientStatusCode:  http.StatusBadGateway,
		ClientMessage:     QoderCredentialRejectedClientMessage,
	}
}

// credentialFailure handles a failure to obtain a usable credential.
func (s *QoderGatewayService) credentialFailure(ctx context.Context, c *gin.Context, account *Account, req *qoderRequest, model string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	message := sanitizeUpstreamErrorMessage(err.Error())
	if isQoderCredentialDead(err) {
		body := qoderErrorBody("Qoder credential rejected: " + message)
		// Marks the account as errored: the admin must sign in again or
		// replace the personal access token.
		if s.rateLimitService != nil {
			s.rateLimitService.HandleUpstreamError(ctx, account, http.StatusUnauthorized, http.Header{}, body, model)
		}
		event := qoderOpsEvent(account, http.StatusUnauthorized, "failover", message)
		event.Stage = string(GatewayFailureStageAccountAuth)
		event.Scope = string(GatewayFailureScopeAccount)
		event.Reason = string(QoderCredentialRejectedReason)
		appendOpsUpstreamError(c, event)
		return qoderCredentialFailoverError(http.StatusUnauthorized, body, nil)
	}
	var httpErr *qoderproxy.HTTPError
	if errors.As(err, &httpErr) {
		return s.upstreamStatusFailure(ctx, c, account, req, model, httpErr.Status, nil, []byte(httpErr.Body))
	}
	return s.transportFailure(ctx, c, account, err)
}

// transportFailure handles a network-level failure (proxy, DNS, TLS, reset).
func (s *QoderGatewayService) transportFailure(ctx context.Context, c *gin.Context, account *Account, err error) error {
	message := sanitizeUpstreamErrorMessage(err.Error())
	setOpsUpstreamError(c, 0, message, "")
	appendOpsUpstreamError(c, qoderOpsEvent(account, 0, "request_error", message))
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		// Client gone: no failover, no penalty for the account.
		return err
	}
	if errors.Is(err, errQoderRefreshInProgress) {
		return &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: qoderErrorBody(message)}
	}
	if classifyUpstreamTransportError(err).Persistent && s.accountRepo != nil {
		until := time.Now().Add(gatewayTransportErrorTempUnschedDuration)
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAccountStateUpdateTimeout)
		defer cancel()
		if setErr := s.accountRepo.SetTempUnschedulable(bgCtx, account.ID, until, "upstream transport error (proxy/network): "+message); setErr != nil {
			logger.FromContext(ctx).Warn("qoder.account_temp_unschedule_transport_failed", zap.Int64("account_id", account.ID), zap.Error(setErr))
		}
	}
	return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: gatewayTransportFailoverBody}
}

// upstreamStatusFailure handles an upstream error status before any output
// reached the client: HTTP errors and failed SSE events alike.
func (s *QoderGatewayService) upstreamStatusFailure(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	req *qoderRequest,
	model string,
	status int,
	headers http.Header,
	body []byte,
) error {
	status = qoderErrorStatus(status)
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if message == "" {
		message = sanitizeUpstreamErrorMessage(truncateString(strings.TrimSpace(string(body)), 512))
	}
	if headers == nil {
		headers = http.Header{}
	}
	if s.rateLimitService != nil {
		s.rateLimitService.HandleUpstreamError(ctx, account, status, headers, body, model)
	}
	if qoderShouldFailover(status) {
		appendOpsUpstreamError(c, qoderOpsEvent(account, status, "failover", message))
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return qoderCredentialFailoverError(status, body, headers)
		}
		return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: headers.Clone()}
	}
	appendOpsUpstreamError(c, qoderOpsEvent(account, status, "http_error", message))
	setOpsUpstreamError(c, status, message, "")
	errType := "upstream_error"
	if status >= 400 && status < 500 {
		errType = "invalid_request_error"
	}
	clientMessage := message
	if clientMessage == "" {
		clientMessage = "Upstream request failed"
	}
	writeQoderClientError(c, req.protocol, mapUpstreamStatusCode(status), errType, clientMessage)
	return fmt.Errorf("qoder upstream error: %d %s", status, message)
}

// writeQoderClientError writes an error in the inbound protocol's format.
func writeQoderClientError(c *gin.Context, protocol qoderProtocol, status int, errType, message string) {
	switch protocol {
	case qoderProtocolAnthropic:
		writeAnthropicError(c, status, errType, message)
	case qoderProtocolResponses:
		writeOpenAIResponsesFallbackError(c, status, errType, message)
	default:
		MarkResponseCommitted(c)
		c.JSON(status, gin.H{
			"error": gin.H{
				"message": message,
				"type":    errType,
				"param":   nil,
				"code":    nil,
			},
		})
	}
}

func qoderClientError(c *gin.Context, protocol qoderProtocol, status int, errType, message string) error {
	writeQoderClientError(c, protocol, status, errType, message)
	return errors.New(message)
}
