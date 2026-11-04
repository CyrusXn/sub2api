package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelQualityOfficialMigration(t *testing.T) {
	content, err := FS.ReadFile("265_model_quality_official.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, guard := range []string{
		"theme IN ('pelican', 'candy', 'official', 'custom')",
		"ADD COLUMN IF NOT EXISTS official_slot TIMESTAMPTZ",
		"CREATE UNIQUE INDEX IF NOT EXISTS model_quality_runs_official_slot_idx",
		"ON model_quality_runs (official_slot) WHERE official_slot IS NOT NULL",
	} {
		require.Contains(t, sql, guard)
	}
}
