package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestShouldUseQoderProxy(t *testing.T) {
	if shouldUseQoderProxy(nil) {
		t.Fatal("nil")
	}
	qoder := &Account{Platform: PlatformQoder}
	grok := &Account{Platform: PlatformGrok}
	if !shouldUseQoderProxy(qoder) {
		t.Fatal("expected qoder")
	}
	if shouldUseQoderProxy(grok) {
		t.Fatal("grok is not qoder")
	}
}
func TestQoderGatewayNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewQoderGatewayService()
	svc.client = &http.Client{Transport: qoderRoundTrip(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/jobToken/exchange"):
			return qoderJSON(`{"token":"jt-test","expires_in":3600}`), nil
		case strings.Contains(req.URL.Path, "/userinfo"):
			if req.Header.Get("Authorization") != "Bearer jt-test" {
				t.Fatalf("userinfo auth %q", req.Header.Get("Authorization"))
			}
			return qoderJSON(`{"id":"user-1","name":"Ada","email":"ada@example.com"}`), nil
		case strings.Contains(req.URL.Path, "/agent_chat_generation"):
			if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer COSY.") {
				t.Fatalf("chat auth %q", req.Header.Get("Authorization"))
			}
			if req.Header.Get("X-Model-Key") != "auto" {
				t.Fatalf("model %q", req.Header.Get("X-Model-Key"))
			}
			if req.URL.Query().Get("Encode") != "" {
				t.Fatal("plaintext chat must not set Encode")
			}
			body := "data: {\"statusCodeValue\":200,\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"ok\\\"}}]}\"}\n\ndata: [DONE]\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		default:
			t.Fatalf("unexpected %s %s", req.Method, req.URL)
			return nil, io.EOF
		}
	})}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	account := &Account{
		Platform:    PlatformQoder,
		Credentials: map[string]any{"personal_token": "pt-test"},
	}
	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Stream {
		t.Fatalf("result %+v", result)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d body %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"content":"ok"`) {
		t.Fatalf("body %s", recorder.Body.String())
	}
}

type qoderRoundTrip func(*http.Request) (*http.Response, error)

func (f qoderRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func qoderJSON(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
