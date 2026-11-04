-- 测试账号不计入人工充值，改为按北京时间每日实际消费计入有效充值。
-- 已固化历史也需要修正；先确认明细完整，避免把清理后的残余消费当成总消费。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM dashboard_business_daily d
        LEFT JOIN (
            SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date, COUNT(*) AS requests
            FROM usage_logs
            GROUP BY 1
        ) u USING (bucket_date)
        WHERE COALESCE(u.requests, 0) < d.total_requests
          -- 建号前不可能产生该账号消费，不能被其他用户旧明细清理误阻断。
          AND d.bucket_date >= (
              SELECT (MIN(created_at) AT TIME ZONE 'Asia/Shanghai')::date
              FROM users WHERE LOWER(TRIM(email)) = 'cyrusxn@163.com'
          )
    ) THEN
        RAISE EXCEPTION '测试账号充值口径回填失败：经营历史使用明细不完整，需先恢复历史消费明细，不能按残余数据重算';
    END IF;
END;
$$;

WITH recharge AS (
    SELECT (rc.used_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date,
           SUM(rc.value) AS amount
    FROM redeem_codes rc
    LEFT JOIN users u ON u.id = rc.used_by
    WHERE rc.status = 'used' AND rc.used_at IS NOT NULL
      AND rc.type IN ('balance', 'admin_balance') AND rc.value > 1
      AND LOWER(TRIM(COALESCE(u.email, ''))) NOT IN ('admin@example.com', 'cyrusxn@163.com')
    GROUP BY 1
), test_usage AS (
    SELECT (ul.created_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date,
           SUM(ul.actual_cost) AS amount
    FROM usage_logs ul
    JOIN users u ON u.id = ul.user_id
    WHERE LOWER(TRIM(u.email)) = 'cyrusxn@163.com'
    GROUP BY 1
), dates AS (
    -- 只修正测试账号涉及的日期，其他账号和历史行完全保留原值。
    SELECT (rc.used_at AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date
    FROM redeem_codes rc
    JOIN users u ON u.id = rc.used_by
    WHERE rc.status = 'used' AND rc.used_at IS NOT NULL
      AND rc.type IN ('balance', 'admin_balance') AND rc.value > 1
      AND LOWER(TRIM(u.email)) = 'cyrusxn@163.com'
    UNION SELECT bucket_date FROM test_usage
)
INSERT INTO dashboard_business_daily (bucket_date, recharge_amount)
SELECT dates.bucket_date, COALESCE(r.amount, 0) + COALESCE(t.amount, 0)
FROM dates
LEFT JOIN recharge r USING (bucket_date)
LEFT JOIN test_usage t USING (bucket_date)
ON CONFLICT (bucket_date) DO UPDATE SET recharge_amount = EXCLUDED.recharge_amount;
