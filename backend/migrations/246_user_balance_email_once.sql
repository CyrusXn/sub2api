-- 同一用户持续低余额只发一次，各入口与所有实例共用持久发送状态。
CREATE TABLE user_balance_email_states (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    claim_token TEXT NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    sent_at TIMESTAMPTZ
);

-- 余额补充（充值、兑换或管理员加款）开启下一次提醒机会，扣费不会重置。
CREATE FUNCTION reset_user_balance_email_state() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM user_balance_email_states WHERE user_id = NEW.id;
    RETURN NEW;
END;
$$;
CREATE TRIGGER users_reset_balance_email_after_credit
AFTER UPDATE OF balance ON users
FOR EACH ROW WHEN (NEW.balance > OLD.balance)
EXECUTE FUNCTION reset_user_balance_email_state();
