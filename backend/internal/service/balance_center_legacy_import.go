package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	BalanceCenterLegacySource          = "legacy_sqlite"
	BalanceCenterLegacyManifestVersion = 1
)

type BalanceCenterLegacyImportOptions struct {
	Execute           bool
	OldMasterKey      string
	PasswordEncryptor SecretEncryptor
	SourceSHA256      string
	ExpectedManifest  *BalanceCenterLegacyImportManifest
	VerifySource      func() error
}

// BalanceCenterLegacyImportManifest 锁定一次不可变旧库备份的全量源数据基线。
type BalanceCenterLegacyImportManifest struct {
	Version                 int     `json:"version"`
	SourceSHA256            string  `json:"source_sha256"`
	Sites                   int     `json:"sites"`
	LegacyKeys              int     `json:"legacy_keys"`
	EnabledKeys             int     `json:"enabled_keys"`
	Snapshots               int     `json:"snapshots"`
	ManualRows              int     `json:"manual_rows"`
	ManualTotal             float64 `json:"manual_total"`
	ManualRechargeEvents    int     `json:"manual_recharge_events"`
	AutomaticRecords        int     `json:"automatic_records"`
	LiandongOrders          int     `json:"liandong_orders"`
	Reconciliations         int     `json:"reconciliations"`
	MatchedKeys             int     `json:"matched_keys"`
	ImportedSiteCredentials int     `json:"imported_site_credentials"`
	SkippedSiteCredentials  int     `json:"skipped_site_credentials"`
}

type BalanceCenterLegacyImportReport struct {
	DryRun                   bool    `json:"dry_run"`
	Sites                    int     `json:"sites"`
	LegacyKeys               int     `json:"legacy_keys"`
	EnabledKeys              int     `json:"enabled_keys"`
	MatchedKeys              int     `json:"matched_keys"`
	Snapshots                int     `json:"snapshots"`
	ManualRows               int     `json:"manual_rows"`
	ManualTotal              float64 `json:"manual_total"`
	ManualRechargeEvents     int     `json:"manual_recharge_events"`
	AutomaticRecords         int     `json:"automatic_records"`
	LiandongOrders           int     `json:"liandong_orders"`
	Reconciliations          int     `json:"reconciliations"`
	ImportedSiteCredentials  int     `json:"imported_site_credentials"`
	SkippedSiteCredentials   int     `json:"skipped_site_credentials"`
	InsertedSites            int     `json:"inserted_sites"`
	InsertedLegacyKeys       int     `json:"inserted_legacy_keys"`
	InsertedSnapshots        int     `json:"inserted_snapshots"`
	InsertedManualRows       int     `json:"inserted_manual_rows"`
	InsertedRechargeEvents   int     `json:"inserted_recharge_events"`
	InsertedAutomaticRecords int     `json:"inserted_automatic_records"`
	InsertedLiandongOrders   int     `json:"inserted_liandong_orders"`
	InsertedReconciliations  int     `json:"inserted_reconciliations"`
}

type legacyBalanceCenterData struct {
	Sites                []legacyBalanceSite
	Keys                 []legacyBalanceKey
	Snapshots            []legacyBalanceSnapshot
	ManualRows           []legacyManualRow
	ManualRechargeEvents []legacyManualRechargeEvent
	AutomaticRecords     []legacyAutomaticRecord
	LiandongOrders       []legacyLiandongOrder
	Reconciliations      []legacyReconciliation
	Settings             map[string]string
}

type legacyBalanceSite struct {
	ID            string
	Name          string
	Provider      string
	BaseURL       string
	Domain        string
	PaymentScale  float64
	Hidden        bool
	LoginUsername string
}

type legacyBalanceKey struct {
	ID           string
	SiteID       string
	Name         string
	Enabled      bool
	SecretCipher string
}

type legacyBalanceSnapshot struct {
	ID             string
	SiteID         string
	KeyID          *string
	Remaining      *float64
	Unit           string
	RateMultiplier *float64
	Status         string
	ErrorCode      string
	FetchedAt      time.Time
}

type legacyManualRow struct {
	ID         string
	Label      string
	Expression string
	Amount     float64
	SortOrder  int
}

type legacyManualRechargeEvent struct {
	ID        string
	RowID     string
	SiteLabel string
	Amount    float64
	CreatedAt time.Time
}

type legacyAutomaticRecord struct {
	Source                  string  `json:"source"`
	ID                      string  `json:"id"`
	SiteLabel               string  `json:"siteLabel"`
	Amount                  float64 `json:"amount"`
	OrderNo                 string  `json:"orderNo"`
	OccurredAt              string  `json:"occurredAt"`
	Note                    string  `json:"note"`
	RunID                   string
	ReconciliationCreatedAt time.Time
}

type legacyLiandongOrder struct {
	TradeNumber string
	GoodsName   string
	TotalAmount float64
	Quantity    int
	Status      int
	CreatedAt   time.Time
}

type legacyReconciliation struct {
	ID               string
	InputJSON        string
	ResultJSON       string
	Status           string
	ExpectedAmount   *float64
	ActualAmount     *float64
	DifferenceAmount *float64
	CreatedAt        time.Time
}

