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
	"io"
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
		newJSONResponse(http.StatusBadGateway, "https://private-upstream.invalid sk-private-secret"),
		pelicanMockResponse("", "actual", false),
		pelicanMockResponse("<html>unfinished", "actual", false),
		newJSONResponse(http.StatusOK, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html></html>\"}\n\n"),
	} {
		svc, _ := pelicanMockService(pelicanMockAccount(), response)
		result := svc.runPelicanQualityCheck(context.Background(), 901, "requested")
		require.Equal(t, "failed", result.Status)
		require.Empty(t, result.Artwork)
		require.Empty(t, result.PreviewPNG)
		require.NotContains(t, result.Reason, "private")
	}
}

func TestPelicanTextModelsExcludeImagesAndUnsupportedProtocols(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "GEMINI-IMAGE", "dall-e-3", "video-1", "text-embedding-3"} {
		svc, upstream := pelicanMockService(pelicanMockAccount())
		result := svc.runPelicanQualityCheck(context.Background(), 901, model)
		require.Equal(t, "unknown", result.Status)
		require.Empty(t, upstream.requests)
	}
	account := pelicanMockAccount()
	account.Platform = PlatformGemini
	svc, upstream := pelicanMockService(account)
	require.Equal(t, "unknown", svc.runPelicanQualityCheck(context.Background(), 901, "gemini-native").Status)
	require.Empty(t, upstream.requests)
}

func TestPelicanContentCheckWorkflow(t *testing.T) {
	pass := `{"pelican":8,"bicycle":8,"pose":8,"composition":8,"errors":8}`
	fail := `{"pelican":4,"bicycle":7,"pose":7,"composition":7,"errors":7}`
	for _, tc := range []struct {
		name, status string
		review       *http.Response
	}{
		{"pass", "success", pelicanMockResponse(pass, "actual-model", false)},
		{"null", "unknown", pelicanMockResponse(strings.Replace(pass, ":8", ":null", 1), "actual-model", false)},
		{"duplicate", "unknown", pelicanMockResponse(strings.Replace(pass, `"pelican":8`, `"pelican":8,"pelican":9`, 1), "actual-model", false)},
		{"api-error", "unknown", newJSONResponse(http.StatusBadRequest, "unsupported score sk-private https://private.invalid")},
		{"stream-error", "unknown", newJSONResponse(http.StatusOK, "data: {\"type\":\"error\",\"error\":{\"message\":\"private\"}}\n\n")},
		{"empty-review", "unknown", pelicanMockResponse("", "actual-model", false)},
		{"low-score", "degraded", pelicanMockResponse(fail, "actual-model", false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, upstream := pelicanMockService(pelicanMockAccount(), pelicanMockResponse(pelicanVisualReferenceHTML, "actual-model", false), tc.review)
			account := svc.accountRepo.(*openAIAccountTestRepo).accountsByID[901]
			account.Credentials["model_mapping"] = map[string]any{"requested-model": "actual-model", "actual-model": "gpt-image-2"}
			result := svc.runPelicanQualityCheck(context.Background(), 901, "requested-model")
			require.Equal(t, tc.status, result.Status)
			require.Equal(t, "actual-model", result.ModelID)
			require.Equal(t, strings.TrimSpace(pelicanVisualReferenceHTML), result.Artwork)
			require.Empty(t, result.PreviewPNG)
			require.NotContains(t, result.Reason, "private")
			if tc.status == "unknown" {
				require.Nil(t, result.Score)
			} else {
				require.NotNil(t, result.Score)
				if tc.status == "success" {
					require.Equal(t, float64(80), *result.Score)
				} else {
					require.Equal(t, 62.5, *result.Score)
				}
			}
			require.Len(t, upstream.requests, 2)
			generation, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, DefaultScheduledTestPrompt, gjson.GetBytes(generation, "input.0.content.0.text").String())
			require.Equal(t, "high", gjson.GetBytes(generation, "reasoning.effort").String())
			body, err := io.ReadAll(upstream.requests[1].Body)
			require.NoError(t, err)
			require.NotContains(t, string(body), `"type":"input_image"`)
			require.Equal(t, "actual-model", gjson.GetBytes(body, "model").String())
			require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
			require.Equal(t, pelicanContentReviewPrompt, gjson.GetBytes(body, "instructions").String())
			require.Equal(t, strings.TrimSpace(pelicanVisualReferenceHTML), gjson.Get(gjson.GetBytes(body, "input.0.content.0.text").String(), "artwork_html").String())
		})
	}
}

