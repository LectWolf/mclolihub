package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoderproxy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tiktoken-go/tokenizer"
	"go.uber.org/zap"
)

var (
	errQoderStreamIdle     = errors.New("qoder upstream stream idle timeout")
	errQoderEmptyResponse  = errors.New("qoder returned an empty response")
	errQoderInvalidToolArg = errors.New("qoder returned truncated tool call arguments")
)

// relay streams one successful upstream response to the client in the
// inbound protocol and returns the result used for usage recording.
//
// Nothing is written to the client before the first semantic delta, so an
// upstream failure that arrives first (failed SSE event, idle timeout, empty
// answer) still lets the handler fail over to another account.
func (s *QoderGatewayService) relay(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	req *qoderRequest,
	resp *http.Response,
	cancelUpstream context.CancelFunc,
	input qoderproxy.ChatInput,
	clientModel string,
	upstreamModel string,
	start time.Time,
) (*ForwardResult, error) {
	defer cancelUpstream()
	defer func() { _ = resp.Body.Close() }()

	var body io.Reader = resp.Body
	var idle atomic.Bool
	var watch *qoderIdleReader
	if timeout := s.streamIdleTimeout(); timeout > 0 {
		watch = newQoderIdleReader(resp.Body, timeout, func() {
			idle.Store(true)
			cancelUpstream()
		})
		body = watch
	}

	acc := newQoderAccumulator(clientModel, start)
	sink := newQoderSink(c, req, clientModel)
	scanErr := qoderproxy.ScanChat(body, func(delta qoderproxy.Delta) error {
		if chunk := acc.apply(delta); chunk != nil {
			sink.chunk(chunk)
		}
		return nil
	})
	if watch != nil {
		watch.stop()
	}
	// Only a read that actually broke counts as idle; a stream that finished
	// right at the deadline stays successful.
	if scanErr != nil && idle.Load() {
		scanErr = errQoderStreamIdle
	}
	if scanErr == nil && !acc.hasOutput() {
		scanErr = errQoderEmptyResponse
	}
	if scanErr == nil && req.protocol == qoderProtocolResponses {
		scanErr = sink.validate()
	}

	usage, estimated := acc.chatUsage(input)
	result := &ForwardResult{
		RequestID:             resp.Header.Get("x-request-id"),
		UpstreamHeaders:       resp.Header,
		Usage:                 qoderClaudeUsage(usage),
		Model:                 clientModel,
		UpstreamModel:         upstreamModel,
		UpstreamResponseModel: acc.upstreamModel,
		Stream:                req.stream,
		Duration:              time.Since(start),
		FirstTokenMs:          acc.firstTokenMs,
		ReasoningEffort:       req.reasoningEffort,
	}

	if scanErr != nil {
		if !sink.started() {
			return nil, s.streamFailure(ctx, c, account, req, upstreamModel, resp.Header, scanErr)
		}
		// Part of the answer already reached the client: failover would
		// splice two answers, so end the stream with a visible error instead.
		message := s.noteMidStreamFailure(ctx, c, account, upstreamModel, resp.Header, scanErr)
		sink.fail(message)
		result.ClientDisconnect = sink.clientGone()
		return result, fmt.Errorf("qoder stream interrupted: %w", scanErr)
	}

	sink.finish(acc.finalChunk(), usage, acc.response(usage))
	result.ClientDisconnect = sink.clientGone()
	if estimated {
		logger.FromContext(ctx).Debug("qoder.usage_estimated",
			zap.Int64("account_id", account.ID),
			zap.String("model", upstreamModel),
			zap.Int("prompt_tokens", usage.PromptTokens),
			zap.Int("completion_tokens", usage.CompletionTokens),
		)
	}
	return result, nil
}

func (s *QoderGatewayService) streamIdleTimeout() time.Duration {
	if s.cfg == nil || s.cfg.Gateway.StreamDataIntervalTimeout <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
}

