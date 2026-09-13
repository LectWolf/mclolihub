package codebuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SegmentLabelUnknown is the locale-neutral placeholder for packages the billing
// API returns without any human-readable name. Callers localize it for display.
const SegmentLabelUnknown = "credits"

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
	// Estimated reports that Deduct has drawn the balance down locally since the
	// last billing probe, so the figure trails real consumption rather than
	// matching the upstream ledger exactly.
	Estimated bool `json:"estimated,omitempty"`
	// Exhausted is set when the upstream rejected a request for lack of credits,
	// which supersedes a stale positive balance.
	Exhausted bool `json:"exhausted,omitempty"`
}

// Deduct draws a locally observed charge out of the cached balance so the admin
// UI tracks consumption between billing probes. Segments expiring soonest are
// drained first, matching CodeBuddy's use-it-or-lose-it ordering.
func (s *CreditsSnapshot) Deduct(amount float64) {
	if s == nil || amount <= 0 {
		return
	}
	order := make([]int, 0, len(s.Segments))
	for i := range s.Segments {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		return expiryRank(s.Segments[order[a]]) < expiryRank(s.Segments[order[b]])
	})
	remaining := amount
	for _, index := range order {
		if remaining <= 0 {
			break
		}
		segment := &s.Segments[index]
		if segment.Remaining <= 0 {
			continue
		}
		taken := math.Min(segment.Remaining, remaining)
		segment.Remaining = round2(segment.Remaining - taken)
		remaining -= taken
	}
	if len(s.Segments) == 0 {
		s.Credits = round2(math.Max(0, s.Credits-amount))
	} else {
		total := 0.0
		for _, segment := range s.Segments {
			total += segment.Remaining
		}
		s.Credits = round2(total)
	}
	s.SoonestExpiry = soonestExpiry(s.Segments, time.Now().Unix())
	s.Estimated = true
}

func expiryRank(segment CreditSegment) float64 {
	if segment.ExpiresAt == nil {
		return math.MaxFloat64
	}
	return *segment.ExpiresAt
}

// creditsExhaustedMarkers are the upstream phrases that blame an empty credit
// balance. CodeBuddy reports this as a business error nested in an otherwise
// ordinary 4xx, so the body has to be inspected rather than the status alone.
var creditsExhaustedMarkers = []string{
	"insufficient credit",
	"insufficient_credit",
	"insufficient balance",
	"insufficient_balance",
	"insufficient quota",
	"credit not enough",
	"credit_not_enough",
	"quota exhausted",
	"quota_exhausted",
	"out of credits",
	"积分不足",
	"额度不足",
	"余额不足",
	"资源包已用完",
}

// CreditsExhausted reports whether an upstream rejection blames an empty credit
// balance, which makes a cached positive balance stale regardless of its age.
func CreditsExhausted(statusCode int, body []byte) bool {
	if statusCode < 400 || len(body) == 0 {
		return false
	}
	if len(body) > 16<<10 {
		body = body[:16<<10]
	}
	lowered := strings.ToLower(string(body))
	for _, marker := range creditsExhaustedMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
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

// ErrAuthExpired marks a 401 from a billing endpoint: the access token is dead,
// so retrying the same request is pointless and the caller should refresh.
var ErrAuthExpired = errors.New("codebuddy billing credentials expired")

// creditsFetchAttempts covers an upstream quirk: the resource endpoint
// intermittently answers 200 with an empty Accounts list, which is
// indistinguishable from a genuinely empty balance without a retry.
const creditsFetchAttempts = 3

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
	headers := WebHeaders(host, creds.AccessToken, creds.UID, creds.Domain)
	intl := IsInternationalHost(host)

	var lastErr error
	for attempt := range creditsFetchAttempts {
		if attempt > 0 {
			delay := time.Duration(attempt) * 300 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		raw, err := postBillingJSON(ctx, client, host+ResourcePath, headers, ResourceBody(time.Now()))
		if err != nil {
			if errors.Is(err, ErrAuthExpired) {
				return nil, err
			}
			lastErr = err
			continue
		}
		snapshot, err := ParseCredits(raw, intl)
		if err != nil {
			lastErr = err
			continue
		}
		if snapshot.Count == 0 && attempt < creditsFetchAttempts-1 {
			lastErr = fmt.Errorf("credits response carried no accounts")
			continue
		}
		snapshot.FetchedAt = time.Now().Unix()
		return snapshot, nil
	}
	return nil, fmt.Errorf("query credits: %w", lastErr)
}

// IsInternationalHost reports whether a billing host belongs to the .ai estate.
// The two estates are fully isolated: a domestic token always gets a 401 there,
// and their credits carry different per-credit prices.
func IsInternationalHost(host string) bool {
	return strings.Contains(strings.ToLower(host), ".ai")
}

func doBillingRequest(ctx context.Context, client HTTPDoer, url string, headers map[string]string, body map[string]any) ([]byte, int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, err
	}
	ApplyHeadersToRequest(req, headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
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
				Source:      firstText(item, labelFields, SegmentLabelUnknown),
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
	// Soonest expiry first (undated packages last), matching the order credits
	// are actually spent in and the order the UI should present them.
	sort.SliceStable(out, func(a, b int) bool {
		return expiryRank(out[a]) < expiryRank(out[b])
	})
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
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func round2(n float64) float64 {
	return math.Round(n*100) / 100
}
