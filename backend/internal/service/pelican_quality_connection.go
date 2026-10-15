package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

// A quality run tries the configured model through Chat Completions. Platform,
// account type and model-name heuristics must not decide whether it can work.
func (s *AccountTestService) testQualityChatConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	if account == nil {
		return s.sendErrorAndEnd(c, "Account unavailable for quality generation")
	}
	if account.IsSyntheticUITest() {
		return s.sendErrorAndEnd(c, "Synthetic UI accounts do not call upstream models")
	}
	credentialAccount := account
	if account.IsCredentialShadow() {
		resolved, err := resolveCredentialAccount(c.Request.Context(), s.accountRepo, account)
		if err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		credentialAccount = resolved
	}
	if account.IsGemini() {
		// Keep Gemini's explicit native-base to OpenAI-compatible URL adaptation.
		// Use the selected account's mapping with the resolved credentials.
		if credentialAccount != account {
			copyAccount := *account
			copyAccount.Credentials = make(map[string]any, len(credentialAccount.Credentials))
			for key, value := range credentialAccount.Credentials {
				copyAccount.Credentials[key] = value
			}
			if mapping, ok := account.Credentials["model_mapping"]; ok {
				copyAccount.Credentials["model_mapping"] = mapping
			} else {
				delete(copyAccount.Credentials, "model_mapping")
			}
			return s.testGeminiQualityChatCompletionsConnection(c, &copyAccount, modelID, prompt)
		}
		return s.testGeminiQualityChatCompletionsConnection(c, account, modelID, prompt)
	}
	requestedModel := strings.TrimSpace(modelID)
	if requestedModel == "" {
		requestedModel = account.providerDefaultTestModel()
	}
	if requestedModel == "" {
		requestedModel = openai.DefaultTestModel
	}
	model := resolveAccountTestModel(c.Request.Context(), account, requestedModel)
	if strings.TrimSpace(model) == "" {
		return s.sendErrorAndEnd(c, "No model configured for quality generation")
	}
	token := qualityChatCredential(credentialAccount)
	if token == "" {
		return s.sendErrorAndEnd(c, "No credentials available for quality generation")
	}
	baseURL := strings.TrimSpace(credentialAccount.GetCredential("base_url"))
	if bases, ok := credentialAccount.Credentials["api_base_urls"].(map[string]any); ok {
		if chatBase, ok := bases[APIProtocolChatCompletions].(string); ok && strings.TrimSpace(chatBase) != "" {
			baseURL = strings.TrimSpace(chatBase)
		}
	}
	if baseURL == "" {
		baseURL = credentialAccount.GetOpenAIBaseURL()
	}
	if baseURL == "" {
		baseURL = credentialAccount.defaultProviderBaseURL(APIProtocolChatCompletions)
	}
	if baseURL == "" && credentialAccount.Platform == PlatformGrok {
		baseURL = credentialAccount.GetGrokBaseURL()
	}
	if strings.TrimSpace(baseURL) == "" {
		return s.sendErrorAndEnd(c, "No Chat Completions endpoint configured for quality generation")
	}
	normalized, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
	}
	return s.testOpenAIChatCompletionsConnection(c, account, model, prompt, normalized, token)
}

func qualityChatCredential(account *Account) string {
	for _, field := range []string{"api_key", "access_token"} {
		if token := strings.TrimSpace(account.GetCredential(field)); token != "" {
			return token
		}
	}
	return ""
}
