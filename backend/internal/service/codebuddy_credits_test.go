package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/gin-gonic/gin"
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

func TestCodeBuddyCreditPolicyDenialUnderZeroCreditPolicy(t *testing.T) {
	catalog := []codebuddy.CatalogModel{
		{ID: "auto", Credits: "x0.00"},
		{ID: "glm-5.2", Credits: "x1.00", Aliases: []string{"glm"}},
	}
	account := codeBuddyTestAccount(catalog, nil)
	account.Credentials = map[string]any{codeBuddyCreditPolicyKey: codebuddy.CreditPolicyZeroOnly}

	require.Empty(t, codeBuddyCreditPolicyDenial(account, "auto"))

	priced := codeBuddyCreditPolicyDenial(account, "glm-5.2")
	require.NotEmpty(t, priced)
	require.Contains(t, priced, "x1.00", "a priced model's denial should name its cost")

	// Aliases must be gated too, or the policy is bypassed by renaming the model.
	require.NotEmpty(t, codeBuddyCreditPolicyDenial(account, "glm"))

	// An unsynced model fails closed, but the message must point at the catalog
	// rather than claiming the model is priced.
	absent := codeBuddyCreditPolicyDenial(account, "deepseek-v4.1-flash")
	require.Contains(t, absent, "missing from this CodeBuddy account's synced catalog")
	require.NotContains(t, absent, "costs")

	empty := codeBuddyTestAccount(nil, nil)
	empty.Credentials = account.Credentials
	require.Empty(t, codeBuddyCreditPolicyDenial(empty, "surprise"), "no catalog means nothing to enforce against")
}

func TestCodeBuddyCreditPolicyDenialAllowsEverythingUnderDefaultPolicy(t *testing.T) {
	account := codeBuddyTestAccount([]codebuddy.CatalogModel{{ID: "glm-5.2", Credits: "x9.00"}}, nil)
	require.Empty(t, codeBuddyCreditPolicyDenial(account, "glm-5.2"))
	require.Empty(t, codeBuddyCreditPolicyDenial(account, "anything"))
}

// A local denial writes the whole client-facing body itself. Without the
// committed marker the handler appends its own "Upstream request failed"
// fallback, so the client receives two stacked errors.
func TestLocalErrorWritersMarkResponseCommitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, write := range map[string]func(*gin.Context){
		"chat_completions": func(c *gin.Context) {
			writeChatCompletionsError(c, http.StatusNotFound, "invalid_request_error", "denied")
		},
		"anthropic_messages": func(c *gin.Context) {
			writeAnthropicError(c, http.StatusNotFound, "invalid_request_error", "denied")
		},
		"responses_fallback": func(c *gin.Context) {
			writeOpenAIResponsesFallbackError(c, http.StatusNotFound, "invalid_request_error", "denied")
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			write(c)
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, http.StatusNotFound, recorder.Code)
			require.Contains(t, recorder.Body.String(), "denied")
		})
	}
}
