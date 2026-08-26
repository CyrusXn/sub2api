-- 站点级可维护显示名和充值记录类型，保证站点唯一值与展示标签分离。
ALTER TABLE balance_center_sites ADD COLUMN IF NOT EXISTS display_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE upstream_site_credentials ADD COLUMN IF NOT EXISTS display_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE balance_center_recharge_events ADD COLUMN IF NOT EXISTS record_type VARCHAR(50) NOT NULL DEFAULT 'recharge';
ALTER TABLE balance_center_recharge_events DROP CONSTRAINT IF EXISTS balance_center_recharge_events_record_type_check;
ALTER TABLE balance_center_recharge_events ADD CONSTRAINT balance_center_recharge_events_record_type_check CHECK (record_type IN ('recharge', 'subscription'));
