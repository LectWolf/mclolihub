//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func pelicanChatAccount() *Account {
	return &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "mock-secret", "base_url": "http://chat-test.example/v1"},
		Extra:       map[string]any{"openai_responses_supported": true}}
}

const pelicanChatDelta = "data: {\"model\":\"actual-model\",\"choices\":[{\"delta\":{\"content\":\"<html>partial\"},\"finish_reason\":null}]}\n\n"

type pelicanReadError struct{ err error }

func (r pelicanReadError) Read([]byte) (int, error) { return 0, r.err }

func TestPelicanChatCompletionEndings(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		wantErr      bool
	}{
		{"done", pelicanChatDelta + "data: [DONE]\n\n", false},
		{"done-without-newline", pelicanChatDelta + "data: [DONE]", false},
		{"finish-stop", pelicanChatDelta + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", false},
		{"finish-length", pelicanChatDelta + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n", false},
		{"finish-length-without-newline", pelicanChatDelta + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}", false},
		{"finish-length-and-done", pelicanChatDelta + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", false},
		{"disconnect", pelicanChatDelta, true},
		{"invalid", "data: invalid\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTestContext()
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pelicanQualityContextKey{}, true))
			err := (&AccountTestService{}).processOpenAIChatCompletionsStream(c, strings.NewReader(tc.stream))
			out, parseErr := parsePelicanTestEvents(recorder.Body.Bytes(), pelicanDocumentLimit)
			if tc.wantErr {
				require.Error(t, err)
				require.Error(t, parseErr)
			} else {
				require.NoError(t, err)
				require.NoError(t, parseErr)
				require.Equal(t, "<html>partial", out.Text)
			}
		})
	}
}

func TestPelicanChatGenerationDoesNotScoreHTML(t *testing.T) {
	for _, protocol := range []string{APIProtocolResponses, APIProtocolAdaptive, APIProtocolChatCompletions} {
		t.Run(protocol, func(t *testing.T) {
			account := pelicanChatAccount()
			account.Credentials["api_protocol"] = protocol
			svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>final</html>", "actual-model", true))
			result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
			require.Equal(t, "success", result.Status, result.Reason)
			require.Equal(t, "<html>final</html>", result.Artwork)
			require.Equal(t, "actual-model", result.ModelID)
			require.Nil(t, result.Score)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
		})
	}
}

func TestPelicanChatGenerationRetriesDisconnect(t *testing.T) {
	for _, readErr := range []error{nil, io.ErrUnexpectedEOF} {
		t.Run("disconnect-"+func() string {
			if readErr == nil {
				return "EOF"
			}
			return "read-error"
		}(), func(t *testing.T) {
			broken := newJSONResponse(http.StatusOK, pelicanChatDelta)
			if readErr != nil {
				broken.Body = io.NopCloser(io.MultiReader(strings.NewReader(pelicanChatDelta), pelicanReadError{readErr}))
			}
			svc, upstream := pelicanMockService(pelicanChatAccount(), broken, pelicanMockResponse("<html>final</html>", "retry-model", true))
			result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
			require.Equal(t, "success", result.Status, result.Reason)
			require.Equal(t, "<html>final</html>", result.Artwork)
			require.Equal(t, "<html>final</html>", result.RawOutput)
			require.Equal(t, "retry-model", result.ModelID)
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestPelicanChatGenerationRetriesAtMostFiveTimes(t *testing.T) {
	responses := make([]*http.Response, 7)
	for i := range responses {
		responses[i] = newJSONResponse(http.StatusOK, pelicanChatDelta)
	}
	svc, upstream := pelicanMockService(pelicanChatAccount(), responses...)
	result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "missing_completion", result.Diagnostics.FailureCode)
	require.Equal(t, "<html>partial", result.RawOutput)
	require.Empty(t, result.Artwork)
	require.Len(t, upstream.requests, 6)
	require.Len(t, upstream.responses, 1)
}

func TestPelicanChatGenerationDoesNotRetryPermanentFailures(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		svc, upstream := pelicanMockService(pelicanChatAccount(), newJSONResponse(status, "private upstream payload"))
		result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
		require.Equal(t, "failed", result.Status)
		require.Len(t, upstream.requests, 1)
		require.NotContains(t, result.Reason, "private")
	}
}

func TestPelicanChatGenerationDoesNotRetryInvalidHTML(t *testing.T) {
	svc, upstream := pelicanMockService(pelicanChatAccount(), pelicanMockResponse("<html>unfinished", "model", true))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "<html>unfinished", result.RawOutput)
	require.Len(t, upstream.requests, 1)
}

func TestPelicanRetryWaitStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, pelicanRetryWait(ctx, 1), context.Canceled)
}

