package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestParseImportConfigDefaultsToDryRun(t *testing.T) {
	cfg, err := parseImportConfig([]string{
		"--sqlite", "/tmp/sub2-web.sqlite",
		"--target-dsn", "postgres://sub2api@example/sub2api?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("parseImportConfig returned error: %v", err)
	}
	if cfg.Execute {
		t.Fatal("导入命令必须默认 dry-run，只有显式 --execute 才能写入")
	}
	if cfg.SQLitePath != "/tmp/sub2-web.sqlite" {
		t.Fatalf("sqlite path = %q", cfg.SQLitePath)
	}
}

func TestParseImportConfigRequiresExecuteConfirmation(t *testing.T) {
	_, err := parseImportConfig([]string{
		"--sqlite", "/tmp/sub2-web.sqlite",
		"--target-dsn", "postgres://sub2api@example/sub2api?sslmode=disable",
		"--execute",
	})
	if err == nil {
		t.Fatal("--execute without --confirm-import should fail")
	}
}

func TestParseImportConfigRequiresManifestForExecute(t *testing.T) {
	_, err := parseImportConfig([]string{
		"--sqlite", "/tmp/sub2-web.sqlite",
		"--target-dsn", "postgres://sub2api@example/sub2api?sslmode=disable",
		"--execute",
		"--confirm-import",
	})
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("--execute without --manifest should fail, got %v", err)
	}
}

func TestParseImportConfigRejectsWritingManifestDuringExecute(t *testing.T) {
	_, err := parseImportConfig([]string{
		"--sqlite", "/tmp/sub2-web.sqlite",
		"--target-dsn", "postgres://sub2api@example/sub2api?sslmode=disable",
		"--manifest", "/tmp/import-manifest.json",
		"--write-manifest", "/tmp/next-manifest.json",
		"--execute",
		"--confirm-import",
	})
	if err == nil {
		t.Fatal("--write-manifest must be dry-run only")
	}
}

func TestParseImportConfigRejectsMissingInputs(t *testing.T) {
	if _, err := parseImportConfig([]string{"--target-dsn", "postgres://sub2api@example/sub2api"}); err == nil {
		t.Fatal("missing --sqlite should fail")
	}
	if _, err := parseImportConfig([]string{"--sqlite", "/tmp/sub2-web.sqlite"}); err == nil {
		t.Fatal("missing target DSN should fail")
	}
}

func TestLegacySQLiteReadOnlyDSNUsesReadOnlyMode(t *testing.T) {
	dsn := legacySQLiteReadOnlyDSN("/tmp/sub2 web.sqlite")
	if !strings.Contains(dsn, "mode=ro") {
		t.Fatalf("SQLite DSN must enforce read-only mode: %q", dsn)
	}
	if !strings.Contains(dsn, "sub2%20web.sqlite") {
		t.Fatalf("SQLite path must be URL escaped: %q", dsn)
	}
	if !strings.Contains(dsn, "immutable=1") {
		t.Fatalf("SQLite import must require an immutable backup: %q", dsn)
	}
}

func TestImportHelpUsesFrozenBackupPath(t *testing.T) {
	var output bytes.Buffer
	fs := flag.NewFlagSet("balance-center-import", flag.ContinueOnError)
	fs.SetOutput(&output)
	registerImportFlags(fs, &importConfig{})

	if err := fs.Parse([]string{"--help"}); err != flag.ErrHelp {
		t.Fatalf("parse --help error = %v, want flag.ErrHelp", err)
	}
	help := output.String()
	if !strings.Contains(help, "冻结备份") || strings.Contains(help, "/opt/sub2-web/data/sub2-web.sqlite") {
		t.Fatalf("帮助文本必须引导使用冻结备份，实际输出：%s", help)
	}
}
