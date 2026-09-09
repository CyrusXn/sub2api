-- 余额探测的日期可能来自历史导入；非法日期必须降级为未知，不能中断整页查询。
CREATE OR REPLACE FUNCTION balance_center_asset_timestamp(value TEXT)
RETURNS TIMESTAMPTZ LANGUAGE plpgsql STABLE AS $$
BEGIN
    RETURN NULLIF(value, '')::timestamptz;
EXCEPTION WHEN OTHERS THEN
    RETURN NULL;
END;
$$;

-- 人民币按录入值 1:1 核算。订阅价格已包含在充值事件中，此处只计算剩余资产。
CREATE OR REPLACE VIEW balance_center_asset_values AS
WITH account_values AS (
    SELECT
        LOWER(REGEXP_REPLACE(SPLIT_PART(REGEXP_REPLACE(TRIM(credentials ->> 'base_url'), '^https?://', '', 'i'), '/', 1), ':[0-9]+$', '')) AS normalized_domain,
        CASE WHEN extra #>> '{upstream_billing_probe,balance,status}' = 'ok'
            AND extra #>> '{upstream_billing_probe,balance,amount}' ~ '^-?[0-9]{1,20}(\.[0-9]{1,15})?$'
            AND balance_center_asset_timestamp(extra #>> '{upstream_billing_probe,balance,received_at}') BETWEEN NOW() - INTERVAL '24 hours' AND NOW()
            THEN (extra #>> '{upstream_billing_probe,balance,amount}')::numeric
        END AS balance
    FROM accounts WHERE deleted_at IS NULL
), site_values AS (
    SELECT s.id AS site_id,
        COALESCE(a.manual_balance, (SELECT MIN(v.balance) FROM account_values v WHERE v.normalized_domain = s.normalized_domain)) AS cash_balance,
        CASE WHEN COALESCE(a.subscription_price, 0) = 0 THEN 0::numeric
            -- 自动订阅查询失败可能漏掉重置扣天，旧到期时间不能伪装成准确资产。
            WHEN a.subscription_auto_sync AND (a.subscription_synced_at IS NULL
                OR a.subscription_synced_at < NOW() - INTERVAL '24 hours'
                OR a.subscription_sync_error <> '') THEN NULL
            WHEN a.subscription_expires_at IS NULL OR a.subscription_days <= 0 THEN NULL
            ELSE a.subscription_price / a.subscription_days * LEAST(a.subscription_days,
                GREATEST(0, CEIL(EXTRACT(EPOCH FROM (a.subscription_expires_at - NOW())) / 86400)))
        END AS subscription_balance
    FROM balance_center_sites s
    LEFT JOIN balance_center_asset_settings a ON a.site_id = s.id
)
SELECT site_id, cash_balance, subscription_balance,
    cash_balance + subscription_balance AS total_balance,
    cash_balance IS NOT NULL AND subscription_balance IS NOT NULL AS balance_known
FROM site_values;

-- 独立保存新口径快照，历史错误状态筛选产生的旧零余额不回填、不篡改。
CREATE TABLE IF NOT EXISTS dashboard_business_asset_daily (
    bucket_date DATE PRIMARY KEY,
    ledger_version INTEGER NOT NULL DEFAULT 1,
    cash_balance NUMERIC(30,10),
    subscription_balance NUMERIC(30,10),
    total_balance NUMERIC(30,10),
    known_balance_subtotal NUMERIC(30,10) NOT NULL DEFAULT 0,
    unknown_sites INTEGER NOT NULL DEFAULT 0,
    site_ids BIGINT[] NOT NULL DEFAULT '{}',
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON TABLE dashboard_business_asset_daily IS '人民币资产实账日快照；仅记录当日实际采集值，不补造历史余额。';
