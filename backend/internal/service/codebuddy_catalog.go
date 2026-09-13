package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"go.uber.org/zap"
)

const (
	codeBuddyCatalogExtraKey      = "codebuddy_catalog"
	codeBuddyCreditsExtraKey      = "codebuddy_credits"
	codeBuddyCreditsUsageExtraKey = "codebuddy_credits_usage"
	codeBuddyRequestUsageExtraKey = "codebuddy_request_usage"
	codeBuddyCreditPolicyKey      = "credit_policy"
	codeBuddyUpstreamTimeout      = 15 * time.Second
	// codeBuddyCreditsSnapshotTTL is how long a billing probe stays trustworthy.
	// Past it the cached balance is only a hint and the admin UI flags it stale.
	codeBuddyCreditsSnapshotTTL = 10 * time.Minute
)

type CodeBuddyCatalogSnapshot struct {
	SyncedAt string                   `json:"synced_at"`
	Models   []codebuddy.CatalogModel `json:"models"`
}

func (a *Account) CodeBuddyCreditPolicy() string {
	if a == nil {
		return codebuddy.CreditPolicyAll
	}
	return codebuddy.NormalizeCreditPolicy(a.GetCredential(codeBuddyCreditPolicyKey))
}

func (a *Account) GetCodeBuddyCatalog() []codebuddy.CatalogModel {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[codeBuddyCatalogExtraKey]
	if !ok || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot CodeBuddyCatalogSnapshot
	if json.Unmarshal(body, &snapshot) == nil && len(snapshot.Models) > 0 {
		return snapshot.Models
	}
	var models []codebuddy.CatalogModel
	if json.Unmarshal(body, &models) == nil {
		return models
	}
	return nil
}

// GetCodeBuddyCredits decodes the last billing probe stored on the account. The
// Extra map arrives either as freshly typed structs (same-request writes) or as
// generic maps (JSONB decode), so both shapes route through JSON.
func (a *Account) GetCodeBuddyCredits() *codebuddy.CreditsSnapshot {
	var snapshot codebuddy.CreditsSnapshot
	if !decodeCodeBuddyExtra(a, codeBuddyCreditsExtraKey, &snapshot) {
		return nil
	}
	return &snapshot
}

// GetCodeBuddyCreditsUsage decodes the gateway-side credit tally, returning a
// zero tally when the account has not served a request yet.
func (a *Account) GetCodeBuddyCreditsUsage() codebuddy.CreditsUsage {
	var usage codebuddy.CreditsUsage
	decodeCodeBuddyExtra(a, codeBuddyCreditsUsageExtraKey, &usage)
	return usage
}

// GetCodeBuddyRequestUsage decodes the last official consumption report.
func (a *Account) GetCodeBuddyRequestUsage() *codebuddy.RequestUsage {
	var usage codebuddy.RequestUsage
	if !decodeCodeBuddyExtra(a, codeBuddyRequestUsageExtraKey, &usage) {
		return nil
	}
	return &usage
}

// CodeBuddyCreditsStale reports whether the cached balance has aged past the
// probe TTL, which the admin UI surfaces instead of pretending it is live.
func (a *Account) CodeBuddyCreditsStale() bool {
	snapshot := a.GetCodeBuddyCredits()
	if snapshot == nil || snapshot.FetchedAt <= 0 {
		return true
	}
	return time.Since(time.Unix(snapshot.FetchedAt, 0)) > codeBuddyCreditsSnapshotTTL
}

func decodeCodeBuddyExtra(account *Account, key string, out any) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	raw, ok := account.Extra[key]
	if !ok || raw == nil {
		return false
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(body, out) == nil
}

// codeBuddyFallbackModelIDs is advertised only when nothing is known about the
// account: no manual whitelist and no synced catalog.
var codeBuddyFallbackModelIDs = []string{"auto", "glm-5.2", "deepseek-v4-pro", "kimi-k2.7"}

