-- 避免迁移等待表锁时长时间阻塞仍在运行的旧实例。
SET LOCAL lock_timeout = '2s';
-- 微信只读取邮件成功投递后的镜像，原手机告警继续使用原来的种类。
ALTER TABLE mobile_alerts DROP CONSTRAINT mobile_alerts_kind_check;
ALTER TABLE mobile_alerts ADD CONSTRAINT mobile_alerts_kind_check
    CHECK (kind IN ('low_balance', 'multiplier_changed', 'upstream_balance_empty', 'email_mirror'));
ALTER TABLE mobile_alerts ADD COLUMN email_batch_key TEXT;
CREATE UNIQUE INDEX idx_mobile_alerts_email_batch_key
    ON mobile_alerts (email_batch_key) WHERE email_batch_key IS NOT NULL;
