package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsAccountAlertDedupeMigration(t *testing.T) {
	content, err := FS.ReadFile("222_extend_ops_account_request_alerts.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE ops_alert_events ADD COLUMN IF NOT EXISTS dedupe_key")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_ops_alert_events_rule_dedupe_fired")
	require.Contains(t, sql, "ALTER TABLE ops_alert_account_details ADD COLUMN IF NOT EXISTS error_log_id")
	for _, column := range []string{
		"user_id", "user_email", "api_key_id", "api_key_name",
		"request_id", "client_request_id", "error_reason", "error_message",
		"requested_model", "upstream_model",
	} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+column)
	}
}
