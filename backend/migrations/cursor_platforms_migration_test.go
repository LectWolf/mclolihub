package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursorPlatformsMigration(t *testing.T) {
	content, err := FS.ReadFile("258_add_cursor_platforms.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "user_platform_quotas_platform_check")
	require.Contains(t, sql, "composite_model_routes_target_platform_check")
	require.Contains(t, sql, "'cursor_sand'")
	require.Contains(t, sql, "'cursor'")
	require.Contains(t, sql, "'codebuddy'")
	require.Contains(t, sql, "'opencode_go'")
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go', 'cursor_sand', 'cursor'))")
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go', 'cursor_sand', 'cursor'))")
}
