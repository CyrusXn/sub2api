-- 仅保存真实使用产生的分钟状态，与旧探针历史隔离。
CREATE TABLE IF NOT EXISTS channel_monitor_passive_minutes (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    bucket_start TIMESTAMPTZ NOT NULL,
    request_count BIGINT NOT NULL,
    success_count BIGINT NOT NULL,
    error_count BIGINT NOT NULL,
    first_token_ms BIGINT,
    status VARCHAR(20) NOT NULL CHECK (status IN ('operational', 'degraded', 'error')),
    PRIMARY KEY (group_id, bucket_start)
);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_passive_minutes_time
    ON channel_monitor_passive_minutes (bucket_start);
CREATE TABLE IF NOT EXISTS channel_monitor_passive_watermark (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    data_through TIMESTAMPTZ NOT NULL
);
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
    channel_monitor_passive_minutes, channel_monitor_passive_watermark TO CURRENT_USER;
