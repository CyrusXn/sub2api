-- 永久经营日汇总不参与 usage_logs 或普通仪表盘聚合表的保留清理。
-- 先利用现有日汇总回填全站数据，只扫描管理员自己的明细补齐排除口径。
CREATE TABLE IF NOT EXISTS dashboard_business_daily (
    bucket_date DATE PRIMARY KEY,
    recharge_amount DECIMAL(20, 10) NOT NULL DEFAULT 0,
    total_requests BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    total_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    actual_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    account_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    admin_actual_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    admin_account_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    total_duration_ms BIGINT NOT NULL DEFAULT 0,
    finalized_at TIMESTAMPTZ,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE dashboard_business_daily
    ADD COLUMN IF NOT EXISTS finalized_at TIMESTAMPTZ;

COMMENT ON TABLE dashboard_business_daily IS '永久经营日汇总；usage_logs 清理后仍用于历史 Token、消费、成本和充值查询。';
COMMENT ON COLUMN dashboard_business_daily.finalized_at IS '明细清理前的固化时间；非空后禁止普通聚合覆盖。';

CREATE INDEX IF NOT EXISTS idx_redeem_codes_business_rollup_used_at
    ON redeem_codes(used_at)
    WHERE status = 'used'
      AND type IN ('balance', 'admin_balance')
      AND value > 1
      AND used_at IS NOT NULL;

WITH admin_usage AS (
    SELECT
        (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date,
        COALESCE(SUM(ul.actual_cost), 0) AS admin_actual_cost,
        COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1)), 0) AS admin_account_cost
    FROM usage_logs ul
    JOIN users u ON u.id = ul.user_id
    WHERE LOWER(TRIM(u.email)) = 'admin@example.com'
    GROUP BY 1
), recharge AS (
    SELECT
        (rc.used_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date,
        COALESCE(SUM(rc.value), 0) AS recharge_amount
    FROM redeem_codes rc
    LEFT JOIN users u ON u.id = rc.used_by
    WHERE rc.status = 'used'
      AND rc.used_at IS NOT NULL
      AND rc.type IN ('balance', 'admin_balance')
      AND rc.value > 1
      AND LOWER(TRIM(COALESCE(u.email, ''))) <> 'admin@example.com'
    GROUP BY 1
), dates AS (
    SELECT bucket_date FROM usage_dashboard_daily
    UNION
    SELECT bucket_date FROM admin_usage
    UNION
    SELECT bucket_date FROM recharge
)
INSERT INTO dashboard_business_daily (
    bucket_date,
    recharge_amount,
    total_requests,
    input_tokens,
    output_tokens,
    cache_creation_tokens,
    cache_read_tokens,
    total_cost,
    actual_cost,
    account_cost,
    admin_actual_cost,
    admin_account_cost,
    total_duration_ms,
    computed_at
)
SELECT
    dates.bucket_date,
    COALESCE(recharge.recharge_amount, 0),
    COALESCE(d.total_requests, 0),
    COALESCE(d.input_tokens, 0),
    COALESCE(d.output_tokens, 0),
    COALESCE(d.cache_creation_tokens, 0),
    COALESCE(d.cache_read_tokens, 0),
    COALESCE(d.total_cost, 0),
    COALESCE(d.actual_cost, 0),
    COALESCE(d.account_cost, 0),
    COALESCE(admin_usage.admin_actual_cost, 0),
    COALESCE(admin_usage.admin_account_cost, 0),
    COALESCE(d.total_duration_ms, 0),
    NOW()
FROM dates
LEFT JOIN usage_dashboard_daily d ON d.bucket_date = dates.bucket_date
LEFT JOIN admin_usage ON admin_usage.bucket_date = dates.bucket_date
LEFT JOIN recharge ON recharge.bucket_date = dates.bucket_date
ON CONFLICT (bucket_date) DO NOTHING;

ALTER TABLE ops_system_metrics
    ADD COLUMN IF NOT EXISTS resource_source VARCHAR(16),
    ADD COLUMN IF NOT EXISTS network_receive_bytes_per_second DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS network_transmit_bytes_per_second DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS disk_used_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS disk_total_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS disk_usage_percent DOUBLE PRECISION;

COMMENT ON COLUMN ops_system_metrics.resource_source IS '资源指标来源：host 表示 Node Exporter，container 表示容器回退。';
