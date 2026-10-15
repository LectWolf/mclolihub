package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/gin-gonic/gin"
)

// buildGeminiQualityChatCompletionsURL adapts native Gemini bases to Google's
// OpenAI compatibility endpoint. Explicit compatibility bases and custom prefixes
// are kept intact; the generic OpenAI URL builder would incorrectly insert /v1.
func buildGeminiQualityChatCompletionsURL(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = geminicli.AIStudioBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("invalid Gemini quality base URL")
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		// Already an endpoint.
	case path == "":
		path = "/v1beta/openai/chat/completions"
	case strings.HasSuffix(path, "/v1beta"), strings.HasSuffix(path, "/v1alpha"),
		strings.EqualFold(u.Hostname(), "generativelanguage.googleapis.com") && strings.HasSuffix(path, "/v1"):
		// Google's OpenAI compatibility API uses v1beta, even when the
		// configured native Gemini base selects a different API version.
		path = path[:strings.LastIndex(path, "/")] + "/v1beta/openai/chat/completions"
	default:
		path += "/chat/completions"
	}
	u.Path, _ = url.PathUnescape(path)
	u.RawPath = path
	return u.String(), nil
}

// testGeminiQualityChatCompletionsConnection is only for Pelican generation.
// Normal Gemini connectivity checks retain their native generateContent path.
func (s *AccountTestService) testGeminiQualityChatCompletionsConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	ctx := c.Request.Context()
	if !isPelicanQualityRequest(ctx) || account == nil || !account.IsGemini() || account.IsSyntheticUITest() {
		return s.sendErrorAndEnd(c, "Invalid Gemini quality request context")
	}
	policyModel := qualityPolicyModel(ctx, account, modelID)
	if !accountAllowsQualityModel(account, policyModel) {
		return s.sendErrorAndEnd(c, qualityModelNotAllowedReason)
	}
	if !account.IsSchedulableForModelWithContext(ctx, policyModel) {
		return s.sendErrorAndEnd(c, "Account is not schedulable for quality generation")
	}
	if s.httpUpstream == nil {
		return s.sendErrorAndEnd(c, "HTTP upstream not configured")
	}
	apiKey := qualityChatCredential(account)
	if apiKey == "" {
		return s.sendErrorAndEnd(c, "No credentials available for quality generation")
	}
	apiURL, err := buildGeminiQualityChatCompletionsURL(account.GetCredential("base_url"))
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	apiURL, err = s.validateUpstreamBaseURL(apiURL)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	testModelID := qualityRequestedModel(account, modelID)
	testModelID = account.GetMappedModel(testModelID)
	payload := createOpenAIChatCompletionsTestPayload(testModelID, prompt)
	applyPelicanQualityPayload(ctx, payload, false)
	body, err := json.Marshal(payload)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Chat Completions payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Chat Completions request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", pelicanQualityUserAgent())
	account.ApplyHeaderOverrides(req.Header)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	recordQualityRequest(ctx, account, req, testModelID, proxyURL, apiKey)
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	recordQualityHTTPStatus(ctx, resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pelicanQualityStreamReader(ctx, resp.Body))
		recordQualityHTTPError(ctx, body)
		if s.qualityRateLimitService != nil {
			s.qualityRateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, testModelID)
		}
		return s.sendErrorAndEnd(c, fmt.Sprintf("Chat Completions API (/v1/chat/completions) returned %d: %s", resp.StatusCode, string(body)))
	}
	return s.processOpenAIChatCompletionsStream(c, resp.Body)
}