// streamFailure handles a stream that failed before any output reached the
// client; the handler may still fail over.
func (s *QoderGatewayService) streamFailure(ctx context.Context, c *gin.Context, account *Account, req *qoderRequest, model string, headers http.Header, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var streamErr *qoderproxy.StreamError
	if errors.As(err, &streamErr) {
		return s.upstreamStatusFailure(ctx, c, account, req, model, streamErr.Status, headers, []byte(streamErr.Body))
	}
	if errors.Is(err, errQoderStreamIdle) && s.rateLimitService != nil {
		s.rateLimitService.HandleStreamTimeout(ctx, account, model)
	}
	message := sanitizeUpstreamErrorMessage(err.Error())
	appendOpsUpstreamError(c, qoderOpsEvent(account, http.StatusBadGateway, "failover", message))
	return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: qoderErrorBody(message)}
}

// noteMidStreamFailure records a failure after output started and returns the
// message shown to the client.
func (s *QoderGatewayService) noteMidStreamFailure(ctx context.Context, c *gin.Context, account *Account, model string, headers http.Header, err error) string {
	status := http.StatusBadGateway
	message := "Upstream stream interrupted"
	var streamErr *qoderproxy.StreamError
	switch {
	case errors.As(err, &streamErr):
		status = qoderErrorStatus(streamErr.Status)
		if msg := strings.TrimSpace(extractUpstreamErrorMessage([]byte(streamErr.Body))); msg != "" {
			message = sanitizeUpstreamErrorMessage(msg)
		}
		if s.rateLimitService != nil && ctx.Err() == nil {
			s.rateLimitService.HandleUpstreamError(ctx, account, status, headers, []byte(streamErr.Body), model)
		}
	case errors.Is(err, errQoderStreamIdle):
		message = "Upstream stream idle timeout"
		if s.rateLimitService != nil {
			s.rateLimitService.HandleStreamTimeout(ctx, account, model)
		}
	case errors.Is(err, errQoderInvalidToolArg):
		message = "Upstream returned an incomplete tool call"
	}
	setOpsUpstreamError(c, status, message, "")
	appendOpsUpstreamError(c, qoderOpsEvent(account, status, "stream_error", sanitizeUpstreamErrorMessage(err.Error())))
	return message
}

// ---------------------------------------------------------------------------
// Accumulator: Qoder deltas -> Chat Completions chunks
// ---------------------------------------------------------------------------

type qoderToolState struct {
	index     int
	id        string
	name      string
	fromID    bool
	arguments strings.Builder
}

type qoderAccumulator struct {
	id            string
	created       int64
	model         string
	start         time.Time
	firstTokenMs  *int
	sentRole      bool
	content       strings.Builder
	reasoning     strings.Builder
	tools         []*qoderToolState
	toolsByIndex  map[int]*qoderToolState
	finishReason  string
	upstreamModel string
	usage         *qoderproxy.Usage
}

func newQoderAccumulator(model string, start time.Time) *qoderAccumulator {
	return &qoderAccumulator{
		id:           "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
		created:      time.Now().Unix(),
		model:        model,
		start:        start,
		toolsByIndex: map[int]*qoderToolState{},
	}
}

func (a *qoderAccumulator) hasOutput() bool {
	return a.content.Len() > 0 || a.reasoning.Len() > 0 || len(a.tools) > 0
}

// apply folds one upstream delta in and returns the chunk to stream, or nil
// when the delta carried no output.
func (a *qoderAccumulator) apply(d qoderproxy.Delta) *apicompat.ChatCompletionsChunk {
	if d.Model != "" {
		a.upstreamModel = d.Model
	}
	if d.Usage != nil {
		a.usage = d.Usage
	}
	if d.FinishReason != "" {
		a.finishReason = d.FinishReason
	}
	var delta apicompat.ChatDelta
	has := false
	if d.Reasoning != "" {
		_, _ = a.reasoning.WriteString(d.Reasoning)
		reasoning := d.Reasoning
		delta.ReasoningContent = &reasoning
		has = true
	}
	if d.Content != "" {
		_, _ = a.content.WriteString(d.Content)
		content := d.Content
		delta.Content = &content
		has = true
	}
	for _, call := range d.ToolCalls {
		delta.ToolCalls = append(delta.ToolCalls, a.applyToolCall(call))
		has = true
	}
	if !has {
		return nil
	}
	if !a.sentRole {
		delta.Role = "assistant"
		a.sentRole = true
	}
	if a.firstTokenMs == nil {
		ms := int(time.Since(a.start).Milliseconds())
		a.firstTokenMs = &ms
	}
	return a.chunk(delta, nil)
}

