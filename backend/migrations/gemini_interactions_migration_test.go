package migrations

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestGeminiInteractionsMigrationIsAdditiveAndContainsRecoveryMetadata(t *testing.T) {
	b, err := FS.ReadFile("255_gemini_interactions.sql")
	require.NoError(t, err)
	s := strings.Join(strings.Fields(string(b)), " ")
	require.Contains(t, s, "CREATE TABLE IF NOT EXISTS gemini_interactions")
	for _, field := range []string{"public_id", "upstream_id", "api_key_id", "user_id", "group_id", "account_id", "metadata JSONB", "usage JSONB", "hold_amount NUMERIC(20,8)", "next_poll_at TIMESTAMPTZ"} {
		require.Contains(t, s, field)
	}
	require.NotContains(t, s, "ALTER TABLE")
	require.NotContains(t, s, "DROP TABLE")
	require.NotContains(t, s, "prompt TEXT")
	require.NotContains(t, s, "api_key TEXT")
	require.Contains(t, s, "WHERE settled = FALSE AND deleted_at IS NULL AND upstream_id <> ''")
	require.Contains(t, s, "BEFORE UPDATE OF deleted_at ON api_keys")
	require.Contains(t, s, "gemini_interactions_api_key_pending")
	for _, dependency := range []string{"accounts", "groups", "user_subscriptions", "users"} {
		require.Contains(t, s, "BEFORE UPDATE OF deleted_at ON "+dependency)
	}
	for _, constraint := range []string{"account", "group", "subscription", "user"} {
		require.Contains(t, s, "gemini_interactions_"+constraint+"_pending")
	}
}