// CodeBuddyAvailableModels resolves the models this account advertises.
//
// Precedence is deliberate. The operator's manual whitelist decides *which*
// models are offered and is never silently widened — an entry absent from the
// catalog is still honoured, because an operator may whitelist a model before
// syncing. The synced catalog only supplies credit metadata. The credit policy
// is a final rail that can remove models but never add them, so it stays
// consistent with codeBuddyCreditPolicyDenial on the request path.
func (a *Account) CodeBuddyAvailableModels() []codebuddy.CatalogModel {
	catalog := a.GetCodeBuddyCatalog()
	models := codeBuddyWhitelistedModels(a, catalog)
	if models == nil {
		models = catalog
		if len(models) == 0 {
			models = make([]codebuddy.CatalogModel, 0, len(codeBuddyFallbackModelIDs))
			for _, id := range codeBuddyFallbackModelIDs {
				models = append(models, codebuddy.CatalogModel{ID: id, Name: id})
			}
		}
	}
	return codebuddy.FilterCatalog(models, a.CodeBuddyCreditPolicy())
}

// codeBuddyWhitelistedModels renders the account's manual whitelist as a model
// list, or nil when the account has no usable whitelist.
//
// Clients must request the mapping's keys, so those are what gets advertised;
// credit metadata is looked up from the catalog entry for the mapped target.
func codeBuddyWhitelistedModels(a *Account, catalog []codebuddy.CatalogModel) []codebuddy.CatalogModel {
	mapping := a.GetModelMapping()
	if len(mapping) == 0 {
		return nil
	}
	names := make([]string, 0, len(mapping))
	for from := range mapping {
		from = strings.TrimSpace(from)
		// A wildcard pattern matches models, it is not a model clients can request.
		if from == "" || strings.Contains(from, "*") {
			continue
		}
		names = append(names, from)
	}
	if len(names) == 0 {
		return nil
	}
	// Map iteration order would make the advertised list shuffle between calls.
	sort.Strings(names)
	out := make([]codebuddy.CatalogModel, 0, len(names))
	for _, name := range names {
		target := strings.TrimSpace(mapping[name])
		if target == "" {
			target = name
		}
		entry := codebuddy.CatalogModel{ID: name, Name: name}
		// Aliases are deliberately not copied: only the whitelist key is accepted.
		if model, ok := codebuddy.FindModel(catalog, target); ok {
			if model.Name != "" {
				entry.Name = model.Name
			}
			entry.Credits = model.Credits
			entry.CreditsValue = model.CreditsValue
		}
		out = append(out, entry)
	}
	return out
}

// CodeBuddyGroupModels aggregates what every schedulable CodeBuddy account in
// the group advertises, de-duplicated by model ID.
//
// GetAvailableModels cannot answer this: it only knows model_mapping keys, so a
// CodeBuddy account that draws its models from the synced catalog would
// advertise nothing at all. Credit metadata survives into DisplayName so the
// listing can show each model's multiplier.
func (s *GatewayService) CodeBuddyGroupModels(ctx context.Context, groupID *int64) []openai.Model {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	var accounts []Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupID(ctx, *groupID)
	} else {
		accounts, err = s.accountRepo.ListSchedulable(ctx)
	}
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	models := make([]codebuddy.CatalogModel, 0)
	for i := range accounts {
		account := &accounts[i]
		if !account.IsCodeBuddy() {
			continue
		}
		for _, model := range account.CodeBuddyAvailableModels() {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			models = append(models, model)
		}
	}
	sort.Slice(models, func(a, b int) bool { return models[a].ID < models[b].ID })
	return codeBuddyModelsAsOpenAI(models)
}

// codeBuddyProbe carries what an admin-side upstream probe needs: credentials
// known to be usable plus a client bound to the account's proxy.
type codeBuddyProbe struct {
	creds  codebuddy.Credentials
	client *http.Client
}

// newCodeBuddyProbe prepares credentials and transport for an admin-side probe.
// The access token is refreshed only when it is at or near expiry: CodeBuddy
// hands back a rotated refresh token, so refreshing on every probe both wastes
// an upstream round trip and risks stranding the account if the rotation is not
// persisted.
func (s *CodeBuddyOAuthService) newCodeBuddyProbe(ctx context.Context, account *Account) (*codeBuddyProbe, error) {
	if account == nil || !account.IsCodeBuddy() {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_INVALID_ACCOUNT", "not a codebuddy account")
	}
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_CREDENTIALS_INVALID", err.Error())
	}
	if s != nil && account.IsCodeBuddyOAuth() && codeBuddyProbeTokenExpiring(account) {
		if refreshed, refreshErr := s.RefreshAccountToken(ctx, account); refreshErr == nil && refreshed != nil && refreshed.AccessToken != "" {
			creds.AccessToken = refreshed.AccessToken
			if refreshed.Domain != "" {
				creds.Domain = refreshed.Domain
			}
			s.persistRefreshedCredentials(ctx, account, refreshed)
		}
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := codebuddy.NewClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "CODEBUDDY_OAUTH_PROXY_INVALID", "%v", err)
	}
	return &codeBuddyProbe{creds: creds, client: client}, nil
}

