-- 智检记录独立持久化；仅保存作者脱敏来源，不保存 API Key 或上游凭据。
CREATE TABLE IF NOT EXISTS model_quality_runs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT,
    author TEXT NOT NULL,
    group_name TEXT NOT NULL,
    model VARCHAR(200) NOT NULL,
    theme VARCHAR(20) NOT NULL CHECK (theme IN ('pelican', 'candy', 'custom')),
    prompt TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed')),
    content TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    account_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    CHECK (octet_length(content) <= 524288)
);
CREATE INDEX IF NOT EXISTS model_quality_runs_created_idx ON model_quality_runs (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS model_quality_runs_active_idx ON model_quality_runs (user_id, created_at) WHERE status = 'running';
