-- 忽略仍保留原始流水和去重键，但不会出现在待审核与已确认列表。
ALTER TABLE balance_center_recharge_events
    DROP CONSTRAINT IF EXISTS balance_center_recharge_events_review_status_check,
    ADD CONSTRAINT balance_center_recharge_events_review_status_check
    CHECK (review_status IN ('pending', 'confirmed', 'ignored'));