// applyToolCall merges one tool-call fragment. id and name go out only with
// the first fragment of a call, so clients that concatenate every streamed
// field never see them doubled.
func (a *qoderAccumulator) applyToolCall(call qoderproxy.ToolCall) apicompat.ChatToolCall {
	st, ok := a.toolsByIndex[call.Index]
	// Some upstreams send complete parallel calls that all carry index 0; a
	// new id on a known index starts a new call.
	if ok && call.ID != "" && st.fromID && call.ID != st.id {
		ok = false
	}
	if !ok {
		st = &qoderToolState{index: len(a.tools), id: call.ID, name: call.Name, fromID: call.ID != ""}
		if st.id == "" {
			st.id = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
		}
		a.toolsByIndex[call.Index] = st
		a.tools = append(a.tools, st)
		_, _ = st.arguments.WriteString(call.Arguments)
		index := st.index
		return apicompat.ChatToolCall{
			Index:    &index,
			ID:       st.id,
			Type:     "function",
			Function: apicompat.ChatFunctionCall{Name: st.name, Arguments: call.Arguments},
		}
	}
	out := apicompat.ChatToolCall{Function: apicompat.ChatFunctionCall{Arguments: call.Arguments}}
	if st.name == "" && call.Name != "" {
		st.name = call.Name
		out.Function.Name = call.Name
	}
	_, _ = st.arguments.WriteString(call.Arguments)
	index := st.index
	out.Index = &index
	return out
}

func (a *qoderAccumulator) chunk(delta apicompat.ChatDelta, finish *string) *apicompat.ChatCompletionsChunk {
	return &apicompat.ChatCompletionsChunk{
		ID:      a.id,
		Object:  "chat.completion.chunk",
		Created: a.created,
		Model:   a.model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	}
}

func (a *qoderAccumulator) clientFinishReason() string {
	switch a.finishReason {
	case "length", "max_tokens":
		return "length"
	case "content_filter":
		return "content_filter"
	}
	if len(a.tools) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func (a *qoderAccumulator) finalChunk() *apicompat.ChatCompletionsChunk {
	finish := a.clientFinishReason()
	return a.chunk(apicompat.ChatDelta{}, &finish)
}

func (a *qoderAccumulator) response(usage *apicompat.ChatUsage) *apicompat.ChatCompletionsResponse {
	message := apicompat.ChatMessage{Role: "assistant", ReasoningContent: a.reasoning.String()}
	text := a.content.String()
	if text == "" && len(a.tools) > 0 {
		message.Content = json.RawMessage("null")
	} else {
		raw, _ := json.Marshal(text)
		message.Content = raw
	}
	for _, st := range a.tools {
		arguments := st.arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		message.ToolCalls = append(message.ToolCalls, apicompat.ChatToolCall{
			ID:       st.id,
			Type:     "function",
			Function: apicompat.ChatFunctionCall{Name: st.name, Arguments: arguments},
		})
	}
	return &apicompat.ChatCompletionsResponse{
		ID:      a.id,
		Object:  "chat.completion",
		Created: a.created,
		Model:   a.model,
		Choices: []apicompat.ChatChoice{{Index: 0, Message: message, FinishReason: a.clientFinishReason()}},
		Usage:   usage,
	}
}

// chatUsage returns the upstream-reported usage, or a local tiktoken estimate
// (estimated=true) when the upstream reported none, so requests are never
// recorded as free.
func (a *qoderAccumulator) chatUsage(input qoderproxy.ChatInput) (*apicompat.ChatUsage, bool) {
	if a.usage != nil {
		usage := &apicompat.ChatUsage{
			PromptTokens:     a.usage.PromptTokens,
			CompletionTokens: a.usage.CompletionTokens,
			TotalTokens:      a.usage.PromptTokens + a.usage.CompletionTokens,
		}
		if a.usage.CachedTokens > 0 {
			usage.PromptTokensDetails = &apicompat.ChatTokenDetails{CachedTokens: a.usage.CachedTokens}
		}
		if a.usage.ReasoningTokens > 0 {
			usage.CompletionTokensDetails = &apicompat.ChatTokenDetails{ReasoningTokens: a.usage.ReasoningTokens}
		}
		return usage, false
	}
	prompt := estimateQoderPromptTokens(input)
	completion := qoderTokenCount(a.content.String()) + qoderTokenCount(a.reasoning.String())
	for _, st := range a.tools {
		completion += qoderTokenCount(st.name) + qoderTokenCount(st.arguments.String())
	}
	return &apicompat.ChatUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}, true
}

