//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPelicanQualityCallsConfiguredModelWithoutAccountLabels(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini, PlatformAnthropic, PlatformKimi, PlatformGrok, PlatformOpenCodeGo, PlatformQoder, "custom-provider"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeServiceAccount, AccountTypeUpstream} {
			t.Run(platform+"/"+kind, func(t *testing.T) {
				account := pelicanChatAccount()
				account.Platform = platform
				account.Type = kind
				account.Credentials["api_protocol"] = APIProtocolAnthropic
				account.Credentials["model_mapping"] = map[string]any{"alias": "custom-image-video-tts-embedding-model", "custom-image-video-tts-embedding-model": "wrong-second-mapping"}
				if kind == AccountTypeOAuth {
					delete(account.Credentials, "api_key")
					account.Credentials["access_token"] = "mock-secret"
				}
				svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>ok</html>", "actual", true))
				result := svc.runPelicanQualityCheck(context.Background(), account.ID, "alias")
				require.Equal(t, "success", result.Status, result.Reason)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
				requirePelicanStudioRequest(t, upstream.requests[0], "custom-image-video-tts-embedding-model", DefaultScheduledTestPrompt)
			})
		}
	}
}

func TestPelicanQualityRecordsActualUpstreamRejectionInsteadOfSkipping(t *testing.T) {
	account := pelicanChatAccount()
	account.Platform = "custom-provider"
	account.Type = AccountTypeOAuth
	account.Credentials["model_mapping"] = map[string]any{"alias": "custom-image-model"}
	svc, upstream := pelicanMockService(account, newJSONResponse(http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"This endpoint cannot run the selected model"}}`))
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "alias")
	require.Equal(t, "failed", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 1, result.Diagnostics.AttemptCount)
	require.Equal(t, 400, result.Diagnostics.Attempts[0].HTTPStatus)
	require.Equal(t, "invalid_request_error", result.Diagnostics.Attempts[0].ErrorType)
	require.Contains(t, result.Diagnostics.Attempts[0].Message, "cannot run the selected model")
}

func TestPelicanQualityCredentialShadowMapsSelectedModelOnce(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			parent := pelicanChatAccount()
			parent.ID = 902
			parent.Type = AccountTypeOAuth
			parent.Credentials["model_mapping"] = map[string]any{"resolved-image-model": "wrong-parent-mapping"}
			selected := &Account{ID: 901, Platform: platform, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "resolved-image-model", "resolved-image-model": "wrong-second-mapping"}}}
			svc, upstream := pelicanMockService(selected, pelicanMockResponse("<html>ok</html>", "actual", true))
			svc.accountRepo.(*openAIAccountTestRepo).accountsByID[parent.ID] = parent
			result := svc.runPelicanQualityCheck(context.Background(), selected.ID, "alias")
			require.Equal(t, "success", result.Status, result.Reason)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, selected.ID, result.Diagnostics.Route.AccountID)
			requirePelicanStudioRequest(t, upstream.requests[0], "resolved-image-model", DefaultScheduledTestPrompt)
			require.Equal(t, "wrong-parent-mapping", parent.GetMappedModel("resolved-image-model"), "credentials must not be mutated")
		})
	}
}

func TestPelicanQualityMissingEndpointDoesNotGuessAnotherProvider(t *testing.T) {
	account := pelicanChatAccount()
	account.Platform = "custom-provider"
	delete(account.Credentials, "base_url")
	svc, upstream := pelicanMockService(account)
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "custom-model")
	require.Equal(t, "failed", result.Status)
	require.Empty(t, upstream.requests)
	require.Equal(t, 1, result.Diagnostics.AttemptCount)
	require.Contains(t, result.Diagnostics.Attempts[0].Message, "No Chat Completions endpoint configured")
}

func TestPelicanQualityShadowFailureKeepsSelectedAccountIdentity(t *testing.T) {
	parent := pelicanChatAccount()
	parent.ID = 902
	parent.Type = AccountTypeOAuth
	selected := &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID}
	svc, upstream := pelicanMockService(selected, newJSONResponse(http.StatusUnauthorized, `{"error":{"type":"authentication_error","message":"Expired token"}}`))
	repo := svc.accountRepo.(*openAIAccountTestRepo)
	repo.accountsByID[parent.ID] = parent
	result := svc.runPelicanQualityCheck(context.Background(), selected.ID, "model")
	require.Equal(t, "failed", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, selected.ID, repo.setErrorID)
	require.NotEqual(t, parent.ID, repo.setErrorID)
	require.Equal(t, selected.ID, result.Diagnostics.Route.AccountID)
	require.Equal(t, 401, result.Diagnostics.Attempts[0].HTTPStatus)
}
