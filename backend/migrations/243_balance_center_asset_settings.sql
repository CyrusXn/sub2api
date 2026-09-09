-- 资产配置只记录余额校正和已入账订阅的摊销参数，不生成第二笔充值。
CREATE TABLE balance_center_asset_settings (
    site_id BIGINT PRIMARY KEY REFERENCES balance_center_sites(id),
    manual_balance NUMERIC(20,10) CHECK (manual_balance >= 0),
    subscription_id BIGINT CHECK (subscription_id > 0),
    subscription_price NUMERIC(20,10) NOT NULL DEFAULT 0 CHECK (subscription_price >= 0),
    subscription_days INTEGER NOT NULL DEFAULT 30 CHECK (subscription_days BETWEEN 1 AND 3660),
    subscription_expires_at TIMESTAMPTZ,
    subscription_auto_sync BOOLEAN NOT NULL DEFAULT FALSE,
    subscription_synced_at TIMESTAMPTZ,
    subscription_sync_error TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 用户确认停用站点余额为零；包含没有充值记录的停用站点，避免遗漏后导致总额未知。
-- 现存手工配置优先，启用中的站点查不到余额时仍保持未知。
INSERT INTO balance_center_asset_settings(site_id, manual_balance)
SELECT s.id, 0 FROM balance_center_sites s
WHERE NOT EXISTS(SELECT 1 FROM accounts a WHERE a.deleted_at IS NULL AND a.status <> 'disabled'
    AND LOWER(REGEXP_REPLACE(SPLIT_PART(REGEXP_REPLACE(TRIM(a.credentials->>'base_url'), '^https?://','','i'),'/',1), ':[0-9]+$',''))=s.normalized_domain)
ON CONFLICT(site_id) DO NOTHING;

-- 本次已核实的鱼鱼订阅：808 元已入充值，仅登记其资产参数，不新增充值。
INSERT INTO balance_center_asset_settings(site_id, subscription_id, subscription_price, subscription_days,
    subscription_expires_at, subscription_auto_sync)
SELECT id, 944, 808, 30, '2026-09-28T16:27:44.861586+08:00'::timestamptz, TRUE
FROM balance_center_sites WHERE normalized_domain='sub.anzhiyu.com'
ON CONFLICT(site_id) DO NOTHING;

-- 用户逐项确认 pite 与自建 Grok 无预付余额，使用人工账面零值，不依赖失败探测。
INSERT INTO balance_center_asset_settings(site_id, manual_balance)
SELECT id, 0 FROM balance_center_sites
WHERE normalized_domain IN ('ai.pite.chat', 'grok-api.xnkaixin.eu.cc')
ON CONFLICT(site_id) DO NOTHING;
