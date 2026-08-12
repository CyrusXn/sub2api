-- 链动小铺只允许管理员按需同步；会话请求整体加密保存，不保留明文 Cookie。
CREATE TABLE IF NOT EXISTS balance_center_liandong_sessions (
    id                  SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    request_encrypted   TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
