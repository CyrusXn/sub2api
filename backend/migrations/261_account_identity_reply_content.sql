-- 仅初始化已有真官key账号；显式清空或自行配置过的内容不能被覆盖。
UPDATE accounts AS a
SET extra = COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object(
        'identity_reply_content',
        '我是 ChatGPT，由 OpenAI训练的 AI 助手。我可以用中文和你交流，帮你解答问题、写作、翻译、编程等。'
    ),
    updated_at = NOW()
WHERE a.deleted_at IS NULL
  AND a.platform = 'openai'
  AND a.type = 'apikey'
  AND NOT (COALESCE(a.extra, '{}'::jsonb) ? 'identity_reply_content')
  AND EXISTS (
      SELECT 1
      FROM account_groups AS ag
      JOIN groups AS g ON g.id = ag.group_id
      WHERE ag.account_id = a.id
        AND g.deleted_at IS NULL
        AND g.platform = 'openai'
        AND regexp_replace(lower(g.name), '[[:space:]]', '', 'g') LIKE '%真官key%'
  );
