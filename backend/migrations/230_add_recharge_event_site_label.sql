-- 历史充值可能没有可绑定的上游站点，独立标签用于保留其站点维度，禁止从备注推断身份。
ALTER TABLE balance_center_recharge_events
    ADD COLUMN IF NOT EXISTS site_label VARCHAR(255) NOT NULL DEFAULT '';
