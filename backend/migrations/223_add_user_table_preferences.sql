-- 管理端表格列设置按管理员持久化，支持跨浏览器和跨设备恢复。
CREATE TABLE IF NOT EXISTS user_table_preferences (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    table_key VARCHAR(32) NOT NULL,
    hidden_columns JSONB NOT NULL DEFAULT '[]'::jsonb,
    column_widths JSONB NOT NULL DEFAULT '{}'::jsonb,
    column_order JSONB NOT NULL DEFAULT '[]'::jsonb,
    schema_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_table_preferences_table_key_check
        CHECK (table_key IN ('users', 'groups', 'accounts')),
    CONSTRAINT user_table_preferences_user_table_unique UNIQUE (user_id, table_key)
);

CREATE INDEX IF NOT EXISTS idx_user_table_preferences_user_id
    ON user_table_preferences (user_id);
