package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDashboardBusinessSummaryReadsPermanentDailyRollup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	totalColumns := []string{
		"recharge_amount", "total_requests", "input_tokens", "output_tokens",
		"cache_creation_tokens", "cache_read_tokens", "total_cost", "actual_cost",
		"account_cost", "admin_actual_cost", "admin_account_cost", "upstream_cost", "admin_upstream_cost",
	}
	mock.ExpectQuery(`(?s)FROM dashboard_business_daily`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows(append(totalColumns, totalColumns...)).AddRow(
			100.0, int64(20), int64(100), int64(50), int64(10), int64(5), 8.0, 12.0, 3.0, 2.0, 0.5, 4.0, 0.7,
			40.0, int64(8), int64(40), int64(20), int64(4), int64(2), 3.0, 5.0, 1.0, 1.0, 0.2, 2.0, 0.3,
		))
	mock.ExpectQuery(`(?s)FROM dashboard_business_daily`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"bucket_date", "recharge_amount", "total_requests", "total_tokens",
			"actual_cost", "actual_cost_excluding_admin", "account_cost", "account_cost_excluding_admin", "upstream_cost", "upstream_cost_excluding_admin",
		}).AddRow(time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), 10.0, int64(2), int64(30), 2.0, 1.5, 0.6, 0.5, 0.8, 0.6))
	mock.ExpectClose()

	summary, err := repo.GetDashboardBusinessSummary(context.Background(), start, end)
	require.NoError(t, err)
	require.Equal(t, float64(100), summary.Lifetime.RechargeAmount)
	require.Equal(t, float64(10), summary.Lifetime.ActualCostExcludingAdmin)
	require.Equal(t, int64(165), summary.Lifetime.TotalTokens)
	require.Equal(t, float64(40), summary.Range.RechargeAmount)
	require.Equal(t, float64(4), summary.Lifetime.UpstreamCost)
	require.Equal(t, float64(3.3), summary.Lifetime.UpstreamCostExcludingAdmin)
	require.Equal(t, float64(2), summary.Range.UpstreamCost)
	require.Equal(t, float64(1.7), summary.Range.UpstreamCostExcludingAdmin)
	require.Len(t, summary.Daily, 1)
	require.Equal(t, float64(0.8), summary.Daily[0].UpstreamCost)
	require.Equal(t, float64(0.6), summary.Daily[0].UpstreamCostExcludingAdmin)
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardLast24HourUsageUsesHourlyBucketsAndExactBoundaryDetails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 13, 15, 37, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	mock.ExpectQuery(`(?s)WITH bounds AS .*FROM usage_dashboard_hourly.*actual_cost.*FROM usage_logs`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"tokens", "actual_cost"}).AddRow(int64(12345), 67.89))

	tokens, actualCost, err := repo.GetDashboardLast24HourUsage(context.Background(), start, end)
	require.NoError(t, err)
	require.Equal(t, int64(12345), tokens)
	require.InDelta(t, 67.89, actualCost, 0.0001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardSystemMetricTrendIncludesPersistentDailyNetworkTraffic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 15, 16, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`(?s)FROM ops_system_metrics.*GROUP BY bucket`).
		WithArgs(start, end, "18m0s").
		WillReturnRows(sqlmock.NewRows([]string{
			"bucket", "cpu", "memory_used", "memory_total", "memory_pct",
			"receive", "transmit", "disk_used", "disk_total", "disk_pct", "source",
		}).AddRow(start, 5.0, int64(100), int64(1000), 10.0, 1_000_000.0, 2_000_000.0, int64(10), int64(100), 10.0, "host"))
	mock.ExpectQuery(`(?s)FROM ops_network_traffic_daily`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_date", "receive_bytes", "transmit_bytes"}).
			AddRow(time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC), int64(3_000_000_000), int64(2_000_000_000)).
			AddRow(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), int64(4_000_000_000), int64(1_000_000_000)))

	trend, err := repo.GetDashboardSystemMetricTrend(context.Background(), start, end, 240)
	require.NoError(t, err)
	require.Len(t, trend.NetworkDaily, 2)
	require.Equal(t, int64(7_000_000_000), trend.NetworkTotals.ReceiveBytes)
	require.Equal(t, int64(3_000_000_000), trend.NetworkTotals.TransmitBytes)
	require.Equal(t, int64(10_000_000_000), trend.NetworkTotals.TotalBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupUsageLogsFinalizesBusinessRollupBeforeDeletingDetails(t *testing.T) {
	useGroupUsageRepositoryTestTimezone(t, "UTC")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	fixedNow := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return fixedNow }

	mock.ExpectExec(`(?s)UPDATE dashboard_business_daily.*SET finalized_at`).
		WithArgs(cutoff, "UTC").
		WillReturnResult(sqlmock.NewResult(0, 90))
	mock.ExpectQuery(`(?s)SELECT EXISTS.*pg_partitioned_table`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM usage_group_rollup_state.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)WITH victims AS .*DELETE FROM usage_logs.*RETURNING created_at`).
		WithArgs(cutoff, usageLogsCleanupBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT closed_before::text, retained_from.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"closed_before", "retained_from", "timezone_name"}).
			AddRow("2026-08-14", time.Unix(0, 0).UTC(), "UTC"))
	mock.ExpectCommit()

	require.NoError(t, repo.CleanupUsageLogs(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}
