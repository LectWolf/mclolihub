//go:build unit

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestQualityUpstreamDiagnosticsRetainAllSSEAttempts(t *testing.T) {
	responses := make([]*http.Response, 6)
	for i := range responses {
		responses[i] = newJSONResponse(200, "data: {\"error\":{\"type\":\"upstream_error\",\"message\":\"Provider rejected mock-secret Authorization: Bearer private-key\"}}\n\n")
	}
	svc, upstream := pelicanMockService(pelicanChatAccount(), responses...)
	result := svc.runPelicanQualityCheck(context.Background(), 901, "gpt-6.1-sol")
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "upstream_failure", result.Diagnostics.FailureCode)
	require.Empty(t, result.RawOutput)
	require.Len(t, upstream.requests, 6)
	require.Equal(t, 6, result.Diagnostics.AttemptCount)
	require.Len(t, result.Diagnostics.Attempts, 6)
	for i, attempt := range result.Diagnostics.Attempts {
		require.Equal(t, i+1, attempt.Number)
		require.Equal(t, 200, attempt.HTTPStatus)
		require.Equal(t, "upstream_error", attempt.ErrorType)
		require.Contains(t, attempt.Message, "Provider rejected")
		require.NotContains(t, attempt.Message, "mock-secret")
		require.NotContains(t, attempt.Message, "private-key")
	}
	require.Equal(t, "http://chat-test.example/v1/chat/completions", result.Diagnostics.Route.Endpoint)
	stored := encodeQualityReason(result)
	require.LessOrEqual(t, len(stored), 2000)
	require.NotContains(t, stored, "mock-secret")
	reason, diagnostics := decodeQualityReason(stored)
	require.Equal(t, result.Reason, reason)
	require.Len(t, diagnostics.Attempts, 6)
	require.Equal(t, "upstream_error", diagnostics.Attempts[5].ErrorType)
	public, err := json.Marshal(GroupQualityResult{Reason: reason, Diagnostics: diagnostics})
	require.NoError(t, err)
	require.NotContains(t, string(public), "Provider rejected")
	require.NotContains(t, string(public), "chat-test.example")
	require.NotContains(t, string(public), "attempts")
}

func TestQualityUpstreamDiagnosticsHTTPAndRetrySuccess(t *testing.T) {
	for _, status := range []int{400, 403, 429, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			svc, _ := pelicanMockService(pelicanChatAccount(), newJSONResponse(status, `{"error":{"type":"provider_error","message":"unsupported parameter mock-secret"}}`), pelicanMockResponse("<html>ok</html>", "resolved", true))
			result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
			first := result.Diagnostics.Attempts[0]
			require.Equal(t, status, first.HTTPStatus)
			require.Equal(t, "provider_error", first.ErrorType)
			require.Contains(t, first.Message, "unsupported parameter")
			require.NotContains(t, first.Message, "mock-secret")
			if status == 429 || status == 502 {
				require.Equal(t, "success", result.Status)
				require.Len(t, result.Diagnostics.Attempts, 2)
				require.Empty(t, result.Diagnostics.Attempts[1].FailureCode)
			} else {
				require.Equal(t, "failed", result.Status)
				require.Len(t, result.Diagnostics.Attempts, 1)
			}
			require.NotContains(t, result.Reason, "unsupported parameter")
		})
	}
}

