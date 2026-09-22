package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQoderPlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("259_add_qoder_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "user_platform_quotas_platform_check")
	require.Contains(t, sql, "composite_model_routes_target_platform_check")
	require.Contains(t, sql, "'qoder'")
	require.Contains(t, sql, "'cursor'")
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go', 'cursor_sand', 'cursor', 'qoder'))")
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go', 'cursor_sand', 'cursor', 'qoder'))")
}
