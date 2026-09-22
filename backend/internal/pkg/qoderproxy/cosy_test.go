package qoderproxy

import (
	"strings"
	"testing"
	"time"
)

func TestSigPath(t *testing.T) {
	got := SigPath("https://api2.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common")
	want := "/api/v2/service/pro/sse/agent_chat_generation"
	if got != want {
		t.Fatalf("sig path %q", got)
	}
	if SigPath("https://openapi.qoder.sh/api/v1/userinfo") != "" {
		t.Fatal("expected empty sig path")
	}
}

func TestSignHeaders(t *testing.T) {
	ids := []string{
		"01234567-89ab-cdef-0123-456789abcdef",
		"abcdefab-cdef-0123-4567-89abcdef0123",
		"11111111-2222-3333-4444-555555555555",
	}
	n := 0
	next := func() string {
		id := ids[n%len(ids)]
		n++
		return id
	}
	headers, err := signAt([]byte(`{"a":1}`), ChatURL(), Creds{
		UserID:    "user-1",
		AuthToken: "jt-test",
		Name:      "Ada",
		Email:     "ada@example.com",
		MachineID: "machine-1",
	}, time.Unix(1_700_000_000, 0), next)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(headers.Authorization, "Bearer COSY.") {
		t.Fatalf("authorization %q", headers.Authorization)
	}
	parts := strings.Split(strings.TrimPrefix(headers.Authorization, "Bearer COSY."), ".")
	if len(parts) != 2 || len(parts[1]) != 32 {
		t.Fatalf("authorization payload %q", headers.Authorization)
	}
	for _, key := range []string{
		"Cosy-Key", "Cosy-User", "Cosy-Date", "Cosy-Version", "Cosy-Machineid",
		"Cosy-Bodyhash", "Cosy-Bodylength", "Cosy-Sigpath", "X-Request-Id",
	} {
		if headers.Headers[key] == "" {
			t.Fatalf("missing %s", key)
		}
	}
	if headers.Headers["Cosy-User"] != "user-1" {
		t.Fatalf("user %q", headers.Headers["Cosy-User"])
	}
	if headers.Headers["Cosy-Date"] != "1700000000" {
		t.Fatalf("date %q", headers.Headers["Cosy-Date"])
	}
	if headers.Headers["Cosy-Sigpath"] != "/api/v2/service/pro/sse/agent_chat_generation" {
		t.Fatalf("sigpath %q", headers.Headers["Cosy-Sigpath"])
	}
	if headers.Headers["Cosy-Bodylength"] != "7" {
		t.Fatalf("length %q", headers.Headers["Cosy-Bodylength"])
	}
	if headers.Headers["Cosy-Machineid"] != "machine-1" {
		t.Fatalf("machine %q", headers.Headers["Cosy-Machineid"])
	}
}

func TestPKCS7PadFullBlock(t *testing.T) {
	padded := pkcs7Pad(bytes16(), 16)
	if len(padded) != 32 {
		t.Fatalf("len %d", len(padded))
	}
	if padded[31] != 16 {
		t.Fatalf("pad %d", padded[31])
	}
}

func bytes16() []byte {
	return []byte("0123456789abcdef")
}
