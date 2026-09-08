package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeySmartRoutingMigration(t *testing.T) {
	content, err := FS.ReadFile("252_api_key_smart_routing.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "route_mode IN ('cheapest', 'fastest')")
	require.Contains(t, sql, "SET route_mode = 'smart'")
	require.Contains(t, sql, "DELETE FROM api_key_group_preferences WHERE disabled")
	require.Contains(t, sql, "DROP COLUMN IF EXISTS natural_revert_enabled")
	require.Contains(t, sql, "user_allowed_groups")
	require.Contains(t, sql, "user_group_rate_multipliers")
}
