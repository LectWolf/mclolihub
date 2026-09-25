package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccount_IsExcelBPSEnabled(t *testing.T) {
	parent := int64(9)
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{"nil", nil, false},
		{"oauth enabled", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_excel_bps": true}}, true},
		{"oauth disabled", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_excel_bps": false}}, false},
		{"missing", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, false},
		{"apikey rejected", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_excel_bps": true}}, false},
		{"agent identity rejected", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModeAgentIdentity}, Extra: map[string]any{"openai_excel_bps": true}}, false},
		{"shadow rejected", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent, Extra: map[string]any{"openai_excel_bps": true}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.IsExcelBPSEnabled())
		})
	}
}

func TestAccount_IsExcelBPSEnabledForModel(t *testing.T) {
	all := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_excel_bps": true}}
	require.True(t, all.IsExcelBPSEnabledForModel("gpt-6-astra"))

	scoped := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"openai_excel_bps":        true,
		"openai_excel_bps_models": []any{"gpt-6-astra"},
	}}
	require.True(t, scoped.IsExcelBPSEnabledForModel("gpt-6-astra"))
	require.False(t, scoped.IsExcelBPSEnabledForModel("gpt-6-sol"))
}
