package service

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v4/host"
)

// Match html_studio.py's successful Chat Completions generation request.
func applyPelicanQualityPayload(ctx context.Context, payload map[string]any, responses bool) {
	if !isPelicanQualityRequest(ctx) || responses {
		return
	}
	messages, _ := payload["messages"].([]map[string]any)
	payload["messages"] = append([]map[string]any{
		{"role": "system", "content": "You are a helpful assistant."},
	}, messages...)
	payload["max_completion_tokens"] = 65536
	payload["store"] = false
	payload["stream_options"] = map[string]any{"include_usage": true}
	delete(payload, "reasoning_effort")
	delete(payload, "reasoning")
}

// Use the same platform/architecture spelling as PI's Node/Python clients.
// Account-specific and official-provider header overrides still take priority.
var pelicanQualityUserAgent = sync.OnceValue(func() string {
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	release, err := host.KernelVersion()
	if err != nil || strings.TrimSpace(release) == "" {
		release = "unknown"
	}
	return fmt.Sprintf("pi (%s %s; %s)", platform, strings.TrimSpace(release), arch)
})
