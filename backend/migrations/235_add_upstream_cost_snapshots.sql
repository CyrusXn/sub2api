-- 管理端上游费用必须基于请求结算时的原始消费费用和分组倍率，不能混入管理附加倍率。
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS upstream_cost_base DECIMAL(20, 10),
    ADD COLUMN IF NOT EXISTS upstream_group_rate_multiplier DECIMAL(10, 4);

COMMENT ON COLUMN usage_logs.upstream_cost_base IS '未叠加管理附加倍率的原始消费费用快照，仅管理端上游费用核算使用。';
COMMENT ON COLUMN usage_logs.upstream_group_rate_multiplier IS '本次请求所属分组默认上游倍率快照，仅管理端上游费用核算使用。';

ALTER TABLE dashboard_business_daily
    ADD COLUMN IF NOT EXISTS upstream_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS admin_upstream_cost DECIMAL(20, 10) NOT NULL DEFAULT 0;

-- 对仍保留明细的历史日期按实际倍率回算；早于明细保留期的永久汇总无法凭空恢复，保留为零。
WITH upstream_usage AS (
    SELECT
        (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date,
        COALESCE(SUM(COALESCE(ul.upstream_cost_base, ul.total_cost) * COALESCE(ul.upstream_group_rate_multiplier, ul.rate_multiplier)), 0) AS upstream_cost,
        COALESCE(SUM(COALESCE(ul.upstream_cost_base, ul.total_cost) * COALESCE(ul.upstream_group_rate_multiplier, ul.rate_multiplier)) FILTER (
            WHERE LOWER(TRIM(u.email)) = 'admin@example.com'
        ), 0) AS admin_upstream_cost
    FROM usage_logs ul
    JOIN users u ON u.id = ul.user_id
    GROUP BY 1
)
UPDATE dashboard_business_daily d
SET
    upstream_cost = upstream_usage.upstream_cost,
    admin_upstream_cost = upstream_usage.admin_upstream_cost,
    computed_at = NOW()
FROM upstream_usage
WHERE d.bucket_date = upstream_usage.bucket_date;