func ImportLegacyBalanceCenterSQLite(ctx context.Context, sqliteDB *sql.DB, targetDB *sql.DB, options BalanceCenterLegacyImportOptions) (*BalanceCenterLegacyImportReport, error) {
	data, err := loadLegacyBalanceCenterData(ctx, sqliteDB)
	if err != nil {
		return nil, err
	}
	if options.VerifySource != nil {
		if err := options.VerifySource(); err != nil {
			return nil, err
		}
	}
	report := summarizeLegacyBalanceCenterData(data)
	report.DryRun = !options.Execute
	if targetDB == nil {
		if err := ValidateLegacyImportManifest(report, options.SourceSHA256, options.ExpectedManifest); err != nil {
			return report, err
		}
		return report, nil
	}
	tx, err := targetDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: !options.Execute})
	if err != nil {
		return nil, fmt.Errorf("开始余额中心旧库导入事务失败: %w", err)
	}
	accounts, err := loadLegacyImportAccountFingerprints(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return report, fmt.Errorf("读取目标账号匹配指纹失败: %w", err)
	}
	report.MatchedKeys = countLegacyMatchedKeys(data, accounts, options.OldMasterKey)
	if err := previewLegacySiteCredentials(ctx, tx, data, accounts, options, report); err != nil {
		_ = tx.Rollback()
		return report, err
	}
	if err := ValidateLegacyImportManifest(report, options.SourceSHA256, options.ExpectedManifest); err != nil {
		_ = tx.Rollback()
		return report, err
	}
	if !options.Execute {
		_ = tx.Rollback()
		return report, nil
	}
	// 正式写入会按同一事务快照重新累计实际凭据结果。
	report.ImportedSiteCredentials = 0
	report.SkippedSiteCredentials = 0
	writeErr := writeLegacyBalanceCenterData(ctx, tx, data, report, options, accounts)
	if writeErr != nil {
		_ = tx.Rollback()
		return report, writeErr
	}
	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("提交余额中心旧库导入失败: %w", err)
	}
	return report, nil
}

func countLegacyMatchedKeys(data *legacyBalanceCenterData, accounts []targetAccountFingerprint, oldMasterKey string) int {
	if data == nil {
		return 0
	}
	sites := make(map[string]legacyBalanceSite, len(data.Sites))
	for _, site := range data.Sites {
		sites[site.ID] = site
	}
	matched := 0
	for _, key := range data.Keys {
		if matchLegacyKeyAccount(sites[key.SiteID].Domain, key.SecretCipher, oldMasterKey, accounts) != nil {
			matched++
		}
	}
	return matched
}

