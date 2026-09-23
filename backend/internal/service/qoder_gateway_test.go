package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type qoderRoundTrip func(*http.Request) (*http.Response, error)

func (f qoderRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func qoderJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func qoderSSEResponse(events ...string) *http.Response {
	var b strings.Builder
	for _, event := range events {
		_, _ = b.WriteString("data: " + event + "\n\n")
	}
	_, _ = b.WriteString("data: [DONE]\n\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(b.String())),
	}
}

// qoderEvent wraps an OpenAI-style chunk the way Qoder does: a JSON string
// inside the statusCodeValue envelope.
func qoderEvent(t *testing.T, body map[string]any) string {
	t.Helper()
	inner, err := json.Marshal(body)
	require.NoError(t, err)
	envelope, err := json.Marshal(map[string]any{"statusCodeValue": 200, "body": string(inner)})
	require.NoError(t, err)
	return string(envelope)
}

func qoderTextEvent(t *testing.T, text string) string {
	return qoderEvent(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
}

func qoderUsageEvent(t *testing.T, prompt, completion int) string {
	return qoderEvent(t, map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": prompt, "completion_tokens": completion},
	})
}

type qoderFakeUpstream struct {
	t         *testing.T
	exchanges atomic.Int32
	refreshes atomic.Int32
	chats     atomic.Int32
	chat      func(req *http.Request, body []byte, attempt int32) *http.Response
}

func (f *qoderFakeUpstream) client() *http.Client {
	return &http.Client{Transport: qoderRoundTrip(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/jobToken/exchange"):
			n := f.exchanges.Add(1)
			return qoderJSON(http.StatusOK, `{"token":"jt-`+strconv.Itoa(int(n))+`","expires_in":3600}`), nil
		case strings.HasSuffix(req.URL.Path, "/deviceToken/refresh"):
			f.refreshes.Add(1)
			return qoderJSON(http.StatusOK, `{"token":"dt-new","refresh_token":"drt-new","expires_in":3600}`), nil
		case strings.HasSuffix(req.URL.Path, "/userinfo"):
			return qoderJSON(http.StatusOK, `{"id":"user-1","name":"Ada","email":"ada@example.com"}`), nil
		case strings.Contains(req.URL.Path, "/agent_chat_generation"):
			body, err := io.ReadAll(req.Body)
			require.NoError(f.t, err)
			require.True(f.t, strings.HasPrefix(req.Header.Get("Authorization"), "Bearer COSY."), "chat must be COSY-signed")
			return f.chat(req, body, f.chats.Add(1)), nil
		default:
			f.t.Fatalf("unexpected upstream call %s %s", req.Method, req.URL)
			return nil, io.EOF
		}
	})}
}

func newQoderTestService(t *testing.T, upstream *qoderFakeUpstream) *QoderGatewayService {
	t.Helper()
	upstream.t = t
	svc := NewQoderGatewayService(nil, nil, nil, nil, nil)
	svc.doer = upstream.client()
	return svc
}

func newQoderTestContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, recorder
}

func qoderPATAccount() *Account {
	return &Account{ID: 7, Platform: PlatformQoder, Type: AccountTypeAPIKey, Credentials: map[string]any{"personal_token": "pt-test"}}
}

func TestQoderGatewayChatCompletionsNonStream(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(req *http.Request, body []byte, _ int32) *http.Response {
		require.Equal(t, "qmodel", req.Header.Get("X-Model-Key"), "account model mapping applies")
		require.Empty(t, req.URL.Query().Get("Encode"), "chat body is plain JSON")
		require.Equal(t, "qmodel", gjson.GetBytes(body, "model_config.key").String())
		require.Equal(t, "be brief", gjson.GetBytes(body, "system").String())
		return qoderSSEResponse(qoderTextEvent(t, "o"), qoderTextEvent(t, "k"), qoderUsageEvent(t, 12, 3))
	}}
	svc := newQoderTestService(t, upstream)
	account := qoderPATAccount()
	account.Credentials["model_mapping"] = map[string]any{"auto": "qmodel"}
	c, recorder := newQoderTestContext("/v1/chat/completions")

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account,
		[]byte(`{"model":"auto","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"}]}`), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "ok", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	require.Equal(t, "stop", gjson.Get(recorder.Body.String(), "choices.0.finish_reason").String())
	require.Equal(t, int64(12), gjson.Get(recorder.Body.String(), "usage.prompt_tokens").Int())
	require.False(t, result.Stream)
	require.Equal(t, "auto", result.Model)
	require.Equal(t, "qmodel", result.UpstreamModel)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.EqualValues(t, 1, upstream.exchanges.Load())
}

