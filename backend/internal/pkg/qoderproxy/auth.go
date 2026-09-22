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
)

const (
	userAgent   = "qodercli/1.0.0"
	refreshSkew = 5 * time.Minute
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

// TokenCache remembers PAT to job-token exchanges.
type TokenCache struct {
	mu    sync.Mutex
	items map[string]Identity
}

func NewTokenCache() *TokenCache {
	return &TokenCache{items: map[string]Identity{}}
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
func (c *TokenCache) Resolve(ctx context.Context, client *http.Client, pat, machineID string) (Identity, error) {
	return c.ResolveRegion(ctx, client, RegionGlobal, pat, machineID)
}

// ResolveRegion exchanges a PAT on the China or global Qoder API.
// machineID is kept when the caller already has one.
func (c *TokenCache) ResolveRegion(ctx context.Context, client *http.Client, region Region, pat, machineID string) (Identity, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return Identity{}, fmt.Errorf("qoder: personal token is empty")
	}
	if region != RegionCN {
		region = RegionGlobal
	}
	if client == nil {
		client = http.DefaultClient
	}
	key := string(region) + "\x00" + pat
	machineID = strings.TrimSpace(machineID)
	if c != nil {
		c.mu.Lock()
		cached, ok := c.items[key]
		c.mu.Unlock()
		if ok && time.Until(cached.ExpiresAt) > refreshSkew {
			if machineID != "" {
				cached.MachineID = machineID
			}
			return cached, nil
		}
	}

	identity, err := exchange(ctx, client, region.APIBase(), pat)
	if err != nil {
		return Identity{}, err
	}
	if machineID != "" {
		identity.MachineID = machineID
	} else if identity.MachineID == "" {
		identity.MachineID = newID()
	}
	if c != nil {
		c.mu.Lock()
		c.items[key] = identity
		c.mu.Unlock()
	}
	return identity, nil
}

func exchange(ctx context.Context, client *http.Client, apiBase, pat string) (Identity, error) {
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

	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("qoder: job token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("qoder: job token body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("qoder: job token HTTP %d: %s", resp.StatusCode, truncateRunes(string(body), 240))
	}
	var parsed struct {
		Token        string `json:"token"`
		ExpiresIn    int64  `json:"expires_in"`
		ExpiresAt    string `json:"expires_at"`
		RefreshToken string `json:"refresh_token"`
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
	_ = parsed.RefreshToken
	profile, err := fetchUser(ctx, client, strings.TrimRight(apiBase, "/")+"/api/v1/userinfo", parsed.Token)
	if err != nil {
		identity.UserID = "user-" + newID()[:8]
		return identity, nil
	}
	identity.UserID = profile.UserID
	identity.Name = profile.Name
	identity.Email = profile.Email
	return identity, nil
}

type profile struct {
	UserID string
	Name   string
	Email  string
}

func fetchUser(ctx context.Context, client *http.Client, userInfoURL, jobToken string) (profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return profile{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+jobToken)
	resp, err := client.Do(req)
	if err != nil {
		return profile{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return profile{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return profile{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
		Name   string `json:"name"`
		Email  string `json:"email"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return profile{}, err
	}
	id := firstNonEmpty(parsed.ID, parsed.UserID)
	if id == "" {
		return profile{}, fmt.Errorf("user id missing")
	}
	return profile{UserID: id, Name: parsed.Name, Email: parsed.Email}, nil
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

// FetchProfile loads the user id for a device or job token.
func FetchProfile(ctx context.Context, client *http.Client, userInfoURL, token string) (userID, name, email string, err error) {
	got, err := fetchUser(ctx, client, userInfoURL, token)
	if err != nil {
		return "", "", "", err
	}
	return got.UserID, got.Name, got.Email, nil
}
