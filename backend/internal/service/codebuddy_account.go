package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) testCodeBuddyAccountConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	ctx := c.Request.Context()
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return s.sendErrorAndEnd(c, "Invalid CodeBuddy credentials: "+err.Error())
	}
	token := strings.TrimSpace(creds.AccessToken)
	if s.openaiGatewayService != nil {
		if refreshed, _, tokenErr := s.openaiGatewayService.GetAccessToken(ctx, account); tokenErr == nil && strings.TrimSpace(refreshed) != "" {
			token = strings.TrimSpace(refreshed)
			creds.AccessToken = token
		}
	}
	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = "auto"
	}
	testModelID = account.GetMappedModel(testModelID)
	if strings.TrimSpace(prompt) == "" {
		prompt = "hi"
	}

	payload := map[string]any{
		"model":    testModelID,
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	}
	body, _ := json.Marshal(payload)
	body, err = codebuddy.EnsureLeadingSystemMessage(body)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}

	apiURL := codebuddy.ChatCompletionsURL(creds.Profile)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create CodeBuddy request")
	}
	applyCodeBuddyUpstreamHeaders(req.Header, account, token)
	req.Header.Set("Accept", "text/event-stream")

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})
	s.sendEvent(c, TestEvent{Type: "status", Text: "正在通过 WorkBuddy / CodeBuddy /v2/chat/completions 测试连接"})

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("CodeBuddy request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return s.sendErrorAndEnd(c, fmt.Sprintf("CodeBuddy API returned %d: %s", resp.StatusCode, string(respBody)))
	}
	return s.processOpenAIChatCompletionsStream(c, resp.Body)
}

func (s *AccountTestService) fetchCodeBuddyUpstreamModels(ctx context.Context, account *Account) ([]string, []byte, error) {
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return nil, nil, newUpstreamModelSyncConfigError("Invalid CodeBuddy credentials", err)
	}
	if s.openaiGatewayService != nil {
		if token, _, tokenErr := s.openaiGatewayService.GetAccessToken(ctx, account); tokenErr == nil && strings.TrimSpace(token) != "" {
			creds.AccessToken = strings.TrimSpace(token)
		}
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := codebuddy.NewClient(proxyURL)
	if err != nil {
		return nil, nil, newUpstreamModelSyncConfigError("Invalid CodeBuddy proxy", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, codeBuddyUpstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, codebuddy.ConfigURL(creds.Profile), nil)
	if err != nil {
		return nil, nil, newUpstreamModelSyncConfigError("Failed to build CodeBuddy catalog request", err)
	}
	codebuddy.ApplyHeadersToRequest(req, codebuddy.CatalogHeaders(creds.Profile, creds.AccessToken, creds.Domain, creds.UID, creds.EnterpriseID))
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Failed to request CodeBuddy catalog", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Failed to read CodeBuddy catalog", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &UpstreamModelSyncError{
			Kind:       UpstreamModelSyncErrorUpstream,
			Message:    fmt.Sprintf("CodeBuddy catalog request failed with HTTP %d", resp.StatusCode),
			StatusCode: resp.StatusCode,
		}
	}
	models, err := codebuddy.ParseCatalog(body, codebuddy.ProfileProduct(creds.Profile))
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("CodeBuddy catalog response was invalid", err)
	}
	models = codebuddy.FilterCatalog(models, account.CodeBuddyCreditPolicy())
	ids := codebuddy.CatalogIDs(models)
	if len(ids) == 0 {
		return nil, nil, newUpstreamModelSyncUpstreamError("CodeBuddy catalog returned no models", nil)
	}
	if s.accountRepo != nil {
		snapshot := CodeBuddyCatalogSnapshot{SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: models}
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{codeBuddyCatalogExtraKey: snapshot})
	}
	return ids, body, nil
}
