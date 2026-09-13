package codebuddy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	OAuthTimeout    = 10 * time.Minute
	ResultRetention = 5 * time.Minute
	RequestTimeout  = 15 * time.Second
	RefreshTimeout  = 15 * time.Second
)

type APIResponse struct {
	Code    any             `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (r APIResponse) Success() bool {
	switch v := r.Code.(type) {
	case float64:
		return v == 0 || v == 200
	case json.Number:
		n, _ := v.Int64()
		return n == 0 || n == 200
	case string:
		return v == "0" || v == "200"
	default:
		return false
	}
}

func (r APIResponse) ErrorMessage() string {
	if strings.TrimSpace(r.Msg) != "" {
		return r.Msg
	}
	if strings.TrimSpace(r.Message) != "" {
		return r.Message
	}
	return "upstream request failed"
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func NewClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Timeout: RequestTimeout, Transport: transport}, nil
}

func DoJSON(ctx context.Context, client HTTPDoer, method, rawURL string, headers map[string]string, body any) (*APIResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", DefaultUserAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var parsed APIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode upstream json (http %d): %w", resp.StatusCode, err)
	}
	return &parsed, nil
}

type oauthSession struct {
	State     string
	Host      string
	ExpiresAt time.Time
	Done      bool
	Result    *Credentials
	Error     string
	Client    HTTPDoer
}

type OAuthManager struct {
	mu       sync.Mutex
	sessions map[string]*oauthSession
	client   HTTPDoer
	now      func() time.Time
}

func NewOAuthManager(client HTTPDoer) *OAuthManager {
	if client == nil {
		client = http.DefaultClient
	}
	return &OAuthManager{
		sessions: make(map[string]*oauthSession),
		client:   client,
		now:      time.Now,
	}
}

type StartResult struct {
	LoginID         string `json:"login_id"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
}

