-- 余额中心邮件审计记录收件人和主题，并按快照、类型、收件人保证幂等。
ALTER TABLE balance_center_alert_deliveries
    ADD COLUMN IF NOT EXISTS recipient_email VARCHAR(320) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS subject VARCHAR(500) NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_balance_center_alert_deliveries_identity
    ON balance_center_alert_deliveries (snapshot_id, alert_type, recipient_email);
