//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func pelicanMockService(account *Account, responses ...*http.Response) (*AccountTestService, *queuedHTTPUpstream) {
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	upstream := &queuedHTTPUpstream{responses: responses}
	return &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: rawChatCompletionsTestConfig()}, upstream
}

func pelicanMockAccount() *Account {
	return &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "mock-secret"}}
}

func pelicanMockResponse(text, model string, chat bool) *http.Response {
	var stream string
	if chat {
		data, _ := json.Marshal(map[string]any{"model": model, "choices": []any{map[string]any{"delta": map[string]any{"content": text}, "finish_reason": "stop"}}})
		stream = "data: " + string(data) + "\n\ndata: [DONE]\n\n"
	} else {
		delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": text})
		completed, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"model": model}})
		stream = "data: " + string(delta) + "\n\ndata: " + string(completed) + "\n\n"
	}
	return newJSONResponse(http.StatusOK, stream)
}

func pelicanTestFrames(t *testing.T) ([]pelicanVisualFrame, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 960, 640))
	img.Set(50, 50, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	frames := make([]pelicanVisualFrame, 4)
	for i := range frames {
		frames[i] = pelicanVisualFrame{Time: float64(i) / 2, PNG: base64.StdEncoding.EncodeToString(buf.Bytes())}
	}
	return frames, append([]byte(nil), buf.Bytes()...)
}

func pelicanMockRenderer(t *testing.T) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the local renderer protocol integration test")
	}
	frames, preview := pelicanTestFrames(t)
	encoded, err := json.Marshal(frames)
	require.NoError(t, err)
	literal, err := json.Marshal(string(encoded))
	require.NoError(t, err)
	script := filepath.Join(t.TempDir(), "renderer.mjs")
	require.NoError(t, os.WriteFile(script, []byte("process.stdin.resume();process.stdin.on('end',()=>process.stdout.write("+string(literal)+"));"), 0600))
	t.Setenv("SUB2API_QUALITY_RENDERER_NODE", node)
	t.Setenv("SUB2API_QUALITY_RENDERER_SCRIPT", script)
	pelicanReferenceCache.Lock()
	previous := pelicanReferenceCache.frame
	pelicanReferenceCache.frame = nil
	pelicanReferenceCache.Unlock()
	t.Cleanup(func() {
		pelicanReferenceCache.Lock()
		pelicanReferenceCache.frame = previous
		pelicanReferenceCache.Unlock()
	})
	return preview
}

func TestPelicanResponsesPromptCompatibility(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		for _, args := range [][]string{nil, {""}, {"  custom prompt  "}, {DefaultScheduledTestPrompt}} {
			payload := createOpenAITestPayload("model", oauth, args...)
			encoded, err := json.Marshal(payload)
			require.NoError(t, err)
			want := "hi"
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				want = strings.TrimSpace(args[0])
			}
			require.Equal(t, want, gjson.GetBytes(encoded, "input.0.content.0.text").String())
			require.Equal(t, oauth, gjson.GetBytes(encoded, "store").Exists())
		}
	}
	probe, _ := json.Marshal(createOpenAIProbePayload("model", false))
	require.Equal(t, ".", gjson.GetBytes(probe, "input").String())
	require.Equal(t, int64(5), gjson.GetBytes(probe, "max_output_tokens").Int())
}

func TestPelicanGenerationFailureHasNoArtworkAndSafeReason(t *testing.T) {
	for _, response := range []*http.Response{
		newJSONResponse(http.StatusBadRequest, "https://private-upstream.invalid sk-private-secret"),
		pelicanMockResponse("", "actual", true),
		pelicanMockResponse("<html>unfinished", "actual", true),
	} {
		svc, _ := pelicanMockService(pelicanChatAccount(), response)
		result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
		require.Equal(t, "failed", result.Status)
		require.Empty(t, result.Artwork)
		require.Empty(t, result.PreviewPNG)
		require.NotContains(t, result.Reason, "private")
	}
}

func TestPelicanQualityTriesConfiguredModelsWithoutNameHeuristics(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "GEMINI-IMAGE", "dall-e-3", "video-1", "text-embedding-3", "custom-tts-model"} {
		svc, upstream := pelicanMockService(pelicanChatAccount(), pelicanMockResponse("<html>ok</html>", model, true))
		result := svc.runPelicanQualityCheck(context.Background(), 901, model)
		require.Equal(t, "success", result.Status, result.Reason)
		require.Len(t, upstream.requests, 1)
		require.Equal(t, model, result.ModelID)
	}
}

func TestPelicanFrameParsingAndPreviewLimits(t *testing.T) {
	frames, preview := pelicanTestFrames(t)
	encoded, err := json.Marshal(frames)
	require.NoError(t, err)
	parsed, err := parsePelicanVisualFrames(encoded)
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(parsed[0].PNG)
	require.NoError(t, err)
	require.Equal(t, preview, decoded)
	for _, mutate := range []func([]pelicanVisualFrame) []pelicanVisualFrame{
		func(f []pelicanVisualFrame) []pelicanVisualFrame { return f[:3] },
		func(f []pelicanVisualFrame) []pelicanVisualFrame { f[0].Time = 0.1; return f },
		func(f []pelicanVisualFrame) []pelicanVisualFrame { f[2].Time = f[1].Time; return f },
		func(f []pelicanVisualFrame) []pelicanVisualFrame { f[0].PNG = "not-base64"; return f },
		func(f []pelicanVisualFrame) []pelicanVisualFrame {
			f[0].PNG = base64.StdEncoding.EncodeToString(make([]byte, pelicanPNGMaxBytes+1))
			return f
		},
		func(f []pelicanVisualFrame) []pelicanVisualFrame {
			data, _ := base64.StdEncoding.DecodeString(f[0].PNG)
			f[0].PNG = base64.StdEncoding.EncodeToString(data[:len(data)-12])
			return f
		},
	} {
		copyFrames := append([]pelicanVisualFrame(nil), frames...)
		data, err := json.Marshal(mutate(copyFrames))
		require.NoError(t, err)
		_, err = parsePelicanVisualFrames(data)
		require.Error(t, err)
	}
	_, err = parsePelicanVisualFrames(append(encoded, []byte(" true")...))
	require.Error(t, err)
}

func TestPelicanRendererAutodiscovery(t *testing.T) {
	t.Setenv("SUB2API_QUALITY_RENDERER_SCRIPT", "")
	script, err := pelicanRendererScript()
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(script))
	require.Equal(t, "render.mjs", filepath.Base(script))
	t.Setenv("SUB2API_QUALITY_RENDERER_SCRIPT", "relative.mjs")
	_, err = pelicanRendererScript()
	require.Error(t, err)
}

func TestPelicanDefaultModelMappingIsActuallyRequested(t *testing.T) {
	account := pelicanChatAccount()
	account.Credentials["model_mapping"] = map[string]any{"*": "gpt-image-2"}
	svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>ok</html>", "actual", true))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "")
	require.Equal(t, "success", result.Status, result.Reason)
	require.Len(t, upstream.requests, 1)
	requirePelicanStudioRequest(t, upstream.requests[0], "gpt-image-2", DefaultScheduledTestPrompt)
}
