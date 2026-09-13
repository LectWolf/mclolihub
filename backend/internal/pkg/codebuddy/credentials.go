package codebuddy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Credentials struct {
	AccessToken      string
	RefreshToken     string
	IDToken          string
	TokenType        string
	ExpiresAt        int64 // unix seconds
	RefreshExpiresAt int64 // unix seconds
	Domain           string
	UID              string
	Nickname         string
	EnterpriseID     string
	Profile          string
	Site             string
	Product          string
}

func (c Credentials) Valid() error {
	if strings.TrimSpace(c.AccessToken) == "" {
		return fmt.Errorf("missing access_token")
	}
	if strings.TrimSpace(c.UID) == "" {
		return fmt.Errorf("missing uid")
	}
	if !OriginAllowed(c.Domain, c.AccessToken) {
		return fmt.Errorf("authentication domain is not allowed")
	}
	if _, err := ProfileForAuth(c.Domain, c.AccessToken); err != nil {
		return err
	}
	return nil
}

func ParseCredentials(raw map[string]any) (Credentials, error) {
	if raw == nil {
		return Credentials{}, fmt.Errorf("credentials are empty")
	}
	if nested, ok := raw["auth"].(map[string]any); ok {
		return parseOfficialInfo(raw, nested)
	}
	creds := Credentials{
		AccessToken:      stringField(raw, "access_token", "accessToken", "token"),
		RefreshToken:     stringField(raw, "refresh_token", "refreshToken"),
		IDToken:          stringField(raw, "id_token", "idToken"),
		TokenType:        firstNonEmpty(stringField(raw, "token_type", "tokenType"), "Bearer"),
		ExpiresAt:        unixSeconds(raw["expires_at"], raw["expiresAt"]),
		RefreshExpiresAt: unixSeconds(raw["refresh_expires_at"], raw["refreshExpiresAt"]),
		Domain:           stringField(raw, "domain"),
		UID:              stringField(raw, "uid"),
		Nickname:         stringField(raw, "nickname"),
		EnterpriseID:     stringField(raw, "enterprise_id", "enterpriseId"),
		Profile:          stringField(raw, "profile"),
		Site:             stringField(raw, "site"),
		Product:          stringField(raw, "product"),
	}
	if creds.Profile == "" {
		profile, err := ProfileForAuth(creds.Domain, creds.AccessToken)
		if err != nil {
			return Credentials{}, err
		}
		creds.Profile = profile
	}
	creds.Site = ProfileRegion(creds.Profile)
	creds.Product = ProfileProduct(creds.Profile)
	if creds.Domain == "" {
		if host := TokenIssuerHost(creds.AccessToken); host != "" {
			creds.Domain = host
		}
	}
	if err := creds.Valid(); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

func parseOfficialInfo(root, auth map[string]any) (Credentials, error) {
	account := mapField(root, "account")
	if account == nil {
		if accounts, ok := root["accounts"].([]any); ok && len(accounts) > 0 {
			if first, ok := accounts[0].(map[string]any); ok {
				account = first
			}
		}
	}
	creds := Credentials{
		AccessToken:      stringField(auth, "accessToken", "access_token", "token"),
		RefreshToken:     stringField(auth, "refreshToken", "refresh_token"),
		IDToken:          stringField(auth, "idToken", "id_token"),
		TokenType:        firstNonEmpty(stringField(auth, "tokenType", "token_type"), "Bearer"),
		ExpiresAt:        unixSeconds(auth["expiresAt"], auth["expires_at"]),
		RefreshExpiresAt: unixSeconds(auth["refreshExpiresAt"], auth["refresh_expires_at"]),
		Domain:           stringField(auth, "domain"),
		UID:              stringField(account, "uid"),
		Nickname:         stringField(account, "nickname"),
		EnterpriseID:     stringField(account, "enterpriseId", "enterprise_id"),
	}
	profile, err := ProfileForAuth(creds.Domain, creds.AccessToken)
	if err != nil {
		return Credentials{}, err
	}
	creds.Profile = profile
	creds.Site = ProfileRegion(profile)
	creds.Product = ProfileProduct(profile)
	if err := creds.Valid(); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

func (c Credentials) ToMap() map[string]any {
	out := map[string]any{
		"access_token":  c.AccessToken,
		"refresh_token": c.RefreshToken,
		"token_type":    firstNonEmpty(c.TokenType, "Bearer"),
		"domain":        c.Domain,
		"uid":           c.UID,
		"nickname":      c.Nickname,
		"enterprise_id": c.EnterpriseID,
		"profile":       c.Profile,
		"site":          c.Site,
		"product":       c.Product,
	}
	if c.IDToken != "" {
		out["id_token"] = c.IDToken
	}
	if c.ExpiresAt > 0 {
		out["expires_at"] = c.ExpiresAt
	}
	if c.RefreshExpiresAt > 0 {
		out["refresh_expires_at"] = c.RefreshExpiresAt
	}
	return out
}

func stringField(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		switch v := m[key].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func mapField(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func unixSeconds(values ...any) int64 {
	for _, v := range values {
		ts := toUnixSeconds(v)
		if ts > 0 {
			return ts
		}
	}
	return 0
}

func toUnixSeconds(v any) int64 {
	var n int64
	switch t := v.(type) {
	case int64:
		n = t
	case int:
		n = int64(t)
	case float64:
		n = int64(t)
	case json.Number:
		parsed, err := t.Int64()
		if err != nil {
			return 0
		}
		n = parsed
	case string:
		if strings.TrimSpace(t) == "" {
			return 0
		}
		var parsed int64
		if _, err := fmt.Sscan(t, &parsed); err != nil {
			return 0
		}
		n = parsed
	default:
		return 0
	}
	if n <= 0 {
		return 0
	}
	if n > 1e12 { // milliseconds
		return n / 1000
	}
	return n
}

func ExpiresAtFromNow(expiresIn int64) int64 {
	if expiresIn <= 0 {
		return 0
	}
	return time.Now().Unix() + expiresIn
}
