package migrations

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestTypeSafePlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("265_platform_catalog_constraints.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	require.NotContains(t, sql, "ADD CONSTRAINT")
	for _, platform := range []string{domain.PlatformTypeSafe, domain.PlatformCommandCode, domain.PlatformCline, domain.PlatformCodeBuddy, domain.PlatformQoder} {
		require.True(t, domain.IsConcretePlatform(platform), "platform %s must be registered", platform)
	}
	for _, name := range []string{"241_add_typesafe_platform.sql", "242_drop_platform_check_constraints.sql"} {
		_, err := FS.ReadFile(name)
		require.ErrorIs(t, err, fs.ErrNotExist, "unreleased intermediate platform migration must remain folded into 265")
	}
}
