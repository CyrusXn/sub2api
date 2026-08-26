package migrations

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamCostRateSnapshotMigrationRebuildsEveryAvailableBusinessDay(t *testing.T) {
	content, err := os.ReadFile("237_fix_upstream_cost_rate_snapshot.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "UPDATE dashboard_business_daily d")
	require.NotContains(t, sql, "d.finalized_at IS NULL", "有使用明细的历史日期必须回填正确上游成本")
}
