package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/stretchr/testify/require"
)

// codeBuddyExtraRepo records Extra merges. The gateway writes them from a
// detached goroutine, so access is mutex-guarded and assertions poll.
type codeBuddyExtraRepo struct {
	AccountRepository
	mu      sync.Mutex
	updates map[string]any
}

func (r *codeBuddyExtraRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updates == nil {
		r.updates = map[string]any{}
	}
	for key, value := range updates {
		r.updates[key] = value
	}
	return nil
}

// awaitKeys waits for the detached writer to merge every expected key and
// returns the keys it has seen, so callers can also assert on absent ones.
func (r *codeBuddyExtraRepo) awaitKeys(t *testing.T, expected ...string) map[string]any {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, key := range expected {
			if _, ok := r.updates[key]; !ok {
				return false
			}
		}
		return true
	}, time.Second, 5*time.Millisecond)

	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]any, len(r.updates))
	for key, value := range r.updates {
		seen[key] = value
	}
	return seen
}

func codeBuddyTestAccount(models []codebuddy.CatalogModel, credits *codebuddy.CreditsSnapshot) *Account {
	account := &Account{
		ID:       7,
		Platform: PlatformCodeBuddy,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			codeBuddyCatalogExtraKey: CodeBuddyCatalogSnapshot{Models: models},
		},
	}
	if credits != nil {
		account.Extra[codeBuddyCreditsExtraKey] = credits
	}
	return account
}

func TestRecordCodeBuddyCreditsUsageChargesMultiplierAndDrawsDownBalance(t *testing.T) {
	repo := &codeBuddyExtraRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := codeBuddyTestAccount(
		[]codebuddy.CatalogModel{{ID: "glm-5.2", Credits: "x1.50"}},
		&codebuddy.CreditsSnapshot{Credits: 10, Segments: []codebuddy.CreditSegment{{Remaining: 10, Total: 10}}},
	)

	svc.recordCodeBuddyCreditsUsage(context.Background(), account, "glm-5.2")

	usage := account.GetCodeBuddyCreditsUsage()
	require.Equal(t, int64(1), usage.Requests)
	require.Equal(t, 1.5, usage.Credits)
	require.Zero(t, usage.Unpriced)

	snapshot := account.GetCodeBuddyCredits()
	require.NotNil(t, snapshot)
	require.Equal(t, 8.5, snapshot.Credits)
	require.True(t, snapshot.Estimated)

	written := repo.awaitKeys(t, codeBuddyCreditsUsageExtraKey, codeBuddyCreditsExtraKey)
	require.Len(t, written, 2)
}

func TestRecordCodeBuddyCreditsUsageLeavesBalanceAloneForFreeModels(t *testing.T) {
	repo := &codeBuddyExtraRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := codeBuddyTestAccount(
		[]codebuddy.CatalogModel{{ID: "auto", Credits: "x0.00"}},
		&codebuddy.CreditsSnapshot{Credits: 10},
	)

	svc.recordCodeBuddyCreditsUsage(context.Background(), account, "auto")

	require.Equal(t, int64(1), account.GetCodeBuddyCreditsUsage().Requests)
	require.Equal(t, 10.0, account.GetCodeBuddyCredits().Credits)

	written := repo.awaitKeys(t, codeBuddyCreditsUsageExtraKey)
	require.NotContains(t, written, codeBuddyCreditsExtraKey)
}

func TestRecordCodeBuddyCreditsUsageFlagsUnpricedModels(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: &codeBuddyExtraRepo{}}
	account := codeBuddyTestAccount([]codebuddy.CatalogModel{{ID: "auto", Credits: "x0.00"}}, nil)

	svc.recordCodeBuddyCreditsUsage(context.Background(), account, "model-not-in-catalog")

	usage := account.GetCodeBuddyCreditsUsage()
	require.Equal(t, int64(1), usage.Requests)
	require.Equal(t, int64(1), usage.Unpriced)
	require.Zero(t, usage.Credits)
}

func TestMarkCodeBuddyCreditsExhaustedZeroesCachedBalance(t *testing.T) {
	repo := &codeBuddyExtraRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := codeBuddyTestAccount(nil, &codebuddy.CreditsSnapshot{
		Credits:  42,
		Segments: []codebuddy.CreditSegment{{Remaining: 42, Total: 50}},
	})

	svc.markCodeBuddyCreditsExhausted(context.Background(), account)

	snapshot := account.GetCodeBuddyCredits()
	require.NotNil(t, snapshot)
	require.Zero(t, snapshot.Credits)
	require.True(t, snapshot.Exhausted)
	require.Zero(t, snapshot.Segments[0].Remaining)
	repo.awaitKeys(t, codeBuddyCreditsExtraKey)
}

func TestCodeBuddyModelAllowedUnderZeroCreditPolicy(t *testing.T) {
	catalog := []codebuddy.CatalogModel{
		{ID: "auto", Credits: "x0.00"},
		{ID: "glm-5.2", Credits: "x1.00", Aliases: []string{"glm"}},
	}
	account := codeBuddyTestAccount(catalog, nil)
	account.Credentials = map[string]any{codeBuddyCreditPolicyKey: codebuddy.CreditPolicyZeroOnly}

	require.True(t, codeBuddyModelAllowed(account, "auto"))
	require.False(t, codeBuddyModelAllowed(account, "glm-5.2"))
	// Aliases must be gated too, or the policy is bypassed by renaming the model.
	require.False(t, codeBuddyModelAllowed(account, "glm"))
	// An unknown model stays blocked while the catalog is known.
	require.False(t, codeBuddyModelAllowed(account, "surprise"))

	empty := codeBuddyTestAccount(nil, nil)
	empty.Credentials = account.Credentials
	require.True(t, codeBuddyModelAllowed(empty, "surprise"), "no catalog means nothing to enforce against")
}
