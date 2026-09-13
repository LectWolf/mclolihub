package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	codeBuddyCatalogExtraKey = "codebuddy_catalog"
	codeBuddyCreditsExtraKey = "codebuddy_credits"
	codeBuddyCreditPolicyKey = "credit_policy"
	codeBuddyUpstreamTimeout = 15 * time.Second
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

func (a *Account) CodeBuddyAvailableModels() []codebuddy.CatalogModel {
	models := a.GetCodeBuddyCatalog()
	if len(models) == 0 {
		for _, id := range []string{"auto", "glm-5.2", "deepseek-v4-pro", "kimi-k2.7"} {
			models = append(models, codebuddy.CatalogModel{ID: id, Name: id})
		}
	}
	models = codebuddy.FilterCatalog(models, a.CodeBuddyCreditPolicy())
	if mapping := a.GetModelMapping(); len(mapping) > 0 {
		allowed := map[string]struct{}{}
		for from, to := range mapping {
			allowed[strings.TrimSpace(from)] = struct{}{}
			allowed[strings.TrimSpace(to)] = struct{}{}
		}
		filtered := make([]codebuddy.CatalogModel, 0, len(models))
		for _, model := range models {
			if _, ok := allowed[model.ID]; ok {
				filtered = append(filtered, model)
			}
		}
		if len(filtered) > 0 {
			models = filtered
		}
	}
	return models
}

func (s *CodeBuddyOAuthService) FetchCatalog(ctx context.Context, account *Account) ([]codebuddy.CatalogModel, error) {
	if account == nil || !account.IsCodeBuddy() {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_INVALID_ACCOUNT", "not a codebuddy account")
	}
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_CREDENTIALS_INVALID", err.Error())
	}
	if s != nil && account.Type == AccountTypeOAuth {
		if refreshed, refreshErr := s.RefreshAccountToken(ctx, account); refreshErr == nil && refreshed != nil && refreshed.AccessToken != "" {
			creds.AccessToken = refreshed.AccessToken
			if refreshed.Domain != "" {
				creds.Domain = refreshed.Domain
			}
		}
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := codebuddy.NewClient(proxyURL)
	if err != nil {
		return nil, err
	}
	headers := codebuddy.CatalogHeaders(creds.Profile, creds.AccessToken, creds.Domain, creds.UID, creds.EnterpriseID)
	reqCtx, cancel := context.WithTimeout(ctx, codeBuddyUpstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, codebuddy.ConfigURL(creds.Profile), nil)
	if err != nil {
		return nil, err
	}
	codebuddy.ApplyHeadersToRequest(req, headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_CATALOG_FAILED", "%v", err)
	}
	defer resp.Body.Close()
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
	if account == nil || !account.IsCodeBuddy() {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_INVALID_ACCOUNT", "not a codebuddy account")
	}
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_CREDENTIALS_INVALID", err.Error())
	}
	if s != nil && account.Type == AccountTypeOAuth {
		if refreshed, refreshErr := s.RefreshAccountToken(ctx, account); refreshErr == nil && refreshed != nil && refreshed.AccessToken != "" {
			creds.AccessToken = refreshed.AccessToken
		}
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := codebuddy.NewClient(proxyURL)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, codeBuddyUpstreamTimeout)
	defer cancel()
	snapshot, err := codebuddy.FetchCredits(reqCtx, client, creds)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_CREDITS_FAILED", "%v", err)
	}
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{codeBuddyCreditsExtraKey: snapshot})
	}
	return snapshot, nil
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
		display := model.ID
		if model.Name != "" && model.Credits != "" {
			display = fmt.Sprintf("%s (%s)", model.ID, model.Credits)
		} else if model.Credits != "" {
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

func codeBuddyModelAllowed(account *Account, modelID string) bool {
	if account == nil || !account.IsCodeBuddy() {
		return true
	}
	if account.CodeBuddyCreditPolicy() != codebuddy.CreditPolicyZeroOnly {
		return true
	}
	modelID = strings.TrimSpace(modelID)
	for _, model := range account.GetCodeBuddyCatalog() {
		if model.ID == modelID {
			return codebuddy.IsZeroCredit(model.Credits)
		}
		for _, alias := range model.Aliases {
			if alias == modelID {
				return codebuddy.IsZeroCredit(model.Credits)
			}
		}
	}
	// Unknown model with zero-only policy: allow through if catalog is empty.
	return len(account.GetCodeBuddyCatalog()) == 0
}
