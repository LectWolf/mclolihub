package qoderproxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestTokenCacheSharesConcurrentExchange(t *testing.T) {
	var exchanges atomic.Int32
	release := make(chan struct{})
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/jobToken/exchange"):
			exchanges.Add(1)
			<-release
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"token":"jt-1","expires_in":3600}`))}, nil
		default:
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"user-1"}`))}, nil
		}
	})}
	cache := NewTokenCache()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, err := cache.ResolveRegion(t.Context(), client, RegionCN, "pt-1", "machine-1")
			if err != nil || identity.JobToken != "jt-1" || identity.MachineID != "machine-1" {
				t.Errorf("identity %+v err %v", identity, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("exchanges %d, want 1", got)
	}
}

func TestRefreshLoginRejected(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "openapi.qoder.com.cn" {
			t.Fatalf("host %s", req.URL.Host)
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"invalid refresh token"}`))}, nil
	})}
	_, err := RefreshLogin(t.Context(), client, RegionCN, "drt-dead")
	if !IsRejected(err) || IsAuthError(err) {
		t.Fatalf("err %v", err)
	}
}
