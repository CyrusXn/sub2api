ALTER TABLE balance_center_recharge_events
    ADD COLUMN IF NOT EXISTS review_status VARCHAR(20) NOT NULL DEFAULT 'confirmed',
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;

ALTER TABLE balance_center_recharge_events
    DROP CONSTRAINT IF EXISTS balance_center_recharge_events_review_status_check;
ALTER TABLE balance_center_recharge_events
    ADD CONSTRAINT balance_center_recharge_events_review_status_check
    CHECK (review_status IN ('pending', 'confirmed'));

CREATE INDEX IF NOT EXISTS idx_balance_center_recharge_events_review
    ON balance_center_recharge_events (review_status, occurred_at DESC, id DESC);
