package service

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

//go:embed pelican_quality_visual_reference.html
var pelicanVisualReferenceHTML string

const (
	pelicanDocumentLimit     = 1 << 20
	pelicanPNGMaxBytes       = 2 << 20
	pelicanRenderOutputLimit = 12 << 20
)

type pelicanVisualFrame struct {
	Time float64 `json:"time"`
	PNG  string  `json:"png"`
}

type pelicanQualityContextKey struct{}

func isPelicanQualityRequest(ctx context.Context) bool {
	return ctx.Value(pelicanQualityContextKey{}) != nil
}

func pelicanQualityStreamReader(ctx context.Context, body io.Reader) io.Reader {
	if !isPelicanQualityRequest(ctx) {
		return body
	}
	// Reasoning and usage events can make the stream larger than the visible HTML.
	return io.LimitReader(body, int64(4*pelicanDocumentLimit))
}

func (s *AccountTestService) emitPelicanResolvedModel(c *gin.Context, data map[string]any) {
	if !isPelicanQualityRequest(c.Request.Context()) {
		return
	}
	model, _ := data["model"].(string)
	if response, ok := data["response"].(map[string]any); ok {
		if resolved, ok := response["model"].(string); ok && resolved != "" {
			model = resolved
		}
	}
	if model != "" {
		s.sendEvent(c, TestEvent{Type: "status", Model: model})
	}
}

// Capture only the current request. No earlier scheduled artwork is consulted.
type pelicanCapture struct {
	header http.Header
	bytes.Buffer
	limit    int
	overflow bool
}

func (w *pelicanCapture) Header() http.Header { return w.header }
func (w *pelicanCapture) WriteHeader(int)     {}
func (w *pelicanCapture) Flush()              {}
func (w *pelicanCapture) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		w.overflow = true
		return 0, errors.New("capture limit")
	}
	return w.Buffer.Write(p)
}

type pelicanTestOutput struct {
	Text, Model       string
	Complete, Failed  bool
	ReceivedTextBytes int
	ReceivedEvents    int
	LastEventType     string
	CaptureOverflow   bool
	FailureCode       string
	DeadlineSource    string
}

func parsePelicanTestEvents(raw []byte, limit int) (out pelicanTestOutput, err error) {
	var text strings.Builder
	defer func() { out.Text = text.String() }()
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 2*pelicanDocumentLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event TestEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
			return out, errors.New("invalid test event")
		}
		out.ReceivedEvents++
		switch event.Type {
		case "test_start", "content", "status", "test_complete", "error", "image", "video", "audio":
			out.LastEventType = event.Type
		default:
			out.LastEventType = "other"
		}
		if event.Model != "" {
			out.Model = event.Model
		}
		switch event.Type {
		case "content":
			out.ReceivedTextBytes += len(event.Text)
			if len(event.Text) > limit-text.Len() {
				prefix := event.Text[:limit-text.Len()]
				for len(prefix) > 0 && !utf8.ValidString(prefix) {
					prefix = prefix[:len(prefix)-1]
				}
				_, _ = text.WriteString(prefix)
				out.CaptureOverflow = true
				return out, errors.New("test output limit")
			}
			_, _ = text.WriteString(event.Text)
		case "error", "image", "video", "audio":
			out.Failed = true
		case "test_complete":
			out.Complete = event.Success
			if !event.Success {
				out.Failed = true
			}
		}
	}
	out.Text = text.String()
	if err := scanner.Err(); err != nil {
		return out, errors.New("invalid test stream")
	}
	if out.Failed || !out.Complete {
		return out, errors.New("test did not complete")
	}
	return out, nil
}

