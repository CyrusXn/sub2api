// balance-center-import 从旧 sub2-web SQLite 只读导入余额中心历史数据。
// 默认仅 dry-run；只有同时传入 --execute 与 --confirm-import 才会写入 PostgreSQL。
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	SQLitePath        string
	TargetDSN         string
	OldMasterKey      string
	EncryptionKeyHex  string
	ManifestPath      string
	WriteManifestPath string
	Execute           bool
	ConfirmImport     bool
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
		SQLitePath:       strings.TrimSpace(os.Getenv("BALANCE_CENTER_SQLITE")),
		TargetDSN:        strings.TrimSpace(os.Getenv("BALANCE_CENTER_TARGET_DSN")),
		OldMasterKey:     strings.TrimSpace(os.Getenv("SUB2_WEB_MASTER_KEY")),
		EncryptionKeyHex: strings.TrimSpace(os.Getenv("TOTP_ENCRYPTION_KEY")),
	}
	fs := flag.NewFlagSet("balance-center-import", flag.ContinueOnError)
	registerImportFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return importConfig{}, err
	}
	cfg.SQLitePath = strings.TrimSpace(cfg.SQLitePath)
	cfg.TargetDSN = strings.TrimSpace(cfg.TargetDSN)
	cfg.OldMasterKey = strings.TrimSpace(cfg.OldMasterKey)
	cfg.EncryptionKeyHex = strings.TrimSpace(cfg.EncryptionKeyHex)
	cfg.ManifestPath = strings.TrimSpace(cfg.ManifestPath)
	cfg.WriteManifestPath = strings.TrimSpace(cfg.WriteManifestPath)
	if cfg.SQLitePath == "" {
		return importConfig{}, errors.New("必须提供 --sqlite 或 BALANCE_CENTER_SQLITE")
	}
	if cfg.TargetDSN == "" {
		return importConfig{}, errors.New("必须提供 --target-dsn 或 BALANCE_CENTER_TARGET_DSN")
	}
	if cfg.Execute && !cfg.ConfirmImport {
		return importConfig{}, errors.New("正式写入必须同时传入 --confirm-import")
	}
	if cfg.Execute && cfg.ManifestPath == "" {
		return importConfig{}, errors.New("正式写入必须提供 dry-run 生成的 --manifest")
	}
	if cfg.Execute && cfg.WriteManifestPath != "" {
		return importConfig{}, errors.New("--write-manifest 仅允许在 dry-run 使用")
	}
	return cfg, nil
}

func registerImportFlags(fs *flag.FlagSet, cfg *importConfig) {
	fs.StringVar(&cfg.SQLitePath, "sqlite", cfg.SQLitePath, "旧 sub2-web SQLite 冻结备份路径，例如 /tmp/sub2-web-frozen.sqlite")
	fs.StringVar(&cfg.TargetDSN, "target-dsn", cfg.TargetDSN, "目标 PostgreSQL DSN；也可用 BALANCE_CENTER_TARGET_DSN")
	fs.StringVar(&cfg.OldMasterKey, "old-master-key", cfg.OldMasterKey, "旧 sub2-web master key；仅用于内存匹配和可选网页登录密码迁移")
	fs.StringVar(&cfg.EncryptionKeyHex, "encryption-key-hex", cfg.EncryptionKeyHex, "新系统 TOTP/站点凭据 AES-256 hex key；仅迁移网页登录密码时需要")
	fs.StringVar(&cfg.ManifestPath, "manifest", "", "dry-run 生成的不可变备份清单；正式导入必填")
	fs.StringVar(&cfg.WriteManifestPath, "write-manifest", "", "dry-run 成功后写出当前备份清单")
	fs.BoolVar(&cfg.Execute, "execute", false, "正式写入；默认 false 表示 dry-run 并回滚事务")
	fs.BoolVar(&cfg.ConfirmImport, "confirm-import", false, "配合 --execute 使用，确认本次会写入目标库")
}

func runImport(ctx context.Context, cfg importConfig) (*service.BalanceCenterLegacyImportReport, error) {
	if _, err := os.Stat(cfg.SQLitePath + "-wal"); err == nil {
		return nil, fmt.Errorf("拒绝读取仍有 WAL 的活动 SQLite；请先生成冻结备份")
	}
	if _, err := os.Stat(cfg.SQLitePath + "-shm"); err == nil {
		return nil, fmt.Errorf("拒绝读取仍有 SHM 的活动 SQLite；请先生成冻结备份")
	}
	sourceSHA256, err := fileSHA256(cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("计算旧 SQLite SHA256 失败: %w", err)
	}
	var expectedManifest *service.BalanceCenterLegacyImportManifest
	if cfg.ManifestPath != "" {
		expectedManifest, err = readImportManifest(cfg.ManifestPath)
		if err != nil {
			return nil, err
		}
	}
	sqliteDB, err := sql.Open("sqlite", legacySQLiteReadOnlyDSN(cfg.SQLitePath))
	if err != nil {
		return nil, fmt.Errorf("打开旧 SQLite 失败: %w", err)
	}
	defer sqliteDB.Close()
	if _, err := sqliteDB.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, fmt.Errorf("设置旧 SQLite 只读模式失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	targetDB, err := sql.Open("postgres", cfg.TargetDSN)
	if err != nil {
		return nil, fmt.Errorf("打开目标 PostgreSQL 失败: %w", err)
	}
	defer targetDB.Close()
	if err := targetDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("连接目标 PostgreSQL 失败: %w", err)
	}

	options := service.BalanceCenterLegacyImportOptions{
		Execute:          cfg.Execute,
		OldMasterKey:     cfg.OldMasterKey,
		SourceSHA256:     sourceSHA256,
		ExpectedManifest: expectedManifest,
		VerifySource: func() error {
			sourceAfter, hashErr := fileSHA256(cfg.SQLitePath)
			if hashErr != nil || sourceAfter != sourceSHA256 {
				return fmt.Errorf("旧 SQLite 在读取期间发生变化，必须重新生成冻结备份")
			}
			return nil
		},
	}
	if cfg.OldMasterKey != "" && cfg.EncryptionKeyHex != "" {
		encryptor, err := repository.NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: cfg.EncryptionKeyHex}})
		if err != nil {
			return nil, fmt.Errorf("初始化新系统凭据加密器失败: %w", err)
		}
		options.PasswordEncryptor = encryptor
	}
	report, err := service.ImportLegacyBalanceCenterSQLite(ctx, sqliteDB, targetDB, options)
	if err != nil {
		return report, err
	}
	if cfg.WriteManifestPath != "" {
		manifest := service.LegacyImportManifestFromReport(report, sourceSHA256)
		if err := writeImportManifest(cfg.WriteManifestPath, manifest); err != nil {
			return report, err
		}
	}
	return report, nil
}

func legacySQLiteReadOnlyDSN(path string) string {
	query := url.Values{"mode": {"ro"}, "immutable": {"1"}}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func readImportManifest(path string) (*service.BalanceCenterLegacyImportManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取旧库导入清单失败: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest service.BalanceCenterLegacyImportManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("解析旧库导入清单失败: %w", err)
	}
	return &manifest, nil
}

func writeImportManifest(path string, manifest service.BalanceCenterLegacyImportManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("编码旧库导入清单失败: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入旧库导入清单失败: %w", err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
