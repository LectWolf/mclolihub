package qoderproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// QuotaPool is one credit pool of a Qoder account.
type QuotaPool struct {
	Total     float64
	Used      float64
	Remaining float64
	Unit      string
	// Available is false for pools Qoder reports as disabled or expired.
	Available bool
	// ExpiresAt is zero when the pool does not expire.
	ExpiresAt time.Time
}

// QuotaPackage is a dedicated resource package (China edition).
type QuotaPackage struct {
	QuotaPool
	ID   string
	Name string
}

// Quota is the credits snapshot behind qodercli's usage view
// (GET /api/v2/quota/usage). Pools the account does not have are nil.
type Quota struct {
	UserID    string
	UserType  string
	UsageType string
	Exceeded  bool
	// ExpiresAt is when the plan credits (Plan) reset or end; zero if unknown.
	ExpiresAt time.Time
	Plan      *QuotaPool
	AddOn     *QuotaPool
	Org       *QuotaPool
	Packages  []QuotaPackage
}

// FetchQuota reads the credits of the account a device token belongs to.
// Qoder's own clients send the device token; a job token exchanged from a
// personal access token may be refused (*HTTPError 401/403).
func FetchQuota(ctx context.Context, doer Doer, region Region, token string) (Quota, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Quota{}, fmt.Errorf("qoder: quota needs a token")
	}
	if region != RegionCN {
		region = RegionGlobal
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, region.APIBase()+"/api/v2/quota/usage", nil)
	if err != nil {
		return Quota{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cosy-Version", cosyVersion)
	req.Header.Set("Cosy-ClientType", clientType)
	resp, err := doerOrDefault(doer).Do(req)
	if err != nil {
		return Quota{}, fmt.Errorf("qoder: quota: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Quota{}, fmt.Errorf("qoder: quota body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Quota{}, &HTTPError{Op: "quota", Status: resp.StatusCode, Body: string(body)}
	}
	return ParseQuota(body)
}

// ParseQuota decodes a quota/usage answer. Fields follow the Qoder CLI SDK
// usage info (userQuota, addOnQuota, orgResourcePackage with cap); the China
// edition may add dedicatedResourcePackages.
func ParseQuota(body []byte) (Quota, error) {
	var parsed wireQuota
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Quota{}, fmt.Errorf("qoder: quota json: %w", err)
	}
	if !parsed.hasPools() && len(bytes.TrimSpace(parsed.Data)) > 0 && bytes.TrimSpace(parsed.Data)[0] == '{' {
		var inner wireQuota
		if err := json.Unmarshal(parsed.Data, &inner); err == nil {
			parsed = inner
		}
	}
	quota := Quota{
		UserID:    string(parsed.UserID),
		UserType:  strings.TrimSpace(parsed.UserType),
		UsageType: strings.TrimSpace(parsed.UsageType),
		Exceeded:  parsed.IsQuotaExceeded,
		ExpiresAt: quotaTime(parsed.ExpiresAt),
		Plan:      parsed.UserQuota.pool(),
		AddOn:     parsed.AddOnQuota.pool(),
		Org:       parsed.OrgResourcePackage.pool(),
	}
	for i := range parsed.DedicatedResourcePackages {
		wire := &parsed.DedicatedResourcePackages[i]
		pool := wire.pool()
		if pool == nil {
			continue
		}
		quota.Packages = append(quota.Packages, QuotaPackage{
			QuotaPool: *pool,
			ID:        string(wire.ID),
			Name:      wire.label(),
		})
	}
	return quota, nil
}

type wireQuota struct {
	UserID                    quotaString     `json:"userId"`
	UserType                  string          `json:"userType"`
	UsageType                 string          `json:"usageType"`
	IsQuotaExceeded           bool            `json:"isQuotaExceeded"`
	ExpiresAt                 *quotaNumber    `json:"expiresAt"`
	UserQuota                 *wireQuotaPool  `json:"userQuota"`
	AddOnQuota                *wireQuotaPool  `json:"addOnQuota"`
	OrgResourcePackage        *wireQuotaPool  `json:"orgResourcePackage"`
	DedicatedResourcePackages []wireQuotaPool `json:"dedicatedResourcePackages"`
	Data                      json.RawMessage `json:"data"`
}

func (w *wireQuota) hasPools() bool {
	return w.UserQuota != nil || w.AddOnQuota != nil || w.OrgResourcePackage != nil || len(w.DedicatedResourcePackages) > 0
}

type wireQuotaPool struct {
	ID        quotaString  `json:"id"`
	Name      string       `json:"name"`
	Total     *quotaNumber `json:"total"`
	Cap       *quotaNumber `json:"cap"`
	Used      *quotaNumber `json:"used"`
	Remaining *quotaNumber `json:"remaining"`
	Unit      string       `json:"unit"`
	Available *bool        `json:"available"`
	Status    string       `json:"status"`
	ExpiresAt *quotaNumber `json:"expiresAt"`
	Labels    []struct {
		Dimension string            `json:"dimension"`
		Value     string            `json:"value"`
		ValueI18n map[string]string `json:"valueI18n"`
	} `json:"displayLabels"`
}

// pool returns nil when the account has no credits in this pool (missing, or
// a zero total such as the personal quota of a Teams seat).
func (w *wireQuotaPool) pool() *QuotaPool {
	if w == nil {
		return nil
	}
	total := w.Total
	if total == nil || *total <= 0 {
		total = w.Cap
	}
	if total == nil || *total <= 0 {
		return nil
	}
	used := 0.0
	if w.Used != nil && *w.Used > 0 {
		used = float64(*w.Used)
	}
	remaining := float64(*total) - used
	if w.Remaining != nil {
		remaining = float64(*w.Remaining)
	}
	if remaining < 0 {
		remaining = 0
	}
	available := w.Available == nil || *w.Available
	if strings.Contains(strings.ToUpper(w.Status), "EXPIRED") {
		available = false
	}
	unit := strings.TrimSpace(w.Unit)
	if unit == "" {
		unit = "credits"
	}
	return &QuotaPool{
		Total:     float64(*total),
		Used:      used,
		Remaining: remaining,
		Unit:      unit,
		Available: available,
		ExpiresAt: quotaTime(w.ExpiresAt),
	}
}

func (w *wireQuotaPool) label() string {
	for _, label := range w.Labels {
		if label.Dimension != "title" {
			continue
		}
		for _, candidate := range []string{label.ValueI18n["zh-CN"], label.Value, label.ValueI18n["en-US"]} {
			if text := strings.TrimSpace(candidate); text != "" {
				return truncateRunes(text, 64)
			}
		}
	}
	return truncateRunes(strings.TrimSpace(w.Name), 64)
}

// quotaNoExpiry marks timestamps Qoder uses for "never" (year 9999).
var quotaNoExpiry = time.Date(9000, 1, 1, 0, 0, 0, 0, time.UTC)

// quotaTime reads an epoch timestamp in milliseconds (seconds are accepted
// too); zero, negative and "never" sentinels become the zero time.
func quotaTime(v *quotaNumber) time.Time {
	if v == nil || *v <= 0 {
		return time.Time{}
	}
	n := int64(*v)
	var t time.Time
	if n < 1e12 {
		t = time.Unix(n, 0)
	} else {
		t = time.UnixMilli(n)
	}
	if !t.Before(quotaNoExpiry) {
		return time.Time{}
	}
	return t.UTC()
}

// quotaNumber accepts JSON numbers and numeric strings. Anything else reads as
// zero so one odd field cannot hide the rest of the snapshot.
type quotaNumber float64

func (n *quotaNumber) UnmarshalJSON(raw []byte) error {
	v, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(string(raw)), `"`), 64)
	if err != nil {
		v = 0
	}
	*n = quotaNumber(v)
	return nil
}

// quotaString accepts JSON strings and numbers (ids are sometimes numeric).
type quotaString string

func (s *quotaString) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		*s = ""
		return nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*s = quotaString(strings.TrimSpace(text))
		return nil
	}
	*s = quotaString(string(raw))
	return nil
}
