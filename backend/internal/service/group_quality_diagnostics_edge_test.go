//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQualityDiagnosticRedactsJSONEscapedCredentials(t *testing.T) {
	for _, tc := range []struct{ secret, body string }{
		{"private-key", `{"detail":"\u0070rivate-key"}`},
		{"private<key", `{"detail":"private\u003ckey"}`},
	} {
		capture := &qualityAttemptCapture{secrets: []string{tc.secret}}
		ctx := context.WithValue(context.Background(), qualityAttemptContextKey{}, capture)
		recordQualityHTTPError(ctx, []byte(tc.body))
		var decoded map[string]any
		if json.Unmarshal([]byte(capture.attempt.Message), &decoded) == nil {
			require.NotContains(t, decoded["detail"], tc.secret)
		}
		require.Contains(t, capture.attempt.Message, "[redacted]")
		require.NotContains(t, capture.attempt.Message, tc.secret)
	}
}

func TestQualityDiagnosticEscapedTypeCannotDiscardAttempts(t *testing.T) {
	diagnostics := &GroupQualityDiagnostics{AttemptCount: 6, Stage: "generation"}
	for i := 1; i <= 6; i++ {
		diagnostics.Attempts = append(diagnostics.Attempts, QualityAttemptDiagnostic{Number: i, HTTPStatus: 502, ErrorType: strings.Repeat("<", 48), Message: strings.Repeat("<", 256), FailureCode: "upstream_failure", DurationMs: 12345})
	}
	raw := encodeQualityReason(qualityProbeResult{Reason: "Artwork generation was rejected or failed upstream", Diagnostics: diagnostics})
	require.LessOrEqual(t, len(raw), 2000)
	_, decoded := decodeQualityReason(raw)
	require.NotNil(t, decoded)
	require.Len(t, decoded.Attempts, 6)
	require.True(t, decoded.DetailsTruncated)
	for _, attempt := range decoded.Attempts {
		require.Equal(t, 502, attempt.HTTPStatus)
		require.Equal(t, "upstream_failure", attempt.FailureCode)
		require.NotEmpty(t, attempt.ErrorType)
	}
}
