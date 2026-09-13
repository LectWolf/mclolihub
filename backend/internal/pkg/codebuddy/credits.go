package codebuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CreditSegment struct {
	Remaining   float64  `json:"remaining"`
	Total       float64  `json:"total"`
	ExpiresAt   *float64 `json:"expires_at,omitempty"`
	Source      string   `json:"source,omitempty"`
	PackageCode string   `json:"package_code,omitempty"`
}

type CreditsSnapshot struct {
	Credits       float64         `json:"credits"`
	Count         int             `json:"count"`
	Segments      []CreditSegment `json:"segments,omitempty"`
	SoonestExpiry *float64        `json:"soonest_expiry,omitempty"`
	Intl          bool            `json:"intl"`
	FetchedAt     int64           `json:"fetched_at"`
}

func BillingHost(profile string) string {
	if host, ok := BillingHosts[profile]; ok {
		return host
	}
	return AuthHostCN
}

func ResourceURL(profile string) string {
	return BillingHost(profile) + ResourcePath
}

func WebHeaders(host, accessToken, uid, domain string) map[string]string {
	return map[string]string{
		"accept":            "application/json, text/plain, */*",
		"content-type":      "application/json",
		"x-client-platform": "web",
		"origin":            host,
		"referer":           host + "/profile/plans-usage",
		"authorization":     "Bearer " + accessToken,
		"x-user-id":         uid,
		"x-domain":          domain,
		"user-agent":        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36",
	}
}

func ResourceBody(now time.Time) map[string]any {
	const layout = "2006-01-02 15:04:05"
	return map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              ResourceProductCode,
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format(layout),
		"PackageEndTimeRangeEnd":   now.Add(101 * 365 * 24 * time.Hour).Format(layout),
	}
}

func FetchCredits(ctx context.Context, client HTTPDoer, creds Credentials) (*CreditsSnapshot, error) {
	if client == nil {
		client = http.DefaultClient
	}
	profile := creds.Profile
	if profile == "" {
		parsed, err := ProfileForAuth(creds.Domain, creds.AccessToken)
		if err != nil {
			return nil, err
		}
		profile = parsed
	}
	host := BillingHost(profile)
	body, err := json.Marshal(ResourceBody(time.Now()))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+ResourcePath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	ApplyHeadersToRequest(req, WebHeaders(host, creds.AccessToken, creds.UID, creds.Domain))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("credits unauthorized")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("credits HTTP %d", resp.StatusCode)
	}
	snapshot, err := ParseCredits(raw, strings.Contains(strings.ToLower(host), ".ai"))
	if err != nil {
		return nil, err
	}
	snapshot.FetchedAt = time.Now().Unix()
	return snapshot, nil
}

func ParseCredits(payload []byte, intl bool) (*CreditsSnapshot, error) {
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("decode credits: %w", err)
	}
	accounts := extractAccounts(root)
	segments := mergeSegments(extractSegments(accounts))
	remaining := 0.0
	for _, segment := range segments {
		remaining += segment.Remaining
	}
	return &CreditsSnapshot{
		Credits:       round2(remaining),
		Count:         len(accounts),
		Segments:      segments,
		SoonestExpiry: soonestExpiry(segments, time.Now().Unix()),
		Intl:          intl,
	}, nil
}

