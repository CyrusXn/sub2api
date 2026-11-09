-- 仅将授信账号最早一笔 5000 元加款从经营历史充值额中冲销；后续充值仍按普通流水统计。
DO $$
DECLARE
    credit_date date;
BEGIN
    SELECT (rc.used_at AT TIME ZONE 'Asia/Shanghai')::date
    INTO credit_date
    FROM redeem_codes rc
    JOIN users u ON u.id = rc.used_by
    WHERE LOWER(TRIM(u.email)) = '1002137535@qq.com'
      AND rc.status = 'used'
      AND rc.type IN ('balance', 'admin_balance')
      AND rc.value = 5000
    ORDER BY rc.used_at, rc.id
    LIMIT 1;

    IF credit_date IS NULL THEN
        RETURN;
    END IF;

    UPDATE dashboard_business_daily
    SET recharge_amount = recharge_amount - 5000
    WHERE bucket_date = credit_date
      AND recharge_amount >= 5000;

    IF NOT FOUND THEN
        RAISE EXCEPTION '授信充值经营历史冲销失败：未找到包含该笔 5000 元流水的日汇总';
    END IF;
END;
$$;
