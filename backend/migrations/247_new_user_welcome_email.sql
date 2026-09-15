-- 与用户创建同事务入队，覆盖注册、第三方登录及后台新增；不向历史用户补发。
CREATE OR REPLACE FUNCTION enqueue_new_user_welcome_email() RETURNS TRIGGER LANGUAGE plpgsql AS $function$
DECLARE
    recipient TEXT := LOWER(BTRIM(NEW.email));
    ready_at TIMESTAMPTZ := NOW();
BEGIN
    -- 第三方登录的占位邮箱不能投递，任务保留到绑定真实邮箱后再发送。
    IF recipient = '' OR POSITION('@' IN recipient) <= 1 OR recipient LIKE '%.invalid' THEN
        ready_at := 'infinity'::TIMESTAMPTZ;
    END IF;

    IF TG_OP = 'INSERT' THEN
        INSERT INTO alert_email_outbox (
            source_type, source_id, source_key, alert_type, recipient_email,
            subject, body_html, aggregate_until, available_at
        ) VALUES (
            'user_welcome', NEW.id::TEXT, 'user:' || NEW.id::TEXT, 'user_welcome', recipient,
            '欢迎加入 xn API｜添加微信领取试用额度与交流群邀请',
            $html$<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"></head>
<body style="margin:0;padding:24px;background:#f4f6f8;font-family:Arial,'Microsoft YaHei',sans-serif;color:#243047;line-height:1.8;">
<div style="max-width:600px;margin:0 auto;padding:32px;background:#ffffff;border-radius:16px;">
  <h1 style="margin:0 0 20px;font-size:24px;">欢迎加入 xn API 🎉</h1>
  <p>你好，欢迎成为 xn API 的一员！你的账号已创建，邀请你领取试用体验额度，亲自感受 Astra 不降智、高速、稳定的使用体验。</p>
  <div style="margin:24px 0;padding:20px;background:#eef4ff;border-radius:12px;text-align:center;">
    <div>添加微信，领取试用体验额度</div>
    <strong style="display:block;margin:8px 0;font-size:28px;letter-spacing:1px;">ncwqwert</strong>
    <div>添加时请备注「xn API 试用」</div>
  </div>
  <p>添加后，我们会协助你领取 xn API 试用体验额度，并邀请你加入用户交流群。</p>
  <p>群内每天分享醍醐测试结果，一起关注模型表现、交流使用心得，及时了解服务动态。</p>
  <p>期待在群里与你见面，祝你使用愉快！</p>
  <p style="margin-top:28px;color:#64748b;">xn API 团队</p>
</div></body></html>$html$ || '<!-- welcome-user:' || NEW.id::TEXT || ' -->',
            NOW(), ready_at
        ) ON CONFLICT (source_type, source_key, recipient_email) DO NOTHING;
    ELSE
        -- 只激活本次功能启用后创建且尚未具备真实邮箱的任务；改邮箱不重复发信。
        UPDATE alert_email_outbox
        SET recipient_email = recipient, available_at = ready_at, updated_at = NOW()
        WHERE source_type = 'user_welcome' AND source_key = 'user:' || NEW.id::TEXT
          AND status = 'pending' AND available_at = 'infinity'::TIMESTAMPTZ;
    END IF;
    RETURN NEW;
END;
$function$;

DROP TRIGGER IF EXISTS users_enqueue_welcome_email ON users;
CREATE TRIGGER users_enqueue_welcome_email
AFTER INSERT OR UPDATE OF email ON users
FOR EACH ROW EXECUTE FUNCTION enqueue_new_user_welcome_email();
