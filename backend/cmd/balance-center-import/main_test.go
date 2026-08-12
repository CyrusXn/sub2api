package main

import (
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
}
