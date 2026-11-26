-- 从部署时开始同步，不把已有余额充值历史重新入账。
ALTER TABLE upstream_site_balance_observations
    ADD COLUMN IF NOT EXISTS recharge_history_since TIMESTAMPTZ NOT NULL DEFAULT NOW();
