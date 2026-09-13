package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	codebuddyRequestRefreshTimeout   = 8 * time.Second
	codebuddyRefreshLockWaitTimeout  = 2 * time.Second
	codebuddyRefreshLockPollInterval = 25 * time.Millisecond
	codebuddyTokenCacheSkew          = 5 * time.Minute
)

type CodeBuddyTokenCache = GeminiTokenCache

type CodeBuddyTokenProvider struct {
	accountRepo   AccountRepository
	tokenCache    CodeBuddyTokenCache
	refreshAPI    *OAuthRefreshAPI
	executor      OAuthRefreshExecutor
	refreshPolicy ProviderRefreshPolicy
}

func NewCodeBuddyTokenProvider(accountRepo AccountRepository, tokenCache CodeBuddyTokenCache) *CodeBuddyTokenProvider {
	return &CodeBuddyTokenProvider{
		accountRepo:   accountRepo,
		tokenCache:    tokenCache,
		refreshPolicy: CodeBuddyProviderRefreshPolicy(),
	}
}

func (p *CodeBuddyTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *CodeBuddyTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if !account.IsCodeBuddyOAuth() {
		return "", errors.New("not a codebuddy oauth account")
	}
	accessToken := strings.TrimSpace(account.GetCodeBuddyAccessToken())
	if accessToken == "" {
		return "", errors.New("codebuddy access token is missing")
	}
	if strings.TrimSpace(account.GetCodeBuddyRefreshToken()) == "" {
		return "", errors.New("codebuddy refresh token is missing")
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	cacheKey := CodeBuddyTokenCacheKey(account)
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil {
			cached := strings.TrimSpace(token)
			if cached != "" && cached == accessToken && expiresAt != nil && time.Until(*expiresAt) > codebuddyTokenCacheSkew {
				return cached, nil
			}
		}
	}
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= codebuddyTokenSkew()
	if !needsRefresh {
		return accessToken, nil
	}
	if p.refreshAPI == nil || p.executor == nil {
		if expiresAt != nil && time.Now().Before(*expiresAt) {
			return accessToken, nil
		}
		return "", errors.New("codebuddy oauth refresh is not configured")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, codebuddyRequestRefreshTimeout)
	defer cancel()
	result, err := p.refreshAPI.RefreshIfNeeded(withOAuthRefreshRequestPath(refreshCtx), account, p.executor, codebuddyTokenSkew())
	if err != nil {
		if p.refreshPolicy.OnRefreshError == ProviderRefreshErrorReturn {
			return "", err
		}
		return accessToken, nil
	}
	if result != nil && result.LockHeld {
		if p.refreshPolicy.OnLockHeld == ProviderLockHeldWaitForCache {
			return p.waitForRefreshedToken(refreshCtx, account, cacheKey)
		}
		if expiresAt != nil && time.Now().Before(*expiresAt) {
			return accessToken, nil
		}
		return "", errors.New("codebuddy access token is expired")
	}
	if result != nil && result.Account != nil {
		account = result.Account
	}
	token := strings.TrimSpace(account.GetCodeBuddyAccessToken())
	if token == "" {
		return "", errors.New("codebuddy access token is missing")
	}
	return token, nil
}

func (p *CodeBuddyTokenProvider) waitForRefreshedToken(ctx context.Context, account *Account, cacheKey string) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, codebuddyRefreshLockWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(codebuddyRefreshLockPollInterval)
	defer ticker.Stop()
	for {
		if p.tokenCache != nil {
			if token, err := p.tokenCache.GetAccessToken(waitCtx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
				return strings.TrimSpace(token), nil
			}
		}
		if p.accountRepo != nil {
			latest, err := p.accountRepo.GetByID(waitCtx, account.ID)
			if err == nil && latest != nil {
				token := strings.TrimSpace(latest.GetCodeBuddyAccessToken())
				expiresAt := latest.GetCredentialAsTime("expires_at")
				if token != "" && expiresAt != nil && time.Until(*expiresAt) > codebuddyTokenCacheSkew {
					return token, nil
				}
			}
		}
		select {
		case <-waitCtx.Done():
			token := strings.TrimSpace(account.GetCodeBuddyAccessToken())
			if token != "" {
				return token, nil
			}
			return "", waitCtx.Err()
		case <-ticker.C:
		}
	}
}
