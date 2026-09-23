package qoderproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Device login client id shared by the China and global Qoder apps.
const deviceClientID = "1c5e33e1-364d-4ce6-b02c-acaa81274a5c"

// ErrLoginPending means the browser login has not finished yet.
var ErrLoginPending = errors.New("qoder login pending")

// Region selects the Qoder site used for device login and chat.
type Region string

const (
	RegionCN     Region = "cn"
	RegionGlobal Region = "global"
)

// NormalizeRegion maps a login request onto a site. An empty value follows the
// account form default, which is the China edition.
func NormalizeRegion(raw string) Region {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "global", "intl", "qoder.sh", "qoder.com":
		return RegionGlobal
	default:
		return RegionCN
	}
}

// AccountRegion reads a stored qoder_region. An empty value stays on the global
// site so accounts created before the edition switch keep their original host.
func AccountRegion(raw string) Region {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "cn", "china", "domestic", "qoder.com.cn":
		return RegionCN
	default:
		return RegionGlobal
	}
}

func (r Region) APIBase() string {
	if r == RegionGlobal {
		return "https://openapi.qoder.sh"
	}
	return "https://openapi.qoder.com.cn"
}

func (r Region) webBase() string {
	if r == RegionGlobal {
		return "https://qoder.com"
	}
	return "https://qoder.com.cn"
}

func (r Region) redirectURI() string {
	if r == RegionGlobal {
		return "qoder://aicoding.aicoding-agent/login-success"
	}
	return "qoder-work-cn://"
}

// ChatEndpoint is the signed agent SSE URL for this region.
func (r Region) ChatEndpoint() string {
	host := "https://gateway.qoder.com.cn"
	if r == RegionGlobal {
		host = "https://api2.qoder.sh"
	}
	return host + "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common"
}

// LoginStart is what the browser login page and the later poll need.
type LoginStart struct {
	LoginURL  string
	Nonce     string
	Verifier  string
	MachineID string
	Region    Region
}

// DeviceToken is a completed device login or refresh.
type DeviceToken struct {
	AccessToken  string
	RefreshToken string
	UserID       string
	ExpiresAt    time.Time
}

// StartLogin builds a PKCE device-login URL. An empty machineID is generated.
func StartLogin(region, machineID string) (LoginStart, error) {
	site := NormalizeRegion(region)
	verifier, err := randomVerifier(64)
	if err != nil {
		return LoginStart{}, err
	}
	nonce := newID()
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		machineID = newID()
	}
	query := url.Values{}
	query.Set("nonce", nonce)
	query.Set("challenge", pkceChallenge(verifier))
	query.Set("challenge_method", "S256")
	query.Set("machine_id", machineID)
	query.Set("client_id", deviceClientID)
	query.Set("redirect_uri", site.redirectURI())
	return LoginStart{
		LoginURL:  site.webBase() + "/device/selectAccounts?" + query.Encode(),
		Nonce:     nonce,
		Verifier:  verifier,
		MachineID: machineID,
		Region:    site,
	}, nil
}

// PollLogin waits one round for the device token. ErrLoginPending means try
// again; an *HTTPError means Qoder rejected the login session.
func PollLogin(ctx context.Context, doer Doer, region, nonce, verifier string) (DeviceToken, error) {
	site := NormalizeRegion(region)
	query := url.Values{}
	query.Set("nonce", strings.TrimSpace(nonce))
	query.Set("verifier", strings.TrimSpace(verifier))
	query.Set("challenge_method", "S256")
	endpoint := site.APIBase() + "/api/v1/deviceToken/poll?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return DeviceToken{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := doerOrDefault(doer).Do(req)
	if err != nil {
		return DeviceToken{}, fmt.Errorf("qoder: device poll: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DeviceToken{}, err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusAccepted {
		return DeviceToken{}, ErrLoginPending
	}
	if resp.StatusCode != http.StatusOK {
		return DeviceToken{}, &HTTPError{Op: "device poll", Status: resp.StatusCode, Body: string(body)}
	}
	tok, ok := parseDeviceToken(body)
	if !ok {
		return DeviceToken{}, ErrLoginPending
	}
	return tok, nil
}

// RefreshLogin exchanges a device refresh token for a new access token on the
// account's site. An *HTTPError with a 4xx status means the refresh token is
// no longer accepted.
func RefreshLogin(ctx context.Context, doer Doer, site Region, refreshToken string) (DeviceToken, error) {
	if site != RegionCN {
		site = RegionGlobal
	}
	raw, err := json.Marshal(map[string]string{"refresh_token": strings.TrimSpace(refreshToken)})
	if err != nil {
		return DeviceToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, site.APIBase()+"/api/v1/deviceToken/refresh", bytes.NewReader(raw))
	if err != nil {
		return DeviceToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := doerOrDefault(doer).Do(req)
	if err != nil {
		return DeviceToken{}, fmt.Errorf("qoder: device refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DeviceToken{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return DeviceToken{}, &HTTPError{Op: "device refresh", Status: resp.StatusCode, Body: string(body)}
	}
	tok, ok := parseDeviceToken(body)
	if !ok {
		return DeviceToken{}, fmt.Errorf("qoder: device refresh returned no token")
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = strings.TrimSpace(refreshToken)
	}
	return tok, nil
}

func parseDeviceToken(body []byte) (DeviceToken, bool) {
	var parsed struct {
		Token        string          `json:"token"`
		DeviceToken  string          `json:"device_token"`
		RefreshToken string          `json:"refresh_token"`
		UserID       string          `json:"user_id"`
		ExpiresAt    string          `json:"expires_at"`
		ExpiresIn    int64           `json:"expires_in"`
		RawExpires   json.RawMessage `json:"expiresAt"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return DeviceToken{}, false
	}
	access := firstNonEmpty(parsed.Token, parsed.DeviceToken)
	if access == "" {
		return DeviceToken{}, false
	}
	expiresAt := parsed.ExpiresAt
	if expiresAt == "" && len(parsed.RawExpires) > 0 {
		var asString string
		if json.Unmarshal(parsed.RawExpires, &asString) == nil {
			expiresAt = asString
		}
	}
	return DeviceToken{
		AccessToken:  access,
		RefreshToken: parsed.RefreshToken,
		UserID:       parsed.UserID,
		ExpiresAt:    expiry(expiresAt, parsed.ExpiresIn),
	}, true
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomVerifier(n int) (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}
