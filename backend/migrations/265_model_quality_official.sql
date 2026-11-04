-- 官 key 按小时原子抢占；重启及多节点不会在同一小时重复消耗密钥额度。
ALTER TABLE model_quality_runs DROP CONSTRAINT IF EXISTS model_quality_runs_theme_check;
ALTER TABLE model_quality_runs ADD CONSTRAINT model_quality_runs_theme_check CHECK (theme IN ('pelican', 'candy', 'official', 'custom'));
ALTER TABLE model_quality_runs ADD COLUMN IF NOT EXISTS official_slot TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS model_quality_runs_official_slot_idx ON model_quality_runs (official_slot) WHERE official_slot IS NOT NULL;
