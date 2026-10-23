package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessProfitFallbackIsIndependentFromAssetSnapshots(t *testing.T) {
	content, err := FS.ReadFile("259_dashboard_business_profit_fallback.sql")
	require.NoError(t, err)
	require.Contains(t, string(content), "CREATE TABLE IF NOT EXISTS dashboard_business_usage_frozen")
	require.Contains(t, string(content), "bucket_date DATE PRIMARY KEY")
	require.NotContains(t, string(content), "UPDATE dashboard_business_asset_daily")
}
