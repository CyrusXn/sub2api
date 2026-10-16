-- 独立 Interactions 资源绑定；只记录归属、路由和计费快照，不保存正文或凭据。
CREATE TABLE IF NOT EXISTS gemini_interactions (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    upstream_id VARCHAR(2048) NOT NULL DEFAULT '',
    api_key_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    metadata JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'creating',
    usage JSONB,
    settled BOOLEAN NOT NULL DEFAULT FALSE,
    hold_amount NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (hold_amount >= 0),
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 seconds',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_pending ON gemini_interactions (next_poll_at, id)
    WHERE settled = FALSE AND deleted_at IS NULL AND upstream_id <> '';
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_owner ON gemini_interactions (api_key_id, user_id, group_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_pending_user ON gemini_interactions (user_id)
    WHERE settled = FALSE AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_pending_account ON gemini_interactions (account_id)
    WHERE settled = FALSE AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_pending_key ON gemini_interactions (api_key_id)
    WHERE settled = FALSE AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_pending_group ON gemini_interactions (group_id)
    WHERE settled = FALSE AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_gemini_interactions_pending_subscription ON gemini_interactions ((metadata->>'SubscriptionID'))
    WHERE settled = FALSE AND deleted_at IS NULL;

-- 后台结算需要原计费与路由身份；只阻止仍有未结算新协议任务的软删除。
CREATE OR REPLACE FUNCTION guard_gemini_interaction_dependency_delete() RETURNS TRIGGER AS $$
DECLARE
    pending BOOLEAN := FALSE;
    constraint_name TEXT;
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
        CASE TG_TABLE_NAME
        WHEN 'users' THEN
            SELECT EXISTS (SELECT 1 FROM gemini_interactions
                WHERE user_id = OLD.id AND settled = FALSE AND deleted_at IS NULL) INTO pending;
            constraint_name := 'gemini_interactions_user_pending';
        WHEN 'api_keys' THEN
            SELECT EXISTS (SELECT 1 FROM gemini_interactions
                WHERE api_key_id = OLD.id AND settled = FALSE AND deleted_at IS NULL) INTO pending;
            constraint_name := 'gemini_interactions_api_key_pending';
        WHEN 'accounts' THEN
            SELECT EXISTS (SELECT 1 FROM gemini_interactions
                WHERE account_id = OLD.id AND settled = FALSE AND deleted_at IS NULL) INTO pending;
            constraint_name := 'gemini_interactions_account_pending';
        WHEN 'groups' THEN
            SELECT EXISTS (SELECT 1 FROM gemini_interactions
                WHERE group_id = OLD.id AND settled = FALSE AND deleted_at IS NULL) INTO pending;
            constraint_name := 'gemini_interactions_group_pending';
        WHEN 'user_subscriptions' THEN
            SELECT EXISTS (SELECT 1 FROM gemini_interactions
                WHERE metadata->>'SubscriptionID' = OLD.id::text AND settled = FALSE AND deleted_at IS NULL) INTO pending;
            constraint_name := 'gemini_interactions_subscription_pending';
        ELSE
            RETURN NEW;
        END CASE;
        IF pending THEN
            RAISE EXCEPTION USING ERRCODE = '23503', CONSTRAINT = constraint_name,
                MESSAGE = '该资源有未结算的 Interactions 任务，请完成或取消并等待结算后再删除或撤销';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'gemini_interactions_user_delete_guard'
        AND tgrelid = 'users'::regclass) THEN
        CREATE TRIGGER gemini_interactions_user_delete_guard
            BEFORE UPDATE OF deleted_at ON users
            FOR EACH ROW EXECUTE FUNCTION guard_gemini_interaction_dependency_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'gemini_interactions_key_delete_guard'
        AND tgrelid = 'api_keys'::regclass) THEN
        CREATE TRIGGER gemini_interactions_key_delete_guard
            BEFORE UPDATE OF deleted_at ON api_keys
            FOR EACH ROW EXECUTE FUNCTION guard_gemini_interaction_dependency_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'gemini_interactions_account_delete_guard'
        AND tgrelid = 'accounts'::regclass) THEN
        CREATE TRIGGER gemini_interactions_account_delete_guard
            BEFORE UPDATE OF deleted_at ON accounts
            FOR EACH ROW EXECUTE FUNCTION guard_gemini_interaction_dependency_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'gemini_interactions_group_delete_guard'
        AND tgrelid = 'groups'::regclass) THEN
        CREATE TRIGGER gemini_interactions_group_delete_guard
            BEFORE UPDATE OF deleted_at ON groups
            FOR EACH ROW EXECUTE FUNCTION guard_gemini_interaction_dependency_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'gemini_interactions_subscription_delete_guard'
        AND tgrelid = 'user_subscriptions'::regclass) THEN
        CREATE TRIGGER gemini_interactions_subscription_delete_guard
            BEFORE UPDATE OF deleted_at ON user_subscriptions
            FOR EACH ROW EXECUTE FUNCTION guard_gemini_interaction_dependency_delete();
    END IF;
END;
$$;
