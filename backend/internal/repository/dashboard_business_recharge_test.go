package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestBusinessRechargeSubstitutesTestAccountConsumption(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	// 普通充值规则不变；特殊账号消费独立汇总，不受单笔充值门槛影响。
	mock.ExpectExec(`(?s)test_usage AS.*SUM\(ul.actual_cost\).*LOWER\(TRIM\(u.email\)\) = 'cyrusxn@163.com'.*rc.value > 1.*NOT IN \('admin@example.com', 'cyrusxn@163.com'\).*SELECT bucket_date FROM test_usage.*COALESCE\(recharge.recharge_amount, 0\) \+ COALESCE\(test_usage.actual_cost, 0\).*LEFT JOIN test_usage.*WHERE dashboard_business_daily.finalized_at IS NULL`).
		WithArgs(start, start.AddDate(0, 0, 1), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	repo := newDashboardAggregationRepositoryWithSQL(db)
	require.NoError(t, repo.upsertBusinessDailyAggregates(context.Background(), start, start.AddDate(0, 0, 1)))
	require.NoError(t, mock.ExpectationsWereMet())
}