func extractAccounts(root map[string]any) []map[string]any {
	data, _ := root["data"].(map[string]any)
	candidates := []any{
		lookup(data, "Response", "Data", "Accounts"),
		lookup(data, "data", "Response", "Data", "Accounts"),
		lookup(data, "Accounts"),
		lookup(root, "data", "accounts"),
	}
	for _, candidate := range candidates {
		raw, ok := candidate.([]any)
		if !ok {
			continue
		}
		out := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func lookup(v any, keys ...string) any {
	cur := v
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

var remainingFields = []string{
	"SlicePeriodCapacityRemainPrecise", "SlicePeriodCapacityRemain",
	"CycleCapacityRemainPrecise", "CycleCapacityRemain",
	"CapacityRemainPrecise", "CapacityRemain",
	"RemainPrecise", "Remain", "Remaining", "Balance",
}
var totalFields = []string{
	"SlicePeriodCapacitySizePrecise", "SlicePeriodCapacitySize",
	"CycleCapacitySizePrecise", "CycleCapacitySize",
	"CycleCapacityPrecise", "CycleCapacity",
	"CapacityPrecise", "Capacity", "TotalCapacityPrecise", "TotalCapacity",
	"PackageCapacity", "Quota", "Amount",
}
var expiryFields = []string{
	"DeductionEndTime", "ExpiredTime", "SlicePeriodEndTime", "PackageEndTime",
	"EndTime", "CycleEndTime", "ExpireTime", "ExpirationTime",
	"ValidEndTime", "ValidPeriodEndTime", "EndAt", "ExpireAt",
}
var labelFields = []string{
	"PackageName", "PackageTypeName", "AccountName", "ProductName",
	"Name", "RuleName", "Description",
}

func extractSegments(accounts []map[string]any) []CreditSegment {
	out := make([]CreditSegment, 0)
	for _, account := range accounts {
		items := []map[string]any{account}
		if details, ok := account["SlicePeriodUsageDetails"].([]any); ok && len(details) > 0 {
			items = items[:0]
			for _, detail := range details {
				merged := map[string]any{}
				for k, v := range account {
					merged[k] = v
				}
				if dm, ok := detail.(map[string]any); ok {
					for k, v := range dm {
						merged[k] = v
					}
				}
				items = append(items, merged)
			}
		}
		for _, item := range items {
			remaining, ok := firstNumber(item, remainingFields)
			if !ok || remaining <= 0 {
				continue
			}
			total, hasTotal := firstNumber(item, totalFields)
			if !hasTotal || total < remaining {
				total = remaining
			}
			out = append(out, CreditSegment{
				Remaining:   round2(remaining),
				Total:       round2(total),
				ExpiresAt:   firstTimestamp(item, expiryFields),
				Source:      firstText(item, labelFields, "积分"),
				PackageCode: asString(item["PackageCode"]),
			})
		}
	}
	return out
}

func mergeSegments(segments []CreditSegment) []CreditSegment {
	type key struct {
		code string
		exp  float64
		has  bool
	}
	merged := map[key]CreditSegment{}
	order := make([]key, 0)
	for _, segment := range segments {
		if segment.Remaining <= 0 {
			continue
		}
		k := key{code: segment.PackageCode}
		if k.code == "" {
			k.code = segment.Source
		}
		if segment.ExpiresAt != nil {
			k.exp = *segment.ExpiresAt
			k.has = true
		}
		if existing, ok := merged[k]; ok {
			existing.Remaining += segment.Remaining
			existing.Total += segment.Total
			merged[k] = existing
			continue
		}
		merged[k] = segment
		order = append(order, k)
	}
	out := make([]CreditSegment, 0, len(order))
	for _, k := range order {
		segment := merged[k]
		segment.Remaining = round2(segment.Remaining)
		segment.Total = round2(segment.Total)
		out = append(out, segment)
	}
	return out
}

func soonestExpiry(segments []CreditSegment, now int64) *float64 {
	var best *float64
	nowF := float64(now)
	for _, segment := range segments {
		if segment.ExpiresAt == nil || *segment.ExpiresAt <= nowF || segment.Remaining <= 0 {
			continue
		}
		if best == nil || *segment.ExpiresAt < *best {
			value := *segment.ExpiresAt
			best = &value
		}
	}
	return best
}

func firstNumber(item map[string]any, fields []string) (float64, bool) {
	for _, field := range fields {
		if n, ok := toFloat(item[field]); ok {
			return n, true
		}
	}
	return 0, false
}

func firstTimestamp(item map[string]any, fields []string) *float64 {
	for _, field := range fields {
		raw := item[field]
		if n, ok := toFloat(raw); ok && n > 0 {
			if n > 1e12 {
				n = n / 1000
			}
			return &n
		}
		if s, ok := raw.(string); ok {
			s = strings.TrimSpace(s)
			if len(s) >= 19 {
				if t, err := time.ParseInLocation("2006-01-02 15:04:05", s[:19], time.Local); err == nil {
					n := float64(t.Unix())
					return &n
				}
			}
		}
	}
	return nil
}

func firstText(item map[string]any, fields []string, fallback string) string {
	for _, field := range fields {
		if s := strings.TrimSpace(asString(item[field])); s != "" {
			return s
		}
	}
	return fallback
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconvParseFloat(n)
		return f, err == nil
	default:
		return 0, false
	}
}

func strconvParseFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	var n float64
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func round2(n float64) float64 {
	return float64(int(n*100+0.5)) / 100
}
