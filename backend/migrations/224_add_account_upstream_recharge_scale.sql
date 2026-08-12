-- 上游充值换算系数：1:10 的站点配置为 0.1，余额和声明倍率统一乘此系数。
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS upstream_recharge_scale DECIMAL(10,6) NOT NULL DEFAULT 1.0;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_upstream_recharge_scale_check;

ALTER TABLE accounts
    ADD CONSTRAINT accounts_upstream_recharge_scale_check
        CHECK (upstream_recharge_scale > 0);

-- 历史 HBY 账号沿用原有 1:10 口径；自动探测由下方统一开启。
UPDATE accounts
SET upstream_recharge_scale = 0.1
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type = 'apikey'
  AND LOWER(BTRIM(COALESCE(credentials->>'base_url', '')))
      ~ '^(https?://)?([^/@]+@)?hubway[.]cc(:[0-9]+)?(/|$)';

-- 所有适用的 OpenAI API Key 历史账号统一开启自动探测和 API 长上下文计费。
UPDATE accounts
SET extra = jsonb_set(
        jsonb_set(
            COALESCE(extra, '{}'::jsonb),
            '{upstream_billing_probe_enabled}',
            'true'::jsonb,
            true
        ),
        '{openai_long_context_billing_enabled}',
        'true'::jsonb,
        true
    )
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type = 'apikey';