func (m *OAuthManager) Start(ctx context.Context, site string, client HTTPDoer) (*StartResult, error) {
	if client == nil {
		client = m.client
	}
	host, err := AuthHostForSite(site)
	if err != nil {
		return nil, err
	}
	resp, err := DoJSON(ctx, client, http.MethodPost, host+AuthStatePath, map[string]string{
		"User-Agent":   DefaultUserAgent,
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}, map[string]any{})
	if err != nil {
		return nil, err
	}
	var data struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
		URL     string `json:"url"`
		AuthUrl string `json:"auth_url"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, fmt.Errorf("decode auth/state: %w", err)
	}
	if data.State == "" {
		return nil, fmt.Errorf("auth/state missing state: %s", resp.ErrorMessage())
	}
	authURL := firstNonEmpty(data.AuthURL, data.AuthUrl, data.URL)
	if authURL == "" {
		authURL = host + "/login?state=" + url.QueryEscape(data.State)
	}
	loginID := randomLoginID()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeLocked()
	m.sessions[loginID] = &oauthSession{
		State:     data.State,
		Host:      host,
		ExpiresAt: m.now().Add(OAuthTimeout),
		Client:    client,
	}
	return &StartResult{
		LoginID:         loginID,
		VerificationURI: authURL,
		ExpiresIn:       int64(OAuthTimeout / time.Second),
	}, nil
}

type PollResult struct {
	Done     bool         `json:"done"`
	Error    string       `json:"error,omitempty"`
	UID      string       `json:"uid,omitempty"`
	Nickname string       `json:"nickname,omitempty"`
	Creds    *Credentials `json:"-"`
}

func (m *OAuthManager) Poll(ctx context.Context, loginID string) (*PollResult, error) {
	m.mu.Lock()
	m.purgeLocked()
	session := m.sessions[loginID]
	m.mu.Unlock()
	if session == nil {
		return &PollResult{Done: true, Error: "login request not found or expired"}, nil
	}
	if session.Done {
		return pollFromSession(session), nil
	}
	if m.now().After(session.ExpiresAt) {
		m.mu.Lock()
		session.Done = true
		session.Error = "login timed out"
		m.mu.Unlock()
		return pollFromSession(session), nil
	}

	client := session.Client
	if client == nil {
		client = m.client
	}
	tokenURL := session.Host + AuthTokenPath + "?state=" + url.QueryEscape(session.State)
	resp, err := DoJSON(ctx, client, http.MethodGet, tokenURL, map[string]string{
		"User-Agent": DefaultUserAgent,
		"Accept":     "application/json",
	}, nil)
	if err != nil || !resp.Success() {
		return &PollResult{Done: false}, nil
	}
	var tokenData map[string]any
	if err := json.Unmarshal(resp.Data, &tokenData); err != nil {
		return &PollResult{Done: false}, nil
	}
	accessToken := stringField(tokenData, "accessToken", "access_token")
	if accessToken == "" {
		return &PollResult{Done: false}, nil
	}

	accountHeaders := map[string]string{
		"User-Agent":    DefaultUserAgent,
		"Accept":        "application/json",
		"Authorization": "Bearer " + accessToken,
	}
	if domain := stringField(tokenData, "domain"); domain != "" {
		accountHeaders["X-Domain"] = domain
	}
	accountURL := session.Host + LoginAccountPath + "?state=" + url.QueryEscape(session.State)
	accResp, err := DoJSON(ctx, client, http.MethodGet, accountURL, accountHeaders, nil)
	if err != nil {
		return &PollResult{Done: false}, nil
	}
	var accountData map[string]any
	if err := json.Unmarshal(accResp.Data, &accountData); err != nil {
		return &PollResult{Done: true, Error: "official account payload is invalid"}, nil
	}
	uid := stringField(accountData, "uid")
	if uid == "" {
		return &PollResult{Done: true, Error: "official API did not return uid"}, nil
	}
	creds, err := ParseCredentials(map[string]any{
		"account": accountData,
		"auth":    tokenData,
	})
	if err != nil {
		return &PollResult{Done: true, Error: err.Error()}, nil
	}
	m.mu.Lock()
	session.Done = true
	session.Result = &creds
	m.mu.Unlock()
	return pollFromSession(session), nil
}

func (m *OAuthManager) Take(loginID string) (*Credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[loginID]
	if session == nil {
		return nil, fmt.Errorf("login request not found or expired")
	}
	if !session.Done {
		return nil, fmt.Errorf("login is not complete")
	}
	if session.Error != "" {
		return nil, fmt.Errorf("%s", session.Error)
	}
	if session.Result == nil {
		return nil, fmt.Errorf("login result is empty")
	}
	creds := *session.Result
	delete(m.sessions, loginID)
	return &creds, nil
}

func (m *OAuthManager) purgeLocked() {
	now := m.now()
	for id, session := range m.sessions {
		if now.After(session.ExpiresAt.Add(ResultRetention)) {
			delete(m.sessions, id)
		}
	}
}

func pollFromSession(session *oauthSession) *PollResult {
	result := &PollResult{Done: session.Done, Error: session.Error}
	if session.Result != nil {
		result.UID = session.Result.UID
		result.Nickname = session.Result.Nickname
		result.Creds = session.Result
	}
	return result
}

func randomLoginID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("oa_%d", time.Now().UnixNano())
	}
	return "oa_" + hex.EncodeToString(buf[:])
}

type RefreshResult struct {
	AccessToken      string
	RefreshToken     string
	IDToken          string
	TokenType        string
	ExpiresAt        int64
	RefreshExpiresAt int64
	Domain           string
}

func Refresh(ctx context.Context, client HTTPDoer, creds Credentials, proxyURL string) (*RefreshResult, error) {
	if client == nil {
		built, err := NewClient(proxyURL)
		if err != nil {
			return nil, err
		}
		client = built
	}
	profile := creds.Profile
	if profile == "" {
		parsed, err := ProfileForAuth(creds.Domain, creds.AccessToken)
		if err != nil {
			return nil, err
		}
		profile = parsed
	}
	headers := CredentialHeaders(profile, creds.AccessToken, creds.Domain, creds.UID, creds.EnterpriseID)
	headers["X-Refresh-Token"] = creds.RefreshToken
	headers["X-Auth-Refresh-Source"] = "plugin"
	resp, err := DoJSON(ctx, client, http.MethodPost, RefreshURL(profile), headers, map[string]any{})
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, fmt.Errorf("refresh token failed: %s", resp.ErrorMessage())
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, fmt.Errorf("decode refresh payload: %w", err)
	}
	accessToken := stringField(data, "accessToken", "access_token")
	if accessToken == "" {
		return nil, fmt.Errorf("refresh response missing accessToken")
	}
	expiresAt := unixSeconds(data["expiresAt"], data["expires_at"])
	if expiresAt == 0 {
		if in := unixSeconds(data["expiresIn"], data["expires_in"]); in > 0 && in < 1e10 {
			expiresAt = time.Now().Unix() + in
		}
	}
	refreshExpiresAt := unixSeconds(data["refreshExpiresAt"], data["refresh_expires_at"])
	if refreshExpiresAt == 0 {
		if in := unixSeconds(data["refreshExpiresIn"], data["refresh_expires_in"]); in > 0 && in < 1e10 {
			refreshExpiresAt = time.Now().Unix() + in
		}
	}
	return &RefreshResult{
		AccessToken:      accessToken,
		RefreshToken:     firstNonEmpty(stringField(data, "refreshToken", "refresh_token"), creds.RefreshToken),
		IDToken:          firstNonEmpty(stringField(data, "idToken", "id_token"), creds.IDToken),
		TokenType:        firstNonEmpty(stringField(data, "tokenType", "token_type"), "Bearer"),
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
		Domain:           firstNonEmpty(stringField(data, "domain"), creds.Domain),
	}, nil
}
