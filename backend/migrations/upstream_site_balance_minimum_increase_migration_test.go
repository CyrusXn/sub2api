package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteBalanceMinimumIncreaseMigrationClearsOnlySmallActiveReminders(t *testing.T) {
	content, err := FS.ReadFile("272_upstream_site_balance_minimum_increase.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "UPDATE upstream_site_balance_observations")
	require.Contains(t, sql, "alert_active = FALSE")
	for _, column := range []string{"alert_old_balance", "alert_new_balance", "alert_delta", "alert_detected_at"} {
		require.Contains(t, sql, column+" = NULL")
	}
	require.Contains(t, sql, "alert_version = alert_version + 1")
	require.Contains(t, sql, "WHERE alert_active = TRUE AND alert_delta < 1")
	require.NotRegexp(t, `(?:SET|,) balance = NULL`, sql)
	require.NotContains(t, sql, "alert_currency = NULL")
}
