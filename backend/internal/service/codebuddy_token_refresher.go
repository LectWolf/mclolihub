package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

type CodeBuddyTokenRefresher struct {
	oauthService *CodeBuddyOAuthService
}

func NewCodeBuddyTokenRefresher(oauthService *CodeBuddyOAuthService) *CodeBuddyTokenRefresher {
	return &CodeBuddyTokenRefresher{oauthService: oauthService}
}

func (r *CodeBuddyTokenRefresher) CacheKey(account *Account) string {
	return CodeBuddyTokenCacheKey(account)
}

func (r *CodeBuddyTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.IsCodeBuddyOAuth() && strings.TrimSpace(account.GetCodeBuddyRefreshToken()) != ""
}

func (r *CodeBuddyTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	if strings.TrimSpace(account.GetCodeBuddyAccessToken()) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	if refreshWindow < codebuddyTokenSkew() {
		refreshWindow = codebuddyTokenSkew()
	}
	return time.Until(*expiresAt) < refreshWindow
}

func (r *CodeBuddyTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.oauthService == nil {
		return nil, errors.New("codebuddy oauth service is not configured")
	}
	result, err := r.oauthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return r.oauthService.ApplyRefresh(account, result), nil
}
