package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const qualityModelNotAllowedReason = "Configured quality model is not allowed by this account"

// Resolve the requested model before checking the account's allowlist. Mapping
// to the upstream model happens only after this check, exactly once per request.
func qualityRequestedModel(account *Account, configured string) string {
	if model := strings.TrimSpace(configured); model != "" {
		return model
	}
	if account.IsGemini() {
		return geminicli.DefaultTestModel
	}
	if model := account.providerDefaultTestModel(); model != "" {
		return model
	}
	return openai.DefaultTestModel
}

func accountAllowsQualityModel(account *Account, configured string) bool {
	if !accountAcceptsQualityPrompt(account) {
		return false
	}
	// No explicit restrictions keeps quality generation permissive. Do not add
	// platform-family or model-name exclusions when an admin has not configured them.
	configuredMapping, _ := account.Credentials["model_mapping"].(map[string]any)
	if len(configuredMapping) == 0 {
		return true
	}
	// Reuse the account policy for wildcards, normalized aliases and its explicit
	// passthrough mode. That mode intentionally ignores stale stored restrictions.
	return account.IsModelSupported(qualityRequestedModel(account, configured))
}
