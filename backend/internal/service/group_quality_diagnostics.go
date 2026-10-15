package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Public metadata and private admin evidence share storage, never API serialization.
type GroupQualityDiagnostics struct {
	Stage                    string                     `json:"stage"`
	ConfiguredProtocol       string                     `json:"configured_protocol"`
	TotalDurationMs          int64                      `json:"total_duration_ms"`
	GenerationDurationMs     int64                      `json:"generation_duration_ms"`
	GenerationTimeoutSeconds int                        `json:"generation_timeout_seconds"`
	ReceivedTextBytes        int                        `json:"received_text_bytes"`
	ReceivedEvents           int                        `json:"received_events"`
	LastEventType            string                     `json:"last_event_type,omitempty"`
	CompletionReceived       bool                       `json:"completion_received"`
	CaptureOverflow          bool                       `json:"capture_overflow"`
	FailureCode              string                     `json:"failure_code,omitempty"`
	DeadlineSource           string                     `json:"deadline_source,omitempty"`
	ArtworkAvailable         bool                       `json:"artwork_available"`
	RenderDurationMs         int64                      `json:"render_duration_ms,omitempty"`
	RenderFailureCode        string                     `json:"render_failure_code,omitempty"`
	AttemptCount             int                        `json:"attempt_count,omitempty"`
	Attempts                 []QualityAttemptDiagnostic `json:"-"`
	Route                    *QualityRequestRoute       `json:"-"`
	DetailsTruncated         bool                       `json:"-"`
}

type qualityReasonEnvelope struct {
	Format           string                   `json:"format"`
	Reason           string                   `json:"reason"`
	Diagnostics      *GroupQualityDiagnostics `json:"diagnostics"`
	Attempts         []qualityStoredAttempt   `json:"attempts,omitempty"`
	Route            *QualityRequestRoute     `json:"route,omitempty"`
	DetailsTruncated bool                     `json:"details_truncated,omitempty"`
}

// The existing bounded reason column holds the optional envelope. Plain historical
// reasons remain readable, and every API reader exposes the reason separately.
type qualityStoredAttempt struct {
	Number      int    `json:"n"`
	HTTPStatus  int    `json:"s,omitempty"`
	ErrorType   string `json:"t,omitempty"`
	Message     string `json:"m,omitempty"`
	FailureCode string `json:"c,omitempty"`
	DurationMs  int64  `json:"d"`
}

func encodeQualityReason(result qualityProbeResult) string {
	if result.Diagnostics == nil {
		return result.Reason
	}
	envelope := qualityReasonEnvelope{Format: "aihub-quality-diagnostics-v1", Reason: result.Reason, Diagnostics: result.Diagnostics, Route: result.Diagnostics.Route, DetailsTruncated: result.Diagnostics.DetailsTruncated}
	for _, attempt := range result.Diagnostics.Attempts {
		errorType := qualityDiagnosticCode(attempt.ErrorType)
		if errorType != attempt.ErrorType {
			envelope.DetailsTruncated = true
		}
		envelope.Attempts = append(envelope.Attempts, qualityStoredAttempt{attempt.Number, attempt.HTTPStatus, errorType, attempt.Message, attempt.FailureCode, attempt.DurationMs})
	}
	// Keep all attempt classifications while reducing message excerpts to fit VARCHAR(2000).
	for _, limit := range []int{256, 128, 64, 32, 0} {
		for i := range envelope.Attempts {
			if len(envelope.Attempts[i].Message) > limit {
				envelope.DetailsTruncated = true
				envelope.Attempts[i].Message = qualityBoundedText(envelope.Attempts[i].Message, limit)
			}
		}
		payload, err := json.Marshal(envelope)
		if err == nil && len(payload) <= 2000 {
			return string(payload)
		}
	}
	// A long escaped account label must not displace the request route or attempts.
	if envelope.Route != nil && envelope.Route.AccountName != "" {
		route := *envelope.Route
		envelope.Route = &route
		for _, limit := range []int{64, 32, 0} {
			if len(route.AccountName) <= limit {
				continue
			}
			route.AccountName = qualityBoundedText(route.AccountName, limit)
			envelope.DetailsTruncated = true
			payload, err := json.Marshal(envelope)
			if err == nil && len(payload) <= 2000 {
				return string(payload)
			}
		}
	}

	// Extreme legacy/local metadata must not drop the final upstream evidence.
	envelope.Route = nil
	envelope.DetailsTruncated = true
	envelope.Diagnostics = &GroupQualityDiagnostics{Stage: result.Diagnostics.Stage, FailureCode: result.Diagnostics.FailureCode, AttemptCount: result.Diagnostics.AttemptCount}
	payload, err := json.Marshal(envelope)
	if err == nil && len(payload) <= 2000 {
		return string(payload)
	}
	return result.Reason
}

