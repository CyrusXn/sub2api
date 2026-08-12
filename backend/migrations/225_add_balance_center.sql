-- 余额中心将永久历史与当前状态分开存储，所有运行开关默认关闭。
CREATE TABLE IF NOT EXISTS balance_center_sites (
    id                  BIGSERIAL PRIMARY KEY,
    name                VARCHAR(255) NOT NULL,
    normalized_domain   VARCHAR(255) NOT NULL UNIQUE,
    base_url            TEXT NOT NULL DEFAULT '',
    source              VARCHAR(50) NOT NULL DEFAULT 'sub2api',
    probe_supported     BOOLEAN NOT NULL DEFAULT TRUE,
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS balance_center_account_bindings (
    id                  BIGSERIAL PRIMARY KEY,
    site_id             BIGINT NOT NULL REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id          BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source              VARCHAR(50) NOT NULL DEFAULT 'sub2api',
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (site_id, account_id)
);

CREATE TABLE IF NOT EXISTS balance_center_legacy_keys (
    id                  BIGSERIAL PRIMARY KEY,
    site_id             BIGINT NOT NULL REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id          BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    source              VARCHAR(50) NOT NULL,
    source_key          VARCHAR(255) NOT NULL,
    display_name        VARCHAR(255) NOT NULL DEFAULT '',
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE TABLE IF NOT EXISTS balance_center_snapshots (
    id                  BIGSERIAL PRIMARY KEY,
    site_id             BIGINT NOT NULL REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id          BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    legacy_key_id       BIGINT REFERENCES balance_center_legacy_keys(id) ON DELETE SET NULL,
    source              VARCHAR(50) NOT NULL,
    source_key          VARCHAR(255) NOT NULL,
    status              VARCHAR(20) NOT NULL DEFAULT 'unknown'
                        CHECK (status IN ('ok', 'unsupported', 'failed', 'unknown')),
    balance             DECIMAL(30, 10),
    converted_balance   DECIMAL(30, 10),
    rate_multiplier     DECIMAL(20, 10),
    conversion_scale    DECIMAL(20, 10) NOT NULL DEFAULT 1,
    currency            VARCHAR(20) NOT NULL DEFAULT '',
    reason              VARCHAR(100) NOT NULL DEFAULT '',
    probed_at           TIMESTAMPTZ NOT NULL,
    payload             JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE INDEX IF NOT EXISTS idx_balance_center_snapshots_site_time
    ON balance_center_snapshots (site_id, probed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_balance_center_snapshots_account_time
    ON balance_center_snapshots (account_id, probed_at DESC, id DESC)
    WHERE account_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS balance_center_current_states (
    identity_key        VARCHAR(255) PRIMARY KEY,
    site_id             BIGINT NOT NULL REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id          BIGINT REFERENCES accounts(id) ON DELETE CASCADE,
    legacy_key_id       BIGINT REFERENCES balance_center_legacy_keys(id) ON DELETE CASCADE,
    snapshot_id         BIGINT NOT NULL REFERENCES balance_center_snapshots(id) ON DELETE CASCADE,
    status              VARCHAR(20) NOT NULL DEFAULT 'unknown'
                        CHECK (status IN ('ok', 'unsupported', 'failed', 'unknown')),
    balance             DECIMAL(30, 10),
    converted_balance   DECIMAL(30, 10),
    rate_multiplier     DECIMAL(20, 10),
    conversion_scale    DECIMAL(20, 10) NOT NULL DEFAULT 1,
    currency            VARCHAR(20) NOT NULL DEFAULT '',
    reason              VARCHAR(100) NOT NULL DEFAULT '',
    probed_at           TIMESTAMPTZ NOT NULL,
    last_used_at        TIMESTAMPTZ,
    source              VARCHAR(50) NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_balance_center_current_states_site
    ON balance_center_current_states (site_id, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_balance_center_current_states_account
    ON balance_center_current_states (account_id) WHERE account_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS balance_center_alert_states (
    identity_key                VARCHAR(255) PRIMARY KEY,
    site_id                     BIGINT NOT NULL REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id                  BIGINT REFERENCES accounts(id) ON DELETE CASCADE,
    low_balance_active          BOOLEAN NOT NULL DEFAULT FALSE,
    low_balance_notified_at     TIMESTAMPTZ,
    multiplier_baseline         DECIMAL(20, 10),
    multiplier_notified_at      TIMESTAMPTZ,
    last_success_snapshot_id    BIGINT REFERENCES balance_center_snapshots(id) ON DELETE SET NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS balance_center_manual_rows (
    id                  BIGSERIAL PRIMARY KEY,
    source              VARCHAR(50) NOT NULL,
    source_key          VARCHAR(255) NOT NULL,
    site_id             BIGINT REFERENCES balance_center_sites(id) ON DELETE SET NULL,
    label               VARCHAR(255) NOT NULL DEFAULT '',
    expression          TEXT NOT NULL,
    amount              DECIMAL(30, 10) NOT NULL,
    sort_order          INT NOT NULL DEFAULT 0,
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE TABLE IF NOT EXISTS balance_center_recharge_events (
    id                  BIGSERIAL PRIMARY KEY,
    source              VARCHAR(50) NOT NULL,
    source_key          VARCHAR(255) NOT NULL,
    site_id             BIGINT REFERENCES balance_center_sites(id) ON DELETE SET NULL,
    account_id          BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    amount              DECIMAL(30, 10) NOT NULL,
    currency            VARCHAR(20) NOT NULL DEFAULT 'CNY',
    occurred_at         TIMESTAMPTZ NOT NULL,
    note                TEXT NOT NULL DEFAULT '',
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE INDEX IF NOT EXISTS idx_balance_center_recharge_events_time
    ON balance_center_recharge_events (occurred_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS balance_center_automatic_records (
    id                  BIGSERIAL PRIMARY KEY,
    source              VARCHAR(50) NOT NULL,
    source_key          VARCHAR(255) NOT NULL,
    site_id             BIGINT REFERENCES balance_center_sites(id) ON DELETE SET NULL,
    account_id          BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    amount              DECIMAL(30, 10) NOT NULL,
    currency            VARCHAR(20) NOT NULL DEFAULT 'CNY',
    occurred_at         TIMESTAMPTZ NOT NULL,
    record_type         VARCHAR(50) NOT NULL DEFAULT 'recharge',
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE TABLE IF NOT EXISTS balance_center_liandong_orders (
    id                  BIGSERIAL PRIMARY KEY,
    source              VARCHAR(50) NOT NULL DEFAULT 'liandong',
    source_key          VARCHAR(255) NOT NULL,
    transaction_no      VARCHAR(255) NOT NULL UNIQUE,
    site_id             BIGINT REFERENCES balance_center_sites(id) ON DELETE SET NULL,
    paid_amount         DECIMAL(30, 10) NOT NULL,
    currency            VARCHAR(20) NOT NULL DEFAULT 'CNY',
    paid_at             TIMESTAMPTZ,
    status              VARCHAR(50) NOT NULL DEFAULT '',
    payload             JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE TABLE IF NOT EXISTS balance_center_reconciliations (
    id                  BIGSERIAL PRIMARY KEY,
    source              VARCHAR(50) NOT NULL,
    source_key          VARCHAR(255) NOT NULL,
    period_start        TIMESTAMPTZ,
    period_end          TIMESTAMPTZ,
    expected_amount     DECIMAL(30, 10),
    actual_amount       DECIMAL(30, 10),
    difference_amount   DECIMAL(30, 10),
    status              VARCHAR(50) NOT NULL DEFAULT '',
    details             JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_key)
);

CREATE TABLE IF NOT EXISTS balance_center_alert_deliveries (
    id                  BIGSERIAL PRIMARY KEY,
    identity_key        VARCHAR(255) NOT NULL,
    site_id             BIGINT NOT NULL REFERENCES balance_center_sites(id) ON DELETE CASCADE,
    account_id          BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    snapshot_id         BIGINT REFERENCES balance_center_snapshots(id) ON DELETE SET NULL,
    alert_type          VARCHAR(30) NOT NULL CHECK (alert_type IN ('low_balance', 'multiplier_changed')),
    old_value           DECIMAL(30, 10),
    new_value           DECIMAL(30, 10),
    threshold           DECIMAL(30, 10),
    status              VARCHAR(30) NOT NULL DEFAULT 'pending',
    failure_reason      VARCHAR(255) NOT NULL DEFAULT '',
    attempted_at        TIMESTAMPTZ,
    accepted_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_balance_center_alert_deliveries_time
    ON balance_center_alert_deliveries (created_at DESC, id DESC);

INSERT INTO settings (key, value, updated_at) VALUES
    ('balance_center_enabled', 'false', NOW()),
    ('balance_center_event_probe_enabled', 'false', NOW()),
    ('balance_center_email_enabled', 'false', NOW()),
    ('balance_center_low_balance_threshold', '5', NOW())
ON CONFLICT (key) DO NOTHING;
