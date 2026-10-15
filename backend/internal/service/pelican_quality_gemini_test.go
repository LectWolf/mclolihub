//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/stretchr/testify/require"
)

func TestGeminiQualityChatCompletionsURL(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{" https://generativelanguage.googleapis.com/ ", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1alpha", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/openai/", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/openai/chat/completions/", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://gateway.example", "https://gateway.example/v1beta/openai/chat/completions"},
		{"https://gateway.example/google/v1beta", "https://gateway.example/google/v1beta/openai/chat/completions"},
		{"https://gateway.example/google/v1alpha", "https://gateway.example/google/v1beta/openai/chat/completions"},
		{"https://gateway.example/google/v1beta/openai", "https://gateway.example/google/v1beta/openai/chat/completions"},
		{"https://gateway.example/custom/prefix", "https://gateway.example/custom/prefix/chat/completions"},
		{"https://gateway.example/custom/v1", "https://gateway.example/custom/v1/chat/completions"},
		{"https://gateway.example/custom/openai", "https://gateway.example/custom/openai/chat/completions"},
		{"https://gateway.example/custom/chat/completions", "https://gateway.example/custom/chat/completions"},
		{"https://gateway.example/tenant%2Fcustom/openai", "https://gateway.example/tenant%2Fcustom/openai/chat/completions"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			got, err := buildGeminiQualityChatCompletionsURL(tc.base)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, base := range []string{"/v1beta", "ftp://gateway.example", "https://user:pass@gateway.example", "https://gateway.example?key=mock", "https://gateway.example#fragment", "https://gateway.example?"} {
		_, err := buildGeminiQualityChatCompletionsURL(base)
		require.Error(t, err, base)
	}
}

func pelicanGeminiAccount() *Account {
	account := pelicanChatAccount()
	account.Platform = PlatformGemini
	account.Credentials["base_url"] = "https://generativelanguage.googleapis.com/v1beta/openai"
	return account
}

func TestGeminiQualityChatRequestContract(t *testing.T) {
	for _, base := range []string{"", "https://generativelanguage.googleapis.com", "https://generativelanguage.googleapis.com/v1beta", "https://gateway.example/proxy/v1beta/openai", "https://gateway.example/proxy"} {
		t.Run(base, func(t *testing.T) {
			account := pelicanGeminiAccount()
			account.Credentials["base_url"] = base
			account.Credentials["model_mapping"] = map[string]any{"alias": "gemini-mapped"}
			svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>final</html>", "gemini-actual", true))
			c, recorder := newTestContext()
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pelicanQualityContextKey{}, true))
			require.NoError(t, svc.testGeminiQualityChatCompletionsConnection(c, account, "alias", DefaultScheduledTestPrompt))
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			wantURL, err := buildGeminiQualityChatCompletionsURL(base)
			require.NoError(t, err)
			require.Equal(t, wantURL, req.URL.String())
			requirePelicanStudioRequest(t, req, "gemini-mapped", DefaultScheduledTestPrompt)
			require.Empty(t, req.Header.Get("x-goog-api-key"))
			require.Empty(t, req.URL.RawQuery)
			require.Contains(t, req.Header.Get("User-Agent"), "pi (")
			out, err := parsePelicanTestEvents(recorder.Body.Bytes(), pelicanDocumentLimit)
			require.NoError(t, err)
			require.Equal(t, "<html>final</html>", out.Text)
			require.Equal(t, "gemini-actual", out.Model)
		})
	}
}

func TestGeminiQualityChatDefaultModel(t *testing.T) {
	account := pelicanGeminiAccount()
	svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>final</html>", "gemini-actual", true))
	c, _ := newTestContext()
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pelicanQualityContextKey{}, true))
	require.NoError(t, svc.testGeminiQualityChatCompletionsConnection(c, account, "", DefaultScheduledTestPrompt))
	requirePelicanStudioRequest(t, upstream.requests[0], geminicli.DefaultTestModel, DefaultScheduledTestPrompt)
}

