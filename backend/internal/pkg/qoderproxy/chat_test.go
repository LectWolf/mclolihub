package qoderproxy

import (
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