func TestQualityDiagnosticRedactionAndBoundedStorage(t *testing.T) {
	capture := &qualityAttemptCapture{}
	ctx := context.WithValue(context.Background(), qualityAttemptContextKey{}, capture)
	req := httptest.NewRequest("POST", "https://user:password@example.test/prefix/private-key/v1/chat/completions?token=query-secret", nil)
	req.Header.Set("Authorization", "Bearer private-key")
	req.Header.Set("X-Custom-Auth", "custom-header-secret")
	recordQualityRequest(ctx, pelicanChatAccount(), req, "model", "http://proxy-user:proxy-password@proxy.test:8080", "private-key")
	text := capture.sanitize("private-key custom-header-secret query-secret password https://name:another-password@host/path?token=hidden Bearer unknown-secret api_key=other-secret sk-unconfiguredtoken", 1024)
	for _, secret := range []string{"private-key", "custom-header-secret", "query-secret", "password", "another-password", "hidden", "unknown-secret", "other-secret", "sk-unconfiguredtoken"} {
		require.NotContains(t, text, secret)
	}
	require.NotContains(t, capture.route.Endpoint, "private-key")
	require.NotContains(t, capture.route.Endpoint, "query-secret")
	require.NotContains(t, capture.route.Endpoint, "password")
	diagnostics := &GroupQualityDiagnostics{Stage: "generation", AttemptCount: 6, Route: capture.route}
	for i := 1; i <= 6; i++ {
		diagnostics.Attempts = append(diagnostics.Attempts, QualityAttemptDiagnostic{Number: i, HTTPStatus: 200, ErrorType: "upstream_error", Message: strings.Repeat("中文<error>\n", 100), FailureCode: "upstream_failure", DurationMs: 100000})
	}
	encoded := encodeQualityReason(qualityProbeResult{Reason: "safe reason", Diagnostics: diagnostics})
	require.LessOrEqual(t, len(encoded), 2000)
	require.True(t, utf8.ValidString(encoded))
	reason, decoded := decodeQualityReason(encoded)
	require.Equal(t, "safe reason", reason)
	require.NotNil(t, decoded)
	require.Len(t, decoded.Attempts, 6)
	require.True(t, decoded.DetailsTruncated)
	require.Equal(t, "upstream_error", decoded.Attempts[5].ErrorType)
	require.Equal(t, strings.Repeat("中文<error>\n", 100), diagnostics.Attempts[0].Message, "encoding must not mutate live evidence")
}

func TestQualityAdminDiagnosticsScopedReadAndLegacy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	reader := NewGroupQualityReader(db)
	const id = "12345678-1234-4234-8234-123456789abc"
	_, err = reader.AdminDiagnostics(context.Background(), 7, "invalid")
	require.ErrorIs(t, err, sql.ErrNoRows)
	query := `SELECT reason FROM group_quality_check_history WHERE group_id = \$1 AND result_id = \$2`
	raw := encodeQualityReason(qualityProbeResult{Reason: "safe", Diagnostics: &GroupQualityDiagnostics{AttemptCount: 1, Attempts: []QualityAttemptDiagnostic{{Number: 1, HTTPStatus: 502, ErrorType: "server_error", Message: "private diagnostic"}}}})
	mock.ExpectQuery(query).WithArgs(int64(7), id).WillReturnRows(sqlmock.NewRows([]string{"reason"}).AddRow(raw))
	result, err := reader.AdminDiagnostics(context.Background(), 7, id)
	require.NoError(t, err)
	require.Equal(t, "private diagnostic", result.Attempts[0].Message)
	mock.ExpectQuery(query).WithArgs(int64(8), id).WillReturnRows(sqlmock.NewRows([]string{"reason"}))
	_, err = reader.AdminDiagnostics(context.Background(), 8, id)
	require.ErrorIs(t, err, sql.ErrNoRows)
	mock.ExpectQuery(query).WithArgs(int64(7), id).WillReturnRows(sqlmock.NewRows([]string{"reason"}).AddRow("old safe reason"))
	result, err = reader.AdminDiagnostics(context.Background(), 7, id)
	require.NoError(t, err)
	require.Nil(t, result.Diagnostics)
	require.Empty(t, result.Attempts)
	require.Equal(t, "old safe reason", result.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQualityDiagnosticsPublicHistoryHidesPrivateEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	raw := encodeQualityReason(qualityProbeResult{Reason: "safe", Diagnostics: &GroupQualityDiagnostics{AttemptCount: 1, Route: &QualityRequestRoute{Endpoint: "https://private.example"}, Attempts: []QualityAttemptDiagnostic{{Number: 1, Message: "private provider details"}}}})
	mock.ExpectQuery(`(?s)SELECT s.group_id, h.result_id.*group_quality_check_history`).WillReturnRows(sqlmock.NewRows([]string{"group", "id", "status", "reason", "model", "checked", "artwork", "raw", "score"}).AddRow(7, "id", "failed", raw, "model", time.Now(), false, false, nil))
	history, err := NewGroupQualityReader(db).History(context.Background(), []int64{7})
	require.NoError(t, err)
	encoded, err := json.Marshal(history)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.Contains(t, string(encoded), "safe")
	require.NoError(t, mock.ExpectationsWereMet())
}
