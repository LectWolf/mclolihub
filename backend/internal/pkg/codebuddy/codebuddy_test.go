package codebuddy

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestProfileForAuth(t *testing.T) {
	token := jwtWithISS("https://www.codebuddy.cn/auth/realms/copilot")
	profile, err := ProfileForAuth("www.codebuddy.cn", token)
	if err != nil {
		t.Fatal(err)
	}
	if profile != ProfileCNCLI {
		t.Fatalf("got %s", profile)
	}

	intl, err := ProfileForAuth("www.workbuddy.ai", jwtWithISS("https://www.workbuddy.ai"))
	if err != nil {
		t.Fatal(err)
	}
	if intl != ProfileIntlWork {
		t.Fatalf("got %s", intl)
	}
}

func TestEnsureLeadingSystemMessage(t *testing.T) {
	body := []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)
	out, err := EnsureLeadingSystemMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Messages) != 2 || parsed.Messages[0].Role != "system" {
		t.Fatalf("unexpected messages: %+v", parsed.Messages)
	}
}

func TestParseOfficialInfoCredentials(t *testing.T) {
	raw := map[string]any{
		"account": map[string]any{"uid": "u1", "nickname": "n"},
		"auth": map[string]any{
			"accessToken":  jwtWithISS("https://www.codebuddy.cn/auth/realms/copilot"),
			"refreshToken": "rt",
			"domain":       "www.codebuddy.cn",
			"expiresAt":    float64(1_700_000_000_000),
		},
	}
	creds, err := ParseCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if creds.UID != "u1" || creds.Profile != ProfileCNCLI || creds.ExpiresAt != 1_700_000_000 {
		t.Fatalf("%+v", creds)
	}
}

func jwtWithISS(iss string) string {
	payload, _ := json.Marshal(map[string]string{"iss": iss})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestParseMultiplierAndZeroCredit(t *testing.T) {
	if got := ParseMultiplier("x0.00"); got == nil || *got != 0 {
		t.Fatalf("zero multiplier: %v", got)
	}
	if !IsZeroCredit("x0 credits") {
		t.Fatal("expected zero credit")
	}
	if IsZeroCredit("x1.00") {
		t.Fatal("x1 should not be zero")
	}
}

func TestParseCatalogAndFilter(t *testing.T) {
	payload := []byte(`{"code":0,"data":{"models":[
		{"id":"glm-5.2","name":"GLM","credits":"x1.00","aliases":["glm"]},
		{"id":"auto","name":"Auto","credits":"x0.00"},
		{"id":"hidden","disabled":true,"credits":"x0.00"}
	],"availableModels":["glm-5.2","auto"]}}`)
	models, err := ParseCatalog(payload, ProductCLI)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models", len(models))
	}
	zero := FilterCatalog(models, CreditPolicyZeroOnly)
	if len(zero) != 1 || zero[0].ID != "auto" {
		t.Fatalf("zero filter: %+v", zero)
	}
}

