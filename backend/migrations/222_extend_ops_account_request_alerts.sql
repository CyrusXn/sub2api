-- 账号请求异常按“账号 + 脱敏错误原因”去重，并保存管理员排障所需的非敏感快照。
ALTER TABLE ops_alert_events
    ADD COLUMN IF NOT EXISTS dedupe_key VARCHAR(255) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_ops_alert_events_rule_dedupe_fired
    ON ops_alert_events (rule_id, dedupe_key, fired_at DESC, id DESC)
    WHERE dedupe_key <> '';

ALTER TABLE ops_alert_account_details
    ADD COLUMN IF NOT EXISTS error_log_id BIGINT,
    ADD COLUMN IF NOT EXISTS user_id BIGINT,
    ADD COLUMN IF NOT EXISTS user_email VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS api_key_id BIGINT,
    ADD COLUMN IF NOT EXISTS api_key_name VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS request_id VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS client_request_id VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS requested_model VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS upstream_model VARCHAR(255) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_ops_alert_account_details_error_log
    ON ops_alert_account_details (error_log_id)
    WHERE error_log_id IS NOT NULL;
