package qoderproxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPKCEChallengeLength(t *testing.T) {
	got := pkceChallenge("verifier-example")
	if len(got) != 43 || strings.Contains(got, "=") {
		t.Fatalf("challenge %q", got)
	}
}

func TestStartLoginURL(t *testing.T) {
	start, err := StartLogin("cn", "machine-1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(start.LoginURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "qoder.com.cn" || parsed.Path != "/device/selectAccounts" {
		t.Fatalf("url %s", start.LoginURL)
	}
	q := parsed.Query()
	if q.Get("challenge_method") != "S256" || q.Get("machine_id") != "machine-1" {
		t.Fatalf("query %v", q)
	}
	if q.Get("challenge") != pkceChallenge(start.Verifier) {
		t.Fatal("challenge does not match verifier")
	}
	if q.Get("client_id") != deviceClientID || q.Get("nonce") == "" {
		t.Fatalf("query %v", q)
	}
	if start.Region != RegionCN {
		t.Fatalf("region %s", start.Region)
	}
}

func TestRegionEndpoints(t *testing.T) {
	cn, err := StartLogin("cn", "")
	if err != nil {
		t.Fatal(err)
	}
	global, err := StartLogin("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cn.LoginURL, "https://qoder.com.cn/device/selectAccounts") {
		t.Fatal(cn.LoginURL)
	}
	if !strings.Contains(global.LoginURL, "https://qoder.com/device/selectAccounts") {
		t.Fatal(global.LoginURL)
	}
	if !strings.Contains(cn.LoginURL, "qoder-work-cn") || !strings.Contains(global.LoginURL, "qoder.com%2F") && !strings.Contains(global.LoginURL, "aicoding") {
		t.Fatalf("redirects cn=%s global=%s", cn.LoginURL, global.LoginURL)
	}
	if RegionCN.APIBase() != "https://openapi.qoder.com.cn" || RegionGlobal.APIBase() != "https://openapi.qoder.sh" {
		t.Fatal("api bases")
	}
	if !strings.Contains(RegionCN.ChatEndpoint(), "https://gateway.qoder.com.cn/") {
		t.Fatal(RegionCN.ChatEndpoint())
	}
	if !strings.Contains(RegionGlobal.ChatEndpoint(), "https://api2.qoder.sh/") {
		t.Fatal(RegionGlobal.ChatEndpoint())
	}
	if AccountRegion("") != RegionGlobal || AccountRegion("cn") != RegionCN || AccountRegion("global") != RegionGlobal {
		t.Fatal("stored region")
	}
}

func TestPollLoginPending(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/deviceToken/poll") {
			t.Fatalf("path %s", req.URL.Path)
		}
		if req.URL.Query().Get("verifier") != "ver" || req.URL.Query().Get("nonce") != "nonce" {
			t.Fatalf("query %s", req.URL.RawQuery)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	_, err := PollLogin(t.Context(), client, "global", "nonce", "ver")
	if !errorsIsPending(err) {
		t.Fatal(err)
	}
}

func TestPollLoginSuccess(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "openapi.qoder.sh" {
			t.Fatalf("host %s", req.URL.Host)
		}
		body := `{"token":"dt-1","refresh_token":"drt-1","user_id":"user-1","expires_in":3600}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	tok, err := PollLogin(t.Context(), client, "global", "nonce", "ver")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "dt-1" || tok.RefreshToken != "drt-1" || tok.UserID != "user-1" || tok.ExpiresAt.IsZero() {
		t.Fatalf("%+v", tok)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func errorsIsPending(err error) bool {
	return err == ErrLoginPending
}
