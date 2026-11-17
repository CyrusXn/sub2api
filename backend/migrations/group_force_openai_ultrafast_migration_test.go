package migrations

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupForceOpenAIUltrafastMigration(t *testing.T) {
	const migration = "273_group_force_openai_ultrafast.sql"
	files, err := fs.Glob(FS, "273_*.sql")
	require.NoError(t, err)
	require.Equal(t, []string{migration}, files, "新增迁移必须使用独立编号")
	_, err = FS.ReadFile("251_group_force_openai_ultrafast.sql")
	require.ErrorIs(t, err, fs.ErrNotExist, "官方旧编号不得与已发布的 251_group_i18n 并存")
	content, err := FS.ReadFile(migration)
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql,
		"ADD COLUMN IF NOT EXISTS force_openai_ultrafast BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.force_openai_ultrafast")
}
