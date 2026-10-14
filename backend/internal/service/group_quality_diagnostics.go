package service

import "encoding/json"

// Only bounded local metadata is retained, never upstream payloads or credentials.
type GroupQualityDiagnostics struct {
	Stage                    string `json:"stage"`
	ConfiguredProtocol       string `json:"configured_protocol"`
	TotalDurationMs          int64  `json:"total_duration_ms"`
	GenerationDurationMs     int64  `json:"generation_duration_ms"`
	GenerationTimeoutSeconds int    `json:"generation_timeout_seconds"`
	ReceivedTextBytes        int    `json:"received_text_bytes"`
	ReceivedEvents           int    `json:"received_events"`
	LastEventType            string `json:"last_event_type,omitempty"`
	CompletionReceived       bool   `json:"completion_received"`
	CaptureOverflow          bool   `json:"capture_overflow"`
	FailureCode              string `json:"failure_code,omitempty"`
	DeadlineSource           string `json:"deadline_source,omitempty"`
}

type qualityReasonEnvelope struct {
	Format      string                   `json:"format"`
	Reason      string                   `json:"reason"`
	Diagnostics *GroupQualityDiagnostics `json:"diagnostics"`
}

// The existing bounded reason column holds the optional envelope. Plain historical
// reasons remain readable, and every API reader exposes the reason separately.
func encodeQualityReason(result qualityProbeResult) string {
	if result.Diagnostics == nil {
		return result.Reason
	}
	payload, err := json.Marshal(qualityReasonEnvelope{
		Format: "aihub-quality-diagnostics-v1", Reason: result.Reason, Diagnostics: result.Diagnostics,
	})
	if err != nil || len(payload) > 2000 {
		return result.Reason
	}
	return string(payload)
}

func decodeQualityReason(raw string) (string, *GroupQualityDiagnostics) {
	var envelope qualityReasonEnvelope
	if len(raw) <= 2000 && json.Unmarshal([]byte(raw), &envelope) == nil &&
		envelope.Format == "aihub-quality-diagnostics-v1" && envelope.Diagnostics != nil {
		return envelope.Reason, envelope.Diagnostics
	}
	return raw, nil
}

func pelicanRequestFailureCode(err error) string {
	switch pelicanRequestFailureReason(err) {
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
