package qoderproxy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBuildChatBodyStableSession(t *testing.T) {
	in := ChatInput{
		Model:  "auto",
		UserID: "user-1",
		Messages: []Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
		},
		MaxTokens:        128,
		RequestID:        "req-1",
		BusinessID:       "biz-1",
		BeginAtUnixMilli: 10,
	}
	first, err := BuildChatBody(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildChatBody(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("chat body is not stable")
	}
	text := string(first)
	for _, needle := range []string{
		`"key":"auto"`,
		`"session_type":"qodercli"`,
		`"chat_task":"FREE_INPUT"`,
		`"system":"be brief"`,
		`"max_tokens":128`,
		`"tools":[]`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %s in %s", needle, text)
		}
	}
	if strings.Contains(text, "Encode") {
		t.Fatal("body must stay plain JSON")
	}
}

func TestScanChat(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"statusCodeValue":200,"body":"{\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}"}`,
		"",
		`data: {"statusCodeValue":200,"body":{"choices":[{"delta":{"reasoning_content":"think","tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{}"}}]}}]}}`,
		"data: [DONE]",
		"",
	}, "\n")
	var got Delta
	err := ScanChat(strings.NewReader(raw), func(d Delta) error {
		got.Content += d.Content
		got.Reasoning += d.Reasoning
		got.ToolCalls = append(got.ToolCalls, d.ToolCalls...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hi" || got.Reasoning != "think" {
		t.Fatalf("delta %+v", got)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "lookup" || got.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tools %+v", got.ToolCalls)
	}
}

func TestScanChatUpstreamStatus(t *testing.T) {
	raw := "data: {\"statusCodeValue\":403,\"body\":\"denied\"}\n"
	err := ScanChat(strings.NewReader(raw), nil)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err %v", err)
	}
}

func TestBuildChatBodyNativeTools(t *testing.T) {
	body, err := BuildChatBody(ChatInput{
		Model:  "auto",
		UserID: "user-1",
		Tools:  []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}`)},
		Messages: []Message{
			{Role: "user", Content: "find x"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`}}},
			{Role: "tool", ToolCallID: "call_1", Name: "lookup", Content: "found"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Tools    []map[string]any `json:"tools"`
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Tools) != 1 || len(parsed.Messages) != 3 {
		t.Fatalf("tools %v messages %v", parsed.Tools, parsed.Messages)
	}
	calls, ok := parsed.Messages[1]["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant turn %v", parsed.Messages[1])
	}
	if parsed.Messages[2]["role"] != "tool" || parsed.Messages[2]["tool_call_id"] != "call_1" {
		t.Fatalf("tool turn %v", parsed.Messages[2])
	}
}

func TestBuildChatBodyRendersToolHistoryWithoutTools(t *testing.T) {
	body, err := BuildChatBody(ChatInput{
		Model: "auto",
		Messages: []Message{
			{Role: "user", Content: "find x"},
			{Role: "assistant", Content: "checking", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`}}},
			{Role: "tool", ToolCallID: "call_1", Name: "lookup", Content: "found"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, `"tool_calls"`) || strings.Contains(text, `"role":"tool"`) {
		t.Fatalf("structured tool turns without tool definitions: %s", text)
	}
	for _, needle := range []string{`"tools":[]`, `Tool calls:\n- lookup [call_1]: {\"q\":\"x\"}`, `Tool result (lookup) [call_1]:\nfound`} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %s in %s", needle, text)
		}
	}
}

func TestScanChatUsageFinishAndModel(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"statusCodeValue":200,"body":{"model":"qmodel-2","choices":[{"delta":{"content":"hi"}}]}}`,
		`data: {"statusCodeValue":200,"body":{"choices":[{"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":11,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":6},"completion_tokens_details":{"reasoning_tokens":2}}}}`,
		`data: {"statusCodeValue":200,"body":"","response_meta":{"usage":{"prompt_tokens":99,"completion_tokens":1}}}`,
		"data: [DONE]",
	}, "\n")
	var model, finish string
	var usages []*Usage
	err := ScanChat(strings.NewReader(raw), func(d Delta) error {
		if d.Model != "" {
			model = d.Model
		}
		if d.FinishReason != "" {
			finish = d.FinishReason
		}
		if d.Usage != nil {
			usages = append(usages, d.Usage)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if model != "qmodel-2" || finish != "length" {
		t.Fatalf("model %q finish %q", model, finish)
	}
	if len(usages) != 2 {
		t.Fatalf("usages %+v", usages)
	}
	if got := *usages[0]; got != (Usage{PromptTokens: 11, CompletionTokens: 4, CachedTokens: 6, ReasoningTokens: 2}) {
		t.Fatalf("usage %+v", got)
	}
	if usages[1].PromptTokens != 99 {
		t.Fatalf("response_meta usage %+v", usages[1])
	}
}

func TestScanChatSkipsMessageRecap(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"statusCodeValue":200,"body":{"choices":[{"delta":{"content":"he"}}]}}`,
		`data: {"statusCodeValue":200,"body":{"choices":[{"delta":{"content":"llo"}}]}}`,
		`data: {"statusCodeValue":200,"body":{"choices":[{"message":{"content":"hello","tool_calls":[{"id":"c1","function":{"name":"f","arguments":{"a":1}}}]}}]}}`,
		"data: [DONE]",
	}, "\n")
	var text strings.Builder
	var calls []ToolCall
	err := ScanChat(strings.NewReader(raw), func(d Delta) error {
		_, _ = text.WriteString(d.Content)
		calls = append(calls, d.ToolCalls...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "hello" {
		t.Fatalf("recap text must not repeat: %q", text.String())
	}
	if len(calls) != 1 || calls[0].Arguments != `{"a":1}` {
		t.Fatalf("tool calls only in the final message are kept: %+v", calls)
	}
}

func TestScanChatStreamErrorIsTyped(t *testing.T) {
	raw := "data: {\"statusCodeValue\":429,\"body\":\"{\\\"message\\\":\\\"slow down\\\"}\"}\n" +
		"data: {\"statusCodeValue\":200,\"body\":{\"choices\":[{\"delta\":{\"content\":\"late\"}}]}}\n"
	var seen int
	err := ScanChat(strings.NewReader(raw), func(Delta) error { seen++; return nil })
	var streamErr *StreamError
	if !errors.As(err, &streamErr) || streamErr.Status != 429 || !strings.Contains(streamErr.Body, "slow down") {
		t.Fatalf("err %v", err)
	}
	if seen != 0 {
		t.Fatal("scan must stop at the failed event")
	}
}
