-- 上游费用必须使用账号上游站点的实际倍率，不得混入本站用户分组、专属或管理附加倍率。
COMMENT ON COLUMN usage_logs.upstream_group_rate_multiplier IS '请求发生时账号上游站点的实际结算倍率快照，仅管理端上游费用核算使用。';

-- 235 已写入的倍率来自本站分组，需要按当前保存的上游探测快照修正；无有效探测时退回账号静态倍率。
UPDATE usage_logs ul
SET upstream_group_rate_multiplier = COALESCE((
    SELECT CASE
        WHEN a.extra -> 'upstream_billing_probe' ->> 'status' = 'ok'
            AND a.extra -> 'upstream_billing_probe' -> 'data' ->> 'billing_scope' = 'token'
            AND jsonb_typeof(a.extra -> 'upstream_billing_probe' -> 'data' -> 'resolved_rate_multiplier') = 'number'
        THEN (a.extra -> 'upstream_billing_probe' -> 'data' ->> 'resolved_rate_multiplier')::numeric * CASE
            WHEN a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_rate_enabled' = 'false' THEN 1
            WHEN a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_rate_enabled' = 'true'
                AND jsonb_typeof(a.extra -> 'upstream_billing_probe' -> 'data' -> 'peak_rate_multiplier') = 'number'
                AND NULLIF(a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_start', '') IS NOT NULL
                AND NULLIF(a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_end', '') IS NOT NULL
                AND NULLIF(a.extra -> 'upstream_billing_probe' -> 'data' ->> 'timezone', '') IS NOT NULL
                AND (ul.created_at AT TIME ZONE (a.extra -> 'upstream_billing_probe' -> 'data' ->> 'timezone'))::time >= (a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_start')::time
                AND (ul.created_at AT TIME ZONE (a.extra -> 'upstream_billing_probe' -> 'data' ->> 'timezone'))::time < (a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_end')::time
            THEN (a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_rate_multiplier')::numeric
            WHEN a.extra -> 'upstream_billing_probe' -> 'data' ->> 'peak_rate_enabled' = 'true' THEN 1
            ELSE NULL
        END
        WHEN a.extra -> 'upstream_billing_probe' ->> 'status' = 'failed'
            AND jsonb_typeof(a.extra -> 'upstream_billing_probe' -> 'manual_rate_multiplier') = 'number'
        THEN (a.extra -> 'upstream_billing_probe' ->> 'manual_rate_multiplier')::numeric
        ELSE a.rate_multiplier
    END
    FROM accounts a
    WHERE a.id = ul.account_id
), 1);

-- 所有仍保有使用明细的经营历史都按同一口径回填；没有明细的历史日期无法重算，保留原值。
UPDATE dashboard_business_daily d
SET upstream_cost = scoped.upstream_cost,
    admin_upstream_cost = scoped.admin_upstream_cost,
    computed_at = NOW()
FROM (
    SELECT
        (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date,
        COALESCE(SUM(COALESCE(ul.upstream_cost_base, ul.total_cost) * COALESCE(ul.upstream_group_rate_multiplier, 1)), 0) AS upstream_cost,
        COALESCE(SUM(COALESCE(ul.upstream_cost_base, ul.total_cost) * COALESCE(ul.upstream_group_rate_multiplier, 1)) FILTER (
            WHERE LOWER(TRIM(u.email)) = 'admin@example.com'
        ), 0) AS admin_upstream_cost
    FROM usage_logs ul
    JOIN users u ON u.id = ul.user_id
    GROUP BY 1
) scoped
WHERE d.bucket_date = scoped.bucket_date;
