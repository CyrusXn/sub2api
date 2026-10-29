-- 首包包含等待占位；历史记录保持 NULL，继续保留真实首字的独立语义。
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS first_packet_ms INTEGER;
COMMENT ON COLUMN usage_logs.first_packet_ms IS '服务端首次有效内容或等待占位实际写出并 Flush 的耗时（毫秒）';
