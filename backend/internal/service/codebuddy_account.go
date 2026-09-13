package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