func TestParseCredits(t *testing.T) {
	payload := []byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[
		{"Remain": 12.5, "Capacity": 20, "PackageName": "试用", "PackageCode": "trial", "ExpiredTime": 1900000000}
	]}}}}`)
	snap, err := ParseCredits(payload, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Credits != 12.5 || len(snap.Segments) != 1 {
		t.Fatalf("%+v", snap)
	}
}

func TestModelMultiplier(t *testing.T) {
	models := []CatalogModel{
		{ID: "glm-5.2", Credits: "x1.50", Aliases: []string{"glm"}},
		{ID: "auto", Credits: "x0.00"},
		{ID: "mystery"},
	}
	if value, ok := ModelMultiplier(models, "glm-5.2"); !ok || value != 1.5 {
		t.Fatalf("by id: %v %v", value, ok)
	}
	if value, ok := ModelMultiplier(models, "GLM"); !ok || value != 1.5 {
		t.Fatalf("by alias, case-insensitive: %v %v", value, ok)
	}
	// A free model must stay distinguishable from one the catalog never priced.
	if value, ok := ModelMultiplier(models, "auto"); !ok || value != 0 {
		t.Fatalf("zero-credit model: %v %v", value, ok)
	}
	if _, ok := ModelMultiplier(models, "mystery"); ok {
		t.Fatal("model without a credits field must report unpriced")
	}
	if _, ok := ModelMultiplier(models, "absent"); ok {
		t.Fatal("model outside the catalog must report unpriced")
	}
}

func TestFindModelPrefersIDOverAlias(t *testing.T) {
	models := []CatalogModel{
		{ID: "decoy", Aliases: []string{"shared"}},
		{ID: "shared"},
	}
	model, ok := FindModel(models, "shared")
	if !ok || model.ID != "shared" {
		t.Fatalf("an alias must not shadow another model's real ID: %+v", model)
	}
}

func TestCreditsSnapshotDeductDrainsSoonestExpiryFirst(t *testing.T) {
	later := float64(1_900_000_000)
	sooner := float64(1_800_000_000)
	snapshot := &CreditsSnapshot{
		Credits: 30,
		Segments: []CreditSegment{
			{Remaining: 10, Total: 10, ExpiresAt: &later},
			{Remaining: 5, Total: 5, ExpiresAt: &sooner},
			{Remaining: 15, Total: 15},
		},
	}
	snapshot.Deduct(7)

	if snapshot.Segments[1].Remaining != 0 {
		t.Fatalf("soonest-expiring segment should drain first: %+v", snapshot.Segments)
	}
	if snapshot.Segments[0].Remaining != 8 {
		t.Fatalf("overflow should spill into the next expiring segment: %+v", snapshot.Segments)
	}
	if snapshot.Segments[2].Remaining != 15 {
		t.Fatalf("never-expiring segment should be untouched: %+v", snapshot.Segments)
	}
	if snapshot.Credits != 23 {
		t.Fatalf("credits = %v, want 23", snapshot.Credits)
	}
	if !snapshot.Estimated {
		t.Fatal("a local deduction must mark the snapshot as estimated")
	}
	if snapshot.SoonestExpiry == nil || *snapshot.SoonestExpiry != later {
		t.Fatalf("soonest expiry should skip drained segments: %v", snapshot.SoonestExpiry)
	}
}

func TestCreditsSnapshotDeductWithoutSegments(t *testing.T) {
	snapshot := &CreditsSnapshot{Credits: 2}
	snapshot.Deduct(5)
	if snapshot.Credits != 0 {
		t.Fatalf("balance must floor at zero, got %v", snapshot.Credits)
	}
}

func TestCreditsUsageCharge(t *testing.T) {
	day1 := time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local)
	usage := CreditsUsage{}.Charge(1.5, true, day1)
	usage = usage.Charge(0.5, true, day1)
	if usage.Requests != 2 || usage.Credits != 2 || usage.TotalCredits != 2 {
		t.Fatalf("%+v", usage)
	}

	// An unpriced model still counts as traffic but cannot add credits.
	usage = usage.Charge(0, false, day1)
	if usage.Requests != 3 || usage.Credits != 2 || usage.Unpriced != 1 {
		t.Fatalf("unpriced request: %+v", usage)
	}

	day2 := day1.Add(2 * time.Hour)
	usage = usage.Charge(3, true, day2)
	if usage.Day != day2.Format(usageDayLayout) {
		t.Fatalf("day = %q", usage.Day)
	}
	if usage.Requests != 1 || usage.Credits != 3 || usage.Unpriced != 0 {
		t.Fatalf("daily counters should reset at midnight: %+v", usage)
	}
	if usage.TotalRequests != 4 || usage.TotalCredits != 5 {
		t.Fatalf("lifetime counters must survive the rollover: %+v", usage)
	}
}

func TestMergeSegmentsOrdersBySoonestExpiry(t *testing.T) {
	later := float64(1_900_000_000)
	sooner := float64(1_800_000_000)
	merged := mergeSegments([]CreditSegment{
		{Remaining: 1, Total: 1, PackageCode: "undated"},
		{Remaining: 2, Total: 2, PackageCode: "later", ExpiresAt: &later},
		{Remaining: 3, Total: 3, PackageCode: "sooner", ExpiresAt: &sooner},
	})
	got := []string{merged[0].PackageCode, merged[1].PackageCode, merged[2].PackageCode}
	want := []string{"sooner", "later", "undated"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segment order = %v, want %v", got, want)
		}
	}
}

func TestMergeSegmentsCombinesSamePackageAndExpiry(t *testing.T) {
	expiry := float64(1_800_000_000)
	merged := mergeSegments([]CreditSegment{
		{Remaining: 2, Total: 5, PackageCode: "pkg", ExpiresAt: &expiry},
		{Remaining: 3, Total: 5, PackageCode: "pkg", ExpiresAt: &expiry},
	})
	if len(merged) != 1 || merged[0].Remaining != 5 || merged[0].Total != 10 {
		t.Fatalf("%+v", merged)
	}
}

func TestParseRequestUsagePage(t *testing.T) {
	payload := []byte(`{"code":0,"data":{"total":3,"data":[
		{"requestTime":"2026-09-13 10:00:00","model":"glm-5.2","credit":1.5},
		{"requestTime":"2026-09-13 11:30:00","model":"glm-5.2","credit":"0.5"},
		{"requestTime":"2026-09-12 09:00:00","credit":2}
	]}}`)
	rows, total := parseRequestUsagePage(payload)
	if total != 3 {
		t.Fatalf("total = %d", total)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].day != "2026-09-13" || rows[0].credit != 1.5 {
		t.Fatalf("first row: %+v", rows[0])
	}
	// Credits arrive as both numbers and numeric strings depending on the row.
	if rows[1].credit != 0.5 {
		t.Fatalf("string credit: %+v", rows[1])
	}
	if rows[2].model != "unknown" {
		t.Fatalf("a row without a model must be attributed, not dropped: %+v", rows[2])
	}
}

func TestParseRequestUsagePageSkipsUndatedRows(t *testing.T) {
	rows, _ := parseRequestUsagePage([]byte(`{"data":{"data":[{"model":"m","credit":1}]}}`))
	if len(rows) != 0 {
		t.Fatalf("a row without requestTime cannot be bucketed: %+v", rows)
	}
}

func TestRequestUsageToday(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.Local)
	usage := &RequestUsage{Days: []RequestUsageDay{
		{Day: "2026-09-13", Credits: 4, Requests: 2},
		{Day: "2026-09-12", Credits: 9, Requests: 5},
	}}
	today, ok := usage.Today(now)
	if !ok || today.Credits != 4 || today.Requests != 2 {
		t.Fatalf("%+v %v", today, ok)
	}
	if _, ok := (&RequestUsage{}).Today(now); ok {
		t.Fatal("an empty report has no entry for today")
	}
}

func TestIsInternationalHost(t *testing.T) {
	if !IsInternationalHost("https://www.workbuddy.ai") || !IsInternationalHost("https://www.codebuddy.ai") {
		t.Fatal("the .ai estate must be detected as international")
	}
	if IsInternationalHost("https://www.codebuddy.cn") || IsInternationalHost("https://copilot.tencent.com") {
		t.Fatal("domestic hosts must not be flagged international")
	}
}

func TestCreditsExhausted(t *testing.T) {
	if !CreditsExhausted(403, []byte(`{"code":40301,"msg":"账号积分不足，请充值"}`)) {
		t.Fatal("expected the Chinese exhaustion message to match")
	}
	if !CreditsExhausted(429, []byte(`{"error":{"message":"Insufficient Credit"}}`)) {
		t.Fatal("expected case-insensitive English matching")
	}
	if CreditsExhausted(200, []byte(`{"msg":"积分不足"}`)) {
		t.Fatal("a successful response is never an exhaustion signal")
	}
	if CreditsExhausted(500, []byte(`{"msg":"internal error"}`)) {
		t.Fatal("unrelated failures must not be read as exhaustion")
	}
}
