package qoderproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseQuotaPersonalPlan(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"userType":"personal_standard","usageType":"credits","totalUsagePercentage":37.0,
		"userQuota":{"total":5000,"used":1850,"remaining":3150,"percentage":37,"unit":"credits"},
		"expiresAt":1790812800000}`))
	if err != nil {
		t.Fatal(err)
	}
	if quota.UserType != "personal_standard" || quota.Exceeded {
		t.Fatalf("quota %+v", quota)
	}
	if quota.Plan == nil || quota.Plan.Total != 5000 || quota.Plan.Used != 1850 || quota.Plan.Remaining != 3150 || !quota.Plan.Available {
		t.Fatalf("plan %+v", quota.Plan)
	}
	if !quota.ExpiresAt.Equal(time.UnixMilli(1790812800000)) {
		t.Fatalf("expires %v", quota.ExpiresAt)
	}
	if quota.AddOn != nil || quota.Org != nil || len(quota.Packages) != 0 {
		t.Fatalf("unexpected pools %+v", quota)
	}
}

// A Teams seat can have no personal credits while the organization package
// still has plenty; the zero personal pool must not hide the package.
func TestParseQuotaTeamsOrgPackage(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"userType":"teams","usageType":"credits","isQuotaExceeded":false,
		"userQuota":{"total":0,"used":0,"remaining":0,"percentage":0,"unit":"credits"},
		"orgResourcePackage":{"available":true,"cap":76000,"used":13629,"remaining":62371,"percentage":0.18,"unit":"credits"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if quota.Plan != nil {
		t.Fatalf("zero personal quota should be omitted: %+v", quota.Plan)
	}
	if quota.Org == nil || quota.Org.Total != 76000 || quota.Org.Used != 13629 || quota.Org.Remaining != 62371 {
		t.Fatalf("org %+v", quota.Org)
	}
}

func TestParseQuotaChinaPackages(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"userId":12345,"userQuota":{"used":"120","total":"300"},
		"expiresAt":253402214400000,
		"dedicatedResourcePackages":[
			{"id":"pkg-1","name":"qwen","used":20,"total":500,"expiresAt":1790812800,
			 "displayLabels":[{"dimension":"title","value":"Qwen credits","valueI18n":{"zh-CN":"Qwen 专属积分","en-US":"Qwen credits"}}]},
			{"id":"pkg-2","name":"old","used":10,"total":10,"status":"QUOTA_DETAIL_STATUS_EXPIRED"},
			{"id":"pkg-3","name":"empty","used":0,"total":0}
		]}`))
	if err != nil {
		t.Fatal(err)
	}
	if quota.UserID != "12345" {
		t.Fatalf("user id %q", quota.UserID)
	}
	if !quota.ExpiresAt.IsZero() {
		t.Fatalf("year-9999 sentinel should read as no expiry, got %v", quota.ExpiresAt)
	}
	if quota.Plan == nil || quota.Plan.Remaining != 180 || quota.Plan.Unit != "credits" {
		t.Fatalf("plan %+v", quota.Plan)
	}
	if len(quota.Packages) != 2 {
		t.Fatalf("packages %+v", quota.Packages)
	}
	first := quota.Packages[0]
	if first.Name != "Qwen 专属积分" || first.Remaining != 480 || !first.Available || !first.ExpiresAt.Equal(time.Unix(1790812800, 0)) {
		t.Fatalf("package %+v", first)
	}
	if quota.Packages[1].Available {
		t.Fatalf("expired package should be unavailable: %+v", quota.Packages[1])
	}
}

func TestParseQuotaUnwrapsData(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"code":0,"data":{"userType":"pro","userQuota":{"total":10,"used":4}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if quota.UserType != "pro" || quota.Plan == nil || quota.Plan.Remaining != 6 {
		t.Fatalf("quota %+v plan %+v", quota, quota.Plan)
	}
}

func TestFetchQuotaRequest(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://openapi.qoder.com.cn/api/v2/quota/usage" {
			t.Fatalf("request %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer dt-1" {
			t.Fatalf("authorization %q", req.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"userQuota":{"total":100,"used":25}}`))}, nil
	})}
	quota, err := FetchQuota(context.Background(), client, RegionCN, " dt-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if quota.Plan == nil || quota.Plan.Remaining != 75 {
		t.Fatalf("plan %+v", quota.Plan)
	}
}

func TestFetchQuotaHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "openapi.qoder.sh" {
			t.Fatalf("host %s", req.URL.Host)
		}
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{"message":"token expired"}`))}, nil
	})}
	_, err := FetchQuota(context.Background(), client, RegionGlobal, "jt-1")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusUnauthorized || !IsAuthError(err) {
		t.Fatalf("err %v", err)
	}
}
