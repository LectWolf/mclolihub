package cursorproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// SandCredentials are stored on a cursor_sand account. RenewalCredential mints a
// grok_bot JWT when GrokBotToken is empty; a stored grok_bot token is used as-is.
type SandCredentials struct {
	RenewalCredential string
	GrokBotToken      string
	MachineID         string
	ClientOS          string
}

// StreamRequest is the InferenceService/Stream JSON body.
type StreamRequest struct {
	Messages       []InferenceMessage `json:"messages"`
	Tools          []any              `json:"tools"`
	RequestedModel RequestedModel     `json:"requestedModel"`
	ConversationID string             `json:"conversationId"`
	InvocationID   string             `json:"invocationId"`
}

type InferenceMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type RequestedModel struct {
	ModelID    string           `json:"modelId"`
	MaxMode    bool             `json:"maxMode"`
	Parameters []ModelParameter `json:"parameters"`
}

type ModelParameter struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type renewResponse struct {
	GrokBotToken string `json:"grokBotToken"`
	AccessToken  string `json:"accessToken"`
	ExpiresAtMs  int64  `json:"expiresAtMs"`
}

type cachedSandToken struct {
	grokBotToken string
	expiresAt    time.Time
}

// TokenCache memoizes grokBotToken minted from a renewal credential.
type TokenCache struct {
	mu    sync.Mutex
	items map[string]cachedSandToken
}

func NewTokenCache() *TokenCache {
	return &TokenCache{items: make(map[string]cachedSandToken)}
}

func (c *TokenCache) get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok || time.Now().After(item.expiresAt.Add(-60*time.Second)) {
		return "", false
	}
	return item.grokBotToken, true
}

func (c *TokenCache) put(key, token string, expires time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cachedSandToken{grokBotToken: token, expiresAt: expires}
}

func sandMachineID(creds SandCredentials) string {
	if strings.TrimSpace(creds.MachineID) != "" {
		return strings.TrimSpace(creds.MachineID)
	}
	sum := sha256.Sum256([]byte(creds.RenewalCredential))
	hexed := hex.EncodeToString(sum[:])
	return hexed[:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}

func sandHeaders(creds SandCredentials, machineID, requestID string) http.Header {
	clientOS := sandClientOS(creds)
	h := make(http.Header)
	h.Set("content-type", connectContentType)
	h.Set("connect-protocol-version", "1")
	h.Set("connect-accept-encoding", "identity")
	h.Set("x-cursor-client-type", SandClientType)
	h.Set("x-cursor-client-source", SandClientSource)
	h.Set("x-cursor-client-version", SandClientVersion)
	h.Set("x-sand-box-namespace", SandNamespace)
	h.Set("x-cursor-client-machine-id", machineID)
	h.Set("x-cursor-checksum", Checksum(machineID))
	h.Set("x-ghost-mode", "true")
	h.Set("x-cursor-client-os", clientOS)
	h.Set("User-Agent", sandUserAgent)
	h.Set("x-grok-client-version", xai.ResolveCLIVersion())
	h.Set("x-grok-client-identifier", xai.CLIClientIdentifier)
	h.Set("x-grok-client-mode", xai.CLIClientMode)
	if requestID != "" {
		h.Set("x-request-id", requestID)
	}
	return h
}

const sandUserAgent = "GrokBotLocalProxy/1.0"

func sandClientOS(creds SandCredentials) string {
	if osName := strings.TrimSpace(creds.ClientOS); osName != "" {
		return osName
	}
	return "linux"
}

// ResolveSandToken returns a grok_bot JWT: stored grokBotToken wins, otherwise renew.
func ResolveSandToken(ctx context.Context, client *http.Client, creds SandCredentials, cache *TokenCache) (string, error) {
	if token := strings.TrimSpace(creds.GrokBotToken); token != "" {
		return token, nil
	}
	return RenewGrokBotToken(ctx, client, creds, cache)
}

func renewHeaders() http.Header {
	h := make(http.Header)
	h.Set("content-type", "application/json")
	h.Set("x-cursor-client-type", SandClientType)
	h.Set("x-cursor-client-source", SandClientSource)
	h.Set("x-cursor-client-version", SandClientVersion)
	h.Set("x-sand-box-namespace", SandNamespace)
	return h
}

// RenewGrokBotToken mints grokBotToken from SAND_INFERENCE_RENEWAL_CREDENTIAL.
func RenewGrokBotToken(ctx context.Context, client *http.Client, creds SandCredentials, cache *TokenCache) (string, error) {
	if err := requireNonEmpty("sand_inference_renewal_credential", strings.TrimSpace(creds.RenewalCredential)); err != nil {
		return "", err
	}
	if token, ok := cache.get(creds.RenewalCredential); ok {
		return token, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(map[string]string{"credential": creds.RenewalCredential})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BackendURL+RenewPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header = renewHeaders()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inference-credential renew HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed renewResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("inference-credential renew: %w", err)
	}
	if parsed.GrokBotToken == "" {
		return "", fmt.Errorf("inference-credential renew returned empty grokBotToken")
	}
	exp := time.Now().Add(50 * time.Minute)
	if parsed.ExpiresAtMs > 0 {
		exp = time.UnixMilli(parsed.ExpiresAtMs)
	}
	cache.put(creds.RenewalCredential, parsed.GrokBotToken, exp)
	return parsed.GrokBotToken, nil
}

// Stream sends InferenceService/Stream and returns decoded frames.
func Stream(ctx context.Context, client *http.Client, token string, creds SandCredentials, payload StreamRequest, requestID string) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	machineID := sandMachineID(creds)
	env, err := Envelope(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BackendURL+StreamPath, bytes.NewReader(env))
	if err != nil {
		return nil, err
	}
	req.Header = sandHeaders(creds, machineID, requestID)
	req.Header.Set("authorization", "Bearer "+token)
	return client.Do(req)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
