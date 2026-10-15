//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPelicanGeminiQualityUsesChatAndRetainsDiagnostics(t *testing.T) {
	account := pelicanGeminiAccount()
	svc, upstream := pelicanMockService(account,
		newJSONResponse(http.StatusOK, "data: {\"error\":{\"type\":\"upstream_error\",\"message\":\"provider rejected mock-secret\"}}\n\n"),
		pelicanMockResponse("<html>retry</html>", "gemini-actual", true))
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "gemini-requested")
	require.Equal(t, "success", result.Status, result.Reason)
	require.Equal(t, "<html>retry</html>", result.Artwork)
	require.Equal(t, "<html>retry</html>", result.RawOutput)
	require.Len(t, upstream.requests, 2)
	for _, req := range upstream.requests {
		require.Equal(t, "/v1beta/openai/chat/completions", req.URL.Path)
	}
	require.Equal(t, 2, result.Diagnostics.AttemptCount)
	require.Equal(t, 200, result.Diagnostics.Attempts[0].HTTPStatus)
	require.Equal(t, "upstream_error", result.Diagnostics.Attempts[0].ErrorType)
	require.Contains(t, result.Diagnostics.Attempts[0].Message, "provider rejected")
	require.NotContains(t, result.Diagnostics.Attempts[0].Message, "mock-secret")
	require.Equal(t, 200, result.Diagnostics.Attempts[1].HTTPStatus)
	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", result.Diagnostics.Route.Endpoint)
	require.Equal(t, "gemini-requested", result.Diagnostics.Route.Model)
}
