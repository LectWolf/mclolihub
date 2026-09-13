package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type CodeBuddyOAuthService struct {
	manager   *codebuddy.OAuthManager
	proxyRepo ProxyRepository
}

func NewCodeBuddyOAuthService(proxyRepo ProxyRepository) *CodeBuddyOAuthService {
	return &CodeBuddyOAuthService{
		manager:   codebuddy.NewOAuthManager(nil),
		proxyRepo: proxyRepo,
	}
}

func (s *CodeBuddyOAuthService) Start(ctx context.Context, site string, proxyID *int64) (*codebuddy.StartResult, error) {
	client, err := s.httpClient(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	result, err := s.manager.Start(ctx, site, client)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_OAUTH_START_FAILED", "%v", err)
	}
	return result, nil
}

func (s *CodeBuddyOAuthService) Poll(ctx context.Context, loginID string) (*codebuddy.PollResult, error) {
	if s == nil || s.manager == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "CODEBUDDY_OAUTH_UNAVAILABLE", "codebuddy oauth is not configured")
	}
	result, err := s.manager.Poll(ctx, loginID)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_OAUTH_POLL_FAILED", "%v", err)
	}
	return result, nil
}

func (s *CodeBuddyOAuthService) Take(loginID string) (*codebuddy.Credentials, error) {
	if s == nil || s.manager == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "CODEBUDDY_OAUTH_UNAVAILABLE", "codebuddy oauth is not configured")
	}
	creds, err := s.manager.Take(loginID)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_OAUTH_TAKE_FAILED", err.Error())
	}
	return creds, nil
}

func (s *CodeBuddyOAuthService) ImportCredentials(raw map[string]any) (*codebuddy.Credentials, error) {
	creds, err := codebuddy.ParseCredentials(raw)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_CREDENTIALS_INVALID", err.Error())
	}
	return &creds, nil
}

func (s *CodeBuddyOAuthService) BuildAccountCredentials(creds *codebuddy.Credentials) map[string]any {
	if creds == nil {
		return map[string]any{}
	}
	return creds.ToMap()
}

func (s *CodeBuddyOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*codebuddy.RefreshResult, error) {
	if account == nil || !account.IsCodeBuddyOAuth() {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_OAUTH_INVALID_ACCOUNT", "not a codebuddy oauth account")
	}
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_CREDENTIALS_INVALID", err.Error())
	}
	if strings.TrimSpace(creds.RefreshToken) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CODEBUDDY_OAUTH_REFRESH_TOKEN_MISSING", "refresh token is missing")
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	} else if account.ProxyID != nil {
		parsed, err := s.proxyURL(ctx, account.ProxyID)
		if err != nil {
			return nil, err
		}
		proxyURL = parsed
	}
	client, err := codebuddy.NewClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "CODEBUDDY_OAUTH_PROXY_INVALID", "%v", err)
	}
	refreshCtx, cancel := context.WithTimeout(ctx, codebuddy.RefreshTimeout)
	defer cancel()
	result, err := codebuddy.Refresh(refreshCtx, client, creds, proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CODEBUDDY_OAUTH_REFRESH_FAILED", "%v", err)
	}
	return result, nil
}

func (s *CodeBuddyOAuthService) ApplyRefresh(account *Account, result *codebuddy.RefreshResult) map[string]any {
	creds := map[string]any{}
	if account != nil && account.Credentials != nil {
		creds = MergeCredentials(account.Credentials, map[string]any{})
	}
	if result == nil {
		return creds
	}
	creds["access_token"] = result.AccessToken
	if result.RefreshToken != "" {
		creds["refresh_token"] = result.RefreshToken
	}
	if result.IDToken != "" {
		creds["id_token"] = result.IDToken
	}
	if result.TokenType != "" {
		creds["token_type"] = result.TokenType
	}
	if result.Domain != "" {
		creds["domain"] = result.Domain
	}
	if result.ExpiresAt > 0 {
		creds["expires_at"] = result.ExpiresAt
	}
	if result.RefreshExpiresAt > 0 {
		creds["refresh_expires_at"] = result.RefreshExpiresAt
	}
	return creds
}

func (s *CodeBuddyOAuthService) httpClient(ctx context.Context, proxyID *int64) (*http.Client, error) {
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	client, err := codebuddy.NewClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "CODEBUDDY_OAUTH_PROXY_INVALID", "%v", err)
	}
	return client, nil
}

func (s *CodeBuddyOAuthService) proxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", infraerrors.New(http.StatusBadRequest, "CODEBUDDY_OAUTH_PROXY_NOT_FOUND", "proxy repository is not configured")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil || proxy == nil {
		return "", infraerrors.New(http.StatusBadRequest, "CODEBUDDY_OAUTH_PROXY_NOT_FOUND", "proxy not found")
	}
	return proxy.URL(), nil
}

func CodeBuddyProviderRefreshPolicy() ProviderRefreshPolicy {
	return ProviderRefreshPolicy{
		OnRefreshError: ProviderRefreshErrorReturn,
		OnLockHeld:     ProviderLockHeldWaitForCache,
		FailureTTL:     0,
	}
}

func CodeBuddyTokenCacheKey(account *Account) string {
	if account == nil {
		return "codebuddy:account:0"
	}
	return fmt.Sprintf("codebuddy:account:%d", account.ID)
}

func (a *Account) mustCodeBuddyCredentials() (codebuddy.Credentials, error) {
	if a == nil {
		return codebuddy.Credentials{}, fmt.Errorf("account is nil")
	}
	return codebuddy.ParseCredentials(a.Credentials)
}

func codebuddyTokenSkew() time.Duration { return time.Hour }