func TestQoderGatewayChatCompletionsStreamToolCalls(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, body []byte, _ int32) *http.Response {
		require.Equal(t, "lookup", gjson.GetBytes(body, "tools.0.function.name").String(), "tools are forwarded natively")
		first := qoderEvent(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "function": map[string]any{"name": "lookup", "arguments": `{"q":`}}},
		}}}})
		second := qoderEvent(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "function": map[string]any{"name": "lookup", "arguments": `"x"}`}}},
		}}}})
		return qoderSSEResponse(first, second, qoderUsageEvent(t, 20, 5))
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/chat/completions")

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(), []byte(`{
		"model":"auto","stream":true,"stream_options":{"include_usage":true},
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],
		"messages":[{"role":"user","content":"find x"}]}`), nil)
	require.NoError(t, err)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)

	var chunks []apicompat.ChatCompletionsChunk
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk apicompat.ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		chunks = append(chunks, chunk)
	}
	require.True(t, strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n"))
	require.Len(t, chunks, 4)

	var names, arguments string
	for _, chunk := range chunks[:2] {
		call := chunk.Choices[0].Delta.ToolCalls[0]
		require.Equal(t, 0, *call.Index)
		names += call.Function.Name
		arguments += call.Function.Arguments
	}
	require.Equal(t, "lookup", names, "the name is sent once, not per fragment")
	require.Equal(t, `{"q":"x"}`, arguments)
	require.Equal(t, "call_1", chunks[0].Choices[0].Delta.ToolCalls[0].ID)
	require.Empty(t, chunks[1].Choices[0].Delta.ToolCalls[0].ID)

	require.Equal(t, "tool_calls", *chunks[2].Choices[0].FinishReason)
	require.Empty(t, chunks[3].Choices)
	require.Equal(t, 20, chunks[3].Usage.PromptTokens)
}

func TestQoderGatewayAnthropicStream(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, body []byte, _ int32) *http.Response {
		require.Equal(t, "You are terse.", gjson.GetBytes(body, "system").String())
		return qoderSSEResponse(qoderTextEvent(t, "Hel"), qoderTextEvent(t, "lo"), qoderUsageEvent(t, 30, 7))
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/messages")

	result, err := svc.ForwardAsAnthropic(c.Request.Context(), c, qoderPATAccount(), []byte(`{
		"model":"auto","max_tokens":256,"stream":true,"system":"You are terse.",
		"messages":[{"role":"user","content":"hello"}]}`))
	require.NoError(t, err)
	out := recorder.Body.String()
	require.Contains(t, out, "event: message_start")
	require.Contains(t, out, `"text":"Hel"`)
	require.Contains(t, out, "event: message_delta")
	require.Contains(t, out, `"output_tokens":7`)
	require.True(t, strings.HasSuffix(strings.TrimSpace(out), `data: {"type":"message_stop"}`))
	require.Equal(t, 30, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
}

func TestQoderGatewayResponsesNonStream(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		return qoderSSEResponse(qoderTextEvent(t, "done"), qoderUsageEvent(t, 9, 1))
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/responses")

	result, err := svc.ForwardAsResponses(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","input":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Equal(t, "response", gjson.Get(body, "object").String())
	require.Equal(t, "completed", gjson.Get(body, "status").String())
	require.Contains(t, body, `"text":"done"`)
	require.Equal(t, 9, result.Usage.InputTokens)
}

func TestQoderGatewayRetriesOnceWithFreshJobToken(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, attempt int32) *http.Response {
		if attempt == 1 {
			return qoderJSON(http.StatusUnauthorized, `{"message":"token revoked"}`)
		}
		return qoderSSEResponse(qoderTextEvent(t, "ok"))
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/chat/completions")

	_, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.EqualValues(t, 2, upstream.exchanges.Load(), "the rejected job token is exchanged again")
	require.EqualValues(t, 2, upstream.chats.Load())
}

func TestQoderGatewayFailsOverBeforeOutput(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		failed, err := json.Marshal(map[string]any{"statusCodeValue": 429, "body": `{"message":"quota exhausted"}`})
		require.NoError(t, err)
		return qoderSSEResponse(string(failed))
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/chat/completions")

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), nil)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Zero(t, recorder.Body.Len(), "nothing may reach the client before failover")
	require.False(t, IsResponseCommitted(c))
}

