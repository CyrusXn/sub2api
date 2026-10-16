-- 执行并发与账务结算分离；不确定结果保留冻结款，人工处置必须持久化审计。
ALTER TABLE gemini_interactions ADD COLUMN IF NOT EXISTS review_reason VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE gemini_interactions ADD COLUMN IF NOT EXISTS resolution JSONB;
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_review ON gemini_interactions (id)
    WHERE settled = FALSE AND deleted_at IS NULL AND review_reason <> '';
UPDATE gemini_interactions SET review_reason = 'terminal_usage_missing'
    WHERE settled = FALSE AND usage IS NULL AND status IN ('completed','failed','cancelled','requires_action');
