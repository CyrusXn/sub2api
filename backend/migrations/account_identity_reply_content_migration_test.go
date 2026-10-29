package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountIdentityReplyContentMigration(t *testing.T) {
	content, err := FS.ReadFile("261_account_identity_reply_content.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, guard := range []string{
		"a.deleted_at IS NULL", "a.platform = 'openai'", "a.type = 'apikey'",
		"NOT (COALESCE(a.extra, '{}'::jsonb) ? 'identity_reply_content')",
		"ag.account_id = a.id", "g.deleted_at IS NULL", "g.platform = 'openai'",
		"regexp_replace(lower(g.name), '[[:space:]]', '', 'g') LIKE '%真官key%'",
		"COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object(",
		"我是 ChatGPT，由 OpenAI训练的 AI 助手。我可以用中文和你交流，帮你解答问题、写作、翻译、编程等。",
	} {
		require.Contains(t, sql, guard)
	}
}