// qoderClaudeUsage maps Chat Completions usage onto the usage-log shape:
// input tokens exclude cache reads, as in Anthropic usage.
func qoderClaudeUsage(usage *apicompat.ChatUsage) ClaudeUsage {
	if usage == nil {
		return ClaudeUsage{}
	}
	cached := 0
	if usage.PromptTokensDetails != nil {
		cached = usage.PromptTokensDetails.CachedTokens
	}
	input := usage.PromptTokens - cached
	if input < 0 {
		input = 0
	}
	return ClaudeUsage{
		InputTokens:          input,
		OutputTokens:         usage.CompletionTokens,
		CacheReadInputTokens: cached,
	}
}

const (
	// qoderMessageTokenOverhead approximates per-turn framing tokens.
	qoderMessageTokenOverhead = 4
	// qoderPromptTokenOverhead approximates the reply priming tokens.
	qoderPromptTokenOverhead = 3
)

var (
	qoderTokenizerOnce  sync.Once
	qoderTokenizerCodec tokenizer.Codec
)

func qoderTokenCount(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	qoderTokenizerOnce.Do(func() {
		if codec, err := tokenizer.Get(tokenizer.O200kBase); err == nil {
			qoderTokenizerCodec = codec
		}
	})
	if qoderTokenizerCodec != nil {
		if n, err := qoderTokenizerCodec.Count(text); err == nil {
			return n
		}
	}
	// Rough fallback: about four characters per token.
	return (utf8.RuneCountInString(text) + 3) / 4
}

func estimateQoderPromptTokens(input qoderproxy.ChatInput) int {
	total := qoderPromptTokenOverhead
	for _, msg := range input.Messages {
		total += qoderMessageTokenOverhead + qoderTokenCount(msg.Content)
		for _, call := range msg.ToolCalls {
			total += qoderTokenCount(call.Name) + qoderTokenCount(call.Arguments)
		}
	}
	for _, tool := range input.Tools {
		total += qoderTokenCount(string(tool))
	}
	return total
}

// ---------------------------------------------------------------------------
// Sinks: Chat Completions chunks -> client protocol
// ---------------------------------------------------------------------------

type qoderSink interface {
	chunk(chunk *apicompat.ChatCompletionsChunk)
	// validate reports output that must not be finalized.
	validate() error
	finish(final *apicompat.ChatCompletionsChunk, usage *apicompat.ChatUsage, full *apicompat.ChatCompletionsResponse)
	fail(message string)
	started() bool
	clientGone() bool
}

func newQoderSink(c *gin.Context, req *qoderRequest, model string) qoderSink {
	if !req.stream {
		return &qoderBufferedSink{c: c, req: req, model: model}
	}
	w := &qoderSSEWriter{c: c}
	switch req.protocol {
	case qoderProtocolAnthropic:
		return &qoderAnthropicStreamSink{w: w, state: apicompat.NewChatCompletionsToAnthropicStreamState(model)}
	case qoderProtocolResponses:
		state := apicompat.NewChatCompletionsToResponsesStreamState(model)
		state.CustomTools = req.customTools
		state.FunctionTools = req.functionTools
		state.ToolSearchDeclared = req.toolSearch
		state.NamespaceTools = req.namespaceTools
		return &qoderResponsesStreamSink{w: w, state: state, model: model}
	default:
		return &qoderChatStreamSink{w: w, includeUsage: req.includeUsage}
	}
}

