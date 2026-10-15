package service

import "context"

func applyPelicanQualityPayload(ctx context.Context, payload map[string]any, responses bool) {
	if !isPelicanQualityRequest(ctx) {
		return
	}
	if responses {
		payload["reasoning"] = map[string]any{"effort": "high"}
	} else {
		payload["reasoning_effort"] = "high"
	}
}
