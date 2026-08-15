package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardBusinessRollupMigrationKeepsPermanentDailyMetrics(t *testing.T) {
	content, err := FS.ReadFile("231_add_dashboard_business_and_host_metrics.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS dashboard_business_daily")
	require.Contains(t, sql, "recharge_amount DECIMAL(20, 10) NOT NULL DEFAULT 0")
	require.Contains(t, sql, "admin_actual_cost DECIMAL(20, 10) NOT NULL DEFAULT 0")
	require.Contains(t, sql, "admin_account_cost DECIMAL(20, 10) NOT NULL DEFAULT 0")
	require.Contains(t, sql, "finalized_at TIMESTAMPTZ")
	require.Contains(t, sql, "idx_redeem_codes_business_rollup_used_at")
	require.Contains(t, sql, "INSERT INTO dashboard_business_daily")
	require.Contains(t, sql, "value > 1")
	require.Contains(t, sql, "admin@example.com")
	require.NotContains(t, sql, "DELETE FROM dashboard_business_daily")

	for _, column := range []string{
		"resource_source",
		"network_receive_bytes_per_second",
		"network_transmit_bytes_per_second",
		"disk_used_bytes",
		"disk_total_bytes",
		"disk_usage_percent",
	} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+column)
	}
}