// Only fixed local classifications may leave the quality request boundary.
func pelicanRequestFailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Artwork generation timed out before completion"
	}
	if errors.Is(err, context.Canceled) {
		return "Artwork generation was canceled before completion"
	}
	if err != nil {
		message := err.Error()
		switch {
		case strings.HasPrefix(message, "Chat Completions stream from /v1/chat/completions ended before [DONE]"):
			return "Artwork generation stream ended without a completion event"
		case strings.HasPrefix(message, "Chat Completions stream read error"):
			return "Artwork generation response was incomplete or truncated"
		case strings.HasPrefix(message, "Invalid Chat Completions response"):
			return "Artwork generation returned an invalid response stream"
		case strings.HasPrefix(message, "Chat Completions API (/v1/chat/completions) request failed:"):
			return "Artwork generation response was incomplete or truncated"
		case strings.HasPrefix(message, "Chat Completions API (/v1/chat/completions) returned 5"),
			strings.HasPrefix(message, "Chat Completions API (/v1/chat/completions) returned 429:"),
			message == pelicanRetryableStreamErrorMessage:
			return "Artwork generation was rejected or failed upstream"
		case message == "Quality generation requires a Chat Completions API key account":
			return message
		}
		switch err.Error() {
		case "Stream ended before response.completed":
			return "Artwork generation stream ended without a completion event"
		case "Quality response did not complete", "Quality response did not complete normally":
			return "Artwork generation response was incomplete or truncated"
		case "Invalid quality response stream", "invalid test event", "invalid test stream":
			return "Artwork generation returned an invalid response stream"
		case "test output limit":
			return "Artwork generation output exceeded the size limit"
		case "OpenAI response failed":
			return "Artwork generation was rejected or failed upstream"
		}
	}
	return "Artwork generation failed or did not complete"
}

const pelicanRetryableStreamErrorMessage = "Chat Completions API (/v1/chat/completions) retryable upstream error"

func pelicanRetryableStreamError(data map[string]any) bool {
	errorType, _ := data["type"].(string)
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "upstream_error", "server_error", "overloaded_error", "rate_limit_error":
		return true
	default:
		return false
	}
}

const (
	pelicanGenerationTimeout    = 15 * time.Minute
	pelicanGenerationRetryLimit = 5
)

func (s *AccountTestService) runPelicanRequest(ctx context.Context, accountID int64, modelID, prompt string, limit int) (pelicanTestOutput, error) {
	parentCtx := ctx
	timeout := pelicanGenerationTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last pelicanTestOutput
	var lastErr error
	for retry := 0; retry <= pelicanGenerationRetryLimit; retry++ {
		if err := ctx.Err(); err != nil {
			last.FailureCode = pelicanRequestFailureCode(err)
			last.DeadlineSource = "generation_request"
			if parentCtx.Err() != nil {
				last.DeadlineSource = "parent_context"
			}
			return last, errors.New(pelicanRequestFailureReason(err))
		}
		if retry > 0 {
			if err := pelicanRetryWait(ctx, retry); err != nil {
				last.FailureCode = pelicanRequestFailureCode(err)
				last.DeadlineSource = "generation_request"
				if parentCtx.Err() != nil {
					last.DeadlineSource = "parent_context"
				}
				return last, errors.New(pelicanRequestFailureReason(err))
			}
		}
		w := &pelicanCapture{header: make(http.Header), limit: 4*limit + (64 << 10)}
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/internal/pelican-quality", nil)
		requestErr := s.TestAccountConnection(c, accountID, modelID, prompt, "")
		out, parseErr := parsePelicanTestEvents(w.Bytes(), limit)
		out.CaptureOverflow = out.CaptureOverflow || w.overflow
		failure := parseErr
		if requestErr != nil {
			failure = requestErr
		}
		if ctx.Err() != nil {
			failure = ctx.Err()
			out.DeadlineSource = "generation_request"
			if parentCtx.Err() != nil {
				out.DeadlineSource = "parent_context"
			}
		}
		if w.overflow {
			failure = errors.New("test output limit")
		}
		if failure == nil {
			return out, nil
		}
		out.FailureCode = pelicanRequestFailureCode(failure)
		last, lastErr = out, failure
		if !pelicanRetryableFailure(out.FailureCode) || retry == pelicanGenerationRetryLimit {
			return out, errors.New(pelicanRequestFailureReason(failure))
		}
	}
	return last, errors.New(pelicanRequestFailureReason(lastErr))
}

func pelicanRetryableFailure(code string) bool {
	switch code {
	case "missing_completion", "incomplete_stream", "invalid_stream", "upstream_failure":
		return true
	default:
		return false
	}
}

