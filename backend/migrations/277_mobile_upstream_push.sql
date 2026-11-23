ALTER TABLE mobile_alerts ADD COLUMN upstream_host TEXT;

CREATE TABLE mobile_upstream_balance_states (
    host TEXT PRIMARY KEY,
    alert_active BOOLEAN NOT NULL DEFAULT FALSE,
    wallet_amount DOUBLE PRECISION,
    wallet_scale DOUBLE PRECISION NOT NULL DEFAULT 1,
    wallet_observed_at TIMESTAMPTZ,
    empty_detected_at TIMESTAMPTZ
);

CREATE FUNCTION mobile_upstream_label(p_host TEXT) RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT left(regexp_replace(COALESCE(NULLIF(btrim(credential.display_name), ''),
        NULLIF(btrim(wallet.display_name), ''), NULLIF(substring(site.display_name FROM '^[^【]*【([^】]+)】'), ''), h.host),
        '[[:cntrl:]]', '', 'g'), 100)
    FROM (SELECT p_host AS host) h
    LEFT JOIN upstream_site_credentials credential USING (host)
    LEFT JOIN upstream_site_balance_observations wallet USING (host)
    LEFT JOIN balance_center_sites site ON site.normalized_domain = h.host;
$$;

-- 站点钱包和余额耗尽共用同一状态。密钥限额和订阅额度不参与站点余额计算。
CREATE FUNCTION refresh_mobile_upstream_balance(
    p_host TEXT, p_threshold DOUBLE PRECISION, p_wallet_amount DOUBLE PRECISION,
    p_wallet_scale DOUBLE PRECISION, p_wallet_at TIMESTAMPTZ, p_empty_at TIMESTAMPTZ
) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE
    was_low BOOLEAN;
    amount DOUBLE PRECISION;
    wallet_amount DOUBLE PRECISION;
    wallet_scale DOUBLE PRECISION;
    wallet_at TIMESTAMPTZ;
    empty_at TIMESTAMPTZ;
    title_text TEXT;
    event_kind TEXT;
BEGIN
    p_host := rtrim(lower(btrim(p_host)), '.');
    IF p_host IS NULL OR p_host = '' THEN RETURN; END IF;
    INSERT INTO mobile_upstream_balance_states (host) VALUES (p_host) ON CONFLICT DO NOTHING;
    SELECT alert_active, s.wallet_amount, s.wallet_scale, wallet_observed_at, empty_detected_at
        INTO was_low, wallet_amount, wallet_scale, wallet_at, empty_at
        FROM mobile_upstream_balance_states s WHERE host = p_host FOR UPDATE;

    IF p_wallet_at IS NOT NULL AND (wallet_at IS NULL OR p_wallet_at > wallet_at) THEN
        wallet_at := p_wallet_at;
        wallet_amount := p_wallet_amount;
        wallet_scale := p_wallet_scale;
        UPDATE mobile_upstream_balance_states SET wallet_amount = p_wallet_amount, wallet_scale = p_wallet_scale, wallet_observed_at = p_wallet_at
            WHERE host = p_host;
    END IF;
    IF p_empty_at IS NOT NULL AND (empty_at IS NULL OR p_empty_at > empty_at) THEN
        empty_at := p_empty_at;
        UPDATE mobile_upstream_balance_states SET empty_detected_at = p_empty_at WHERE host = p_host;
    END IF;

    IF empty_at IS NOT NULL AND (wallet_at IS NULL OR wallet_at < empty_at) THEN
        -- 请求拒绝发生后，旧快照不能冒充当前钱包余额。
        event_kind := 'upstream_balance_empty';
    ELSIF wallet_at >= now() - interval '15 minutes' THEN
        IF wallet_amount * wallet_scale < p_threshold THEN
            amount := wallet_amount;
            event_kind := 'low_balance';
        ELSIF was_low THEN
            UPDATE mobile_upstream_balance_states SET alert_active = FALSE WHERE host = p_host;
        END IF;
    END IF;
    IF event_kind IS NULL OR was_low THEN RETURN; END IF;

    title_text := '『' || mobile_upstream_label(p_host) || '』余额不足，当前余额' ||
        COALESCE(to_char(amount::numeric, 'FM999999999999999999999999990.00'), '未知');
    -- 与倍率事件共用提交顺序锁，断线游标不会越过未提交事件。
    PERFORM pg_advisory_xact_lock(20914, 276);
    INSERT INTO mobile_alerts (kind, title, body, upstream_host) VALUES (event_kind, title_text, '', p_host);
    UPDATE mobile_upstream_balance_states SET alert_active = TRUE WHERE host = p_host;
END;
$$;

CREATE INDEX idx_mobile_alerts_upstream_rate ON mobile_alerts (upstream_host, created_at DESC)
    WHERE upstream_host IS NOT NULL AND kind = 'multiplier_changed';

CREATE FUNCTION append_mobile_upstream_rate(p_host TEXT, p_change TEXT) RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    p_host := rtrim(lower(btrim(p_host)), '.');
    IF p_host IS NULL OR p_host = '' THEN RETURN; END IF;
    PERFORM pg_advisory_xact_lock(20914, 276);
    -- 同站同一倍率变化短时间内可能由多个账号同时发现。
    IF EXISTS (SELECT 1 FROM mobile_alerts WHERE upstream_host = p_host AND kind = 'multiplier_changed'
               AND created_at >= now() - interval '5 minutes' AND body = p_change) THEN RETURN; END IF;
    INSERT INTO mobile_alerts (kind, title, body, upstream_host)
    VALUES ('multiplier_changed', '『' || mobile_upstream_label(p_host) || '』倍率变化', p_change, p_host);
END;
$$;

-- 事务提交后才唤醒其他实例，事件内容仍从持久表按游标读取。
CREATE FUNCTION notify_mobile_alert_stream() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('sub2api_mobile_alerts', '');
    RETURN NULL;
END;
$$;

CREATE TRIGGER notify_mobile_alert_stream AFTER INSERT ON mobile_alerts
FOR EACH STATEMENT EXECUTE FUNCTION notify_mobile_alert_stream();

CREATE FUNCTION mobile_upstream_balance_threshold() RETURNS DOUBLE PRECISION LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT CASE WHEN value ~ '^[0-9]{1,20}(\.[0-9]{1,20})?$'
                                THEN value::double precision END
                     FROM settings WHERE key = 'balance_center_low_balance_threshold'), 5);
$$;

CREATE OR REPLACE FUNCTION capture_mobile_ops_balance_alert()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE host TEXT;
BEGIN
    IF COALESCE(NEW.dimensions ->> 'account_id', '') !~ '^[0-9]{1,18}$' THEN RETURN NEW; END IF;
    SELECT COALESCE(site.normalized_domain, substring(lower(btrim(acc.credentials ->> 'base_url')) FROM '^https?://([a-z0-9.-]+)([:/?#]|$)'))
        INTO host FROM accounts acc
        LEFT JOIN balance_center_current_states cs ON cs.account_id = acc.id
        LEFT JOIN balance_center_sites site ON site.id = cs.site_id
        WHERE acc.id = (NEW.dimensions ->> 'account_id')::bigint;
    PERFORM refresh_mobile_upstream_balance(host, mobile_upstream_balance_threshold(), NULL, NULL, NULL, NEW.fired_at);
    RETURN NEW;
END;
$$;
