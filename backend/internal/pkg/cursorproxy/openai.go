package cursorproxy

import (
	"strings"

	"github.com/google/uuid"
)

const (
	RoleUser      = "INFERENCE_MESSAGE_ROLE_USER"
	RoleAssistant = "INFERENCE_MESSAGE_ROLE_ASSISTANT"
	RoleSystem    = "INFERENCE_MESSAGE_ROLE_SYSTEM"
)

type ChatMessage struct {
	Role    string
	Content string
}

func OpenAIMessages(messages []ChatMessage) []InferenceMessage {
	out := make([]InferenceMessage, 0, len(messages))
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		role := RoleUser
		switch strings.ToLower(strings.TrimSpace(m.Role)) {
		case "assistant":
			role = RoleAssistant
		case "system", "developer":
			role = RoleSystem
		}
		out = append(out, InferenceMessage{Role: role, Text: text})
	}
	return out
}

func ConcatUserText(messages []ChatMessage) string {
	var parts []string
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "system" || role == "developer" {
			parts = append(parts, text)
			continue
		}
		if role == "assistant" {
			parts = append(parts, "Assistant: "+text)
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func NewStreamPayload(modelID string, messages []ChatMessage) StreamRequest {
	return StreamRequest{
		Messages: OpenAIMessages(messages),
		Tools:    []any{},
		RequestedModel: RequestedModel{
			ModelID:    modelID,
			MaxMode:    true,
			Parameters: []ModelParameter{},
		},
		ConversationID: "proxy-" + uuid.NewString(),
		InvocationID:   uuid.NewString(),
	}
}

func NewIDs() (conversationID, messageID string) {
	return uuid.NewString(), uuid.NewString()
}

// FrameText extracts visible assistant text from a Stream or Run frame.
func FrameText(frame map[string]any) string {
	if frame == nil {
		return ""
	}
	if tp := firstMap(frame, "textPart", "text_part"); tp != nil {
		if s, _ := tp["text"].(string); s != "" {
			return s
		}
	}
	if s, _ := frame["text"].(string); s != "" {
		return s
	}
	if iu := firstMap(frame, "interactionUpdate", "interaction_update"); iu != nil {
		return walkText(iu)
	}
	if inv := firstMap(frame, "invocationResponse", "invocation_response"); inv != nil {
		if resp := firstMap(inv, "response"); resp != nil {
			return FrameText(resp)
		}
	}
	return ""
}

func FrameError(frame map[string]any) (code, message string) {
	return extractConnectError(frame)
}

func FrameModel(frame map[string]any) string {
	if frame == nil {
		return ""
	}
	if info := firstMap(frame, "responseInfo", "response_info"); info != nil {
		if m := firstMap(info, "model"); m != nil {
			if s := firstString(m, "modelId", "model_id"); s != "" {
				return s
			}
		}
		if s := firstString(info, "model"); s != "" {
			return s
		}
	}
	if iu := firstMap(frame, "interactionUpdate", "interaction_update"); iu != nil {
		if s := firstString(iu, "modelId", "model_id"); s != "" {
			return s
		}
	}
	return ""
}

func FrameTurnEnded(frame map[string]any) bool {
	if frame == nil {
		return false
	}
	iu := firstMap(frame, "interactionUpdate", "interaction_update")
	if iu == nil {
		return false
	}
	if _, ok := iu["turnEnded"]; ok {
		return true
	}
	if _, ok := iu["turn_ended"]; ok {
		return true
	}
	return false
}

func FrameRunReady(frame map[string]any) (model string, ok bool) {
	if frame == nil {
		return "", false
	}
	ready := firstMap(frame, "runReady", "run_ready")
	if ready == nil {
		return "", false
	}
	if rm := firstMap(ready, "resolvedModel", "resolved_model"); rm != nil {
		model = firstString(rm, "modelId", "model_id")
	}
	if model == "" {
		model = firstString(ready, "routedModelDisplayName", "routed_model_display_name")
	}
	return model, true
}

func FrameInvocationEnded(frame map[string]any) bool {
	if frame == nil {
		return false
	}
	return firstMap(frame, "invocationEnd", "invocation_end") != nil
}
