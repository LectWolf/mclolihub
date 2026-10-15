package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/google/uuid"
)

// Private evidence is deliberately excluded from GroupQualityDiagnostics JSON.
// Only the admin diagnostics endpoint exposes these sanitized, bounded values.
type QualityAttemptDiagnostic struct {
	Number      int    `json:"number"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	Message     string `json:"message,omitempty"`
	FailureCode string `json:"failure_code,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
}

type QualityRequestRoute struct {
	AccountID    int64  `json:"account_id"`
	Platform     string `json:"platform"`
	Endpoint     string `json:"endpoint"`
	Model        string `json:"model"`
	ProxyEnabled bool   `json:"proxy_enabled"`
}

type GroupQualityAdminDiagnostics struct {
	ResultID         string                     `json:"result_id"`
	Reason           string                     `json:"reason"`
	Diagnostics      *GroupQualityDiagnostics   `json:"diagnostics"`
	Attempts         []QualityAttemptDiagnostic `json:"attempts"`
	Route            *QualityRequestRoute       `json:"route,omitempty"`
	DetailsTruncated bool                       `json:"details_truncated,omitempty"`
}

type qualityAttemptContextKey struct{}
type qualityAttemptCapture struct {
	attempt   QualityAttemptDiagnostic
	route     *QualityRequestRoute
	secrets   []string
	truncated bool
}

func qualityCapture(ctx context.Context) *qualityAttemptCapture {
	capture, _ := ctx.Value(qualityAttemptContextKey{}).(*qualityAttemptCapture)
	return capture
}

var qualityDiagnosticURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var qualityDiagnosticBearer = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;"']+`)
var qualityDiagnosticKey = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+`)

func qualityBoundedText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func (capture *qualityAttemptCapture) sanitize(text string, limit int) string {
	text = qualityDiagnosticBearer.ReplaceAllString(text, "Bearer [redacted]")
	// Normalize JSON escapes before removing exact credential values.
	text = logredact.RedactText(text, "authorization", "api_key", "apikey", "x-api-key", "x-goog-api-key", "token", "cookie", "secret")
	for _, secret := range capture.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
			text = strings.ReplaceAll(text, url.QueryEscape(secret), "[redacted]")
			text = strings.ReplaceAll(text, url.PathEscape(secret), "[redacted]")
			encoded, _ := json.Marshal(secret)
			if len(encoded) > 2 {
				text = strings.ReplaceAll(text, string(encoded[1:len(encoded)-1]), "[redacted]")
			}
		}
	}
	text = qualityDiagnosticURL.ReplaceAllStringFunc(text, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[redacted-url]"
		}
		return parsed.Scheme + "://" + parsed.Host + "/[redacted-path]"
	})
	text = qualityDiagnosticBearer.ReplaceAllString(text, "Bearer [redacted]")
	text = qualityDiagnosticKey.ReplaceAllString(text, "[redacted]")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	if len(strings.TrimSpace(text)) > limit {
		capture.truncated = true
	}
	return qualityBoundedText(strings.TrimSpace(text), limit)
}

func qualityDiagnosticCode(code string) string {
	code = qualityBoundedText(code, 48)
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, code)
}

func recordQualityRequest(ctx context.Context, account *Account, req *http.Request, model, proxyURL, authToken string) {
	capture := qualityCapture(ctx)
	if capture == nil {
		return
	}
	capture.secrets = append(capture.secrets, authToken)
	if proxy, err := url.Parse(proxyURL); err == nil && proxy.User != nil {
		capture.secrets = append(capture.secrets, proxy.User.Username())
		if password, ok := proxy.User.Password(); ok {
			capture.secrets = append(capture.secrets, password)
		}
	}
	// Headers are never retained. Include their values only in the redaction set.
	for name, values := range req.Header {
		switch strings.ToLower(name) {
		case "content-type", "accept", "user-agent":
			continue
		}
		for _, value := range values {
			capture.secrets = append(capture.secrets, value)
			if strings.HasPrefix(strings.ToLower(value), "bearer ") {
				capture.secrets = append(capture.secrets, value[7:])
			}
		}
	}
	for _, values := range req.URL.Query() {
		capture.secrets = append(capture.secrets, values...)
	}
	if req.URL.User != nil {
		capture.secrets = append(capture.secrets, req.URL.User.Username())
		if password, ok := req.URL.User.Password(); ok {
			capture.secrets = append(capture.secrets, password)
		}
	}
	// Store only origin and a credential-redacted path; queries/userinfo are omitted.
	endpoint := req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath()
	for _, secret := range capture.secrets {
		if secret != "" {
			endpoint = strings.ReplaceAll(endpoint, secret, "[redacted]")
			endpoint = strings.ReplaceAll(endpoint, url.PathEscape(secret), "[redacted]")
		}
	}
	if len(endpoint) > 160 {
		capture.truncated = true
	}
	capture.route = &QualityRequestRoute{AccountID: account.ID, Platform: account.Platform, Endpoint: qualityBoundedText(endpoint, 160), Model: capture.sanitize(model, 80), ProxyEnabled: proxyURL != ""}
}

func recordQualityHTTPStatus(ctx context.Context, status int) {
	if capture := qualityCapture(ctx); capture != nil {
		capture.attempt.HTTPStatus = status
	}
}

func recordQualityUpstreamError(ctx context.Context, data map[string]any) {
	capture := qualityCapture(ctx)
	if capture == nil {
		return
	}
	errorType, _ := data["type"].(string)
	if errorType == "" {
		errorType, _ = data["status"].(string)
	}
	message, _ := data["message"].(string)
	capture.attempt.ErrorType = qualityDiagnosticCode(capture.sanitize(errorType, 48))
	capture.attempt.Message = capture.sanitize(message, 256)
}

func recordQualityHTTPError(ctx context.Context, body []byte) {
	capture := qualityCapture(ctx)
	if capture == nil {
		return
	}
	var data map[string]any
	if json.Unmarshal(body, &data) == nil {
		if nested, ok := data["error"].(map[string]any); ok {
			recordQualityUpstreamError(ctx, nested)
		} else {
			recordQualityUpstreamError(ctx, data)
		}
	}
	if capture.attempt.Message == "" {
		capture.attempt.Message = capture.sanitize(string(body), 256)
	}
}

func (c *GroupQualityChecker) AdminDiagnostics(ctx context.Context, groupID int64, resultID string) (*GroupQualityAdminDiagnostics, error) {
	if c == nil || c.db == nil || groupID <= 0 {
		return nil, sql.ErrNoRows
	}
	if _, err := uuid.Parse(resultID); err != nil {
		return nil, sql.ErrNoRows
	}
	var raw string
	err := c.db.QueryRowContext(ctx, `SELECT reason FROM group_quality_check_history WHERE group_id = $1 AND result_id = $2`, groupID, resultID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	reason, diagnostics := decodeQualityReason(raw)
	result := &GroupQualityAdminDiagnostics{ResultID: resultID, Reason: reason, Diagnostics: diagnostics, Attempts: []QualityAttemptDiagnostic{}}
	if diagnostics != nil {
		result.Attempts = diagnostics.Attempts
		if result.Attempts == nil {
			result.Attempts = []QualityAttemptDiagnostic{}
		}
		result.Route = diagnostics.Route
		result.DetailsTruncated = diagnostics.DetailsTruncated
	}
	return result, nil
}

func finishQualityAttempt(capture *qualityAttemptCapture, started time.Time, number int, failure error, failureCode string) QualityAttemptDiagnostic {
	capture.attempt.Number = number
	capture.attempt.DurationMs = time.Since(started).Milliseconds()
	capture.attempt.FailureCode = failureCode
	if failure != nil && capture.attempt.Message == "" {
		capture.attempt.Message = capture.sanitize(failure.Error(), 256)
	}
	return capture.attempt
}
