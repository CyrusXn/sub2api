package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBusinessProfitHistoryKeepsSnapshotGapsAndCurrentLedger(t *testing.T) {
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := newDashboardAggregationRepositoryWithSQL(db)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	repo.clock = func() time.Time { return now }
	day := func(n int) time.Time { return time.Date(2026, 9, n, 0, 0, 0, 0, time.UTC) }
	profit := 13581.93
	result := &service.DashboardBusinessSummary{Ledger: service.DashboardBusinessLedger{LifetimeProfitEstimate: &profit}}
	// 窗口计算覆盖所有历史而不是查询区间，日界线使用业务时区；当前值来自同一响应的总账。
	mock.ExpectQuery(`(?s)WITH calendar.*MIN\(bucket_date\).*occurred_at AT TIME ZONE \$2.*SUM.*OVER.*ledger_version = 1.*LAG\(profit\).*site_ids = previous_sites.*ORDER BY day DESC`).
		WithArgs("2026-09-15", "Asia/Shanghai", now.In(timezone.Location()), profit).
		WillReturnRows(sqlmock.NewRows([]string{"day", "profit", "daily_profit", "captured_at"}).
			AddRow(day(15), profit, 500.0, now).
			AddRow(day(14), 13081.93, nil, now.Add(-12*time.Hour-time.Second)).
			AddRow(day(13), nil, nil, nil))
	require.NoError(t, repo.populateBusinessProfitHistory(context.Background(), result))
	require.Len(t, result.ProfitHistory, 3)
	require.Equal(t, "2026-09-15", result.ProfitHistory[0].BucketDate)
	require.Equal(t, "current", result.ProfitHistory[0].Status)
	require.Equal(t, profit, *result.ProfitHistory[0].CumulativeProfit)
	require.Equal(t, 500.0, *result.ProfitHistory[0].DailyProfit)
	require.Equal(t, "snapshot", result.ProfitHistory[1].Status)
	require.Nil(t, result.ProfitHistory[1].DailyProfit)
	require.Equal(t, "missing_snapshot", result.ProfitHistory[2].Status)
	require.Nil(t, result.ProfitHistory[2].CumulativeProfit)
	require.NoError(t, mock.ExpectationsWereMet())
}
