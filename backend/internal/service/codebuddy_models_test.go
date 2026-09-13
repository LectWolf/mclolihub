package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/stretchr/testify/require"
)

func codeBuddyModelIDs(models []codebuddy.CatalogModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func codeBuddyAccountWithWhitelist(catalog []codebuddy.CatalogModel, mapping map[string]any) *Account {
	account := codeBuddyTestAccount(catalog, nil)
	account.Credentials = map[string]any{"model_mapping": mapping}
	return account
}

func TestCodeBuddyAvailableModelsFallsBackWhenNothingIsKnown(t *testing.T) {
	account := codeBuddyTestAccount(nil, nil)
	require.Equal(t, codeBuddyFallbackModelIDs, codeBuddyModelIDs(account.CodeBuddyAvailableModels()))
}

func TestCodeBuddyAvailableModelsUsesCatalogWithoutWhitelist(t *testing.T) {
	account := codeBuddyTestAccount([]codebuddy.CatalogModel{
		{ID: "glm-5.2", Credits: "x1.00"},
		{ID: "auto", Credits: "x0.00"},
	}, nil)
	require.Equal(t, []string{"glm-5.2", "auto"}, codeBuddyModelIDs(account.CodeBuddyAvailableModels()))
}

// The whitelist is the operator's decision. Widening it back to the full catalog
// when an entry happens to be unsynced would silently expose models they
// deliberately excluded.
func TestCodeBuddyAvailableModelsHonoursWhitelistEvenWhenUnsynced(t *testing.T) {
	account := codeBuddyAccountWithWhitelist(
		[]codebuddy.CatalogModel{{ID: "glm-5.2", Credits: "x1.00"}, {ID: "auto", Credits: "x0.00"}},
		map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"},
	)
	require.Equal(t, []string{"deepseek-v4.1-flash"}, codeBuddyModelIDs(account.CodeBuddyAvailableModels()))
}

func TestCodeBuddyAvailableModelsWhitelistCarriesCatalogCredits(t *testing.T) {
	account := codeBuddyAccountWithWhitelist(
		[]codebuddy.CatalogModel{{ID: "glm-5.2", Name: "GLM 5.2", Credits: "x1.50", Aliases: []string{"glm"}}},
		map[string]any{"glm-5.2": "glm-5.2"},
	)
	models := account.CodeBuddyAvailableModels()
	require.Len(t, models, 1)
	require.Equal(t, "x1.50", models[0].Credits)
	require.Equal(t, "GLM 5.2", models[0].Name)
	// Only the whitelist key is accepted on the wire, so advertising the target's
	// aliases would invite requests the mapping cannot resolve.
	require.Empty(t, models[0].Aliases)
}

// A renaming mapping advertises the client-facing key while pricing it from the
// upstream target it resolves to.
func TestCodeBuddyAvailableModelsAdvertisesMappingKeys(t *testing.T) {
	account := codeBuddyAccountWithWhitelist(
		[]codebuddy.CatalogModel{{ID: "glm-5.2", Credits: "x2.00"}},
		map[string]any{"gpt-4o": "glm-5.2"},
	)
	models := account.CodeBuddyAvailableModels()
	require.Equal(t, []string{"gpt-4o"}, codeBuddyModelIDs(models))
	require.Equal(t, "x2.00", models[0].Credits)
}

func TestCodeBuddyAvailableModelsSkipsWildcardWhitelistEntries(t *testing.T) {
	catalog := []codebuddy.CatalogModel{{ID: "glm-5.2", Credits: "x1.00"}}
	wildcardOnly := codeBuddyAccountWithWhitelist(catalog, map[string]any{"*": "glm-5.2"})
	// Nothing concrete was whitelisted, so the catalog stands in rather than the
	// pattern being advertised as a model ID.
	require.Equal(t, []string{"glm-5.2"}, codeBuddyModelIDs(wildcardOnly.CodeBuddyAvailableModels()))

	mixed := codeBuddyAccountWithWhitelist(catalog, map[string]any{"*": "glm-5.2", "auto": "auto"})
	require.Equal(t, []string{"auto"}, codeBuddyModelIDs(mixed.CodeBuddyAvailableModels()))
}

func TestCodeBuddyAvailableModelsOrderIsStable(t *testing.T) {
	account := codeBuddyAccountWithWhitelist(nil, map[string]any{
		"zeta": "zeta", "alpha": "alpha", "mid": "mid",
	})
	for range 5 {
		require.Equal(t, []string{"alpha", "mid", "zeta"}, codeBuddyModelIDs(account.CodeBuddyAvailableModels()))
	}
}

// The credit policy is a rail, not a source: it may drop whitelisted models but
// must stay consistent with what the request path would deny.
func TestCodeBuddyAvailableModelsCreditPolicyTrimsWhitelist(t *testing.T) {
	account := codeBuddyAccountWithWhitelist(
		[]codebuddy.CatalogModel{{ID: "glm-5.2", Credits: "x1.00"}, {ID: "auto", Credits: "x0.00"}},
		map[string]any{"glm-5.2": "glm-5.2", "auto": "auto"},
	)
	account.Credentials[codeBuddyCreditPolicyKey] = codebuddy.CreditPolicyZeroOnly
	require.Equal(t, []string{"auto"}, codeBuddyModelIDs(account.CodeBuddyAvailableModels()))
}

type codeBuddyGroupAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *codeBuddyGroupAccountRepo) ListSchedulableByGroupID(_ context.Context, _ int64) ([]Account, error) {
	return r.accounts, nil
}

func TestCodeBuddyGroupModelsMergesAccountsAndSkipsOtherPlatforms(t *testing.T) {
	first := *codeBuddyTestAccount([]codebuddy.CatalogModel{
		{ID: "glm-5.2", Credits: "x1.00"},
		{ID: "auto", Credits: "x0.00"},
	}, nil)
	second := *codeBuddyTestAccount([]codebuddy.CatalogModel{
		{ID: "auto", Credits: "x0.00"},
		{ID: "kimi-k2.7", Credits: "x0.50"},
	}, nil)
	other := Account{ID: 99, Platform: PlatformOpenAI}

	groupID := int64(1)
	svc := &GatewayService{accountRepo: &codeBuddyGroupAccountRepo{accounts: []Account{first, second, other}}}
	models := svc.CodeBuddyGroupModels(context.Background(), &groupID)

	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	require.Equal(t, []string{"auto", "glm-5.2", "kimi-k2.7"}, ids, "sorted and de-duplicated")
	for _, model := range models {
		require.Equal(t, "codebuddy", model.OwnedBy)
	}
	// The multiplier is the price signal that matters on CodeBuddy, so it must
	// survive into the advertised label.
	require.Contains(t, models[1].DisplayName, "x1.00")
}
