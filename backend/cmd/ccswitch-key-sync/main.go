package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultEndpointPath          = "/api/v1/admin/usage/cc-switch-api-key-candidates"
	attributionLeaseEndpointPath = "/api/v1/admin/usage/cc-switch-ip-attribution-lease"
	defaultAppType               = "codex"
	defaultWindow                = 60
	defaultLimit                 = 20
	defaultBackupKeep            = 3
)

type apiResponse struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    candidatesPayload `json:"data"`
}

type candidatesPayload struct {
	Candidates []candidate `json:"candidates"`
}

type candidate struct {
	APIKey    string    `json:"api_key"`
	APIKeyID  int64     `json:"api_key_id"`
	UserID    int64     `json:"user_id"`
	IPAddress string    `json:"ip_address"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

type config struct {
	BaseURL       string
	AdminAPIKey   string
	JWT           string
	DBPath        string
	AppType       string
	Interval      time.Duration
	WindowSeconds int
	Limit         int
	FallbackKey   string
	BackupKeep    int
	Once          bool
	DryRun        bool
}

type currentProvider struct {
	ID             string
	SettingsConfig string
	Meta           string
	CurrentKey     string
}

type keySelection struct {
	TargetKey string
	Source    string
	Candidate *candidate
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		fatal(err)
	}
	startupProvider, err := readCurrentProvider(cfg)
	if err != nil {
		fatal(err)
	}
	cfg.FallbackKey, err = resolveFallbackKey(cfg.FallbackKey, startupProvider.CurrentKey)
	if err != nil {
		fatal(err)
	}
	if cfg.Once {
		if err := runOnce(context.Background(), cfg); err != nil {
			fatal(err)
		}
		return
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		if err := runOnce(context.Background(), cfg); err != nil {
			fmt.Fprintf(os.Stderr, "同步失败: %v\n", err)
		}
		<-ticker.C
	}
}

func parseConfig() (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, fmt.Errorf("读取用户目录失败: %w", err)
	}
	cfg := config{
		BaseURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("SUB2API_BASE_URL")), "/"),
		AdminAPIKey:   strings.TrimSpace(os.Getenv("SUB2API_ADMIN_API_KEY")),
		JWT:           strings.TrimSpace(os.Getenv("SUB2API_JWT")),
		DBPath:        filepath.Join(home, ".cc-switch", "cc-switch.db"),
		AppType:       defaultAppType,
		Interval:      time.Minute,
		WindowSeconds: defaultWindow,
		Limit:         defaultLimit,
		FallbackKey:   strings.TrimSpace(os.Getenv("SUB2API_CC_SWITCH_FALLBACK_API_KEY")),
		BackupKeep:    defaultBackupKeep,
	}
	flag.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "Sub2API 后端地址，例如 http://127.0.0.1:3000")
	flag.StringVar(&cfg.AdminAPIKey, "admin-api-key", cfg.AdminAPIKey, "管理员 API Key；也可用 SUB2API_ADMIN_API_KEY")
	flag.StringVar(&cfg.JWT, "jwt", cfg.JWT, "管理员 JWT；也可用 SUB2API_JWT")
	flag.StringVar(&cfg.DBPath, "ccswitch-db", cfg.DBPath, "CC-Switch SQLite 数据库路径")
	flag.StringVar(&cfg.AppType, "app-type", cfg.AppType, "CC-Switch app_type，默认 codex")
	flag.DurationVar(&cfg.Interval, "interval", cfg.Interval, "轮询间隔")
	flag.IntVar(&cfg.WindowSeconds, "window-seconds", cfg.WindowSeconds, "查询最近多少秒的 GPT 使用记录")
	flag.IntVar(&cfg.Limit, "limit", cfg.Limit, "候选 Key 数量")
	flag.StringVar(&cfg.FallbackKey, "fallback-key", cfg.FallbackKey, "无候选时使用的管理员自用 Key；为空则使用启动时当前 provider 的 Key")
	flag.IntVar(&cfg.BackupKeep, "backup-keep", cfg.BackupKeep, "保留最近多少份 CC-Switch 数据库备份")
	flag.BoolVar(&cfg.Once, "once", false, "只同步一次后退出")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "只打印决策，不写入 CC-Switch 数据库")
	flag.Parse()

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return config{}, errors.New("必须设置 SUB2API_BASE_URL 或 --base-url")
	}
	if cfg.AdminAPIKey == "" && cfg.JWT == "" {
		return config{}, errors.New("必须设置 SUB2API_ADMIN_API_KEY 或 SUB2API_JWT")
	}
	if cfg.WindowSeconds < 30 || cfg.WindowSeconds > 300 {
		return config{}, errors.New("window-seconds 必须在 30-300 之间")
	}
	if cfg.Limit < 1 || cfg.Limit > 50 {
		return config{}, errors.New("limit 必须在 1-50 之间")
	}
	if cfg.BackupKeep < 0 || cfg.BackupKeep > 20 {
		return config{}, errors.New("backup-keep 必须在 0-20 之间")
	}
	return cfg, nil
}

func runOnce(ctx context.Context, cfg config) error {
	provider, err := readCurrentProvider(cfg)
	if err != nil {
		return err
	}

	candidates, err := fetchCandidates(ctx, cfg)
	if err != nil {
		return err
	}
	selection := selectTargetKey(provider.CurrentKey, cfg.FallbackKey, candidates)
	targetKey := selection.TargetKey
	if selection.Candidate != nil && !cfg.DryRun {
		if err := createAttributionLease(ctx, cfg, *selection.Candidate); err != nil {
			return err
		}
	}

	if provider.CurrentKey == targetKey {
		fmt.Printf("无需更新: provider=%s source=%s key=%s\n", provider.ID, selection.Source, maskKey(targetKey))
		return nil
	}
	if cfg.DryRun {
		fmt.Printf("DRY-RUN: provider=%s source=%s %s -> %s\n", provider.ID, selection.Source, maskKey(provider.CurrentKey), maskKey(targetKey))
		return nil
	}
	if cfg.BackupKeep > 0 {
		if err := backupDatabase(cfg.DBPath, cfg.BackupKeep); err != nil {
			return err
		}
	}
	if err := updateCurrentProviderKey(cfg, provider, targetKey); err != nil {
		return err
	}
	if selection.Candidate != nil {
		picked := selection.Candidate
		fmt.Printf("已更新: provider=%s key=%s candidate_api_key_id=%d user_id=%d ip=%s model=%s\n", provider.ID, maskKey(targetKey), picked.APIKeyID, picked.UserID, picked.IPAddress, picked.Model)
	} else {
		fmt.Printf("已恢复 fallback: provider=%s key=%s\n", provider.ID, maskKey(targetKey))
	}
	return nil
}

func createAttributionLease(ctx context.Context, cfg config, selected candidate) error {
	body, err := json.Marshal(map[string]int64{"api_key_id": selected.APIKeyID})
	if err != nil {
		return fmt.Errorf("构造 IP 归因租约请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+attributionLeaseEndpointPath, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.AdminAPIKey != "" {
		req.Header.Set("x-api-key", cfg.AdminAPIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+cfg.JWT)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 IP 归因租约接口失败: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("IP 归因租约接口返回 HTTP %d", resp.StatusCode)
	}
	var out apiResponse
	if err := json.Unmarshal(responseBody, &out); err != nil {
		return fmt.Errorf("解析 IP 归因租约响应失败: %w", err)
	}
	if out.Code != 0 {
		return fmt.Errorf("IP 归因租约接口返回错误: %s", out.Message)
	}
	return nil
}

func resolveFallbackKey(configuredKey string, startupProviderKey string) (string, error) {
	fallbackKey := strings.TrimSpace(configuredKey)
	if fallbackKey == "" {
		fallbackKey = strings.TrimSpace(startupProviderKey)
	}
	if fallbackKey == "" {
		return "", errors.New("当前 CC-Switch provider 未读取到可用 fallback Key")
	}
	return fallbackKey, nil
}

// selectTargetKey 保持仍有新请求的当前用户 Key，否则选择最新候选，最后回退管理员 Key。
func selectTargetKey(currentKey string, fallbackKey string, candidates []candidate) keySelection {
	currentKey = strings.TrimSpace(currentKey)
	fallbackKey = strings.TrimSpace(fallbackKey)
	if currentKey != "" && currentKey != fallbackKey {
		for i := range candidates {
			if strings.TrimSpace(candidates[i].APIKey) == currentKey {
				return keySelection{TargetKey: currentKey, Source: "current_active", Candidate: &candidates[i]}
			}
		}
	}
	for i := range candidates {
		candidateKey := strings.TrimSpace(candidates[i].APIKey)
		if candidateKey != "" {
			return keySelection{TargetKey: candidateKey, Source: "candidate", Candidate: &candidates[i]}
		}
	}
	return keySelection{TargetKey: fallbackKey, Source: "fallback"}
}

func fetchCandidates(ctx context.Context, cfg config) ([]candidate, error) {
	u, err := url.Parse(cfg.BaseURL + defaultEndpointPath)
	if err != nil {
		return nil, fmt.Errorf("构造候选接口地址失败: %w", err)
	}
	q := u.Query()
	q.Set("window_seconds", strconv.Itoa(cfg.WindowSeconds))
	q.Set("limit", strconv.Itoa(cfg.Limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if cfg.AdminAPIKey != "" {
		req.Header.Set("x-api-key", cfg.AdminAPIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+cfg.JWT)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求候选接口失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("候选接口返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out apiResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析候选接口响应失败: %w", err)
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("候选接口返回错误: %s", out.Message)
	}
	return out.Data.Candidates, nil
}

func readCurrentProvider(cfg config) (currentProvider, error) {
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return currentProvider{}, err
	}
	defer db.Close()
	row := db.QueryRow("SELECT id, settings_config, meta FROM providers WHERE app_type = ? AND is_current = 1 LIMIT 1", cfg.AppType)
	var p currentProvider
	if err := row.Scan(&p.ID, &p.SettingsConfig, &p.Meta); err != nil {
		return currentProvider{}, fmt.Errorf("读取当前 CC-Switch provider 失败: %w", err)
	}
	key, err := extractKeyFromSettings(p.SettingsConfig)
	if err != nil {
		return currentProvider{}, err
	}
	p.CurrentKey = key
	return p, nil
}

func updateCurrentProviderKey(cfg config, provider currentProvider, apiKey string) error {
	settingsConfig, changedSettings, err := replaceAPIKeyJSON(provider.SettingsConfig, apiKey)
	if err != nil {
		return err
	}
	meta, changedMeta, err := replaceAPIKeyJSON(provider.Meta, apiKey)
	if err != nil {
		return err
	}
	if !changedSettings && !changedMeta {
		return errors.New("未找到可替换的 API Key 字段")
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("UPDATE providers SET settings_config = ?, meta = ? WHERE id = ? AND app_type = ? AND is_current = 1", settingsConfig, meta, provider.ID, cfg.AppType)
	if err != nil {
		return fmt.Errorf("更新 CC-Switch provider 失败: %w", err)
	}
	return nil
}

func extractKeyFromSettings(raw string) (string, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return "", fmt.Errorf("解析 settings_config 失败: %w", err)
	}
	auth, ok := root["auth"].(map[string]any)
	if !ok {
		return "", nil
	}
	if key, ok := auth["OPENAI_API_KEY"].(string); ok {
		return strings.TrimSpace(key), nil
	}
	return "", nil
}

func replaceAPIKeyJSON(raw string, apiKey string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var root any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return "", false, fmt.Errorf("解析 CC-Switch JSON 失败: %w", err)
	}
	changed := replaceAPIKeyValues(root, apiKey)
	if obj, ok := root.(map[string]any); ok {
		if config, ok := obj["config"].(string); ok {
			updated, configChanged := replaceConfigAPIKey(config, apiKey)
			if configChanged {
				obj["config"] = updated
				changed = true
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	buf, err := json.Marshal(root)
	if err != nil {
		return "", false, err
	}
	return string(buf), true, nil
}

func replaceAPIKeyValues(v any, apiKey string) bool {
	changed := false
	switch node := v.(type) {
	case map[string]any:
		for key, value := range node {
			if isAPIKeyField(key) {
				if _, ok := value.(string); ok {
					node[key] = apiKey
					changed = true
					continue
				}
			}
			if replaceAPIKeyValues(value, apiKey) {
				changed = true
			}
		}
	case []any:
		for _, item := range node {
			if replaceAPIKeyValues(item, apiKey) {
				changed = true
			}
		}
	}
	return changed
}

func isAPIKeyField(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	return normalized == "apikey" || normalized == "openaiapikey"
}

var configAPIKeyRE = regexp.MustCompile("(?m)^(\\s*(?:api_key|openai_api_key)\\s*=\\s*\")([^\"]*)(\".*)$")

func replaceConfigAPIKey(config string, apiKey string) (string, bool) {
	changed := false
	updated := configAPIKeyRE.ReplaceAllStringFunc(config, func(line string) string {
		parts := configAPIKeyRE.FindStringSubmatch(line)
		if len(parts) != 4 {
			return line
		}
		changed = true
		return parts[1] + apiKey + parts[3]
	})
	return updated, changed
}

func backupDatabase(dbPath string, keep int) error {
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups", "sub2api-key-sync")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	backupPath := filepath.Join(backupDir, "cc-switch-"+time.Now().Format("20060102-150405")+".db")
	if err := copyFile(dbPath, backupPath); err != nil {
		return fmt.Errorf("备份 CC-Switch 数据库失败: %w", err)
	}
	return pruneBackups(backupDir, keep)
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "cc-switch-") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	for len(paths) > keep {
		if err := os.Remove(paths[0]); err != nil {
			return err
		}
		paths = paths[1:]
	}
	return nil
}

func maskKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 10 {
		return "[REDACTED]"
	}
	return key[:6] + "..." + key[len(key)-4:]
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
