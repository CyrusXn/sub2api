ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS client_first_packet_ms INTEGER;
COMMENT ON COLUMN usage_logs.client_first_packet_ms IS '浏览器从发起请求到首次收到文本或占位的实测耗时，仅用于展示，不参与健康、超时或计费';
