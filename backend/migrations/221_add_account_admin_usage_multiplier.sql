ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS admin_usage_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_admin_usage_multiplier_nonnegative,
    ADD CONSTRAINT accounts_admin_usage_multiplier_nonnegative
        CHECK (admin_usage_multiplier >= 0);
