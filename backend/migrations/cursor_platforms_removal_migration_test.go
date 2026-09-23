package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursorPlatformsRemovalMigration(t *testing.T) {
	content, err := FS.ReadFile("262_remove_cursor_platforms.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 遗留账号只停用、不删除。
	require.Contains(t, sql, "UPDATE accounts SET status = 'error', schedulable = FALSE,")
	require.Contains(t, sql, "WHERE platform IN ('cursor_sand', 'cursor') AND deleted_at IS NULL;")
	require.NotContains(t, sql, "DELETE FROM accounts")

	// 约束重建前必须先清掉引用已下线平台的行，否则 ADD CONSTRAINT 会失败。
	deleteQuotas := strings.Index(sql, "DELETE FROM user_platform_quotas WHERE platform IN ('cursor_sand', 'cursor');")
	deleteRoutes := strings.Index(sql, "DELETE FROM composite_model_routes WHERE target_platform IN ('cursor_sand', 'cursor');")
	addQuotaCheck := strings.Index(sql, "ADD CONSTRAINT user_platform_quotas_platform_check")
	addRouteCheck := strings.Index(sql, "ADD CONSTRAINT composite_model_routes_target_platform_check")
	require.GreaterOrEqual(t, deleteQuotas, 0)
	require.GreaterOrEqual(t, deleteRoutes, 0)
	require.Greater(t, addQuotaCheck, deleteQuotas)
	require.Greater(t, addRouteCheck, deleteRoutes)

	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go', 'qoder'))")
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go', 'qoder'))")

	// 新约束里不能再出现已下线的平台。
	checks := sql[addQuotaCheck:]
	require.NotContains(t, checks, "'cursor_sand'")
	require.NotContains(t, checks, "'cursor'")
}
