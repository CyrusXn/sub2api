package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 共享资产汇总保留未知站点；部分资产的小计不能伪装成完整总余额。
const businessAssetAggregateSQL = `
 SELECT CASE WHEN COUNT(*) > 0 AND COUNT(cash_balance) = COUNT(*) THEN SUM(cash_balance) END AS cash_balance,
        CASE WHEN COUNT(*) > 0 AND COUNT(subscription_balance) = COUNT(*) THEN SUM(subscription_balance) END AS subscription_balance,
        CASE WHEN COUNT(*) > 0 AND BOOL_AND(balance_known) THEN SUM(total_balance) END AS total_balance,
        COALESCE(SUM(COALESCE(cash_balance, 0) + COALESCE(subscription_balance, 0)), 0) AS known_balance_subtotal,
        COUNT(*) FILTER (WHERE NOT balance_known) AS unknown_sites,
        COALESCE(ARRAY_AGG(site_id ORDER BY site_id), '{}'::bigint[]) AS site_ids
 FROM balance_center_asset_values
`

// 只写当日新版本快照；历史回填不调用，旧版日表余额保持原状。
func (r *dashboardAggregationRepository) snapshotBusinessAssetLedger(ctx context.Context) error {
	_, err := r.sql.ExecContext(ctx, `
 INSERT INTO dashboard_business_asset_daily
   (bucket_date, ledger_version, cash_balance, subscription_balance, total_balance, known_balance_subtotal, unknown_sites, site_ids, captured_at)
 SELECT (NOW() AT TIME ZONE $1)::date, 1, a.*, NOW()
 FROM (`+businessAssetAggregateSQL+`) a
 ON CONFLICT (bucket_date) DO UPDATE SET
   ledger_version = EXCLUDED.ledger_version, cash_balance = EXCLUDED.cash_balance,
   subscription_balance = EXCLUDED.subscription_balance, total_balance = EXCLUDED.total_balance,
   known_balance_subtotal = EXCLUDED.known_balance_subtotal, unknown_sites = EXCLUDED.unknown_sites,
   site_ids = EXCLUDED.site_ids, captured_at = EXCLUDED.captured_at
 `, timezone.Name())
	return err
}

func (r *dashboardAggregationRepository) populateBusinessLedger(ctx context.Context, start, end time.Time, result *service.DashboardBusinessSummary) error {
	// 只接受所选结束日与开始日前一天，绝不拿区间内任意一天冒充边界。
	query := `WITH current_assets AS (` + businessAssetAggregateSQL + `),
 ending AS (
   SELECT total_balance, site_ids, NOW() AS captured_at FROM current_assets WHERE $2::date = $3::date
   UNION ALL
   SELECT total_balance, site_ids, captured_at FROM dashboard_business_asset_daily
   WHERE bucket_date = $2::date AND $2::date < $3::date AND ledger_version = 1
 ), opening AS (
   SELECT total_balance, site_ids, captured_at FROM dashboard_business_asset_daily
   WHERE bucket_date = $1::date - 1 AND ledger_version = 1
 )
 SELECT c.cash_balance, c.subscription_balance, c.total_balance, c.known_balance_subtotal, c.unknown_sites,
        o.total_balance, e.total_balance, o.captured_at, e.captured_at,
        COALESCE(o.site_ids = e.site_ids, false),
        COALESCE(o.captured_at = $4::timestamptz AND
          (e.captured_at = $5::timestamptz OR $2::date = $3::date), false)
 FROM current_assets c LEFT JOIN opening o ON true LEFT JOIN ending e ON true`
	var cash, subscription, total, opening, closing sql.NullFloat64
	var openedAt, closedAt sql.NullTime
	var sameSites, exactBoundaries bool
	ledger := &result.Ledger
	err := scanSingleRow(ctx, r.sql, query, []any{start.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02"), r.now().In(timezone.Location()).Format("2006-01-02"), start, end},
		&cash, &subscription, &total, &ledger.KnownBalanceSubtotal, &ledger.UnknownSites,
		&opening, &closing, &openedAt, &closedAt, &sameSites, &exactBoundaries)
	if err != nil {
		return err
	}
	ledger.CashBalance, ledger.SubscriptionBalance, ledger.TotalBalance = nullableFloat64(cash), nullableFloat64(subscription), nullableFloat64(total)
	ledger.OpeningBalance, ledger.ClosingBalance = nullableFloat64(opening), nullableFloat64(closing)
	if openedAt.Valid {
		ledger.OpeningCapturedAt = &openedAt.Time
	}
	if closedAt.Valid {
		ledger.ClosingCapturedAt = &closedAt.Time
	}
	// 不裁剪负数或超过充值的消费：可能存在期初资产、退款或人工调整，需要如实对账。
	if total.Valid {
		consumption := result.UpstreamRechargeTotal - total.Float64
		profit := result.Lifetime.ActualCost - consumption
		ledger.LifetimeConsumptionEstimate, ledger.LifetimeProfitEstimate = &consumption, &profit
	}
	ledger.RangeStatus = "missing_boundary"
	if opening.Valid && closing.Valid {
		ledger.RangeStatus = "site_scope_changed"
		if sameSites && !exactBoundaries {
			// 日内最后一次采集未必是零点余额，不能搭配整日充值冒充精确实账。
			ledger.RangeStatus = "inexact_boundary"
		}
		if sameSites && exactBoundaries {
			consumption := opening.Float64 + result.RangeUpstreamRechargeTotal - closing.Float64
			profit := result.Range.ActualCost - consumption
			ledger.RangeConsumption, ledger.RangeProfit = &consumption, &profit
			ledger.RangeStatus = "available"
		}
	}
	return nil
}