func previewLegacySiteCredentials(ctx context.Context, tx *sql.Tx, data *legacyBalanceCenterData, accounts []targetAccountFingerprint, options BalanceCenterLegacyImportOptions, report *BalanceCenterLegacyImportReport) error {
	if data == nil || options.PasswordEncryptor == nil || options.OldMasterKey == "" {
		return nil
	}
	for _, site := range data.Sites {
		if site.LoginUsername == "" {
			continue
		}
		matched := false
		for _, key := range data.Keys {
			if key.SiteID == site.ID && matchLegacyKeyAccount(site.Domain, key.SecretCipher, options.OldMasterKey, accounts) != nil {
				matched = true
				break
			}
		}
		cipherText := data.Settings["siteLoginPassword."+site.ID]
		if !matched || cipherText == "" {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upstream_site_credentials WHERE host=$1)`, site.Domain).Scan(&exists); err != nil {
			return err
		}
		if exists {
			report.SkippedSiteCredentials++
			continue
		}
		if _, err := decryptLegacySub2WebSecret(cipherText, options.OldMasterKey); err != nil {
			report.SkippedSiteCredentials++
			continue
		}
		report.ImportedSiteCredentials++
	}
	return nil
}

func LegacyImportManifestFromReport(report *BalanceCenterLegacyImportReport, sourceSHA256 string) BalanceCenterLegacyImportManifest {
	if report == nil {
		return BalanceCenterLegacyImportManifest{Version: BalanceCenterLegacyManifestVersion, SourceSHA256: strings.ToLower(strings.TrimSpace(sourceSHA256))}
	}
	return BalanceCenterLegacyImportManifest{
		Version:                 BalanceCenterLegacyManifestVersion,
		SourceSHA256:            strings.ToLower(strings.TrimSpace(sourceSHA256)),
		Sites:                   report.Sites,
		LegacyKeys:              report.LegacyKeys,
		EnabledKeys:             report.EnabledKeys,
		Snapshots:               report.Snapshots,
		ManualRows:              report.ManualRows,
		ManualTotal:             roundLegacyMoney(report.ManualTotal),
		ManualRechargeEvents:    report.ManualRechargeEvents,
		AutomaticRecords:        report.AutomaticRecords,
		LiandongOrders:          report.LiandongOrders,
		Reconciliations:         report.Reconciliations,
		MatchedKeys:             report.MatchedKeys,
		ImportedSiteCredentials: report.ImportedSiteCredentials,
		SkippedSiteCredentials:  report.SkippedSiteCredentials,
	}
}

func ValidateLegacyImportManifest(report *BalanceCenterLegacyImportReport, sourceSHA256 string, expected *BalanceCenterLegacyImportManifest) error {
	if expected == nil {
		return nil
	}
	if expected.Version != BalanceCenterLegacyManifestVersion {
		return fmt.Errorf("旧库导入清单版本不支持: got %d want %d", expected.Version, BalanceCenterLegacyManifestVersion)
	}
	actual := LegacyImportManifestFromReport(report, sourceSHA256)
	expectedCopy := *expected
	expectedCopy.SourceSHA256 = strings.ToLower(strings.TrimSpace(expectedCopy.SourceSHA256))
	expectedCopy.ManualTotal = roundLegacyMoney(expectedCopy.ManualTotal)
	for _, mismatch := range legacyImportManifestMismatches(actual, expectedCopy) {
		return fmt.Errorf("旧库导入清单校验失败: %s", mismatch)
	}
	return nil
}

func legacyImportManifestMismatches(actual, expected BalanceCenterLegacyImportManifest) []string {
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"source_sha256", actual.SourceSHA256, expected.SourceSHA256},
		{"sites", actual.Sites, expected.Sites},
		{"legacy_keys", actual.LegacyKeys, expected.LegacyKeys},
		{"enabled_keys", actual.EnabledKeys, expected.EnabledKeys},
		{"snapshots", actual.Snapshots, expected.Snapshots},
		{"manual_rows", actual.ManualRows, expected.ManualRows},
		{"manual_total", actual.ManualTotal, expected.ManualTotal},
		{"manual_recharge_events", actual.ManualRechargeEvents, expected.ManualRechargeEvents},
		{"automatic_records", actual.AutomaticRecords, expected.AutomaticRecords},
		{"liandong_orders", actual.LiandongOrders, expected.LiandongOrders},
		{"reconciliations", actual.Reconciliations, expected.Reconciliations},
		{"matched_keys", actual.MatchedKeys, expected.MatchedKeys},
		{"imported_site_credentials", actual.ImportedSiteCredentials, expected.ImportedSiteCredentials},
		{"skipped_site_credentials", actual.SkippedSiteCredentials, expected.SkippedSiteCredentials},
	}
	for _, check := range checks {
		if check.got != check.want {
			return []string{fmt.Sprintf("%s got %v want %v", check.name, check.got, check.want)}
		}
	}
	return nil
}

func summarizeLegacyBalanceCenterData(data *legacyBalanceCenterData) *BalanceCenterLegacyImportReport {
	report := &BalanceCenterLegacyImportReport{
		Sites:                  len(data.Sites),
		LegacyKeys:             len(data.Keys),
		Snapshots:              len(data.Snapshots),
		ManualRows:             len(data.ManualRows),
		ManualRechargeEvents:   len(data.ManualRechargeEvents),
		AutomaticRecords:       len(data.AutomaticRecords),
		LiandongOrders:         len(data.LiandongOrders),
		Reconciliations:        len(data.Reconciliations),
		SkippedSiteCredentials: 0,
	}
	for _, key := range data.Keys {
		if key.Enabled {
			report.EnabledKeys++
		}
	}
	for _, row := range data.ManualRows {
		report.ManualTotal += row.Amount
	}
	report.ManualTotal = roundLegacyMoney(report.ManualTotal)
	return report
}

func loadLegacyBalanceCenterData(ctx context.Context, db *sql.DB) (*legacyBalanceCenterData, error) {
	if db == nil {
		return nil, errors.New("旧 SQLite 数据库不能为空")
	}
	data := &legacyBalanceCenterData{Settings: map[string]string{}}
	var err error
	if data.Sites, err = loadLegacySites(ctx, db); err != nil {
		return nil, err
	}
	if data.Keys, err = loadLegacyKeys(ctx, db); err != nil {
		return nil, err
	}
	if data.Snapshots, err = loadLegacySnapshots(ctx, db); err != nil {
		return nil, err
	}
	if data.Settings, err = loadLegacySettings(ctx, db); err != nil {
		return nil, err
	}
	if data.ManualRows, err = legacyManualRowsFromSettings(data.Settings); err != nil {
		return nil, err
	}
	if len(data.ManualRows) == 0 {
		if data.ManualRows, err = legacyManualRowsFromLatestReconciliation(ctx, db); err != nil {
			return nil, err
		}
	}
	if legacySQLiteHasTable(ctx, db, "manual_recharge_events") {
		if data.ManualRechargeEvents, err = loadLegacyManualRechargeEvents(ctx, db); err != nil {
			return nil, err
		}
	}
	if data.LiandongOrders, err = loadLegacyLiandongOrders(ctx, db); err != nil {
		return nil, err
	}
	if data.Reconciliations, data.AutomaticRecords, err = loadLegacyReconciliations(ctx, db); err != nil {
		return nil, err
	}
	return data, nil
}

func loadLegacySites(ctx context.Context, db *sql.DB) ([]legacyBalanceSite, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, provider, base_url, hidden, COALESCE(login_username, ''), payment_scale FROM sites ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("读取旧站点失败: %w", err)
	}
	defer rows.Close()
	var items []legacyBalanceSite
	for rows.Next() {
		var item legacyBalanceSite
		var hidden int
		if err := rows.Scan(&item.ID, &item.Name, &item.Provider, &item.BaseURL, &hidden, &item.LoginUsername, &item.PaymentScale); err != nil {
			return nil, err
		}
		item.Hidden = hidden != 0
		item.Domain = normalizeBalanceCenterLegacyDomain(item.BaseURL)
		if item.PaymentScale <= 0 {
			item.PaymentScale = 1
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadLegacyKeys(ctx context.Context, db *sql.DB) ([]legacyBalanceKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, site_id, name, enabled, secret_cipher FROM site_keys ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("读取旧 Key 元数据失败: %w", err)
	}
	defer rows.Close()
	var items []legacyBalanceKey
	for rows.Next() {
		var item legacyBalanceKey
		var enabled int
		if err := rows.Scan(&item.ID, &item.SiteID, &item.Name, &enabled, &item.SecretCipher); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadLegacySnapshots(ctx context.Context, db *sql.DB) ([]legacyBalanceSnapshot, error) {
	errorCodeExpr := "''"
	if legacySQLiteHasColumn(ctx, db, "snapshots", "error_code") {
		errorCodeExpr = "COALESCE(error_code, '')"
	}
	query := fmt.Sprintf(`SELECT id, site_id, key_id, remaining, COALESCE(unit, ''), rate_multiplier, status, %s, fetched_at FROM snapshots ORDER BY fetched_at ASC, id ASC`, errorCodeExpr)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("读取旧历史快照失败: %w", err)
	}
	defer rows.Close()
	var items []legacyBalanceSnapshot
	for rows.Next() {
		var item legacyBalanceSnapshot
		var keyID sql.NullString
		var remaining, rate sql.NullFloat64
		var fetched string
		if err := rows.Scan(&item.ID, &item.SiteID, &keyID, &remaining, &item.Unit, &rate, &item.Status, &item.ErrorCode, &fetched); err != nil {
			return nil, err
		}
		if keyID.Valid && strings.TrimSpace(keyID.String) != "" {
			v := keyID.String
			item.KeyID = &v
		}
		if remaining.Valid {
			v := remaining.Float64
			item.Remaining = &v
		}
		if rate.Valid {
			v := rate.Float64
			item.RateMultiplier = &v
		}
		parsed, err := parseLegacyTime(fetched)
		if err != nil {
			return nil, fmt.Errorf("旧快照时间无效: %w", err)
		}
		item.FetchedAt = parsed
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadLegacySettings(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("读取旧设置失败: %w", err)
	}
	defer rows.Close()
	settings := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, rows.Err()
}

func loadLegacyManualRechargeEvents(ctx context.Context, db *sql.DB) ([]legacyManualRechargeEvent, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, row_id, site_label, amount, created_at FROM manual_recharge_events ORDER BY created_at ASC, rowid ASC`)
	if err != nil {
		return nil, fmt.Errorf("读取旧手工充值事件失败: %w", err)
	}
	defer rows.Close()
	var items []legacyManualRechargeEvent
	for rows.Next() {
		var item legacyManualRechargeEvent
		var created string
		if err := rows.Scan(&item.ID, &item.RowID, &item.SiteLabel, &item.Amount, &created); err != nil {
			return nil, err
		}
		t, err := parseLegacyTime(created)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = t
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadLegacyLiandongOrders(ctx context.Context, db *sql.DB) ([]legacyLiandongOrder, error) {
	rows, err := db.QueryContext(ctx, `SELECT trade_number, goods_name, total_amount, quantity, status, created_at FROM liandong_orders ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("读取旧链动订单失败: %w", err)
	}
	defer rows.Close()
	var items []legacyLiandongOrder
	for rows.Next() {
		var item legacyLiandongOrder
		var created string
		if err := rows.Scan(&item.TradeNumber, &item.GoodsName, &item.TotalAmount, &item.Quantity, &item.Status, &created); err != nil {
			return nil, err
		}
		t, err := parseLegacyTime(created)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = t
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadLegacyReconciliations(ctx context.Context, db *sql.DB) ([]legacyReconciliation, []legacyAutomaticRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, input_json, result_json, created_at FROM reconcile_runs ORDER BY created_at ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("读取旧对账记录失败: %w", err)
	}
	defer rows.Close()
	var reconciliations []legacyReconciliation
	var automatic []legacyAutomaticRecord
	automaticIndex := make(map[string]int)
	for rows.Next() {
		var item legacyReconciliation
		var created string
		if err := rows.Scan(&item.ID, &item.InputJSON, &item.ResultJSON, &created); err != nil {
			return nil, nil, err
		}
		t, err := parseLegacyTime(created)
		if err != nil {
			return nil, nil, err
		}
		item.CreatedAt = t
		applyLegacyReconciliationResult(&item)
		reconciliations = append(reconciliations, item)
		for _, record := range legacyAutomaticRecordsFromInput(item.ID, item.InputJSON) {
			// 旧自动记录可能没有独立时间，保留所属对账时间作为稳定回退值。
			record.ReconciliationCreatedAt = item.CreatedAt
			key := legacyAutomaticRecordKey(record)
			if index, exists := automaticIndex[key]; exists {
				// 重复订单可以更新元数据，但缺失独立时间时必须沿用首次出现时间。
				previous := automatic[index]
				record.ReconciliationCreatedAt = previous.ReconciliationCreatedAt
				if _, err := parseLegacyTime(record.OccurredAt); err != nil {
					if _, previousErr := parseLegacyTime(previous.OccurredAt); previousErr == nil {
						record.OccurredAt = previous.OccurredAt
					}
				}
				automatic[index] = record
				continue
			}
			automaticIndex[key] = len(automatic)
			automatic = append(automatic, record)
		}
	}
	return reconciliations, automatic, rows.Err()
}

func legacyManualRowsFromSettings(settings map[string]string) ([]legacyManualRow, error) {
	raw := strings.TrimSpace(settings["calculator.manualRows"])
	if raw == "" {
		return nil, nil
	}
	return parseLegacyManualRowsJSON(raw)
}

func legacyManualRowsFromLatestReconciliation(ctx context.Context, db *sql.DB) ([]legacyManualRow, error) {
	if !legacySQLiteHasTable(ctx, db, "reconcile_runs") {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT input_json FROM reconcile_runs ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("读取旧对账手工基线失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var inputJSON string
		if err := rows.Scan(&inputJSON); err != nil {
			return nil, err
		}
		var input struct {
			ManualRows json.RawMessage `json:"manualRows"`
		}
		if err := json.Unmarshal([]byte(inputJSON), &input); err != nil || len(input.ManualRows) == 0 {
			continue
		}
		parsed, err := parseLegacyManualRowsJSON(string(input.ManualRows))
		if err != nil {
			return nil, fmt.Errorf("解析旧对账手工基线失败: %w", err)
		}
		if len(parsed) > 0 {
			return parsed, nil
		}
	}
	return nil, rows.Err()
}

func parseLegacyManualRowsJSON(raw string) ([]legacyManualRow, error) {
	var drafts []struct {
		ID         string   `json:"id"`
		Label      string   `json:"label"`
		Expression string   `json:"expression"`
		Total      *float64 `json:"total"`
	}
	if err := json.Unmarshal([]byte(raw), &drafts); err != nil {
		return nil, fmt.Errorf("解析旧手工基线失败: %w", err)
	}
	rows := make([]legacyManualRow, 0, len(drafts))
	for i, draft := range drafts {
		expression := strings.TrimSpace(draft.Expression)
		amount := 0.0
		var err error
		if expression != "" {
			amount, err = EvaluateLegacyManualExpression(expression)
			if err != nil {
				return nil, fmt.Errorf("解析旧手工表达式 %s 失败: %w", draft.Label, err)
			}
		} else if draft.Total != nil {
			amount = roundLegacyMoney(*draft.Total)
			expression = formatLegacyManualAmount(amount)
		} else {
			return nil, fmt.Errorf("旧手工基线 %s 缺少表达式", draft.Label)
		}
		rows = append(rows, legacyManualRow{ID: nonEmptyLegacyID(draft.ID, fmt.Sprintf("manual-%d", i+1)), Label: strings.TrimSpace(draft.Label), Expression: expression, Amount: amount, SortOrder: i})
	}
	return rows, nil
}

func legacyAutomaticRecordsFromInput(runID, inputJSON string) []legacyAutomaticRecord {
	var input struct {
		AutomaticRecords []legacyAutomaticRecord `json:"automaticRecords"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return nil
	}
	for i := range input.AutomaticRecords {
		input.AutomaticRecords[i].RunID = runID
	}
	return input.AutomaticRecords
}

func applyLegacyReconciliationResult(item *legacyReconciliation) {
	var result struct {
		Status           string   `json:"status"`
		ManualTotal      *float64 `json:"manualTotal"`
		AutomaticTotal   *float64 `json:"automaticTotal"`
		DifferenceAmount *float64 `json:"differenceAmount"`
	}
	if err := json.Unmarshal([]byte(item.ResultJSON), &result); err == nil {
		item.Status = result.Status
		item.ExpectedAmount = result.ManualTotal
		item.ActualAmount = result.AutomaticTotal
		item.DifferenceAmount = result.DifferenceAmount
	}
	if item.Status == "" {
		item.Status = "legacy"
	}
}

func writeLegacyBalanceCenterData(ctx context.Context, tx *sql.Tx, data *legacyBalanceCenterData, report *BalanceCenterLegacyImportReport, options BalanceCenterLegacyImportOptions, accounts []targetAccountFingerprint) error {
	siteIDs := map[string]int64{}
	siteByLegacyID := map[string]legacyBalanceSite{}
	for _, site := range data.Sites {
		siteByLegacyID[site.ID] = site
		id, inserted, err := insertLegacySite(ctx, tx, site)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedSites++
		}
		siteIDs[site.ID] = id
	}

	legacyKeyIDs := map[string]int64{}
	legacyKeyAccountIDs := map[string]*int64{}
	for _, key := range data.Keys {
		site := siteByLegacyID[key.SiteID]
		accountID := matchLegacyKeyAccount(site.Domain, key.SecretCipher, options.OldMasterKey, accounts)
		id, inserted, err := insertLegacyKey(ctx, tx, siteIDs[key.SiteID], accountID, key)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedLegacyKeys++
		}
		legacyKeyIDs[key.ID] = id
		legacyKeyAccountIDs[key.ID] = accountID
	}

	for _, site := range data.Sites {
		if err := maybeImportLegacySiteCredential(ctx, tx, site, data.Settings, legacyKeyAccountIDs, data.Keys, options, report); err != nil {
			return err
		}
	}

	for _, snapshot := range data.Snapshots {
		site := siteByLegacyID[snapshot.SiteID]
		siteID := siteIDs[snapshot.SiteID]
		var legacyKeyID *int64
		var accountID *int64
		if snapshot.KeyID != nil {
			if id, ok := legacyKeyIDs[*snapshot.KeyID]; ok {
				legacyKeyID = &id
			}
			accountID = legacyKeyAccountIDs[*snapshot.KeyID]
		}
		snapshotID, inserted, err := insertLegacySnapshot(ctx, tx, siteID, legacyKeyID, accountID, site, snapshot)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedSnapshots++
		}
		if legacyKeyID != nil {
			if err := upsertLegacyCurrentState(ctx, tx, siteID, *legacyKeyID, accountID, snapshotID, site, snapshot); err != nil {
				return err
			}
		}
	}
	for _, row := range data.ManualRows {
		inserted, err := insertLegacyManualRow(ctx, tx, siteIDForLabel(siteIDs, siteByLegacyID, row.Label), row)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedManualRows++
		}
	}
	for _, event := range data.ManualRechargeEvents {
		inserted, err := insertLegacyRechargeEvent(ctx, tx, siteIDForLabel(siteIDs, siteByLegacyID, event.SiteLabel), event)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedRechargeEvents++
		}
	}
	for _, record := range data.AutomaticRecords {
		inserted, err := insertLegacyAutomaticRecord(ctx, tx, siteIDForLabel(siteIDs, siteByLegacyID, record.SiteLabel), record)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedAutomaticRecords++
		}
	}
	for _, order := range data.LiandongOrders {
		inserted, err := insertLegacyLiandongOrder(ctx, tx, order)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedLiandongOrders++
		}
	}
	for _, item := range data.Reconciliations {
		inserted, err := insertLegacyReconciliation(ctx, tx, item)
		if err != nil {
			return err
		}
		if inserted {
			report.InsertedReconciliations++
		}
	}
	return nil
}

