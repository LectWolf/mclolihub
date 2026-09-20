package cursorproxy

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	env, err := Envelope(map[string]any{"hello": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if env[0] != 0 {
		t.Fatalf("flags=%d", env[0])
	}
	length := binary.BigEndian.Uint32(env[1:5])
	if int(length) != len(env)-5 {
		t.Fatalf("length=%d body=%d", length, len(env)-5)
	}
	frames, rest, err := DecodeFrames(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 || len(frames) != 1 {
		t.Fatalf("frames=%d rest=%d", len(frames), len(rest))
	}
	if frames[0]["hello"] != "ok" {
		t.Fatalf("got %#v", frames[0])
	}
}

func TestOpenAIMessages(t *testing.T) {
	got := OpenAIMessages([]ChatMessage{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "yo"},
		{Role: "user", Content: "  "},
	})
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Role != RoleSystem || got[1].Role != RoleUser || got[2].Role != RoleAssistant {
		t.Fatalf("%+v", got)
	}
}

func TestNewCLILoginSession(t *testing.T) {
	sess, err := NewCLILoginSession()
	if err != nil {
		t.Fatal(err)
	}
	if sess.UUID == "" || sess.Verifier == "" || sess.Challenge == "" {
		t.Fatalf("%+v", sess)
	}
	if !strings.Contains(sess.LoginURL, "loginDeepControl") || !strings.Contains(sess.LoginURL, "challenge=") {
		t.Fatalf("url=%s", sess.LoginURL)
	}
}

func TestRunInferenceMessages(t *testing.T) {
	run := RunInferenceRunRequest("c1", "claude-fable-5-1", "ok")
	if run["runRequest"] == nil {
		t.Fatal("missing runRequest")
	}
	inv := RunInferenceInvoke("i1", "c1", "claude-fable-5-1", []InferenceMessage{{Role: RoleUser, Text: "ok"}})
	if inv["invokeModel"] == nil {
		t.Fatal("missing invokeModel")
	}
}

func TestFrameRunReady(t *testing.T) {
	_, ok := FrameRunReady(map[string]any{"heartbeat": map[string]any{}})
	if ok {
		t.Fatal("heartbeat is not run_ready")
	}
	model, ok := FrameRunReady(map[string]any{
		"runReady": map[string]any{
			"resolvedModel": map[string]any{"modelId": "claude-fable-5-1"},
		},
	})
	if !ok || model != "claude-fable-5-1" {
		t.Fatalf("model=%s ok=%v", model, ok)
	}
}

func TestChecksumStableShape(t *testing.T) {
	cs := Checksum("c8b94651-f0a5-55bd-961a-921860efc3bf")
	if !json.Valid([]byte("\"" + cs + "\"")) {
		t.Fatalf("not ascii: %q", cs)
	}
	if len(cs) < 36 {
		t.Fatalf("short checksum %q", cs)
	}
}

func TestExtractConnectError(t *testing.T) {
	code, msg := extractConnectError(map[string]any{
		"error": map[string]any{
			"code":    "invalid_argument",
			"message": "Sand traffic is not supported on this endpoint",
		},
	})
	if code != "invalid_argument" || msg == "" {
		t.Fatalf("code=%s msg=%s", code, msg)
	}
}
