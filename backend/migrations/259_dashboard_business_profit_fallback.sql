-- 请求估算与真实资产快照分表，补齐历史缺口但不伪造余额。已冻结记录只读，不跟随当前倍率重算。
CREATE TABLE IF NOT EXISTS dashboard_business_usage_frozen (
    bucket_date DATE PRIMARY KEY,
    upstream_cost NUMERIC(30,10) NOT NULL,
    daily_profit NUMERIC(30,10) NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('usage_logs', 'permanent_rollup')),
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