type targetAccountFingerprint struct {
	AccountID   int64
	Domain      string
	Fingerprint string
}

func loadLegacyImportAccountFingerprints(ctx context.Context, tx *sql.Tx) ([]targetAccountFingerprint, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(credentials->>'base_url', ''), COALESCE(credentials->>'api_key', '') FROM accounts WHERE platform='openai' AND type='apikey' AND deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []targetAccountFingerprint
	for rows.Next() {
		var item targetAccountFingerprint
		var apiKey string
		if err := rows.Scan(&item.AccountID, &item.Domain, &apiKey); err != nil {
			return nil, err
		}
		item.Domain = normalizeBalanceCenterLegacyDomain(item.Domain)
		if item.Domain == "" || apiKey == "" {
			continue
		}
		item.Fingerprint = legacySecretFingerprint(apiKey)
		out = append(out, item)
	}
	return out, rows.Err()
}

func matchLegacyKeyAccount(domain, cipherText, oldMasterKey string, accounts []targetAccountFingerprint) *int64 {
	if strings.TrimSpace(oldMasterKey) == "" || strings.TrimSpace(cipherText) == "" || strings.TrimSpace(domain) == "" {
		return nil
	}
	plain, err := decryptLegacySub2WebSecret(cipherText, oldMasterKey)
	if err != nil {
		return nil
	}
	fp := legacySecretFingerprint(plain)
	for _, account := range accounts {
		if account.Domain == domain && account.Fingerprint == fp {
			id := account.AccountID
			return &id
		}
	}
	return nil
}

