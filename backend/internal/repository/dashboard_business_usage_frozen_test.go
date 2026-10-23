package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestFreezeBusinessUsageUsesSameRequestCostAndNeverOverwritesHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := newDashboardAggregationRepositoryWithSQL(db)
	mock.ExpectExec(`(?s)INSERT INTO dashboard_business_usage_frozen.*upstream_cost_base.*upstream_group_rate_multiplier.*WHERE u.requests = d.total_requests.*u.requests < d.total_requests AND d.finalized_at IS NOT NULL.*ON CONFLICT \(bucket_date\) DO NOTHING`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.freezeMissingBusinessUsage(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBusinessUsageFreezesOnlyFromAggregationWatermark(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := newDashboardAggregationRepositoryWithSQL(db)
	mock.ExpectExec(`(?s)INSERT INTO dashboard_business_usage_frozen.*DO NOTHING`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)INSERT INTO usage_dashboard_aggregation_watermark`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateAggregationWatermark(context.Background(), repo.now()))
	require.NoError(t, mock.ExpectationsWereMet())
}
