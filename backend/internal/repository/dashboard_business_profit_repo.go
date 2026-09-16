package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 先累计全部历史，再由页面过滤明细范围，避免起始日期改变累计收益。
// 以次日零点为开区间，包含当日 23:59:59 及其小数秒；历史资产保留实际采集时间。
func (r *dashboardAggregationRepository) populateBusinessProfitHistory(ctx context.Context, result *service.DashboardBusinessSummary) error {
	now := r.now().In(timezone.Location())
	query := `WITH calendar AS (
 SELECT generate_series(COALESCE((SELECT MIN(bucket_date) FROM dashboard_business_daily), $1::date), $1::date, INTERVAL '1 day')::date AS day
), recharges AS (
 SELECT (occurred_at AT TIME ZONE $2)::date AS day, SUM(amount) AS amount
 FROM balance_center_recharge_events WHERE occurred_at <= $3 GROUP BY 1
), cumulative AS (
 SELECT c.day, SUM(COALESCE(d.actual_cost, 0)) OVER (ORDER BY c.day) AS charges,
   SUM(COALESCE(e.amount, 0)) OVER (ORDER BY c.day)
     + COALESCE((SELECT SUM(amount) FROM recharges WHERE day < (SELECT MIN(day) FROM calendar)), 0) AS recharged
 FROM calendar c LEFT JOIN dashboard_business_daily d ON d.bucket_date = c.day
 LEFT JOIN recharges e ON e.day = c.day
), valued AS (
 SELECT c.day,
   CASE WHEN c.day = $1::date THEN $4::double precision ELSE c.charges - c.recharged + a.total_balance END AS profit,
   CASE WHEN c.day = $1::date THEN $3::timestamptz ELSE a.captured_at END AS captured_at,
   CASE WHEN c.day = $1::date THEN (SELECT site_ids FROM (` + businessAssetAggregateSQL + `) current_assets) ELSE a.site_ids END AS site_ids
 FROM cumulative c LEFT JOIN dashboard_business_asset_daily a ON a.bucket_date = c.day AND a.ledger_version = 1
), compared AS (
 SELECT *, LAG(profit) OVER (ORDER BY day) AS previous_profit,
   LAG(site_ids) OVER (ORDER BY day) AS previous_sites FROM valued
)
SELECT day, profit,
 CASE WHEN site_ids = previous_sites THEN profit - previous_profit END AS daily_profit,
 captured_at FROM compared ORDER BY day DESC`
	rows, err := r.sql.QueryContext(ctx, query, now.Format("2006-01-02"), timezone.Name(), now, result.Ledger.LifetimeProfitEstimate)
	if err != nil {
		return err
	}
	defer rows.Close()
	result.ProfitHistory = make([]service.DashboardBusinessProfitPoint, 0)
	for rows.Next() {
		var point service.DashboardBusinessProfitPoint
		var day, captured sql.NullTime
		var profit, daily sql.NullFloat64
		if err := rows.Scan(&day, &profit, &daily, &captured); err != nil {
			return err
		}
		point.BucketDate = day.Time.Format("2006-01-02")
		point.CumulativeProfit, point.DailyProfit = nullableFloat64(profit), nullableFloat64(daily)
		point.Status = "missing_snapshot"
		if captured.Valid {
			point.CapturedAt = &captured.Time
		}
		if profit.Valid {
			point.Status = "snapshot"
			if point.BucketDate == now.Format("2006-01-02") {
				point.Status = "current"
			}
		}
		result.ProfitHistory = append(result.ProfitHistory, point)
	}
	return rows.Err()
}
