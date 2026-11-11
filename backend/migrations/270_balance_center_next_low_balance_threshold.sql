ALTER TABLE balance_center_alert_states
    ADD COLUMN IF NOT EXISTS low_balance_threshold DECIMAL(20, 10),
    ADD COLUMN IF NOT EXISTS next_low_balance_threshold DECIMAL(20, 10);