func decodeQualityReason(raw string) (string, *GroupQualityDiagnostics) {
	var envelope qualityReasonEnvelope
	if len(raw) <= 2000 && json.Unmarshal([]byte(raw), &envelope) == nil &&
		envelope.Format == "aihub-quality-diagnostics-v1" && envelope.Diagnostics != nil {
		for _, attempt := range envelope.Attempts {
			envelope.Diagnostics.Attempts = append(envelope.Diagnostics.Attempts, QualityAttemptDiagnostic{attempt.Number, attempt.HTTPStatus, attempt.ErrorType, attempt.Message, attempt.FailureCode, attempt.DurationMs})
		}
		envelope.Diagnostics.Route = envelope.Route
		envelope.Diagnostics.DetailsTruncated = envelope.DetailsTruncated
		return envelope.Reason, envelope.Diagnostics
	}
	return raw, nil
}

func pelicanRequestFailureCode(err error) string {
	switch pelicanRequestFailureReason(err) {
	case qualityModelNotAllowedReason:
		return "model_not_allowed"
	case "Artwork generation timed out before completion":
		return "generation_timeout"
	case "Artwork generation was canceled before completion":
		return "generation_canceled"
	case "Artwork generation stream ended without a completion event":
		return "missing_completion"
	case "Artwork generation response was incomplete or truncated":
		return "incomplete_stream"
	case "Artwork generation returned an invalid response stream":
		return "invalid_stream"
	case "Artwork generation output exceeded the size limit":
		return "output_limit"
	case "Artwork generation was rejected or failed upstream":
		return "upstream_failure"
	default:
		return "generation_failed"
	}
}

func classifyPelicanRendererStderr(stderr string) string {
	message := strings.ToLower(stderr)
	for _, code := range []string{"renderer_root_forbidden", "renderer_resource_unavailable", "renderer_dependency_unavailable", "renderer_chromium_unavailable", "renderer_sandbox_failed", "renderer_timeout", "renderer_output_limit", "renderer_artwork_rejected", "renderer_failed"} {
		if strings.Contains(message, code) {
			return code
		}
	}
	switch {
	case strings.Contains(message, "root rendering is forbidden"):
		return "renderer_root_forbidden"
	case strings.Contains(message, "cannot find package"), strings.Contains(message, "playwright") && strings.Contains(message, "err_module_not_found"):
		return "renderer_dependency_unavailable"
	case strings.Contains(message, "cannot find module"), strings.Contains(message, "err_module_not_found"):
		return "renderer_resource_unavailable"
	case strings.Contains(message, "executable doesn't exist"), strings.Contains(message, "executable does not exist"), strings.Contains(message, "browser executable") && strings.Contains(message, "not found"):
		return "renderer_chromium_unavailable"
	case strings.Contains(message, "exceeded 30 second deadline"):
		return "renderer_timeout"
	case strings.Contains(message, "rendered output") && strings.Contains(message, "exceeds"):
		return "renderer_output_limit"
	case strings.Contains(message, "unsafe html"), strings.Contains(message, "invalid svg artwork"):
		return "renderer_artwork_rejected"
	default:
		return "renderer_failed"
	}
}

func pelicanRenderFailureCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "renderer_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "renderer_canceled"
	}
	if err == nil {
		return ""
	}
	switch err.Error() {
	case "renderer path must be absolute", "renderer resource unavailable":
		return "renderer_resource_unavailable"
	case "renderer executable unavailable":
		return "renderer_node_unavailable"
	case "renderer output too large", "frame PNG too large":
		return "renderer_output_limit"
	case "invalid artwork size":
		return "renderer_artwork_rejected"
	case "invalid renderer frame count", "trailing renderer content", "invalid frame time", "invalid PNG encoding", "invalid frame dimensions", "invalid frame PNG":
		return "renderer_invalid_output"
	default:
		return classifyPelicanRendererStderr(err.Error())
	}
}