func TestPelicanContentCheckDoesNotRequireRendering(t *testing.T) {
	t.Setenv("SUB2API_QUALITY_RENDERER_SCRIPT", filepath.Join(t.TempDir(), "missing.mjs"))
	pass := `{"pelican":8,"bicycle":8,"pose":8,"composition":8,"errors":8}`
	svc, upstream := pelicanMockService(pelicanMockAccount(), pelicanMockResponse(pelicanVisualReferenceHTML, "actual", false), pelicanMockResponse(pass, "actual", false))
	result := svc.runPelicanQualityCheck(context.Background(), 901, "model")
	require.Equal(t, "success", result.Status)
	require.NotNil(t, result.Score)
	require.Equal(t, float64(80), *result.Score)
	require.NotEmpty(t, result.Artwork)
	require.Empty(t, result.PreviewPNG)
	require.Len(t, upstream.requests, 2)
}

func TestPelicanCNChatAndAdaptiveUseIndependentVisualRequests(t *testing.T) {
	frames, _ := pelicanTestFrames(t)
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolAdaptive, APIProtocolResponses} {
		account := adaptiveCNAccountTestAccount(901, PlatformKimi)
		account.Credentials["api_protocol"] = protocol
		account.Credentials["base_url"] = "http://cn-test.example/v1"
		if protocol == APIProtocolResponses {
			account.Extra = map[string]any{"openai_responses_supported": false}
		}
		chat := protocol != APIProtocolResponses
		pass := `{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":true}}`
		svc, upstream := pelicanMockService(account, pelicanMockResponse(pass, "actual-cn-model", chat))
		status, _ := svc.assessPelicanFrames(context.Background(), 901, "model", frames[0], frames)
		require.Equal(t, "success", status, protocol)
		require.Len(t, upstream.requests, 1)
		body, err := io.ReadAll(upstream.requests[0].Body)
		require.NoError(t, err)
		kind := "input_image"
		if chat {
			kind = "image_url"
		}
		require.Equal(t, 5, strings.Count(string(body), `"type":"`+kind+`"`))
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

func TestPelicanVerdictRejectsUnboundedOrAmbiguousJSON(t *testing.T) {
	for _, verdict := range []string{
		"", `{"checks":{}}`, `{"checks":{"pelican":true,"pelican":false,"bicycle":true,"riding":true,"motion":true}}`,
		`{"checks":{"pelican":"true","bicycle":true,"riding":true,"motion":true}}`,
		`{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":true},"reason":"approve"}`,
		strings.Repeat("x", pelicanReviewLimit+1),
	} {
		status, _ := parsePelicanVisualReview(verdict)
		require.Equal(t, "unknown", status)
	}
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

func TestPelicanIncompleteVisualStreamIsUnknown(t *testing.T) {
	frames, _ := pelicanTestFrames(t)
	pass := `{"checks":{"pelican":true,"bicycle":true,"riding":true,"motion":true}}`
	for _, chat := range []bool{false, true} {
		response := pelicanMockResponse(pass, "model", chat)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		if chat {
			body = bytes.Replace(body, []byte(`"finish_reason":"stop"`), []byte(`"finish_reason":"length"`), 1)
		} else {
			body = bytes.Replace(body, []byte(`"response":{"model":"model"}`), []byte(`"response":{"model":"model","status":"incomplete"}`), 1)
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		account := pelicanMockAccount()
		if chat {
			account = adaptiveCNAccountTestAccount(901, PlatformKimi)
			account.Credentials["api_protocol"] = APIProtocolChatCompletions
		}
		svc, upstream := pelicanMockService(account, response)
		status, _ := svc.assessPelicanFrames(context.Background(), 901, "model", frames[0], frames)
		require.Equal(t, "unknown", status)
		require.Len(t, upstream.requests, 1)
	}
}

func TestPelicanDefaultModelMappingCannotGenerateImages(t *testing.T) {
	account := pelicanMockAccount()
	account.Credentials["model_mapping"] = map[string]any{"*": "gpt-image-2"}
	svc, upstream := pelicanMockService(account)
	result := svc.runPelicanQualityCheck(context.Background(), 901, "")
	require.NotEqual(t, "success", result.Status)
	require.Empty(t, upstream.requests)
	require.Empty(t, result.Artwork)
}
