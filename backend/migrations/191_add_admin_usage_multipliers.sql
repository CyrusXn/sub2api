ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS admin_usage_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS admin_usage_multiplier DECIMAL(10,4);

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_admin_usage_multiplier_nonnegative,
    ADD CONSTRAINT groups_admin_usage_multiplier_nonnegative
        CHECK (admin_usage_multiplier >= 0);

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_admin_usage_multiplier_nonnegative,
    ADD CONSTRAINT users_admin_usage_multiplier_nonnegative
        CHECK (admin_usage_multiplier IS NULL OR admin_usage_multiplier >= 0);
