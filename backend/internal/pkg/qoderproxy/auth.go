package qoderproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	userAgent   = "qodercli/1.0.0"
	refreshSkew = 5 * time.Minute
	// exchangeTimeout bounds a shared PAT exchange, which outlives any single
	// caller's context.
	exchangeTimeout = 30 * time.Second
)

// Identity is a job token plus the user it belongs to.
type Identity struct {
	UserID    string
	Name      string
	Email     string
	JobToken  string
	MachineID string
	ExpiresAt time.Time
}

// TokenCache remembers PAT to job-token exchanges. Concurrent misses for the
// same PAT share one exchange.
type TokenCache struct {
	mu     sync.Mutex
	items  map[string]Identity
	flight singleflight.Group
}

func NewTokenCache() *TokenCache {
	return &TokenCache{items: map[string]Identity{}}
}

func tokenCacheKey(region Region, pat string) string {
	return string(region) + "\x00" + pat
}

// Invalidate drops cached job tokens for a PAT on every region.
func (c *TokenCache) Invalidate(pat string) {
	if c == nil {
		return
	}
	pat = strings.TrimSpace(pat)
	c.mu.Lock()
	for key := range c.items {
		if key == pat || strings.HasSuffix(key, "\x00"+pat) {
			delete(c.items, key)
		}
	}
	c.mu.Unlock()
}

// Resolve exchanges a personal access token for a job token on the global site.
func (c *TokenCache) Resolve(ctx context.Context, doer Doer, pat, machineID string) (Identity, error) {
	return c.ResolveRegion(ctx, doer, RegionGlobal, pat, machineID)
}

// ResolveRegion exchanges a PAT on the China or global Qoder API.
// machineID is kept when the caller already has one.
func (c *TokenCache) ResolveRegion(ctx context.Context, doer Doer, region Region, pat, machineID string) (Identity, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return Identity{}, fmt.Errorf("qoder: personal token is empty")
	}
	if region != RegionCN {
		region = RegionGlobal
	}
	machineID = strings.TrimSpace(machineID)
	withMachine := func(identity Identity) Identity {
		if machineID != "" {
			identity.MachineID = machineID
		} else if identity.MachineID == "" {
			identity.MachineID = newID()
		}
		return identity
	}
	if c == nil {
		identity, err := exchange(ctx, doer, region.APIBase(), pat)
		if err != nil {
			return Identity{}, err
		}
		return withMachine(identity), nil
	}

	key := tokenCacheKey(region, pat)
	if cached, ok := c.fresh(key); ok {
		return withMachine(cached), nil
	}
	results := c.flight.DoChan(key, func() (any, error) {
		if cached, ok := c.fresh(key); ok {
			return cached, nil
		}
		// The result is shared with other waiters: one caller's cancellation
		// must not fail everyone else's request, so the exchange gets its own
		// deadline instead.
		exchangeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exchangeTimeout)
		defer cancel()
		identity, err := exchange(exchangeCtx, doer, region.APIBase(), pat)
		if err != nil {
			return Identity{}, err
		}
		identity.MachineID = newID()
		c.mu.Lock()
		c.items[key] = identity
		c.mu.Unlock()
		return identity, nil
	})
	select {
	case <-ctx.Done():
		return Identity{}, ctx.Err()
	case result := <-results:
		if result.Err != nil {
			return Identity{}, result.Err
		}
		identity, _ := result.Val.(Identity)
		return withMachine(identity), nil
	}
}

func (c *TokenCache) fresh(key string) (Identity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, ok := c.items[key]
	if !ok || time.Until(cached.ExpiresAt) <= refreshSkew {
		return Identity{}, false
	}
	return cached, true
}

func exchange(ctx context.Context, doer Doer, apiBase, pat string) (Identity, error) {
	raw, err := json.Marshal(map[string]string{"personal_token": pat})
	if err != nil {
		return Identity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(apiBase, "/")+"/api/v1/jobToken/exchange", bytes.NewReader(raw))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cosy-Version", cosyVersion)
	req.Header.Set("Cosy-ClientType", clientType)

	resp, err := doerOrDefault(doer).Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("qoder: job token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("qoder: job token body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, &HTTPError{Op: "job token", Status: resp.StatusCode, Body: string(body)}
	}
	var parsed struct {
		Token     string `json:"token"`
		ExpiresIn int64  `json:"expires_in"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Identity{}, fmt.Errorf("qoder: job token json: %w", err)
	}
	if strings.TrimSpace(parsed.Token) == "" {
		return Identity{}, fmt.Errorf("qoder: job token response has no token")
	}
	identity := Identity{
		JobToken:  parsed.Token,
		ExpiresAt: expiry(parsed.ExpiresAt, parsed.ExpiresIn),
	}
	profile, err := fetchUser(ctx, doer, strings.TrimRight(apiBase, "/")+"/api/v1/userinfo", parsed.Token)
	if err != nil {
		// COSY needs some uid; a placeholder keeps the request valid when user
		// info is temporarily unavailable.
		identity.UserID = "user-" + newID()[:8]
		return identity, nil
	}
	identity.UserID = profile.UserID
	identity.Name = profile.Name
	identity.Email = profile.Email
	return identity, nil
}

// Profile is the user a Qoder token belongs to.
type Profile struct {
	UserID string
	Name   string
	Email  string
}

func fetchUser(ctx context.Context, doer Doer, userInfoURL, token string) (Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := doerOrDefault(doer).Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("qoder: user info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Profile{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Profile{}, &HTTPError{Op: "user info", Status: resp.StatusCode, Body: string(body)}
	}
	var parsed struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
		Name   string `json:"name"`
		Email  string `json:"email"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Profile{}, err
	}
	id := firstNonEmpty(parsed.ID, parsed.UserID)
	if id == "" {
		return Profile{}, fmt.Errorf("qoder: user info has no user id")
	}
	return Profile{UserID: id, Name: parsed.Name, Email: parsed.Email}, nil
}

func expiry(expiresAt string, expiresIn int64) time.Time {
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			return t
		}
	}
	if expiresIn > 0 {
		return time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return time.Now().Add(24 * time.Hour)
}

// FetchProfile loads the user a device or job token belongs to.
func FetchProfile(ctx context.Context, doer Doer, region Region, token string) (Profile, error) {
	return fetchUser(ctx, doer, region.APIBase()+"/api/v1/userinfo", token)
}