func insertLegacySite(ctx context.Context, tx *sql.Tx, site legacyBalanceSite) (int64, bool, error) {
	metadata, _ := json.Marshal(map[string]any{"legacy_site_id": site.ID, "provider": site.Provider, "hidden": site.Hidden})
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_sites (name, normalized_domain, base_url, source, probe_supported, metadata)
VALUES ($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT (normalized_domain) DO NOTHING RETURNING id`,
		site.Name, site.Domain, site.BaseURL, BalanceCenterLegacySource, legacyProbeSupported(site), string(metadata)).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("导入旧站点失败: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM balance_center_sites WHERE normalized_domain=$1`, site.Domain).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

func insertLegacyKey(ctx context.Context, tx *sql.Tx, siteID int64, accountID *int64, key legacyBalanceKey) (int64, bool, error) {
	metadata, _ := json.Marshal(map[string]any{"legacy_key_id": key.ID})
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_legacy_keys (site_id, account_id, source, source_key, display_name, enabled, metadata)
VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb) ON CONFLICT (source, source_key) DO NOTHING RETURNING id`,
		siteID, accountID, BalanceCenterLegacySource, key.ID, key.Name, key.Enabled, string(metadata)).Scan(&id)
	if err == nil {
		if accountID != nil {
			_, _ = tx.ExecContext(ctx, `INSERT INTO balance_center_account_bindings (site_id, account_id, source) VALUES ($1,$2,$3) ON CONFLICT (site_id, account_id) DO NOTHING`, siteID, *accountID, BalanceCenterLegacySource)
		}
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("导入旧 Key 元数据失败: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM balance_center_legacy_keys WHERE source=$1 AND source_key=$2`, BalanceCenterLegacySource, key.ID).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

