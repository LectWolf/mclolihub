package qoderproxy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	maxOutputTokens = 32768
	chatURL         = "https://api2.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common"
)

// ChatURL is the global agent SSE endpoint. The body is plain JSON; Encode is omitted.
func ChatURL() string { return chatURL }

// Message is one chat turn after content parts have been flattened to text.
// ToolCalls is set on assistant turns that called tools; ToolCallID and Name
// are set on tool results.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// ChatInput is everything needed to build one upstream request.
type ChatInput struct {
	Model    string
	UserID   string
	Messages []Message
	// Tools are OpenAI function tools, forwarded verbatim. When empty, tool
	// history is rendered as text because the upstream only accepts structured
	// tool turns alongside tool definitions.
	Tools            []json.RawMessage
	MaxTokens        int
	ThinkingEffort   string
	RequestID        string
	BusinessID       string
	BeginAtUnixMilli int64
}

// ToolCall is one native tool call, or one streamed fragment of it. Index
// groups streamed argument pieces.
type ToolCall struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// Usage is the token accounting Qoder reports for a chat.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	ReasoningTokens  int
}

// Delta is one parsed SSE update.
type Delta struct {
	Content      string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason string
	// Model is the model the upstream says it used, when it says so.
	Model string
	Usage *Usage
}

// StreamError is a failed event inside a 200 SSE stream (statusCodeValue is
// neither 0 nor 200). Status carries the upstream status so callers can treat
// 401/403/429/5xx like the matching HTTP answers.
type StreamError struct {
	Status int
	Body   string
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("qoder: upstream status %d: %s", e.Status, truncateRunes(e.Body, 240))
}

// BuildChatBody returns the JSON body Qoder's agent endpoint expects.
func BuildChatBody(in ChatInput) ([]byte, error) {
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return nil, fmt.Errorf("qoder: model is required")
	}
	maxTokens := in.MaxTokens
	if maxTokens <= 0 || maxTokens > maxOutputTokens {
		maxTokens = maxOutputTokens
	}
	toolsEnabled := len(in.Tools) > 0
	system, turns := splitSystem(in.Messages)
	lastUser := lastUserText(turns)
	recordID := recordID(model, turns, maxTokens)
	sessionID := sessionID(in.UserID, model)
	name := truncateRunes(lastUser, 30)
	if name == "" {
		name = "chat"
	}
	tools := make([]json.RawMessage, 0, len(in.Tools))
	tools = append(tools, in.Tools...)

	extra := map[string]any{
		"context": []any{},
		"modelConfig": map[string]any{
			"key":          model,
			"is_reasoning": in.ThinkingEffort != "",
		},
		"originalContent": lastUser,
	}
	if in.ThinkingEffort != "" {
		extra["thinking_effort"] = in.ThinkingEffort
	}

	body := map[string]any{
		"request_id":       firstNonEmpty(in.RequestID, recordID),
		"request_set_id":   recordID,
		"chat_record_id":   recordID,
		"session_id":       sessionID,
		"stream":           true,
		"chat_task":        "FREE_INPUT",
		"is_reply":         true,
		"is_retry":         false,
		"source":           1,
		"version":          "3",
		"session_type":     "qodercli",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"code_language":    "",
		"chat_prompt":      "",
		"image_urls":       nil,
		"aliyun_user_type": "",
		"system":           system,
		"messages":         wireMessages(turns, toolsEnabled),
		"tools":            tools,
		"parameters":       map[string]any{"max_tokens": maxTokens},
		"model_config":     map[string]any{"key": model},
		"chat_context": map[string]any{
			"chatPrompt": "",
			"imageUrls":  nil,
			"features":   []any{},
			"text":       lastUser,
			"extra":      extra,
		},
		"business": map[string]any{
			"product":  "cli",
			"version":  "1.0.0",
			"type":     "agent",
			"stage":    "start",
			"id":       firstNonEmpty(in.BusinessID, recordID),
			"name":     name,
			"begin_at": in.BeginAtUnixMilli,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("qoder: marshal chat body: %w", err)
	}
	return raw, nil
}

func splitSystem(messages []Message) (string, []Message) {
	var system strings.Builder
	turns := make([]Message, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system", "developer":
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			if system.Len() > 0 {
				_, _ = system.WriteString("\n\n")
			}
			_, _ = system.WriteString(msg.Content)
		case "":
			msg.Role = "user"
			turns = append(turns, msg)
		default:
			msg.Role = role
			turns = append(turns, msg)
		}
	}
	return system.String(), turns
}

