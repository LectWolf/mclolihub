//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Assert the wire contract validated by html_studio.py, including omitted fields.
func requirePelicanStudioRequest(t *testing.T, req *http.Request, model, prompt string) {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	expected, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": prompt},
		},
		"stream":                true,
		"max_completion_tokens": 65536,
		"store":                 false,
		"stream_options":        map[string]any{"include_usage": true},
	})
	require.NoError(t, err)
	require.JSONEq(t, string(expected), string(body))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
	require.Equal(t, "Bearer mock-secret", req.Header.Get("Authorization"))
}

func TestPelicanStudioRequestContract(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformKimi} {
		for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAdaptive} {
			t.Run(platform+"/"+protocol, func(t *testing.T) {
				account := pelicanChatAccount()
				account.Platform = platform
				account.Credentials["api_protocol"] = protocol
				svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>final</html>", "actual-model", true))
				result := svc.runPelicanQualityCheck(context.Background(), account.ID, "requested")
				require.Equal(t, "success", result.Status, result.Reason)
				require.Len(t, upstream.requests, 1)
				req := upstream.requests[0]
				require.Equal(t, "/v1/chat/completions", req.URL.Path)
				requirePelicanStudioRequest(t, req, "requested", DefaultScheduledTestPrompt)
				require.True(t, len(req.Header.Get("User-Agent")) > len("pi ("))
				require.Contains(t, req.Header.Get("User-Agent"), "pi (")
			})
		}
	}
}

func TestPelicanStudioRequestRetryKeepsContract(t *testing.T) {
	svc, upstream := pelicanMockService(pelicanChatAccount(),
		newJSONResponse(http.StatusOK, pelicanChatDelta),
		pelicanMockResponse("<html>final</html>", "actual-model", true))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
	require.Equal(t, "success", result.Status, result.Reason)
	require.Len(t, upstream.requests, 2)
	for _, req := range upstream.requests {
		requirePelicanStudioRequest(t, req, "requested", DefaultScheduledTestPrompt)
	}
}

func TestPelicanStudioRequestDoesNotChangeAccountConnectionTest(t *testing.T) {
	account := pelicanChatAccount()
	account.Extra["openai_responses_supported"] = false
	svc, upstream := pelicanMockService(account, pelicanMockResponse("hello", "actual-model", true))
	c, _ := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "requested", "hello", AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"requested","messages":[{"role":"user","content":"hello"}],"stream":true}`, string(body))
	require.Empty(t, upstream.requests[0].Header.Get("User-Agent"))
}

func TestPelicanStudioRequestHonorsAccountHeaders(t *testing.T) {
	account := pelicanChatAccount()
	account.Credentials["header_override_enabled"] = true
	account.Credentials["header_overrides"] = map[string]any{"User-Agent": "custom-client/1"}
	svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>final</html>", "actual-model", true))
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "requested")
	require.Equal(t, "success", result.Status, result.Reason)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "custom-client/1", upstream.requests[0].Header.Get("User-Agent"))
}
