package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// 仅对已结束且聚合完整的历史缺口固化一次；只有已最终固化的日汇总允许少于原请求数的残余明细。
func (r *dashboardAggregationRepository) freezeMissingBusinessUsage(ctx context.Context) error {
	_, err := r.sql.ExecContext(ctx, `
 INSERT INTO dashboard_business_usage_frozen (bucket_date, upstream_cost, daily_profit, source)
 SELECT d.bucket_date,
   CASE WHEN u.requests = d.total_requests AND u.requests > 0 THEN u.upstream_cost ELSE d.upstream_cost END,
   CASE WHEN u.requests = d.total_requests AND u.requests > 0 THEN u.charges - u.upstream_cost ELSE d.actual_cost - d.upstream_cost END,
   CASE WHEN u.requests = d.total_requests AND u.requests > 0 THEN 'usage_logs' ELSE 'permanent_rollup' END
 FROM (
   SELECT d.* FROM dashboard_business_daily d
   LEFT JOIN dashboard_business_asset_daily a ON a.bucket_date = d.bucket_date AND a.ledger_version = 1
   LEFT JOIN dashboard_business_asset_daily p ON p.bucket_date = d.bucket_date - 1 AND p.ledger_version = 1
   WHERE d.bucket_date < $1::date
     AND (a.total_balance IS NULL OR p.total_balance IS NULL OR a.site_ids IS DISTINCT FROM p.site_ids)
     AND NOT EXISTS (SELECT 1 FROM dashboard_business_usage_frozen f WHERE f.bucket_date = d.bucket_date)
 ) d
 LEFT JOIN LATERAL (
   SELECT COUNT(*) AS requests, COALESCE(SUM(ul.actual_cost), 0) AS charges,
     COALESCE(SUM(`+upstreamCostSQLExpr("ul", "upstream_account")+`), 0) AS upstream_cost
   FROM usage_logs ul LEFT JOIN accounts upstream_account ON upstream_account.id = ul.account_id
   WHERE ul.created_at >= (d.bucket_date::timestamp AT TIME ZONE $2)
     AND ul.created_at < ((d.bucket_date + 1)::timestamp AT TIME ZONE $2)
 ) u ON true
 WHERE u.requests = d.total_requests
    OR (u.requests < d.total_requests AND d.finalized_at IS NOT NULL)
 ON CONFLICT (bucket_date) DO NOTHING`, r.now().In(timezone.Location()).Format("2006-01-02"), timezone.Name())
	return err
}
