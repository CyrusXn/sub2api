-- 鱼鱼日额度用于重置提醒，与按人民币摊销的订阅资产分开保存。
ALTER TABLE balance_center_asset_settings ADD COLUMN subscription_daily_remaining_usd NUMERIC(30,10);