// wireMessages renders turns for the upstream. With tools declared, assistant
// tool calls and tool results travel as structured OpenAI turns; without them
// the upstream would reject the orphaned tool turns, so they become text.
func wireMessages(turns []Message, toolsEnabled bool) []map[string]any {
	out := make([]map[string]any, 0, len(turns))
	for _, msg := range turns {
		switch {
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0 && toolsEnabled:
			calls := make([]map[string]any, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				arguments := call.Arguments
				if strings.TrimSpace(arguments) == "" {
					arguments = "{}"
				}
				calls = append(calls, map[string]any{
					"id":   call.ID,
					"type": "function",
					"function": map[string]any{
						"name":      call.Name,
						"arguments": arguments,
					},
				})
			}
			out = append(out, map[string]any{"role": "assistant", "content": msg.Content, "tool_calls": calls})
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			out = append(out, map[string]any{"role": "assistant", "content": joinSections(msg.Content, renderToolCalls(msg.ToolCalls))})
		case msg.Role == "tool" && toolsEnabled:
			turn := map[string]any{"role": "tool", "content": msg.Content, "tool_call_id": msg.ToolCallID}
			if msg.Name != "" {
				turn["name"] = msg.Name
			}
			out = append(out, turn)
		case msg.Role == "tool":
			out = append(out, map[string]any{"role": "user", "content": renderToolResult(msg)})
		default:
			out = append(out, map[string]any{"role": msg.Role, "content": msg.Content})
		}
	}
	return out
}

func renderToolCalls(calls []ToolCall) string {
	var b strings.Builder
	_, _ = b.WriteString("Tool calls:")
	for _, call := range calls {
		_, _ = b.WriteString("\n- ")
		_, _ = b.WriteString(call.Name)
		if call.ID != "" {
			_, _ = b.WriteString(" [" + call.ID + "]")
		}
		if args := strings.TrimSpace(call.Arguments); args != "" {
			_, _ = b.WriteString(": " + args)
		}
	}
	return b.String()
}

func renderToolResult(msg Message) string {
	var b strings.Builder
	_, _ = b.WriteString("Tool result")
	if msg.Name != "" {
		_, _ = b.WriteString(" (" + msg.Name + ")")
	}
	if msg.ToolCallID != "" {
		_, _ = b.WriteString(" [" + msg.ToolCallID + "]")
	}
	if msg.Content != "" {
		_, _ = b.WriteString(":\n" + msg.Content)
	}
	return b.String()
}

func joinSections(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n\n")
}

func lastUserText(turns []Message) string {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" && strings.TrimSpace(turns[i].Content) != "" {
			return turns[i].Content
		}
	}
	if len(turns) > 0 {
		return turns[len(turns)-1].Content
	}
	return ""
}

func sessionID(userID, model string) string {
	return shortHash([]byte("qoder-session"), []byte(userID), []byte(model))
}

func recordID(model string, turns []Message, maxTokens int) string {
	parts := [][]byte{[]byte("qoder-record"), []byte(model)}
	for _, msg := range turns {
		if msg.Role != "" {
			parts = append(parts, []byte(msg.Role))
		}
		if msg.Content != "" {
			parts = append(parts, []byte(msg.Content))
		}
		for _, call := range msg.ToolCalls {
			parts = append(parts, []byte(call.ID), []byte(call.Name), []byte(call.Arguments))
		}
		if msg.ToolCallID != "" {
			parts = append(parts, []byte(msg.ToolCallID))
		}
	}
	parts = append(parts, []byte(fmt.Sprintf("mt=%d", maxTokens)))
	return shortHash(parts...)
}