func pelicanRetryWait(ctx context.Context, retry int) error {
	delay := time.Duration(120*(1<<(retry-1))) * time.Millisecond
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pelicanTextModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return !isOpenAIImageModel(model) && !strings.Contains(model, "image") && !strings.HasPrefix(model, "dall-e") && !strings.Contains(model, "video") && !strings.Contains(model, "tts") && !strings.Contains(model, "embedding")
}

func pelicanVisualProtocolSupported(account *Account) bool {
	if account == nil || account.IsSyntheticUITest() {
		return false
	}
	if account.IsOpenAI() {
		return true
	}
	return account.IsCNProvider() && (account.GetAPIProtocol() == APIProtocolResponses || account.GetAPIProtocol() == APIProtocolChatCompletions || account.GetAPIProtocol() == APIProtocolAdaptive)
}

func pelicanHTML(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 && strings.HasSuffix(text, "```") {
			text = strings.TrimSpace(text[newline+1 : len(text)-3])
		}
	}
	lower := strings.ToLower(text)
	start := strings.Index(lower, "<html")
	end := strings.LastIndex(lower, "</html>")
	if start < 0 || end < start || len(text) > pelicanDocumentLimit {
		return ""
	}
	if doctype := strings.Index(lower, "<!doctype html>"); doctype >= 0 && doctype < start {
		start = doctype
	}
	return text[start : end+len("</html>")]
}

func (s *AccountTestService) runPelicanQualityCheck(ctx context.Context, accountID int64, modelID string) (result qualityProbeResult) {
	started := time.Now()
	result = qualityProbeResult{Status: "unknown", ModelID: modelID, Diagnostics: &GroupQualityDiagnostics{Stage: "account_validation", GenerationTimeoutSeconds: int(pelicanGenerationTimeout / time.Second)}}
	defer func() { result.Diagnostics.TotalDurationMs = time.Since(started).Milliseconds() }()
	if s == nil || s.accountRepo == nil {
		result.Reason = "Quality evaluator unavailable"
		return result
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		result.Status = "failed"
		result.Reason = "Account unavailable for quality generation"
		return result
	}
	if !pelicanVisualProtocolSupported(account) || !pelicanTextModel(account.GetMappedModel(modelID)) {
		result.Reason = "Quality evaluation requires a supported text protocol"
		return result
	}
	switch protocol := account.GetAPIProtocol(); protocol {
	case APIProtocolResponses, APIProtocolChatCompletions, APIProtocolAdaptive:
		result.Diagnostics.ConfiguredProtocol = protocol
	default:
		result.Diagnostics.ConfiguredProtocol = "platform_default"
	}
	result.Diagnostics.Stage = "generation"
	generationStarted := time.Now()
	generationCtx := context.WithValue(ctx, pelicanQualityContextKey{}, true)
	generated, err := s.runPelicanRequest(generationCtx, accountID, modelID, DefaultScheduledTestPrompt, pelicanDocumentLimit)
	result.Diagnostics.GenerationDurationMs = time.Since(generationStarted).Milliseconds()
	result.Diagnostics.ReceivedTextBytes = generated.ReceivedTextBytes
	result.Diagnostics.ReceivedEvents = generated.ReceivedEvents
	result.Diagnostics.LastEventType = generated.LastEventType
	result.Diagnostics.CompletionReceived = generated.Complete
	result.Diagnostics.CaptureOverflow = generated.CaptureOverflow
	result.Diagnostics.FailureCode = generated.FailureCode
	result.Diagnostics.DeadlineSource = generated.DeadlineSource
	result.RawOutput = generated.Text
	if generated.Model != "" {
		result.ModelID = generated.Model
	}
	if err != nil {
		result.Status = "failed"
		result.Reason = err.Error()
		return result
	}
	document := pelicanHTML(generated.Text)
	if document == "" {
		result.Status = "failed"
		result.Reason = "Artwork generation did not return a complete HTML document"
		return result
	}
	result.Artwork = document
	result.Diagnostics.ArtworkAvailable = true
	result.Diagnostics.Stage = "completed"
	result.Status = "success"
	result.Reason = ""
	return result
}