// qoderSSEWriter commits SSE headers lazily, on the first frame.
type qoderSSEWriter struct {
	c     *gin.Context
	begun bool
	gone  bool
}

func (w *qoderSSEWriter) write(frame string) {
	if w.gone || frame == "" {
		return
	}
	if !w.begun {
		w.begun = true
		MarkResponseCommitted(w.c)
		header := w.c.Writer.Header()
		header.Set("Content-Type", "text/event-stream; charset=utf-8")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		w.c.Status(http.StatusOK)
	}
	if _, err := io.WriteString(w.c.Writer, frame); err != nil {
		w.gone = true
		return
	}
	w.c.Writer.Flush()
}

type qoderChatStreamSink struct {
	w            *qoderSSEWriter
	includeUsage bool
}

func qoderChatSSE(chunk *apicompat.ChatCompletionsChunk) string {
	sse, err := apicompat.ChatChunkToSSE(*chunk)
	if err != nil {
		return ""
	}
	return sse
}

func (s *qoderChatStreamSink) chunk(chunk *apicompat.ChatCompletionsChunk) {
	s.w.write(qoderChatSSE(chunk))
}

func (s *qoderChatStreamSink) validate() error { return nil }

func (s *qoderChatStreamSink) finish(final *apicompat.ChatCompletionsChunk, usage *apicompat.ChatUsage, _ *apicompat.ChatCompletionsResponse) {
	s.w.write(qoderChatSSE(final))
	if s.includeUsage && usage != nil {
		s.w.write(qoderChatSSE(&apicompat.ChatCompletionsChunk{
			ID:      final.ID,
			Object:  final.Object,
			Created: final.Created,
			Model:   final.Model,
			Choices: []apicompat.ChatChunkChoice{},
			Usage:   usage,
		}))
	}
	s.w.write("data: [DONE]\n\n")
}

func (s *qoderChatStreamSink) fail(message string) {
	raw, _ := json.Marshal(gin.H{"error": gin.H{"message": message, "type": "upstream_error"}})
	s.w.write("data: " + string(raw) + "\n\n")
	s.w.write("data: [DONE]\n\n")
}

func (s *qoderChatStreamSink) started() bool    { return s.w.begun }
func (s *qoderChatStreamSink) clientGone() bool { return s.w.gone }

type qoderAnthropicStreamSink struct {
	w     *qoderSSEWriter
	state *apicompat.ChatCompletionsToAnthropicStreamState
}

func (s *qoderAnthropicStreamSink) writeEvents(events []apicompat.AnthropicStreamEvent) {
	for _, event := range events {
		if sse, err := apicompat.ResponsesAnthropicEventToSSE(event); err == nil {
			s.w.write(sse)
		}
	}
}

func (s *qoderAnthropicStreamSink) chunk(chunk *apicompat.ChatCompletionsChunk) {
	s.writeEvents(apicompat.ChatCompletionsChunkToAnthropicEvents(chunk, s.state))
}

func (s *qoderAnthropicStreamSink) validate() error { return nil }

func (s *qoderAnthropicStreamSink) finish(final *apicompat.ChatCompletionsChunk, usage *apicompat.ChatUsage, _ *apicompat.ChatCompletionsResponse) {
	last := *final
	last.Usage = usage
	s.chunk(&last)
	s.writeEvents(apicompat.FinalizeChatCompletionsAnthropicStream(s.state))
}

func (s *qoderAnthropicStreamSink) fail(message string) {
	s.w.write(buildAnthropicStreamErrorSSE("api_error", message))
}

func (s *qoderAnthropicStreamSink) started() bool    { return s.w.begun }
func (s *qoderAnthropicStreamSink) clientGone() bool { return s.w.gone }

type qoderResponsesStreamSink struct {
	w     *qoderSSEWriter
	state *apicompat.ChatCompletionsToResponsesStreamState
	model string
}

