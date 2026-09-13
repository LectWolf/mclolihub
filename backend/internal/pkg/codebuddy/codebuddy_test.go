package codebuddy

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestProfileForAuth(t *testing.T) {
	token := jwtWithISS("https://www.codebuddy.cn/auth/realms/copilot")
	profile, err := ProfileForAuth("www.codebuddy.cn", token)
	if err != nil {
		t.Fatal(err)
	}
	if profile != ProfileCNCLI {
		t.Fatalf("got %s", profile)
	}

	intl, err := ProfileForAuth("www.workbuddy.ai", jwtWithISS("https://www.workbuddy.ai"))
	if err != nil {
		t.Fatal(err)
	}
	if intl != ProfileIntlWork {
		t.Fatalf("got %s", intl)
	}
}

func TestEnsureLeadingSystemMessage(t *testing.T) {
	body := []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`)
	out, err := EnsureLeadingSystemMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Messages) != 2 || parsed.Messages[0].Role != "system" {
		t.Fatalf("unexpected messages: %+v", parsed.Messages)
	}
}

func TestParseOfficialInfoCredentials(t *testing.T) {
	raw := map[string]any{
		"account": map[string]any{"uid": "u1", "nickname": "n"},
		"auth": map[string]any{
			"accessToken":  jwtWithISS("https://www.codebuddy.cn/auth/realms/copilot"),
			"refreshToken": "rt",
			"domain":       "www.codebuddy.cn",
			"expiresAt":    float64(1_700_000_000_000),
		},
	}
	creds, err := ParseCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if creds.UID != "u1" || creds.Profile != ProfileCNCLI || creds.ExpiresAt != 1_700_000_000 {
		t.Fatalf("%+v", creds)
	}
}

func jwtWithISS(iss string) string {
	payload, _ := json.Marshal(map[string]string{"iss": iss})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
