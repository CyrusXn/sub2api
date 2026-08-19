-- 持久化宿主机网络采样差值，并按北京时间累计每日流量。
ALTER TABLE ops_system_metrics
    ADD COLUMN IF NOT EXISTS network_receive_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS network_transmit_bytes BIGINT;

CREATE TABLE IF NOT EXISTS ops_network_traffic_daily (
    bucket_date DATE PRIMARY KEY,
    receive_bytes BIGINT NOT NULL DEFAULT 0,
    transmit_bytes BIGINT NOT NULL DEFAULT 0,
    sample_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ops_network_traffic_daily_receive_non_negative CHECK (receive_bytes >= 0),
    CONSTRAINT ops_network_traffic_daily_transmit_non_negative CHECK (transmit_bytes >= 0)
);

COMMENT ON TABLE ops_network_traffic_daily IS '按北京时间持久化的宿主机每日下行和上行流量，分钟指标清理后仍保留。';
COMMENT ON COLUMN ops_network_traffic_daily.receive_bytes IS '当日宿主机下行字节总量。';
COMMENT ON COLUMN ops_network_traffic_daily.transmit_bytes IS '当日宿主机上行字节总量。';
