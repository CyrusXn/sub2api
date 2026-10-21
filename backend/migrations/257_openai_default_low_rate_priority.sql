-- 默认调度启用低倍率优先；复用已有开关，管理员仍可在发布后显式关闭。
INSERT INTO settings (key, value)
VALUES ('openai_low_upstream_rate_priority_enabled', 'true')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