func TestPelicanChatGenerationTriesOAuthWithoutResponsesFallback(t *testing.T) {
	account := pelicanChatAccount()
	account.Type = AccountTypeOAuth
	delete(account.Credentials, "api_key")
	account.Credentials["access_token"] = "mock-access-token"
	svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>ok</html>", "actual", true))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
	require.Equal(t, "success", result.Status, result.Reason)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
	require.Equal(t, "Bearer mock-access-token", upstream.requests[0].Header.Get("Authorization"))
}

func TestPelicanChatFailureClassification(t *testing.T) {
	for _, tc := range []struct{ message, code string }{
		{"Chat Completions stream from /v1/chat/completions ended before [DONE]", "missing_completion"},
		{"Chat Completions stream read error from /v1/chat/completions: unexpected EOF", "incomplete_stream"},
		{"Invalid Chat Completions response from /v1/chat/completions: expected JSON data", "invalid_stream"},
		{"Chat Completions API (/v1/chat/completions) returned 502: private", "upstream_failure"},
	} {
		require.Equal(t, tc.code, pelicanRequestFailureCode(errors.New(tc.message)))
	}
}

func TestPelicanCNGenerationAlwaysUsesChatCompletions(t *testing.T) {
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolAdaptive, APIProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			account := adaptiveCNAccountTestAccount(901, PlatformKimi)
			account.Status, account.Schedulable = StatusActive, true
			account.Credentials["api_protocol"] = protocol
			account.Credentials["base_url"] = "http://cn-test.example/v1"
			svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>final</html>", "actual-model", true))
			result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
			require.Equal(t, "success", result.Status, result.Reason)
			require.Nil(t, result.Score)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
		})
	}
}

type pelicanCancelReader struct{ cancel context.CancelFunc }

func (r pelicanCancelReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.ErrUnexpectedEOF
}

func TestPelicanChatGenerationCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broken := newJSONResponse(http.StatusOK, "")
	broken.Body = io.NopCloser(io.MultiReader(strings.NewReader(pelicanChatDelta), pelicanCancelReader{cancel}))
	svc, upstream := pelicanMockService(pelicanChatAccount(), broken)
	result := svc.runPelicanQualityCheck(ctx, 901, "requested")
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "generation_canceled", result.Diagnostics.FailureCode)
	require.Equal(t, "parent_context", result.Diagnostics.DeadlineSource)
	require.Equal(t, "<html>partial", result.RawOutput)
	require.Len(t, upstream.requests, 1)
}

func TestPelicanChatGenerationReadsAllChunks(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"<html></html>\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" trailing output\"}}]}\n\ndata: [DONE]\n\n"
	svc, upstream := pelicanMockService(pelicanChatAccount(), newJSONResponse(http.StatusOK, stream))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
	require.Equal(t, "success", result.Status)
	require.Equal(t, "<html></html>", result.Artwork)
	require.Equal(t, "<html></html> trailing output", result.RawOutput)
	require.Len(t, upstream.requests, 1)
}

func TestQualityResultStatusDoesNotDependOnScore(t *testing.T) {
	for _, status := range []string{"success", "degraded", "failed", "unknown"} {
		for _, value := range []float64{1, 100} {
			result := &GroupQualityResult{Status: status, Reason: "saved reason", Score: &value}
			applyQualityStatusVerdict(result)
			require.Equal(t, status, result.Status)
			require.Equal(t, "saved reason", result.Reason)
			if status == "success" || status == "degraded" {
				require.Equal(t, status == "success", *result.Passed)
				require.Equal(t, status == "degraded", *result.IsDegraded)
			} else {
				require.Nil(t, result.Passed)
				require.Nil(t, result.IsDegraded)
			}
		}
	}
}

func TestPelicanChatGenerationRetriesStreamUpstreamError(t *testing.T) {
	for _, errorType := range []string{"upstream_error", "server_error", "overloaded_error", "rate_limit_error"} {
		t.Run(errorType, func(t *testing.T) {
			broken := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"error\":{\"type\":\"" + errorType + "\",\"message\":\"Our servers are currently overloaded. private-secret\"}}\n\n"
			svc, upstream := pelicanMockService(pelicanChatAccount(), newJSONResponse(http.StatusOK, broken),
				pelicanMockResponse("<html>retry</html>", "retry-model", true))
			result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
			require.Equal(t, "success", result.Status, result.Reason)
			require.Equal(t, "<html>retry</html>", result.Artwork)
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestPelicanChatGenerationDoesNotRetryPermanentStreamError(t *testing.T) {
	broken := "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"private-secret\"}}\n\n"
	svc, upstream := pelicanMockService(pelicanChatAccount(), newJSONResponse(http.StatusOK, broken))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
	require.Equal(t, "failed", result.Status)
	require.Len(t, upstream.requests, 1)
	require.NotContains(t, result.Reason, "private")
}
