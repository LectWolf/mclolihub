package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

func codeBuddyChatCompletionsURL(account *Account) (string, error) {
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return "", fmt.Errorf("invalid codebuddy credentials: %w", err)
	}
	return codebuddy.ChatCompletionsURL(creds.Profile), nil
}

func applyCodeBuddyUpstreamHeaders(header http.Header, account *Account, accessToken string) {
	if account == nil || header == nil {
		return
	}
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return
	}
	if strings.TrimSpace(accessToken) != "" {
		creds.AccessToken = accessToken
	}
	for key, value := range codebuddy.CredentialHeaders(creds.Profile, creds.AccessToken, creds.Domain, creds.UID, creds.EnterpriseID) {
		header.Set(key, value)
	}
}

func prepareCodeBuddyChatBody(body []byte) ([]byte, error) {
	return codebuddy.EnsureLeadingSystemMessage(body)
}

// codeBuddyRequestCredits resolves what one upstream call costs. CodeBuddy bills
// per request against the catalog multiplier instead of per token, so an "x0"
// model is genuinely free; the boolean separates that from a model the catalog
// never priced.
func codeBuddyRequestCredits(account *Account, upstreamModel string) (float64, bool) {
	if account == nil || !account.IsCodeBuddy() {
		return 0, false
	}
	return codebuddy.ModelMultiplier(account.GetCodeBuddyCatalog(), upstreamModel)
}

// recordCodeBuddyCreditsUsage folds a served request into the account's credit
// tally and draws the same amount out of the cached balance, so the admin UI
// tracks burn rate without probing the billing API on every request. The probe
// remains authoritative and overwrites the estimate whenever it runs.
func (s *OpenAIGatewayService) recordCodeBuddyCreditsUsage(ctx context.Context, account *Account, upstreamModel string) {
	if s == nil || account == nil || account.ID <= 0 || !account.IsCodeBuddy() {
		return
	}
	credits, priced := codeBuddyRequestCredits(account, upstreamModel)
	updates := map[string]any{
		codeBuddyCreditsUsageExtraKey: account.GetCodeBuddyCreditsUsage().Charge(credits, priced, time.Now()),
	}
	if credits > 0 {
		if snapshot := account.GetCodeBuddyCredits(); snapshot != nil {
			snapshot.Deduct(credits)
			updates[codeBuddyCreditsExtraKey] = snapshot
		}
	}
	s.persistCodeBuddyExtra(ctx, account, updates)
}

// markCodeBuddyCreditsExhausted records that the upstream refused a call for
// lack of credits. A cached positive balance would otherwise keep the account
// looking healthy until the next probe.
func (s *OpenAIGatewayService) markCodeBuddyCreditsExhausted(ctx context.Context, account *Account) {
	if s == nil || account == nil || account.ID <= 0 || !account.IsCodeBuddy() {
		return
	}
	snapshot := account.GetCodeBuddyCredits()
	if snapshot == nil {
		snapshot = &codebuddy.CreditsSnapshot{}
	}
	if snapshot.Exhausted && snapshot.Credits == 0 {
		return
	}
	snapshot.Credits = 0
	snapshot.Exhausted = true
	snapshot.Estimated = true
	for i := range snapshot.Segments {
		snapshot.Segments[i].Remaining = 0
	}
	snapshot.SoonestExpiry = nil
	logger.L().Warn("codebuddy upstream reported exhausted credits",
		zap.Int64("account_id", account.ID),
		zap.String("account_name", account.Name),
	)
	s.persistCodeBuddyExtra(ctx, account, map[string]any{codeBuddyCreditsExtraKey: snapshot})
}

// persistCodeBuddyExtra mirrors the updates onto the request-scoped account copy
// and merges them into the stored Extra.
//
// The in-memory half is synchronous so callers observe their own write, while
// the database write runs detached: it sits in front of the first upstream byte
// and must not add latency there. Both halves are best effort — losing a tally
// update must never fail a request the upstream already served, and the billing
// probe reconciles the balance either way.
func (s *OpenAIGatewayService) persistCodeBuddyExtra(ctx context.Context, account *Account, updates map[string]any) {
	if len(updates) == 0 {
		return
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any, len(updates))
	}
	for key, value := range updates {
		account.Extra[key] = value
	}
	if s.accountRepo == nil {
		return
	}
	accountID := account.ID
	// Detached from the request context: the client may abort a stream while the
	// charge has already been incurred upstream.
	stateCtx, cancel := openAIAccountStateContext(ctx)
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				logger.L().Error("codebuddy credits extra update panicked",
					zap.Int64("account_id", accountID),
					zap.Any("recover", r),
				)
			}
		}()
		if err := s.accountRepo.UpdateExtra(stateCtx, accountID, updates); err != nil {
			logger.L().Debug("codebuddy credits extra update failed",
				zap.Int64("account_id", accountID),
				zap.Error(err),
			)
		}
	}()
}
