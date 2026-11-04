-- 删除检测结果后保留小时占位，防止官 key 定时任务在同一小时重新调用。
ALTER TABLE model_quality_runs ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
