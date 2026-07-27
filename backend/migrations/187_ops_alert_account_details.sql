-- 保存账号请求告警的可定位账号快照，不保留凭据、备注或上游原始响应。
CREATE TABLE IF NOT EXISTS ops_alert_account_details (
    id BIGSERIAL PRIMARY KEY,
    alert_event_id BIGINT NOT NULL REFERENCES ops_alert_events(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL,
    account_name TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    group_id BIGINT,
    group_name TEXT NOT NULL DEFAULT '',
    diagnosis VARCHAR(64) NOT NULL DEFAULT '',
    error_phase VARCHAR(64) NOT NULL DEFAULT '',
    status_code INTEGER NOT NULL DEFAULT 0,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_ops_alert_account_details_event_account UNIQUE (alert_event_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_ops_alert_account_details_account_time
    ON ops_alert_account_details (account_id, occurred_at DESC);