func codeBuddyProbeTokenExpiring(account *Account) bool {
	if strings.TrimSpace(account.GetCodeBuddyAccessToken()) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	return expiresAt == nil || time.Until(*expiresAt) <= codebuddyTokenSkew()
}

// persistRefreshedCredentials stores the rotated tokens so the next refresh does
// not replay a refresh token the upstream has already consumed.
func (s *CodeBuddyOAuthService) persistRefreshedCredentials(ctx context.Context, account *Account, refreshed *codebuddy.RefreshResult) {
	updated := s.ApplyRefresh(account, refreshed)
	if len(updated) == 0 {
		return
	}
	account.Credentials = updated
	if s.accountRepo == nil {
		return
	}
	if err := s.accountRepo.Update(ctx, account); err != nil {
		logger.L().Warn("codebuddy probe could not persist refreshed credentials",
			zap.Int64("account_id", account.ID),
			zap.Error(err),
		)
	}
}

func (s *CodeBuddyOAuthService) FetchCatalog(ctx context.Context, account *Account) ([]codebuddy.CatalogModel, error) {
	probe, err := s.newCodeBuddyProbe(ctx, account)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, codeBuddyUpstreamTimeout)
	defer cancel()
	creds := probe.creds
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, codebuddy.ConfigURL(creds.Profile), nil)
	if err != nil {
		return nil, err
	}
	codebuddy.ApplyHeadersToRequest(req, codebuddy.CatalogHeaders(creds.Profile, creds.AccessToken, creds.Domain, creds.UID, creds.EnterpriseID))
	resp, err := probe.client.Do(req)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_CATALOG_FAILED", "%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_CATALOG_FAILED", "%v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_CATALOG_FAILED", "catalog HTTP %d", resp.StatusCode)
	}
	models, err := codebuddy.ParseCatalog(body, codebuddy.ProfileProduct(creds.Profile))
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "CODEBUDDY_CATALOG_INVALID", err.Error())
	}
	return models, nil
}

func (s *CodeBuddyOAuthService) PersistCatalog(ctx context.Context, account *Account, models []codebuddy.CatalogModel) error {
	if s == nil || s.accountRepo == nil || account == nil {
		return nil
	}
	snapshot := CodeBuddyCatalogSnapshot{SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: models}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{codeBuddyCatalogExtraKey: snapshot})
}

func (s *CodeBuddyOAuthService) QueryCredits(ctx context.Context, account *Account) (*codebuddy.CreditsSnapshot, error) {
	probe, err := s.newCodeBuddyProbe(ctx, account)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, codeBuddyUpstreamTimeout)
	defer cancel()
	snapshot, err := codebuddy.FetchCredits(reqCtx, probe.client, probe.creds)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_CREDITS_FAILED", "%v", err)
	}
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{codeBuddyCreditsExtraKey: snapshot})
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[codeBuddyCreditsExtraKey] = snapshot
	return snapshot, nil
}

// QueryRequestUsage pulls CodeBuddy's own per-request consumption report. This
// is the authoritative figure — it covers every client that used the account,
// not just this gateway — so it takes precedence over the local tally wherever
// both exist.
func (s *CodeBuddyOAuthService) QueryRequestUsage(ctx context.Context, account *Account, days int) (*codebuddy.RequestUsage, error) {
	probe, err := s.newCodeBuddyProbe(ctx, account)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, codeBuddyUpstreamTimeout)
	defer cancel()
	usage, err := codebuddy.FetchRequestUsage(reqCtx, probe.client, probe.creds, days)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_USAGE_FAILED", "%v", err)
	}
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{codeBuddyRequestUsageExtraKey: usage})
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[codeBuddyRequestUsageExtraKey] = usage
	return usage, nil
}

// CodeBuddyCreditsReport pairs the upstream balance with the two views of how it
// was spent: CodeBuddy's own billing report and this gateway's local tally.
type CodeBuddyCreditsReport struct {
	*codebuddy.CreditsSnapshot
	// Usage is the gateway-side tally, which only sees traffic routed here.
	Usage codebuddy.CreditsUsage `json:"usage"`
	// Official is CodeBuddy's billed consumption, absent when the report could
	// not be fetched.
	Official *codebuddy.RequestUsage `json:"official,omitempty"`
	// Stale reports that the balance came from a cached probe older than
	// codeBuddyCreditsSnapshotTTL.
	Stale bool `json:"stale"`
}

