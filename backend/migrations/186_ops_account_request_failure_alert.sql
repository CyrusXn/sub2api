-- 账号连接/请求异常采用错误落库事件即时触发，定时评估器仅负责健康恢复。
INSERT INTO ops_alert_rules (
    name,
    description,
    enabled,
    metric_type,
    operator,
    threshold,
    window_minutes,
    sustained_minutes,
    severity,
    notify_email,
    cooldown_minutes,
    created_at,
    updated_at
) VALUES (
    '账号请求异常',
    '上游账号连接、凭证获取、重试耗尽或无可用账号时立即触发，并自动判别余额不足、全部不可用或部分异常。',
    true,
    'account_request_failure',
    '>',
    0,
    5,
    1,
    'P1',
    true,
    10,
    NOW(),
    NOW()
) ON CONFLICT (name) DO NOTHING;
