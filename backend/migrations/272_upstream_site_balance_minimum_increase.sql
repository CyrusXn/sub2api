-- 新规则只提醒单次至少 1 个余额单位的上涨；清除旧小额提醒，保留最新采样基线。
-- 清空提醒金额及发现时间，并递增版本，避免后续涨幅重新合入旧提醒。
UPDATE upstream_site_balance_observations
SET alert_active = FALSE,
    alert_old_balance = NULL,
    alert_new_balance = NULL,
    alert_delta = NULL,
    alert_detected_at = NULL,
    alert_version = alert_version + 1,
    updated_at = NOW()
WHERE alert_active = TRUE AND alert_delta < 1;
