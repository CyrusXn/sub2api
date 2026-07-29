CREATE TABLE IF NOT EXISTS upstream_site_credentials (
    host VARCHAR(255) PRIMARY KEY,
    login_username VARCHAR(320) NOT NULL,
    password_encrypted TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

