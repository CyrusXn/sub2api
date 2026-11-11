CREATE TABLE IF NOT EXISTS upstream_site_balance_observations (
    host VARCHAR(255) PRIMARY KEY,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    base_url TEXT NOT NULL,
    balance NUMERIC(20, 8),
    currency VARCHAR(16) NOT NULL DEFAULT 'USD',
    observed_at TIMESTAMPTZ,
    next_probe_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_error VARCHAR(120) NOT NULL DEFAULT '',
    alert_active BOOLEAN NOT NULL DEFAULT FALSE,
    alert_old_balance NUMERIC(20, 8),
    alert_new_balance NUMERIC(20, 8),
    alert_delta NUMERIC(20, 8),
    alert_currency VARCHAR(16) NOT NULL DEFAULT 'USD',
    alert_detected_at TIMESTAMPTZ,
    alert_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upstream_site_balance_observations_due
    ON upstream_site_balance_observations (next_probe_at, host);

CREATE INDEX IF NOT EXISTS idx_upstream_site_balance_observations_active
    ON upstream_site_balance_observations (host) WHERE alert_active = TRUE;

INSERT INTO upstream_site_balance_observations (host, display_name, base_url)
SELECT host, display_name, 'https://' || host
FROM upstream_site_credentials
ON CONFLICT (host) DO NOTHING;