func TestGeminiQualityChatRejectsInvalidRequestsWithoutUpstream(t *testing.T) {
	for _, name := range []string{"ordinary", "missing-key", "synthetic", "invalid-url"} {
		t.Run(name, func(t *testing.T) {
			account := pelicanGeminiAccount()
			model := "gemini-text"
			switch name {
			case "missing-key":
				account.Credentials["api_key"] = " "
			case "synthetic":
				account.Extra["synthetic_ui_test"] = true
			case "invalid-url":
				account.Credentials["base_url"] = "https://gateway.example?key=mock"
			}
			svc, upstream := pelicanMockService(account)
			c, _ := newTestContext()
			if name != "ordinary" {
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pelicanQualityContextKey{}, true))
			}
			require.Error(t, svc.testGeminiQualityChatCompletionsConnection(c, account, model, DefaultScheduledTestPrompt))
			require.Empty(t, upstream.requests)
		})
	}
}

func TestGeminiOrdinaryConnectionKeepsNativeProtocol(t *testing.T) {
	account := pelicanGeminiAccount()
	account.Credentials["base_url"] = "https://generativelanguage.googleapis.com"
	svc, upstream := pelicanMockService(account, newJSONResponse(http.StatusOK,
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
	c, _ := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "gemini-text", "hello", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "/v1beta/models/gemini-text:streamGenerateContent", req.URL.Path)
	require.Equal(t, "sse", req.URL.Query().Get("alt"))
	require.Equal(t, "mock-secret", req.Header.Get("x-goog-api-key"))
	require.Empty(t, req.Header.Get("Authorization"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"contents"`)
	require.NotContains(t, string(body), `"messages"`)
	require.NotContains(t, string(body), `"max_completion_tokens"`)
}

func TestGeminiQualityChatPreservesStreamFailure(t *testing.T) {
	account := pelicanGeminiAccount()
	svc, upstream := pelicanMockService(account, newJSONResponse(http.StatusOK, pelicanChatDelta))
	c, recorder := newTestContext()
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pelicanQualityContextKey{}, true))
	err := svc.testGeminiQualityChatCompletionsConnection(c, account, "gemini-text", DefaultScheduledTestPrompt)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	out, parseErr := parsePelicanTestEvents(recorder.Body.Bytes(), pelicanDocumentLimit)
	require.Error(t, parseErr)
	require.Equal(t, "<html>partial", out.Text)
	require.Equal(t, "missing_completion", pelicanRequestFailureCode(err))
}

func TestGeminiQualityGenerationUsesChatAndCapturesHTTPAttempts(t *testing.T) {
	account := pelicanGeminiAccount()
	svc, upstream := pelicanMockService(account,
		newJSONResponse(http.StatusServiceUnavailable, `{"error":{"type":"overloaded_error","message":"temporary mock-secret"}}`),
		pelicanMockResponse("<html>final</html>", "gemini-actual", true))
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "gemini-text")
	require.Equal(t, "success", result.Status, result.Reason)
	require.Equal(t, "<html>final</html>", result.Artwork)
	require.Equal(t, "gemini-actual", result.ModelID)
	require.Len(t, upstream.requests, 2)
	for _, req := range upstream.requests {
		require.Equal(t, "/v1beta/openai/chat/completions", req.URL.Path)
		requirePelicanStudioRequest(t, req, "gemini-text", DefaultScheduledTestPrompt)
	}
	require.Len(t, result.Diagnostics.Attempts, 2)
	require.Equal(t, http.StatusServiceUnavailable, result.Diagnostics.Attempts[0].HTTPStatus)
	require.Equal(t, "overloaded_error", result.Diagnostics.Attempts[0].ErrorType)
	require.NotContains(t, result.Diagnostics.Attempts[0].Message, "mock-secret")
	require.Equal(t, http.StatusOK, result.Diagnostics.Attempts[1].HTTPStatus)
	require.NotNil(t, result.Diagnostics.Route)
	require.Equal(t, PlatformGemini, result.Diagnostics.Route.Platform)
	require.Contains(t, result.Diagnostics.Route.Endpoint, "/v1beta/openai/chat/completions")
}