func insertLegacySnapshot(ctx context.Context, tx *sql.Tx, siteID int64, legacyKeyID, accountID *int64, site legacyBalanceSite, snapshot legacyBalanceSnapshot) (int64, bool, error) {
	var balance, converted any
	if snapshot.Remaining != nil {
		balance = *snapshot.Remaining
		converted = roundLegacyMoney(*snapshot.Remaining * site.PaymentScale)
	}
	payload, _ := json.Marshal(map[string]any{"legacy_snapshot_id": snapshot.ID, "legacy_key_id": snapshot.KeyID, "legacy_status": snapshot.Status})
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_snapshots (site_id, account_id, legacy_key_id, source, source_key, status, balance, converted_balance, rate_multiplier, conversion_scale, currency, reason, probed_at, payload)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb) ON CONFLICT (source, source_key) DO NOTHING RETURNING id`,
		siteID, accountID, legacyKeyID, BalanceCenterLegacySource, snapshot.ID, normalizeLegacySnapshotStatus(snapshot.Status), balance, converted, snapshot.RateMultiplier, site.PaymentScale, snapshot.Unit, legacySnapshotReason(snapshot), snapshot.FetchedAt, string(payload)).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("导入旧历史快照失败: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM balance_center_snapshots WHERE source=$1 AND source_key=$2`, BalanceCenterLegacySource, snapshot.ID).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