func shortHash(parts ...[]byte) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write(part)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:16]
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ScanChat reads an agent SSE body and calls fn for each delta. [DONE] ends
// the stream. A failed event (statusCodeValue neither 0 nor 200) stops the
// scan and is returned as a *StreamError.
func ScanChat(r io.Reader, fn func(Delta) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	// choices[].message may repeat what choices[].delta already streamed. Once
	// deltas carried text (or tool calls), message text (or tool calls) is a
	// recap and must not be emitted twice.
	sawText, sawTools := false, false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			return nil
		}
		ev, err := parseEvent(payload)
		if err != nil {
			return err
		}
		if ev.err != nil {
			return ev.err
		}
		delta := ev.delta
		if delta.Content != "" || delta.Reasoning != "" {
			sawText = true
		} else if !sawText && (ev.recap.Content != "" || ev.recap.Reasoning != "") {
			delta.Content, delta.Reasoning = ev.recap.Content, ev.recap.Reasoning
			sawText = true
		}
		if len(delta.ToolCalls) > 0 {
			sawTools = true
		} else if !sawTools && len(ev.recap.ToolCalls) > 0 {
			delta.ToolCalls = ev.recap.ToolCalls
			sawTools = true
		}
		if fn != nil {
			if err := fn(delta); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

type sseEvent struct {
	// delta holds choices[].delta output plus event-level fields (model,
	// usage, finish reason); recap holds choices[].message output.
	delta Delta
	recap Delta
	err   *StreamError
}

type wireUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	CachedTokens        int `json:"cached_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *wireUsage) toUsage() *Usage {
	if u == nil || (u.PromptTokens == 0 && u.CompletionTokens == 0) {
		return nil
	}
	cached := u.PromptTokensDetails.CachedTokens
	if cached == 0 {
		cached = u.CachedTokens
	}
	return &Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CachedTokens:     cached,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
	}
}

type wireChoicePart struct {
	Content          string            `json:"content"`
	ReasoningContent string            `json:"reasoning_content"`
	ToolCalls        []json.RawMessage `json:"tool_calls"`
}

func parseEvent(payload string) (sseEvent, error) {
	var envelope struct {
		StatusCodeValue int             `json:"statusCodeValue"`
		Body            json.RawMessage `json:"body"`
		ResponseMeta    struct {
			Usage *wireUsage `json:"usage"`
		} `json:"response_meta"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return sseEvent{}, fmt.Errorf("qoder: sse event: %w", err)
	}
	if envelope.StatusCodeValue != 0 && envelope.StatusCodeValue != 200 {
		return sseEvent{err: &StreamError{Status: envelope.StatusCodeValue, Body: string(unwrapBody(envelope.Body))}}, nil
	}
	var ev sseEvent
	ev.delta.Usage = envelope.ResponseMeta.Usage.toUsage()
	inner := unwrapBody(envelope.Body)
	if len(inner) == 0 {
		return ev, nil
	}
	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Delta        wireChoicePart `json:"delta"`
			Message      wireChoicePart `json:"message"`
			FinishReason *string        `json:"finish_reason"`
		} `json:"choices"`
		Usage *wireUsage `json:"usage"`
	}
	if err := json.Unmarshal(inner, &parsed); err != nil {
		return sseEvent{}, fmt.Errorf("qoder: sse body: %w", err)
	}
	ev.delta.Model = strings.TrimSpace(parsed.Model)
	if usage := parsed.Usage.toUsage(); usage != nil {
		ev.delta.Usage = usage
	}
	for _, choice := range parsed.Choices {
		appendChoicePart(&ev.delta, choice.Delta)
		appendChoicePart(&ev.recap, choice.Message)
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			ev.delta.FinishReason = strings.TrimSpace(*choice.FinishReason)
		}
	}
	return ev, nil
}

func appendChoicePart(dst *Delta, part wireChoicePart) {
	dst.Content += part.Content
	dst.Reasoning += part.ReasoningContent
	for _, raw := range part.ToolCalls {
		if call, ok := parseToolCall(raw); ok {
			dst.ToolCalls = append(dst.ToolCalls, call)
		}
	}
}

func unwrapBody(raw json.RawMessage) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return []byte(text)
		}
	}
	return raw
}

func parseToolCall(raw json.RawMessage) (ToolCall, bool) {
	var call struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return ToolCall{}, false
	}
	arguments := toolArguments(call.Function.Arguments)
	if call.ID == "" && call.Function.Name == "" && arguments == "" {
		return ToolCall{}, false
	}
	return ToolCall{
		Index:     call.Index,
		ID:        call.ID,
		Name:      call.Function.Name,
		Arguments: arguments,
	}, true
}

// toolArguments accepts the OpenAI string form and a bare JSON object, which
// some upstreams send for complete (non-streamed) calls.
func toolArguments(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}
	return string(raw)
}
