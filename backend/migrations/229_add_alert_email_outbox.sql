-- 上游告警邮件先写入持久化队列，再按收件人和固定时间窗汇总发送。
CREATE TABLE IF NOT EXISTS alert_email_outbox (
    id                  BIGSERIAL PRIMARY KEY,
    source_type         VARCHAR(50) NOT NULL,
    source_id           VARCHAR(100) NOT NULL DEFAULT '',
    source_key          VARCHAR(255) NOT NULL,
    alert_type          VARCHAR(50) NOT NULL,
    recipient_email     VARCHAR(320) NOT NULL,
    subject             VARCHAR(500) NOT NULL,
    body_html           TEXT NOT NULL,
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'sent')),
    aggregate_until     TIMESTAMPTZ NOT NULL,
    available_at        TIMESTAMPTZ NOT NULL,
    attempt_count       INTEGER NOT NULL DEFAULT 0,
    claimed_at          TIMESTAMPTZ,
    sent_at             TIMESTAMPTZ,
    last_error          VARCHAR(1000) NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_type, source_key, recipient_email)
);

CREATE INDEX IF NOT EXISTS idx_alert_email_outbox_pending
    ON alert_email_outbox (available_at, recipient_email, created_at, id)
    WHERE status = 'pending';
