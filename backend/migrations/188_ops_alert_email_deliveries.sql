-- 记录运维告警邮件的逐收件人投递结果，支持后台审计和夜间静默汇总。
CREATE TABLE IF NOT EXISTS ops_alert_email_deliveries (
    id BIGSERIAL PRIMARY KEY,
    alert_event_id BIGINT REFERENCES ops_alert_events(id) ON DELETE SET NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    recipient_email TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    rule_name TEXT NOT NULL DEFAULT '',
    severity VARCHAR(16) NOT NULL DEFAULT '',
    target_site TEXT NOT NULL DEFAULT '',
    account_summary TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    detail_html TEXT NOT NULL DEFAULT '',
    error_ids BIGINT[] NOT NULL DEFAULT '{}',
    is_digest BOOLEAN NOT NULL DEFAULT false,
    digested_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_ops_alert_email_deliveries_idempotency UNIQUE (idempotency_key)
);

ALTER TABLE ops_alert_email_deliveries
    ADD COLUMN IF NOT EXISTS detail_html TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_ops_alert_email_deliveries_created_at
    ON ops_alert_email_deliveries (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_ops_alert_email_deliveries_status_time
    ON ops_alert_email_deliveries (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ops_alert_email_deliveries_event
    ON ops_alert_email_deliveries (alert_event_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ops_alert_email_deliveries_quiet_pending
    ON ops_alert_email_deliveries (recipient_email, created_at)
    WHERE status = 'quiet_hours' AND digested_at IS NULL;
