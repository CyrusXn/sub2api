-- 蓝绿发布和应用回退期间保留旧列，避免官方改名使仍在服务的 0.2.1 实例查询失败。
-- 先补齐两列，后续官方迁移会沿用双列分支，不执行破坏旧实例的重命名。
SET LOCAL lock_timeout = '5s';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb;
UPDATE groups SET model_allowlist = models_list_config
WHERE model_allowlist = '{}'::jsonb AND models_list_config <> '{}'::jsonb;
UPDATE groups SET models_list_config = model_allowlist
WHERE models_list_config IS DISTINCT FROM model_allowlist;

-- 旧实例写旧列、新实例写新列；仅同步发生变化的一侧，允许清空配置并支持回退后的写入。
CREATE OR REPLACE FUNCTION sync_group_model_allowlist_compat() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF COALESCE(NEW.model_allowlist, '{}'::jsonb) = '{}'::jsonb THEN
            NEW.model_allowlist := COALESCE(NEW.models_list_config, '{}'::jsonb);
        END IF;
        NEW.models_list_config := NEW.model_allowlist;
    ELSIF NEW.model_allowlist IS DISTINCT FROM OLD.model_allowlist THEN
        NEW.models_list_config := NEW.model_allowlist;
    ELSIF NEW.models_list_config IS DISTINCT FROM OLD.models_list_config THEN
        NEW.model_allowlist := NEW.models_list_config;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS group_model_allowlist_compat ON groups;
CREATE TRIGGER group_model_allowlist_compat
BEFORE INSERT OR UPDATE ON groups
FOR EACH ROW EXECUTE FUNCTION sync_group_model_allowlist_compat();
