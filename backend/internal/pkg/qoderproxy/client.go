package qoderproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Doer sends one HTTP request. *http.Client satisfies it; the gateway passes an
// adapter over its proxy-aware upstream client so every Qoder call (token
// exchange, device refresh, user info, chat) follows the account proxy.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

func doerOrDefault(d Doer) Doer {
	if d == nil {
		return http.DefaultClient
	}
	return d
}

// HTTPError is a non-2xx answer from a Qoder API. Body is bounded and may be
// empty.
type HTTPError struct {
	Op     string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("qoder: %s HTTP %d: %s", e.Op, e.Status, truncateRunes(e.Body, 240))
}

// IsAuthError reports whether err means Qoder rejected the credential itself
// (HTTP 401/403 on a token or chat call). Retrying with the same credential
// cannot help.
func IsAuthError(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == http.StatusUnauthorized || httpErr.Status == http.StatusForbidden
	}
	return false
}

// IsRejected reports whether err is a 4xx answer other than 429: the request
// or credential was refused and resending it unchanged cannot succeed. A
// refresh token that fails this way is dead and needs a new device login.
func IsRejected(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status >= 400 && httpErr.Status < 500 && httpErr.Status != http.StatusTooManyRequests
	}
	return false
}

// OpenChat posts a signed chat body and returns the upstream response.
// The caller must close the body. A non-200 status is still returned so the
// caller can read the error payload.
func OpenChat(ctx context.Context, doer Doer, identity Identity, model string, body []byte, chatURL string) (*http.Response, error) {
	if strings.TrimSpace(chatURL) == "" {
		chatURL = ChatURL()
	}
	signed, err := Sign(body, chatURL, Creds{
		UserID:    identity.UserID,
		AuthToken: identity.JobToken,
		Name:      identity.Name,
		Email:     identity.Email,
		MachineID: identity.MachineID,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", signed.Authorization)
	req.Header.Set("X-Model-Key", strings.TrimSpace(model))
	for key, value := range signed.Headers {
		req.Header.Set(key, value)
	}
	resp, err := doerOrDefault(doer).Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: chat: %w", err)
	}
	return resp, nil
}

// ReadErrorBody reads a bounded upstream error body.
func ReadErrorBody(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, 4096))
	return strings.TrimSpace(string(raw))
}