func (s *qoderResponsesStreamSink) writeEvents(events []apicompat.ResponsesStreamEvent) {
	for _, event := range events {
		if sse, err := apicompat.ResponsesEventToSSE(event); err == nil {
			s.w.write(sse)
		}
	}
}

func (s *qoderResponsesStreamSink) chunk(chunk *apicompat.ChatCompletionsChunk) {
	s.writeEvents(apicompat.ChatCompletionsChunkToResponsesEvents(chunk, s.state))
}

// validate refuses to complete a response whose tool-call arguments were cut
// off: Codex would persist the broken call and replay it on the next turn.
func (s *qoderResponsesStreamSink) validate() error {
	if err := s.state.ValidateToolCallArguments(); err != nil {
		return fmt.Errorf("%w: %v", errQoderInvalidToolArg, err)
	}
	return nil
}

func (s *qoderResponsesStreamSink) finish(final *apicompat.ChatCompletionsChunk, usage *apicompat.ChatUsage, _ *apicompat.ChatCompletionsResponse) {
	last := *final
	last.Usage = usage
	s.chunk(&last)
	s.writeEvents(apicompat.FinalizeChatCompletionsResponsesStream(s.state))
	s.w.write("data: [DONE]\n\n")
}

func (s *qoderResponsesStreamSink) fail(message string) {
	raw, err := json.Marshal(gin.H{
		"type": "response.failed",
		"response": gin.H{
			"id":         s.state.ResponseID,
			"object":     "response",
			"created_at": time.Now().Unix(),
			"model":      s.model,
			"status":     "failed",
			"output":     []any{},
			"error":      gin.H{"code": "server_error", "message": message},
		},
	})
	if err != nil {
		return
	}
	s.w.write("event: response.failed\ndata: " + string(raw) + "\n\n")
}

func (s *qoderResponsesStreamSink) started() bool    { return s.w.begun }
func (s *qoderResponsesStreamSink) clientGone() bool { return s.w.gone }

// qoderBufferedSink answers non-streaming clients once the upstream finished.
type qoderBufferedSink struct {
	c       *gin.Context
	req     *qoderRequest
	model   string
	written bool
}

func (s *qoderBufferedSink) chunk(*apicompat.ChatCompletionsChunk) {}

func (s *qoderBufferedSink) validate() error { return nil }

func (s *qoderBufferedSink) finish(_ *apicompat.ChatCompletionsChunk, _ *apicompat.ChatUsage, full *apicompat.ChatCompletionsResponse) {
	s.written = true
	MarkResponseCommitted(s.c)
	switch s.req.protocol {
	case qoderProtocolAnthropic:
		s.c.JSON(http.StatusOK, apicompat.ChatCompletionsResponseToAnthropic(full, s.model))
	case qoderProtocolResponses:
		s.c.JSON(http.StatusOK, apicompat.ChatCompletionsResponseToResponses(full, s.model, s.req.customTools, s.req.functionTools, s.req.toolSearch, s.req.namespaceTools))
	default:
		s.c.JSON(http.StatusOK, full)
	}
}

func (s *qoderBufferedSink) fail(message string) {
	if s.written {
		return
	}
	s.written = true
	writeQoderClientError(s.c, s.req.protocol, http.StatusBadGateway, "upstream_error", message)
}

// Nothing reaches a non-streaming client before finish, so a failure can
// always fail over.
func (s *qoderBufferedSink) started() bool    { return s.written }
func (s *qoderBufferedSink) clientGone() bool { return false }

// qoderIdleReader cancels the upstream request when no bytes arrive within
// timeout (gateway.stream_data_interval_timeout).
type qoderIdleReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func newQoderIdleReader(r io.Reader, timeout time.Duration, onIdle func()) *qoderIdleReader {
	return &qoderIdleReader{r: r, timer: time.AfterFunc(timeout, onIdle), timeout: timeout}
}

func (ir *qoderIdleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.timeout)
	}
	return n, err
}

func (ir *qoderIdleReader) stop() {
	ir.timer.Stop()
}
