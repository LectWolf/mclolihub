//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func pelicanCompletionSnapshot(text, model string, eventOnly bool) string {
	data := map[string]any{"response": map[string]any{
		"model": model, "status": "completed",
		"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
	}}
	prefix := ""
	if eventOnly {
		prefix = "event: response.completed\n"
	} else {
		data["type"] = "response.completed"
	}
	encoded, _ := json.Marshal(data)
	return prefix + "data: " + string(encoded) + "\n\n"
}

func TestAccountTestService_ResponsesStreamCompatibility(t *testing.T) {
	delta := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html></html>\"}\n\n"
	complete := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	for _, tc := range []struct {
		name, stream, wantText string
		wantErr                bool
	}{
		{"normal", delta + complete, "<html></html>", false},
		{"event-name-only", "event: response.output_text.delta\ndata: {\"delta\":\"<html></html>\"}\n\nevent: response.completed\ndata: {\"response\":{\"status\":\"completed\"}}\n\n", "<html></html>", false},
		{"multiline-data", "event: response.output_text.delta\ndata: {\ndata: \"delta\":\"<html></html>\"}\n\n" + complete, "<html></html>", false},
		{"final-line-without-newline", delta + strings.TrimRight(complete, "\n"), "<html></html>", false},
		{"final-frame-without-blank-line", delta + strings.TrimSuffix(complete, "\n"), "<html></html>", false},
		{"final-snapshot-only", pelicanCompletionSnapshot("<html></html>", "gpt-6.1-sol", true), "<html></html>", false},
		{"snapshot-does-not-duplicate-deltas", delta + pelicanCompletionSnapshot("<html></html>", "gpt-6.1-sol", false), "<html></html>", false},
		{"response-done", delta + "event: response.done\ndata: {\"response\":{\"status\":\"completed\"}}\n\n", "<html></html>", false},
		{"delta-only", delta, "<html></html>", true},
		{"done-only", "data: [DONE]\n\n", "", true},
		{"delta-and-done", delta + "data: [DONE]\n\n", "<html></html>", true},
		{"text-done-only", "event: response.output_text.done\ndata: {\"text\":\"<html>partial\"}\n\ndata: [DONE]\n\n", "<html>partial", true},
		{"content-part-done-only", "event: response.content_part.done\ndata: {\"part\":{\"type\":\"output_text\",\"text\":\"<html>part\"}}\n\ndata: [DONE]\n\n", "<html>part", true},
		{"incomplete", delta + "event: response.incomplete\ndata: {\"response\":{\"status\":\"incomplete\"}}\n\n" + complete, "<html></html>", true},
		{"incomplete-completed-event", delta + "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}\n\n", "<html></html>", true},
		{"failed", delta + "event: response.failed\ndata: {\"response\":{\"error\":{\"message\":\"sk-private https://private.invalid\"}}}\n\n" + complete, "<html></html>", true},
		{"error", "event: error\ndata: {\"error\":{\"message\":\"sk-private https://private.invalid\"}}\n\n" + complete, "", true},
		{"invalid-json", "event: response.completed\ndata: invalid\n\n", "", true},
		{"null-json", "event: response.completed\ndata: null\n\n", "", true},
		{"empty-event-does-not-leak", delta + "event: response.completed\n\ndata: {}\n\n", "<html></html>", true},
		{"json-type-takes-precedence", delta + "event: response.completed\ndata: {\"type\":\"response.incomplete\"}\n\n", "<html></html>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := newTestContext()
			ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), pelicanQualityContextKey{}, true))
			err := (&AccountTestService{}).processOpenAIStream(ctx, strings.NewReader(tc.stream))
			out, parseErr := parsePelicanTestEvents(recorder.Body.Bytes(), pelicanDocumentLimit)
			if tc.wantErr {
				require.Error(t, err)
				require.Error(t, parseErr)
				require.False(t, out.Complete)
				require.NotContains(t, recorder.Body.String(), "private")
			} else {
				require.NoError(t, err)
				require.NoError(t, parseErr)
				require.True(t, out.Complete)
			}
			require.Equal(t, tc.wantText, out.Text)
		})
	}
}

func TestPelicanRequestFailureReasonIsBoundedAndSafe(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "Artwork generation timed out before completion"},
		{context.Canceled, "Artwork generation was canceled before completion"},
		{errors.New("Stream ended before response.completed"), "Artwork generation stream ended without a completion event"},
		{errors.New("Quality response did not complete"), "Artwork generation response was incomplete or truncated"},
		{errors.New("Invalid quality response stream"), "Artwork generation returned an invalid response stream"},
		{errors.New("test output limit"), "Artwork generation output exceeded the size limit"},
		{errors.New("OpenAI response failed"), "Artwork generation was rejected or failed upstream"},
		{errors.New("sk-private https://private.invalid"), "Artwork generation failed or did not complete"},
	} {
		require.Equal(t, tc.want, pelicanRequestFailureReason(tc.err))
	}
}