// CreditsReport returns the account's credit standing, probing the billing API
// only when the caller forces it or the cached balance has aged out. Probing on
// every read would put an upstream round trip behind each admin page load.
func (s *CodeBuddyOAuthService) CreditsReport(ctx context.Context, account *Account, refresh bool) (*CodeBuddyCreditsReport, error) {
	if account == nil || !account.IsCodeBuddy() {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_INVALID_ACCOUNT", "not a codebuddy account")
	}
	snapshot := account.GetCodeBuddyCredits()
	official := account.GetCodeBuddyRequestUsage()
	if refresh || snapshot == nil || account.CodeBuddyCreditsStale() {
		probed, err := s.QueryCredits(ctx, account)
		if err != nil {
			// A failed probe should not hide a balance we already know about.
			if snapshot == nil {
				return nil, err
			}
		} else {
			snapshot = probed
		}
		// The consumption report is supplementary: a balance is still worth
		// returning when only this call fails.
		if fetched, usageErr := s.QueryRequestUsage(ctx, account, codebuddy.RequestUsageMaxDays); usageErr == nil {
			official = fetched
		} else {
			logger.L().Debug("codebuddy request usage probe failed",
				zap.Int64("account_id", account.ID),
				zap.Error(usageErr),
			)
		}
	}
	return &CodeBuddyCreditsReport{
		CreditsSnapshot: snapshot,
		Usage:           account.GetCodeBuddyCreditsUsage(),
		Official:        official,
		Stale:           account.CodeBuddyCreditsStale(),
	}, nil
}

func CodeBuddyAvailableOpenAIModels(account *Account) []openai.Model {
	if account == nil {
		return nil
	}
	return codeBuddyModelsAsOpenAI(account.CodeBuddyAvailableModels())
}

func codeBuddyModelsAsOpenAI(models []codebuddy.CatalogModel) []openai.Model {
	out := make([]openai.Model, 0, len(models))
	for _, model := range models {
		// The credit multiplier is the price signal that matters on CodeBuddy, so
		// surface it in the label wherever the catalog reported one.
		display := model.ID
		if model.Credits != "" {
			display = fmt.Sprintf("%s (%s)", model.ID, model.Credits)
		}
		out = append(out, openai.Model{
			ID:          model.ID,
			Object:      "model",
			Type:        "model",
			OwnedBy:     "codebuddy",
			DisplayName: display,
		})
	}
	return out
}

// codeBuddyCreditPolicyDenial reports why the account's zero-credit policy
// rejects modelID, or returns "" when the request may proceed.
//
// The two denial causes need different messages because they need different
// fixes: a priced model means the policy is doing its job, while a model absent
// from the stored catalog means the catalog is stale and the operator cannot
// tell what the model costs. The absent case fails closed — the whole point of
// the zero-credit policy is to never spend credits unintentionally.
func codeBuddyCreditPolicyDenial(account *Account, modelID string) string {
	if account == nil || !account.IsCodeBuddy() {
		return ""
	}
	if account.CodeBuddyCreditPolicy() != codebuddy.CreditPolicyZeroOnly {
		return ""
	}
	catalog := account.GetCodeBuddyCatalog()
	// Nothing synced yet: there is no pricing to enforce against, so enforcing
	// would block every model on the account.
	if len(catalog) == 0 {
		return ""
	}
	modelID = strings.TrimSpace(modelID)
	model, found := codebuddy.FindModel(catalog, modelID)
	if !found {
		return fmt.Sprintf(
			"model %s is missing from this CodeBuddy account's synced catalog, so its credit cost is unknown and the account's zero-credit policy cannot admit it; sync the account's models, or set its credit policy to all models",
			modelID,
		)
	}
	if codebuddy.IsZeroCredit(model.Credits) {
		return ""
	}
	cost := strings.TrimSpace(model.Credits)
	if cost == "" {
		cost = "an unreported amount of"
	}
	return fmt.Sprintf(
		"model %s costs %s credits and this CodeBuddy account is restricted to zero-credit models; set its credit policy to all models to allow it",
		modelID, cost,
	)
}
