package cursorproxy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// IDECredentials are stored on a cursor (IDE) account.
// SessionToken comes from Cursor CLI login (loginDeepControl), type=session.
type IDECredentials struct {
	SessionToken  string
	RefreshToken  string
	ClientVersion string
	MachineID     string
}

func ideVersion(creds IDECredentials) string {
	if v := strings.TrimSpace(creds.ClientVersion); v != "" {
		return v
	}
	return IDEClientVersion
}

func ideHeaders(creds IDECredentials, requestID string) http.Header {
	machineID := strings.TrimSpace(creds.MachineID)
	if machineID == "" {
		machineID = sandMachineID(SandCredentials{RenewalCredential: creds.SessionToken})
	}
	h := make(http.Header)
	h.Set("content-type", connectContentType)
	h.Set("connect-protocol-version", "1")
	h.Set("connect-accept-encoding", "identity")
	h.Set("te", "trailers")
	h.Set("x-cursor-client-type", IDEClientType)
	h.Set("x-cursor-client-version", ideVersion(creds))
	h.Set("x-cursor-client-os", "win32")
	h.Set("x-cursor-checksum", Checksum(machineID))
	h.Set("x-ghost-mode", "true")
	h.Set("x-cursor-streaming", "true")
	if requestID != "" {
		h.Set("x-request-id", requestID)
	}
	h.Set("authorization", "Bearer "+strings.TrimSpace(creds.SessionToken))
	return h
}

// RunInferenceRunRequest is the first client frame on InferenceService/RunInference.
func RunInferenceRunRequest(conversationID, modelID, userText string) map[string]any {
	return map[string]any{
		"runRequest": map[string]any{
			"conversationId": conversationID,
			"requestedModel": map[string]any{
				"modelId":    modelID,
				"maxMode":    true,
				"parameters": []any{},
			},
			"routingConversation": []any{
				map[string]any{"role": "USER", "text": userText},
			},
		},
	}
}

// RunInferenceInvoke is sent after run_ready.
func RunInferenceInvoke(invocationID, conversationID, modelID string, messages []InferenceMessage) map[string]any {
	return map[string]any{
		"invokeModel": map[string]any{
			"invocationId": invocationID,
			"request": map[string]any{
				"messages": messages,
				"tools":    []any{},
				"requestedModel": map[string]any{
					"modelId":    modelID,
					"maxMode":    true,
					"parameters": []any{},
				},
				"conversationId": conversationID,
				"invocationId":   invocationID,
			},
		},
	}
}

func http2Client(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1"},
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

// RunInference opens InferenceService/RunInference over HTTP/2.
// The caller writes Connect frames into body (typically an io.Pipe).
func RunInference(ctx context.Context, client *http.Client, creds IDECredentials, body io.Reader, requestID string) (*http.Response, error) {
	if err := requireNonEmpty("session_token", strings.TrimSpace(creds.SessionToken)); err != nil {
		return nil, err
	}
	if client == nil {
		client = http2Client(3 * time.Minute)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BackendURL+RunInferencePath, body)
	if err != nil {
		return nil, err
	}
	req.Header = ideHeaders(creds, requestID)
	req.ContentLength = -1
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != 0 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("InferenceService/RunInference HTTP %d: %s", resp.StatusCode, truncate(string(raw), 240))
	}
	return resp, nil
}

// CLILoginSession is the PKCE state for Cursor CLI (agent login / loginDeepControl).
type CLILoginSession struct {
	UUID      string `json:"uuid"`
	Verifier  string `json:"verifier"`
	Challenge string `json:"challenge"`
	LoginURL  string `json:"login_url"`
}

type CLILoginToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Email        string `json:"email"`
	AuthID       string `json:"auth_id"`
}

var ErrCLILoginPending = errors.New("cursor CLI login pending")

func NewCLILoginSession() (CLILoginSession, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return CLILoginSession{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	id := strings.ToLower(fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]))
	loginURL := CLILoginPage + "?challenge=" + url.QueryEscape(challenge) + "&uuid=" + url.QueryEscape(id) + "&mode=login"
	return CLILoginSession{
		UUID:      id,
		Verifier:  verifier,
		Challenge: challenge,
		LoginURL:  loginURL,
	}, nil
}

func PollCLILogin(ctx context.Context, client *http.Client, sessionUUID, verifier string) (*CLILoginToken, error) {
	if err := requireNonEmpty("uuid", strings.TrimSpace(sessionUUID)); err != nil {
		return nil, err
	}
	if err := requireNonEmpty("verifier", strings.TrimSpace(verifier)); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	q := url.Values{}
	q.Set("uuid", sessionUUID)
	q.Set("verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BackendURL+CLILoginPollPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-ghost-mode", "true")
	req.Header.Set("user-agent", "Mozilla/5.0 Cursor-CLI")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return parseCLILoginToken(raw)
	case http.StatusNotFound, http.StatusNoContent, http.StatusAccepted:
		return nil, ErrCLILoginPending
	default:
		if looksPending(raw) {
			return nil, ErrCLILoginPending
		}
		return nil, fmt.Errorf("CLI login poll HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
}

func parseCLILoginToken(raw []byte) (*CLILoginToken, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("CLI login poll: %w", err)
	}
	token := firstString(payload, "accessToken", "access_token", "token")
	if token == "" {
		if firstString(payload, "status") == "pending" || firstString(payload, "error") == "pending" {
			return nil, ErrCLILoginPending
		}
		return nil, fmt.Errorf("CLI login poll: empty access token")
	}
	return &CLILoginToken{
		AccessToken:  token,
		RefreshToken: firstString(payload, "refreshToken", "refresh_token"),
		Email:        firstString(payload, "email", "authEmail"),
		AuthID:       firstString(payload, "authId", "auth_id"),
	}, nil
}

func looksPending(raw []byte) bool {
	s := strings.ToLower(string(raw))
	return strings.Contains(s, "pending") || strings.Contains(s, "not ready") || strings.TrimSpace(s) == ""
}