var pelicanRenderSlot = make(chan struct{}, 1)
var pelicanReferenceCache struct {
	sync.Mutex
	frame *pelicanVisualFrame
}

func pelicanReferenceFrame(ctx context.Context) (pelicanVisualFrame, error) {
	pelicanReferenceCache.Lock()
	cached := pelicanReferenceCache.frame
	pelicanReferenceCache.Unlock()
	if cached != nil {
		return *cached, nil
	}
	frames, err := renderPelicanVisualFrames(ctx, pelicanVisualReferenceHTML)
	if err != nil {
		return pelicanVisualFrame{}, err
	}
	pelicanReferenceCache.Lock()
	pelicanReferenceCache.frame = &frames[0]
	pelicanReferenceCache.Unlock()
	return frames[0], nil
}

func pelicanRendererScript() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("SUB2API_QUALITY_RENDERER_SCRIPT")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("renderer path must be absolute")
		}
		return configured, nil
	}
	var roots []string
	if executable, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(executable))
	}
	if cwd, err := os.Getwd(); err == nil {
		for i := 0; i < 5; i++ {
			roots = append(roots, cwd)
			parent := filepath.Dir(cwd)
			if parent == cwd {
				break
			}
			cwd = parent
		}
	}
	for _, root := range roots {
		for _, relative := range []string{"scripts/quality-renderer/render.mjs", "backend/scripts/quality-renderer/render.mjs"} {
			path := filepath.Join(root, filepath.FromSlash(relative))
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", errors.New("renderer resource unavailable")
}

func renderPelicanVisualFrames(ctx context.Context, document string) ([]pelicanVisualFrame, error) {
	if strings.TrimSpace(document) == "" || len(document) > pelicanDocumentLimit {
		return nil, errors.New("invalid artwork size")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case pelicanRenderSlot <- struct{}{}:
		defer func() { <-pelicanRenderSlot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	script, err := pelicanRendererScript()
	if err != nil {
		return nil, err
	}
	runner := strings.TrimSpace(os.Getenv("SUB2API_QUALITY_RENDERER_NODE"))
	if runner == "" {
		runner = "node"
	}
	// Operator-controlled executable/resource only; model content enters stdin, with no shell.
	cmd := exec.CommandContext(ctx, runner, script) // #nosec G702 -- trusted deployment paths, never request or model input
	cmd.Stdin = strings.NewReader(document)
	output := &pelicanCapture{limit: pelicanRenderOutputLimit}
	cmd.Stdout = output
	stderr := &pelicanCapture{limit: 16 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil || output.overflow {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) || errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("renderer executable unavailable")
		}
		if output.overflow {
			return nil, errors.New("renderer output too large")
		}
		return nil, errors.New(classifyPelicanRendererStderr(stderr.String()))
	}
	return parsePelicanVisualFrames(output.Bytes())
}

func parsePelicanVisualFrames(output []byte) ([]pelicanVisualFrame, error) {
	if len(output) > pelicanRenderOutputLimit {
		return nil, errors.New("renderer output too large")
	}
	var frames []pelicanVisualFrame
	d := json.NewDecoder(bytes.NewReader(output))
	d.DisallowUnknownFields()
	if err := d.Decode(&frames); err != nil || len(frames) != 4 {
		return nil, errors.New("invalid renderer frame count")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing renderer content")
	}
	previous := -1.0
	for i, frame := range frames {
		if math.IsNaN(frame.Time) || math.IsInf(frame.Time, 0) || frame.Time <= previous || frame.Time < 0 || frame.Time > 10 || (i == 0 && frame.Time != 0) {
			return nil, errors.New("invalid frame time")
		}
		previous = frame.Time
		if len(frame.PNG) > base64.StdEncoding.EncodedLen(pelicanPNGMaxBytes) {
			return nil, errors.New("frame PNG too large")
		}
		data, err := base64.StdEncoding.DecodeString(frame.PNG)
		if err != nil || len(data) > pelicanPNGMaxBytes {
			return nil, errors.New("invalid PNG encoding")
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != 960 || config.Height != 640 {
			return nil, errors.New("invalid frame dimensions")
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			return nil, errors.New("invalid frame PNG")
		}
	}
	return frames, nil
}
