-- 每个站点仅使用一个代表账号观察现金变化，避免多个 API Key 重复记账。
CREATE TABLE balance_center_recharge_baselines (
    site_id BIGINT PRIMARY KEY REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    key_fingerprint TEXT NOT NULL,
    balance NUMERIC(30,10) NOT NULL,
    conversion_scale NUMERIC(30,10) NOT NULL,
    currency VARCHAR(20) NOT NULL,
    probed_at TIMESTAMPTZ NOT NULL
);
