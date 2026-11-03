-- 保留文本生成请求标识，允许在用量日志异步落库后关联真实上游账号。
ALTER TABLE model_quality_runs ADD COLUMN IF NOT EXISTS request_id VARCHAR(100);
