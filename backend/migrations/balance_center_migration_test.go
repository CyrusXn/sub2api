package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceCenterMigrationDefinesIndependentHistoryAndCurrentState(t *testing.T) {
	content, err := FS.ReadFile("225_add_balance_center.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, table := range []string{
		"balance_center_sites",
		"balance_center_account_bindings",
		"balance_center_legacy_keys",
		"balance_center_snapshots",
		"balance_center_current_states",
		"balance_center_alert_states",
		"balance_center_manual_rows",
		"balance_center_recharge_events",
		"balance_center_automatic_records",
		"balance_center_liandong_orders",
		"balance_center_reconciliations",
		"balance_center_alert_deliveries",
	} {
		require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS "+table)
	}
	require.Contains(t, sql, "UNIQUE (source, source_key)")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_balance_center_snapshots_site_time")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_balance_center_snapshots_account_time")
	require.Contains(t, sql, "CHECK (status IN ('ok', 'unsupported', 'failed', 'unknown'))")
}

func TestBalanceCenterLiandongSessionMigrationStoresCiphertextOnly(t *testing.T) {
	content, err := FS.ReadFile("227_add_balance_center_liandong_session.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS balance_center_liandong_sessions")
	require.Contains(t, sql, "request_encrypted TEXT NOT NULL")
	require.NotContains(t, sql, "cookie")
	require.NotContains(t, sql, "request_headers")
}

func TestAlertEmailOutboxMigrationDefinesPersistentAggregationQueue(t *testing.T) {
	content, err := FS.ReadFile("229_add_alert_email_outbox.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS alert_email_outbox")
	require.Contains(t, sql, "UNIQUE (source_type, source_key, recipient_email)")
	require.Contains(t, sql, "available_at TIMESTAMPTZ NOT NULL")
	require.Contains(t, sql, "WHERE status = 'pending'")
}

func TestRechargeSiteLabelMigrationPreservesUnboundLegacySites(t *testing.T) {
	content, err := FS.ReadFile("230_add_recharge_event_site_label.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE balance_center_recharge_events")
	require.Contains(t, sql, "site_label VARCHAR(255) NOT NULL DEFAULT ''")
}
