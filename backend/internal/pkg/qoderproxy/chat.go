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

// ChatURL is the agent SSE endpoint. The body is plain JSON; Encode is omitted.
func ChatURL() string { return chatURL }

// Message is one chat turn after content parts have been flattened.
type Message struct {
	Role    string
	Content string
}

// ChatInput is everything needed to build one upstream request.
type ChatInput struct {
	Model            string
	UserID           string
	Messages         []Message
	MaxTokens        int
	ThinkingEffort   string
	ToolsNote        string
	RequestID        string
	BusinessID       string
	BeginAtUnixMilli int64
}

// Delta is one parsed SSE update. Err is set when the upstream event itself failed.
type Delta struct {
	Content   string
	Reasoning string
	ToolCalls []ToolCall
	Err       error
}

// ToolCall is one native tool-call fragment. Index groups streamed argument pieces.
type ToolCall struct {
	Index     int
	ID        string
	Name      string
	Arguments string
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
	system, turns := splitSystem(in.Messages)
	if note := strings.TrimSpace(in.ToolsNote); note != "" {
		if system != "" {
			system += "\n\n"
		}
		system += note
	}
	lastUser := lastUserText(turns)
	recordID := recordID(model, turns, maxTokens)
	sessionID := sessionID(in.UserID, model)
	name := truncateRunes(lastUser, 30)
	if name == "" {
		name = "chat"
	}

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
		"messages":         wireMessages(turns),
		"tools":            []any{},
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
		content := msg.Content
		switch role {
		case "system", "developer":
			if strings.TrimSpace(content) == "" {
				continue
			}
			if system.Len() > 0 {
				_, _ = system.WriteString("\n\n")
			}
			_, _ = system.WriteString(content)
		case "tool":
			turns = append(turns, Message{Role: "user", Content: content})
		default:
			if role == "" {
				role = "user"
			}
			turns = append(turns, Message{Role: role, Content: content})
		}
	}
	return system.String(), turns
}

func wireMessages(turns []Message) []map[string]string {
	out := make([]map[string]string, 0, len(turns))
	for _, msg := range turns {
		out = append(out, map[string]string{"role": msg.Role, "content": msg.Content})
	}
	return out
}

func lastUserText(turns []Message) string {
	for i := len(turns) - 1; i >= 0; i-- {
		if strings.EqualFold(turns[i].Role, "user") && strings.TrimSpace(turns[i].Content) != "" {
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

// ScanChat reads an agent SSE body and calls fn for each delta.
// [DONE] ends the stream. A non-zero, non-200 statusCodeValue is returned as Delta.Err
// and also as the function error when no later success arrives.
func ScanChat(r io.Reader, fn func(Delta) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var streamErr error
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			return streamErr
		}
		delta, err := parseEvent(payload)
		if err != nil {
			return err
		}
		if delta.Err != nil {
			streamErr = delta.Err
		}
		if fn != nil {
			if err := fn(delta); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return streamErr
}

func parseEvent(payload string) (Delta, error) {
	var envelope struct {
		StatusCodeValue int             `json:"statusCodeValue"`
		Body            json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return Delta{}, fmt.Errorf("qoder: sse event: %w", err)
	}
	if envelope.StatusCodeValue != 0 && envelope.StatusCodeValue != 200 {
		return Delta{Err: fmt.Errorf("qoder: upstream status %d: %s", envelope.StatusCodeValue, truncateRunes(string(envelope.Body), 240))}, nil
	}
	inner := unwrapBody(envelope.Body)
	if len(inner) == 0 {
		return Delta{}, nil
	}
	var parsed struct {
		Choices []struct {
			Delta struct {
				Content          string            `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
			Message struct {
				Content          string            `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(inner, &parsed); err != nil {
		return Delta{}, fmt.Errorf("qoder: sse body: %w", err)
	}
	var out Delta
	for _, choice := range parsed.Choices {
		out.Content += firstNonEmpty(choice.Delta.Content, choice.Message.Content)
		out.Reasoning += firstNonEmpty(choice.Delta.ReasoningContent, choice.Message.ReasoningContent)
		rawTools := choice.Delta.ToolCalls
		if len(rawTools) == 0 {
			rawTools = choice.Message.ToolCalls
		}
		for _, raw := range rawTools {
			call, ok := parseToolCall(raw)
			if ok {
				out.ToolCalls = append(out.ToolCalls, call)
			}
		}
	}
	return out, nil
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
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return ToolCall{}, false
	}
	if call.ID == "" && call.Function.Name == "" && call.Function.Arguments == "" {
		return ToolCall{}, false
	}
	return ToolCall{
		Index:     call.Index,
		ID:        call.ID,
		Name:      call.Function.Name,
		Arguments: call.Function.Arguments,
	}, true
}