func TestQoderGatewayBusinessStatusCodeBecomesBadGateway(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		failed, err := json.Marshal(map[string]any{"statusCodeValue": 10001, "body": `{"message":"system busy"}`})
		require.NoError(t, err)
		return qoderSSEResponse(string(failed))
	}}
	svc := newQoderTestService(t, upstream)
	c, _ := newQoderTestContext("/v1/chat/completions")

	_, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), nil)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode, "handlers reuse the status for the client reply")
}

func TestQoderGatewayMidStreamErrorEndsStream(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		failed, err := json.Marshal(map[string]any{"statusCodeValue": 500, "body": `{"message":"backend crashed"}`})
		require.NoError(t, err)
		return qoderSSEResponse(qoderTextEvent(t, "partial"), string(failed))
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/chat/completions")

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), nil)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "output already started: no failover")
	require.NotNil(t, result, "partial usage is still recorded")
	out := recorder.Body.String()
	require.Contains(t, out, `"content":"partial"`)
	require.Contains(t, out, "backend crashed")
	require.True(t, strings.HasSuffix(out, "data: [DONE]\n\n"))
}

func TestQoderGatewayEstimatesUsageWhenUpstreamReportsNone(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		return qoderSSEResponse(qoderTextEvent(t, "The quick brown fox jumps over the lazy dog."))
	}}
	svc := newQoderTestService(t, upstream)
	c, _ := newQoderTestContext("/v1/chat/completions")

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","messages":[{"role":"user","content":"Tell me a sentence about a fox."}]}`), nil)
	require.NoError(t, err)
	require.Positive(t, result.Usage.InputTokens)
	require.Positive(t, result.Usage.OutputTokens)
}

func TestQoderGatewayEmptyResponseFailsOver(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		return qoderSSEResponse()
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/chat/completions")

	_, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), nil)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Zero(t, recorder.Body.Len())
}

// qoderStalledBody blocks until the upstream request is canceled, like a
// connection that sent headers and then went silent.
type qoderStalledBody struct{ ctx context.Context }

func (b qoderStalledBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b qoderStalledBody) Close() error { return nil }

