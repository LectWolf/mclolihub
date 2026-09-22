package qoderproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenChat posts a signed chat body and returns the upstream response.
// The caller must close the body. A non-200 status is still returned so the
// caller can read the error payload.
func OpenChat(ctx context.Context, client *http.Client, identity Identity, model string, body []byte, chatURL string) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
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
	resp, err := client.Do(req)
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
