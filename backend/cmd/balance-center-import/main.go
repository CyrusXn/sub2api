// balance-center-import 从旧 sub2-web SQLite 只读导入余额中心历史数据。
// 默认仅 dry-run；只有同时传入 --execute 与 --confirm-import 才会写入 PostgreSQL。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

type importConfig struct {
	SQLitePath          string
	TargetDSN           string
	OldMasterKey        string
	EncryptionKeyHex    string
	ExpectedManualTotal float64
	Execute             bool
	ConfirmImport       bool
}

func main() {
	cfg, err := parseImportConfig(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	report, err := runImport(context.Background(), cfg)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fatal(fmt.Errorf("输出导入报告失败: %w", err))
	}
}

func parseImportConfig(args []string) (importConfig, error) {
	cfg := importConfig{
		SQLitePath:          strings.TrimSpace(os.Getenv("BALANCE_CENTER_SQLITE")),
		TargetDSN:           strings.TrimSpace(os.Getenv("BALANCE_CENTER_TARGET_DSN")),
		OldMasterKey:        strings.TrimSpace(os.Getenv("SUB2_WEB_MASTER_KEY")),
		EncryptionKeyHex:    strings.TrimSpace(os.Getenv("TOTP_ENCRYPTION_KEY")),
		ExpectedManualTotal: service.BalanceCenterLegacyExpectedManual,
	}
	fs := flag.NewFlagSet("balance-center-import", flag.ContinueOnError)
	fs.StringVar(&cfg.SQLitePath, "sqlite", cfg.SQLitePath, "旧 sub2-web SQLite 路径，例如 /opt/sub2-web/data/sub2-web.sqlite")
	fs.StringVar(&cfg.TargetDSN, "target-dsn", cfg.TargetDSN, "目标 PostgreSQL DSN；也可用 BALANCE_CENTER_TARGET_DSN")
	fs.StringVar(&cfg.OldMasterKey, "old-master-key", cfg.OldMasterKey, "旧 sub2-web master key；仅用于内存匹配和可选网页登录密码迁移")
	fs.StringVar(&cfg.EncryptionKeyHex, "encryption-key-hex", cfg.EncryptionKeyHex, "新系统 TOTP/站点凭据 AES-256 hex key；仅迁移网页登录密码时需要")
	fs.Float64Var(&cfg.ExpectedManualTotal, "expected-manual-total", cfg.ExpectedManualTotal, "手工基线总额强校验")
	fs.BoolVar(&cfg.Execute, "execute", false, "正式写入；默认 false 表示 dry-run 并回滚事务")
	fs.BoolVar(&cfg.ConfirmImport, "confirm-import", false, "配合 --execute 使用，确认本次会写入目标库")
	if err := fs.Parse(args); err != nil {
		return importConfig{}, err
	}
	cfg.SQLitePath = strings.TrimSpace(cfg.SQLitePath)
	cfg.TargetDSN = strings.TrimSpace(cfg.TargetDSN)
	cfg.OldMasterKey = strings.TrimSpace(cfg.OldMasterKey)
	cfg.EncryptionKeyHex = strings.TrimSpace(cfg.EncryptionKeyHex)
	if cfg.SQLitePath == "" {
		return importConfig{}, errors.New("必须提供 --sqlite 或 BALANCE_CENTER_SQLITE")
	}
	if cfg.TargetDSN == "" {
		return importConfig{}, errors.New("必须提供 --target-dsn 或 BALANCE_CENTER_TARGET_DSN")
	}
	if cfg.Execute && !cfg.ConfirmImport {
		return importConfig{}, errors.New("正式写入必须同时传入 --confirm-import")
	}
	if cfg.ExpectedManualTotal <= 0 {
		return importConfig{}, errors.New("expected-manual-total 必须大于 0")
	}
	return cfg, nil
}

func runImport(ctx context.Context, cfg importConfig) (*service.BalanceCenterLegacyImportReport, error) {
	sqliteDB, err := sql.Open("sqlite", legacySQLiteReadOnlyDSN(cfg.SQLitePath))
	if err != nil {
		return nil, fmt.Errorf("打开旧 SQLite 失败: %w", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, fmt.Errorf("设置旧 SQLite 只读模式失败: %w", err)
	}

	targetDB, err := sql.Open("postgres", cfg.TargetDSN)
	if err != nil {
		return nil, fmt.Errorf("打开目标 PostgreSQL 失败: %w", err)
	}
	defer targetDB.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := targetDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("连接目标 PostgreSQL 失败: %w", err)
	}

	options := service.BalanceCenterLegacyImportOptions{
		Execute:             cfg.Execute,
		OldMasterKey:        cfg.OldMasterKey,
		ExpectedManualTotal: cfg.ExpectedManualTotal,
	}
	if cfg.OldMasterKey != "" && cfg.EncryptionKeyHex != "" {
		encryptor, err := repository.NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: cfg.EncryptionKeyHex}})
		if err != nil {
			return nil, fmt.Errorf("初始化新系统凭据加密器失败: %w", err)
		}
		options.PasswordEncryptor = encryptor
	}
	return service.ImportLegacyBalanceCenterSQLite(ctx, sqliteDB, targetDB, options)
}

func legacySQLiteReadOnlyDSN(path string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
