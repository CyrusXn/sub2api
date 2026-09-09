package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 余额缺失、站点变更和消费越界必须保留真实语义，不能回退请求估算或硬裁剪。
func TestBusinessLedgerBoundaryAccounting(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		total, opening, closing any
		sameSites               bool
		status                  string
		wantConsumption         *float64
	}{
		{"缺少期初", 80.0, nil, 80.0, true, "missing_boundary", nil},
		{"缺少期末", 80.0, 100.0, nil, true, "missing_boundary", nil},
		{"站点范围变化", 80.0, 100.0, 80.0, false, "site_scope_changed", nil},
		{"超过区间充值不裁剪", 80.0, 100.0, 80.0, true, "available", ledgerTestFloat(30)},
		{"负消费不裁剪", 180.0, 100.0, 180.0, true, "available", ledgerTestFloat(-70)},
		{"当前资产未知不生成累计估值", nil, nil, nil, true, "missing_boundary", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := newDashboardAggregationRepositoryWithSQL(db)
			start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 0, 2)
			mock.ExpectQuery(`(?s)bucket_date = \$2::date.*bucket_date = \$1::date - 1.*ledger_version = 1`).
				WithArgs("2026-09-01", "2026-09-02", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{"cash", "subscription", "total", "known", "unknown", "opening", "closing", "opened_at", "closed_at", "same_sites", "exact_boundaries"}).
					AddRow(tc.total, 0.0, tc.total, 0.0, 0, tc.opening, tc.closing, nil, nil, tc.sameSites, true))
			result := &service.DashboardBusinessSummary{UpstreamRechargeTotal: 100, RangeUpstreamRechargeTotal: 10}
			result.Range.ActualCost = 5
			require.NoError(t, repo.populateBusinessLedger(context.Background(), start, end, result))
			require.Equal(t, tc.status, result.Ledger.RangeStatus)
			require.Equal(t, tc.wantConsumption, result.Ledger.RangeConsumption)
			if tc.wantConsumption == nil {
				require.Nil(t, result.Ledger.RangeProfit)
			} else {
				require.Equal(t, 5-*tc.wantConsumption, *result.Ledger.RangeProfit)
			}
			if tc.total == nil {
				require.Nil(t, result.Ledger.LifetimeConsumptionEstimate)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func ledgerTestFloat(value float64) *float64 { return &value }