func TestQoderGatewayIdleStreamFailsOver(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(req *http.Request, _ []byte, _ int32) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: qoderStalledBody{ctx: req.Context()}}
	}}
	svc := newQoderTestService(t, upstream)
	svc.cfg = &config.Config{}
	svc.cfg.Gateway.StreamDataIntervalTimeout = 1
	c, recorder := newQoderTestContext("/v1/chat/completions")

	started := time.Now()
	_, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`), nil)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "idle timeout")
	require.Zero(t, recorder.Body.Len())
	require.Less(t, time.Since(started), 5*time.Second)
}

func TestQoderGatewayRefreshesExpiredDeviceToken(t *testing.T) {
	var chatUser string
	upstream := &qoderFakeUpstream{chat: func(req *http.Request, _ []byte, _ int32) *http.Response {
		chatUser = req.Header.Get("Cosy-User")
		return qoderSSEResponse(qoderTextEvent(t, "ok"))
	}}
	svc := newQoderTestService(t, upstream)
	account := &Account{ID: 9, Platform: PlatformQoder, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"qoder_region":  "cn",
		"access_token":  "dt-old",
		"refresh_token": "drt-old",
		"expires_at":    time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		"user_id":       "user-9",
	}}
	c, recorder := newQoderTestContext("/v1/chat/completions")

	_, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account,
		[]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.EqualValues(t, 1, upstream.refreshes.Load())
	require.Zero(t, upstream.exchanges.Load(), "device login does not use the PAT exchange")
	require.Equal(t, "dt-new", account.GetCredential("access_token"))
	require.Equal(t, "drt-new", account.GetCredential("refresh_token"))
	require.Equal(t, "user-9", chatUser)
}

func TestQoderGatewayRejectedPATIsCredentialFailure(t *testing.T) {
	svc := NewQoderGatewayService(nil, nil, nil, nil, nil)
	svc.doer = &http.Client{Transport: qoderRoundTrip(func(req *http.Request) (*http.Response, error) {
		require.True(t, strings.HasSuffix(req.URL.Path, "/jobToken/exchange"))
		return qoderJSON(http.StatusUnauthorized, `{"message":"invalid personal token"}`), nil
	})}
	c, recorder := newQoderTestContext("/v1/chat/completions")

	_, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), nil)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.IsCredentialFailure())
	require.Equal(t, QoderCredentialRejectedReason, failoverErr.Reason)
	require.Zero(t, recorder.Body.Len())
}

func TestQoderGatewayUpstreamClientErrorUsesInboundProtocol(t *testing.T) {
	upstream := &qoderFakeUpstream{chat: func(_ *http.Request, _ []byte, _ int32) *http.Response {
		return qoderJSON(http.StatusBadRequest, `{"message":"unknown model key"}`)
	}}
	svc := newQoderTestService(t, upstream)
	c, recorder := newQoderTestContext("/v1/messages")

	_, err := svc.ForwardAsAnthropic(c.Request.Context(), c, qoderPATAccount(),
		[]byte(`{"model":"nope","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "a bad request fails the same way on every account")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "error", gjson.Get(recorder.Body.String(), "type").String())
	require.Equal(t, "unknown model key", gjson.Get(recorder.Body.String(), "error.message").String())
}

func TestQoderChatInputConversion(t *testing.T) {
	var req apicompat.ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"auto","max_completion_tokens":64,"reasoning_effort":"high",
		"tools":[{"type":"function","function":{"name":"read_file"}},{"type":"web_search"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"https://x/y.png"}}]},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"p\":1}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"file body"}
		]}`), &req))
	in, err := qoderChatInput(&req)
	require.NoError(t, err)
	require.Equal(t, 64, in.MaxTokens)
	require.Equal(t, "high", in.ThinkingEffort)
	require.Len(t, in.Tools, 1, "provider built-ins are dropped")
	require.Equal(t, "read_file", gjson.GetBytes(in.Tools[0], "function.name").String())
	require.Equal(t, "object", gjson.GetBytes(in.Tools[0], "function.parameters.type").String())
	require.Len(t, in.Messages, 3)
	require.Equal(t, "look\n[image omitted]", in.Messages[0].Content)
	require.Equal(t, "call_1", in.Messages[1].ToolCalls[0].ID)
	require.Equal(t, `{"p":1}`, in.Messages[1].ToolCalls[0].Arguments)
	require.Equal(t, "tool", in.Messages[2].Role)
	require.Equal(t, "call_1", in.Messages[2].ToolCallID)
}

func TestQoderMachineIDIsStablePerAccount(t *testing.T) {
	a := &Account{ID: 42, Platform: PlatformQoder}
	require.Equal(t, qoderMachineID(a), qoderMachineID(a))
	require.NotEqual(t, qoderMachineID(a), qoderMachineID(&Account{ID: 43, Platform: PlatformQoder}))
	a.Credentials = map[string]any{"machine_id": "fixed"}
	require.Equal(t, "fixed", qoderMachineID(a))
}
