package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestV0216MigrationsFollowCustomBaseline(t *testing.T) {
	// 已上线定制迁移不能改名；新发版迁移从 251 起，避免覆盖历史 242–250。
	for _, name := range []string{"251_group_i18n.sql", "252_gemini_cached_contents.sql", "253_gemini_cached_contents_channel_usage.sql", "254_gemini_cached_contents_mutation_lease.sql"} {
		files, err := fs.Glob(FS, name[:3]+"_*.sql")
		if err != nil || len(files) != 1 || files[0] != name {
			t.Fatalf("新增迁移编号不唯一: %s: %v (%v)", name, files, err)
		}
		content, err := fs.ReadFile(FS, name)
		if err != nil || !strings.Contains(string(content), "IF NOT EXISTS") {
			t.Fatalf("新增迁移缺失或不可重试: %s (%v)", name, err)
		}
	}
}