func upsertLegacyCurrentState(ctx context.Context, tx *sql.Tx, siteID, legacyKeyID int64, accountID *int64, snapshotID int64, site legacyBalanceSite, snapshot legacyBalanceSnapshot) error {
	identityKey := fmt.Sprintf("legacy:%d", legacyKeyID)
	if accountID != nil {
		identityKey = fmt.Sprintf("account:%d", *accountID)
	}
	var balance, converted any
	if snapshot.Remaining != nil {
		balance = *snapshot.Remaining
		converted = roundLegacyMoney(*snapshot.Remaining * site.PaymentScale)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO balance_center_current_states (identity_key, site_id, account_id, legacy_key_id, snapshot_id, status, balance, converted_balance, rate_multiplier, conversion_scale, currency, reason, probed_at, source)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (identity_key) DO UPDATE SET site_id=EXCLUDED.site_id, account_id=EXCLUDED.account_id, legacy_key_id=EXCLUDED.legacy_key_id, snapshot_id=EXCLUDED.snapshot_id, status=EXCLUDED.status, balance=EXCLUDED.balance, converted_balance=EXCLUDED.converted_balance, rate_multiplier=EXCLUDED.rate_multiplier, conversion_scale=EXCLUDED.conversion_scale, currency=EXCLUDED.currency, reason=EXCLUDED.reason, probed_at=EXCLUDED.probed_at, source=EXCLUDED.source, updated_at=NOW()
WHERE balance_center_current_states.probed_at < EXCLUDED.probed_at`,
		identityKey, siteID, accountID, legacyKeyID, snapshotID, normalizeLegacySnapshotStatus(snapshot.Status), balance, converted, snapshot.RateMultiplier, site.PaymentScale, snapshot.Unit, legacySnapshotReason(snapshot), snapshot.FetchedAt, BalanceCenterLegacySource)
	return err
}

func insertLegacyManualRow(ctx context.Context, tx *sql.Tx, siteID *int64, row legacyManualRow) (bool, error) {
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_manual_rows (source, source_key, site_id, label, expression, amount, sort_order)
VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (source, source_key) DO NOTHING RETURNING id`,
		BalanceCenterLegacySource, row.ID, siteID, row.Label, row.Expression, row.Amount, row.SortOrder).Scan(new(int64))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func insertLegacyRechargeEvent(ctx context.Context, tx *sql.Tx, siteID *int64, event legacyManualRechargeEvent) (bool, error) {
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_recharge_events (source, source_key, site_id, amount, currency, occurred_at, note)
VALUES ($1,$2,$3,$4,'CNY',$5,$6) ON CONFLICT (source, source_key) DO NOTHING RETURNING id`,
		"legacy_manual_recharge", event.ID, siteID, event.Amount, event.CreatedAt, event.SiteLabel).Scan(new(int64))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func insertLegacyAutomaticRecord(ctx context.Context, tx *sql.Tx, siteID *int64, record legacyAutomaticRecord) (bool, error) {
	sourceKey := legacyAutomaticRecordKey(record)
	occurredAt, err := legacyAutomaticOccurredAt(record)
	if err != nil {
		return false, err
	}
	metadata, _ := json.Marshal(map[string]any{"site_label": record.SiteLabel, "order_no": record.OrderNo, "note": record.Note})
	err = tx.QueryRowContext(ctx, `INSERT INTO balance_center_automatic_records (source, source_key, site_id, amount, currency, occurred_at, record_type, metadata)
VALUES ($1,$2,$3,$4,'CNY',$5,$6,$7::jsonb) ON CONFLICT (source, source_key) DO NOTHING RETURNING id`,
		"legacy_automatic", sourceKey, siteID, record.Amount, occurredAt, record.Source, string(metadata)).Scan(new(int64))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func legacyAutomaticOccurredAt(record legacyAutomaticRecord) (time.Time, error) {
	if occurredAt, err := parseLegacyTime(record.OccurredAt); err == nil {
		return occurredAt, nil
	}
	if !record.ReconciliationCreatedAt.IsZero() {
		return record.ReconciliationCreatedAt, nil
	}
	return time.Time{}, errors.New("旧自动记录缺少稳定发生时间")
}

func legacyAutomaticRecordKey(record legacyAutomaticRecord) string {
	stableID := nonEmptyLegacyID(record.OrderNo, record.ID)
	if stableID == "" {
		stableID = record.RunID + ":" + record.SiteLabel + ":" + record.OccurredAt + ":" + formatLegacyManualAmount(record.Amount)
	}
	return strings.ToLower(strings.TrimSpace(record.Source)) + ":" + stableID
}

func insertLegacyLiandongOrder(ctx context.Context, tx *sql.Tx, order legacyLiandongOrder) (bool, error) {
	payload, _ := json.Marshal(map[string]any{"goods_name": order.GoodsName, "quantity": order.Quantity, "legacy_status": order.Status})
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_liandong_orders (source, source_key, transaction_no, paid_amount, currency, paid_at, status, payload)
VALUES ('liandong',$1,$1,$2,'CNY',$3,$4,$5::jsonb) ON CONFLICT (transaction_no) DO NOTHING RETURNING id`,
		order.TradeNumber, order.TotalAmount, order.CreatedAt, strconv.Itoa(order.Status), string(payload)).Scan(new(int64))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func insertLegacyReconciliation(ctx context.Context, tx *sql.Tx, item legacyReconciliation) (bool, error) {
	details, _ := json.Marshal(legacyReconciliationDetails(item))
	err := tx.QueryRowContext(ctx, `INSERT INTO balance_center_reconciliations (source, source_key, expected_amount, actual_amount, difference_amount, status, details, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT (source, source_key) DO NOTHING RETURNING id`,
		BalanceCenterLegacySource, item.ID, item.ExpectedAmount, item.ActualAmount, item.DifferenceAmount, item.Status, string(details), item.CreatedAt).Scan(new(int64))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func legacyReconciliationDetails(item legacyReconciliation) map[string]any {
	if item.Status == "" && strings.TrimSpace(item.ResultJSON) != "" {
		applyLegacyReconciliationResult(&item)
	}
	return map[string]any{
		"status":            item.Status,
		"manual_total":      item.ExpectedAmount,
		"automatic_total":   item.ActualAmount,
		"difference_amount": item.DifferenceAmount,
	}
}

func maybeImportLegacySiteCredential(ctx context.Context, tx *sql.Tx, site legacyBalanceSite, settings map[string]string, matches map[string]*int64, keys []legacyBalanceKey, options BalanceCenterLegacyImportOptions, report *BalanceCenterLegacyImportReport) error {
	if site.LoginUsername == "" || options.PasswordEncryptor == nil || options.OldMasterKey == "" {
		return nil
	}
	matched := false
	for _, key := range keys {
		if key.SiteID == site.ID && matches[key.ID] != nil {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	cipherText := settings["siteLoginPassword."+site.ID]
	if cipherText == "" {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upstream_site_credentials WHERE host=$1)`, site.Domain).Scan(&exists); err != nil {
		return err
	}
	if exists {
		report.SkippedSiteCredentials++
		return nil
	}
	password, err := decryptLegacySub2WebSecret(cipherText, options.OldMasterKey)
	if err != nil {
		report.SkippedSiteCredentials++
		return nil
	}
	encrypted, err := options.PasswordEncryptor.Encrypt(password)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO upstream_site_credentials (host, login_username, password_encrypted) VALUES ($1,$2,$3)`, site.Domain, site.LoginUsername, encrypted); err != nil {
		return err
	}
	report.ImportedSiteCredentials++
	return nil
}

func EvaluateLegacyManualExpression(expression string) (float64, error) {
	expr := strings.TrimSpace(strings.ReplaceAll(expression, "×", "*"))
	if expr == "" {
		return 0, errors.New("表达式为空")
	}
	if index := strings.LastIndex(expr, "="); index >= 0 {
		expr = strings.TrimSpace(expr[index+1:])
	}
	total := 0.0
	for _, term := range strings.Split(expr, "+") {
		product := 1.0
		parts := strings.Split(term, "*")
		for _, part := range parts {
			value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil || !isFiniteLegacyNumber(value) {
				return 0, fmt.Errorf("无法解析数值 %q", part)
			}
			product *= value
		}
		total += product
	}
	return roundLegacyMoney(total), nil
}

func decryptLegacySub2WebSecret(payload, masterKey string) (string, error) {
	parts := strings.Split(payload, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", errors.New("旧密文格式无效")
	}
	key, err := decodeLegacyBase64URL(masterKey)
	if err != nil || len(key) != 32 {
		return "", errors.New("旧 master key 无效")
	}
	iv, err := decodeLegacyBase64URL(parts[1])
	if err != nil {
		return "", err
	}
	tag, err := decodeLegacyBase64URL(parts[2])
	if err != nil {
		return "", err
	}
	encrypted, err := decodeLegacyBase64URL(parts[3])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, append(encrypted, tag...), nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func decodeLegacyBase64URL(value string) ([]byte, error) {
	if out, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return out, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func legacySecretFingerprint(value string) string {
	sum := sha256.Sum256([]byte("sub2api-balance-center:" + value))
	return base64.RawStdEncoding.EncodeToString(sum[:])
}

func normalizeBalanceCenterLegacyDomain(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	return strings.ToLower(strings.TrimSpace(rawURL))
}

func legacyProbeSupported(site legacyBalanceSite) bool {
	name := strings.ToLower(strings.TrimSpace(site.Name))
	domain := strings.ToLower(strings.TrimSpace(site.Domain))
	return name != "aigw" && name != "aigc" && !strings.Contains(domain, "aigw") && !strings.Contains(domain, "aigc")
}

func normalizeLegacySnapshotStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok":
		return "ok"
	case "unsupported":
		return "unsupported"
	case "error", "failed":
		return "failed"
	default:
		return "unknown"
	}
}

func legacySnapshotReason(snapshot legacyBalanceSnapshot) string {
	if normalizeLegacySnapshotStatus(snapshot.Status) == "ok" {
		return ""
	}
	if snapshot.ErrorCode != "" {
		return trimLegacyReason(snapshot.ErrorCode)
	}
	return "legacy_probe_failed"
}

func siteIDForLabel(ids map[string]int64, sites map[string]legacyBalanceSite, label string) *int64 {
	key := canonicalLegacyLabel(label)
	for legacyID, site := range sites {
		if canonicalLegacyLabel(site.Name) == key {
			id := ids[legacyID]
			return &id
		}
	}
	return nil
}

func canonicalLegacyLabel(value string) string {
	return strings.NewReplacer("api", "", "API", "", " ", "", "_", "", "-", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}

func parseLegacyTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("时间为空")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported time %q", value)
}

func legacySQLiteHasTable(ctx context.Context, db *sql.DB, table string) bool {
	var exists int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists)
	return exists > 0
}

func legacySQLiteHasColumn(ctx context.Context, db *sql.DB, table, column string) bool {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err == nil && name == column {
			return true
		}
	}
	return false
}

func nonEmptyLegacyID(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func trimLegacyReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 100 {
		return value[:100]
	}
	return value
}

func isFiniteLegacyNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func roundLegacyMoney(value float64) float64 {
	return math.Round((value+math.SmallestNonzeroFloat64)*100) / 100
}

func formatLegacyManualAmount(value float64) string {
	return strconv.FormatFloat(roundLegacyMoney(value), 'f', -1, 64)
}
