package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	_ "modernc.org/sqlite"
)

func TestImportLegacyBalanceCenterSQLiteDryRunSummarizesLegacyData(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{})
	if err != nil {
		t.Fatalf("dry-run import should validate the legacy baseline: %v", err)
	}

	if !report.DryRun {
		t.Fatal("nil target DB should keep the import in dry-run mode")
	}
	if report.Sites != 3 {
		t.Fatalf("sites = %d, want 3", report.Sites)
	}
	if report.EnabledKeys != 2 {
		t.Fatalf("enabled keys = %d, want 2", report.EnabledKeys)
	}
	if report.Snapshots != 2 {
		t.Fatalf("snapshots = %d, want 2 latest successful snapshots", report.Snapshots)
	}
	if report.ManualRows != 15 {
		t.Fatalf("manual rows = %d, want 15", report.ManualRows)
	}
	if report.ManualTotal != 2262.37 {
		t.Fatalf("manual total = %.2f, want 2262.37", report.ManualTotal)
	}
	if report.ManualRechargeEvents != 16 {
		t.Fatalf("recharge events = %d, want 1 actual plus 15 opening events", report.ManualRechargeEvents)
	}
	if report.ActualRechargeTotal != 10 || report.OpeningRechargeTotal != 2252.37 {
		t.Fatalf("recharge totals mismatch: actual %.2f opening %.2f", report.ActualRechargeTotal, report.OpeningRechargeTotal)
	}
	if report.AutomaticRecords != 1 || report.SourceAutomaticRecords != 1 || report.DeduplicatedAutomaticRecords != 0 {
		t.Fatalf("automatic record summary mismatch: %#v", report)
	}
	if report.LiandongOrders != 1 {
		t.Fatalf("legacy liandong orders = %d, want 1", report.LiandongOrders)
	}
	if report.Reconciliations != 1 {
		t.Fatalf("legacy reconciliations = %d, want 1", report.Reconciliations)
	}
	if report.UnmatchedRechargeSites != 12 {
		t.Fatalf("unmatched recharge sites = %d, want 12", report.UnmatchedRechargeSites)
	}
}

func TestWriteLegacyBalanceCenterDataPersistsSupplementalHistory(t *testing.T) {
	targetDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	defer targetDB.Close()

	mock.ExpectBegin()
	tx, err := targetDB.Begin()
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	data := &legacyBalanceCenterData{
		ManualRows:       []legacyManualRow{{ID: "row-1", Label: "旧站点", Expression: "10", Amount: 10}},
		AutomaticRecords: []legacyAutomaticRecord{{Source: "legacy", ID: "auto-1", SiteLabel: "旧站点", Amount: 10, OccurredAt: now.Format(time.RFC3339)}},
		LiandongOrders:   []legacyLiandongOrder{{TradeNumber: "trade-1", GoodsName: "充值", TotalAmount: 10, Quantity: 1, Status: 1, CreatedAt: now}},
		Reconciliations:  []legacyReconciliation{{ID: "run-1", Status: "same", CreatedAt: now}},
		Settings:         map[string]string{},
	}
	report := &BalanceCenterLegacyImportReport{}
	mock.ExpectQuery("INSERT INTO balance_center_manual_rows").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO balance_center_automatic_records").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO balance_center_liandong_orders").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectQuery("INSERT INTO balance_center_reconciliations").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(4))
	mock.ExpectRollback()

	if err := writeLegacyBalanceCenterData(context.Background(), tx, data, report, BalanceCenterLegacyImportOptions{}, nil); err != nil {
		t.Fatalf("write supplemental history: %v", err)
	}
	if report.InsertedManualRows != 1 || report.InsertedAutomaticRecords != 1 || report.InsertedLiandongOrders != 1 || report.InsertedReconciliations != 1 {
		t.Fatalf("inserted supplemental counts mismatch: %#v", report)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestLoadLegacyBalanceCenterDataBuildsPerSiteOpeningEvents(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())

	data, err := loadLegacyBalanceCenterData(context.Background(), db)
	if err != nil {
		t.Fatalf("load legacy data: %v", err)
	}

	var total float64
	openingByLabel := map[string]float64{}
	openingNoteByLabel := map[string]string{}
	for _, event := range data.ManualRechargeEvents {
		total += event.Amount
		if strings.HasPrefix(event.ID, "opening:") {
			openingByLabel[event.SiteLabel] = event.Amount
			openingNoteByLabel[event.SiteLabel] = event.Note
		}
	}
	if roundLegacyMoney(total) != 2262.37 {
		t.Fatalf("actual plus opening total = %.2f, want 2262.37", total)
	}
	if openingByLabel["HBY"] != 327 {
		t.Fatalf("HBY opening = %.2f, want 327", openingByLabel["HBY"])
	}
	if !strings.Contains(openingNoteByLabel["HBY"], "原表达式：337") {
		t.Fatalf("HBY opening note must preserve the source expression, got %q", openingNoteByLabel["HBY"])
	}
	if openingByLabel["Fox"] != 10 {
		t.Fatalf("unmatched Fox opening must remain independently attributable, got %.2f", openingByLabel["Fox"])
	}
}

func TestLoadLegacySnapshotsKeepsLatestSuccessfulSnapshotPerKey(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())
	if _, err := db.Exec(`INSERT INTO snapshots (id, site_id, key_id, remaining, unit, rate_multiplier, status, error_code, fetched_at)
		VALUES ('snap-hby-new-failed', 'site-hby', 'key-hby', NULL, 'USD', NULL, 'error', 'upstream_error', '2026-08-12T10:00:00Z'),
		       ('snap-hby-new-ok', 'site-hby', 'key-hby', 12, 'USD', 0.8, 'ok', '', '2026-08-12T09:30:00Z')`); err != nil {
		t.Fatalf("insert snapshot history: %v", err)
	}

	items, err := loadLegacySnapshots(context.Background(), db)
	if err != nil {
		t.Fatalf("load latest snapshots: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("latest successful snapshots = %d, want 2", len(items))
	}
	if items[0].ID != "snap-hby-new-ok" || items[0].Remaining == nil || *items[0].Remaining != 12 {
		t.Fatalf("HBY latest successful snapshot mismatch: %#v", items[0])
	}
}

func TestUniqueLegacyAccountMatchRejectsAmbiguousCandidates(t *testing.T) {
	fingerprint := legacySecretFingerprint("same-key")
	accounts := []targetAccountFingerprint{
		{AccountID: 11, Domain: "example.com", Fingerprint: fingerprint},
		{AccountID: 12, Domain: "example.com", Fingerprint: fingerprint},
	}
	if id, ambiguous := uniqueLegacyAccountIDWithStatus("example.com", fingerprint, accounts); id != nil || !ambiguous {
		t.Fatalf("ambiguous account match must stay unbound, got %v", id)
	}
	accounts = accounts[:1]
	if id, ambiguous := uniqueLegacyAccountIDWithStatus("example.com", fingerprint, accounts); id == nil || ambiguous || *id != 11 {
		t.Fatalf("unique account match = %v, want 11", id)
	}
}

func TestImportLegacyBalanceCenterSQLiteRejectsWrongManualBaseline(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, `[{"id":"bad","label":"HBY","expression":"20"}]`)

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{
		ExpectedManifest: &BalanceCenterLegacyImportManifest{ManualRows: 1, ManualTotal: 21},
	})
	if err == nil {
		t.Fatal("expected wrong manual baseline to fail")
	}
	if report == nil || report.ManualTotal != 20 {
		t.Fatalf("report should expose the parsed wrong total, got %#v", report)
	}
}

func TestImportLegacyBalanceCenterSQLiteAcceptsCurrentManualBaselineWithoutHardcodedTotal(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, `[{"id":"current","label":"新基线","expression":"3229.77"}]`)

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{})
	if err != nil {
		t.Fatalf("current immutable backup should define its own baseline: %v", err)
	}
	if report.ManualRows != 1 || report.ManualTotal != 3229.77 {
		t.Fatalf("unexpected current baseline: %#v", report)
	}
}

func TestValidateLegacyImportManifestChecksAllSourceCountsAndHash(t *testing.T) {
	report := &BalanceCenterLegacyImportReport{
		Sites: 14, LegacyKeys: 36, EnabledKeys: 36, Snapshots: 139841,
		ManualRows: 16, ManualTotal: 3229.77, ManualRechargeEvents: 15,
		AutomaticRecords: 5, LiandongOrders: 0, Reconciliations: 25,
	}
	manifest := LegacyImportManifestFromReport(report, "sha256-value")
	if err := ValidateLegacyImportManifest(report, "sha256-value", &manifest); err != nil {
		t.Fatalf("matching manifest should pass: %v", err)
	}
	manifest.Snapshots++
	if err := ValidateLegacyImportManifest(report, "sha256-value", &manifest); err == nil {
		t.Fatal("snapshot count drift must fail")
	}
	manifest.Snapshots--
	if err := ValidateLegacyImportManifest(report, "other-hash", &manifest); err == nil {
		t.Fatal("SQLite hash drift must fail")
	}
}

func TestImportLegacyBalanceCenterSQLiteAllowsMissingManualRechargeTable(t *testing.T) {
	db := newLegacyBalanceSQLite(t, false)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{})
	if err != nil {
		t.Fatalf("missing manual recharge table should be compatible: %v", err)
	}
	if report.ManualRechargeEvents != 15 || report.ActualRechargeEvents != 0 || report.OpeningRechargeEvents != 15 {
		t.Fatalf("missing actual table should produce opening events only, got %#v", report)
	}
}

func TestImportLegacyBalanceCenterSQLiteRestoresManualRowsFromLatestReconcileInput(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())
	if _, err := db.Exec(`DELETE FROM settings WHERE key='calculator.manualRows'`); err != nil {
		t.Fatalf("delete manual row setting: %v", err)
	}
	inputJSON := `{"manualRows":` + legacyManualBaselineRowsJSON() + `,"automaticRecords":[]}`
	if _, err := db.Exec(`UPDATE reconcile_runs SET input_json=? WHERE id='run-1'`, inputJSON); err != nil {
		t.Fatalf("replace reconcile input: %v", err)
	}

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{})
	if err != nil {
		t.Fatalf("manual rows should fall back to latest reconciliation input: %v", err)
	}
	if report.ManualRows != 15 {
		t.Fatalf("manual rows = %d, want 15", report.ManualRows)
	}
	if report.ManualTotal != 2262.37 {
		t.Fatalf("manual total = %.2f, want 2262.37", report.ManualTotal)
	}
}

func TestLegacyAutomaticRecordsDeduplicateRepeatedOrdersAcrossReconciliations(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())
	const firstCreatedAt = "2026-08-12T09:00:00Z"
	const firstOccurredAt = "2026-08-11T08:00:00Z"
	if _, err := db.Exec(`UPDATE reconcile_runs SET created_at=? WHERE id='run-1'`, firstCreatedAt); err != nil {
		t.Fatalf("更新首次对账时间失败: %v", err)
	}
	var original string
	if err := db.QueryRow(`SELECT input_json FROM reconcile_runs WHERE id='run-1'`).Scan(&original); err != nil {
		t.Fatalf("read reconcile input: %v", err)
	}
	var firstInput map[string]any
	if err := json.Unmarshal([]byte(original), &firstInput); err != nil {
		t.Fatalf("解析首次对账输入失败: %v", err)
	}
	records := firstInput["automaticRecords"].([]any)
	records[0].(map[string]any)["occurredAt"] = firstOccurredAt
	firstBytes, err := json.Marshal(firstInput)
	if err != nil {
		t.Fatalf("编码首次对账输入失败: %v", err)
	}
	if _, err := db.Exec(`UPDATE reconcile_runs SET input_json=? WHERE id='run-1'`, string(firstBytes)); err != nil {
		t.Fatalf("更新首次自动记录失败: %v", err)
	}
	delete(records[0].(map[string]any), "occurredAt")
	secondBytes, err := json.Marshal(firstInput)
	if err != nil {
		t.Fatalf("编码第二次对账输入失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reconcile_runs (id, input_json, result_json, created_at) VALUES ('run-2', ?, '{"status":"same"}', '2026-08-12T10:00:00Z')`, string(secondBytes)); err != nil {
		t.Fatalf("insert repeated reconcile: %v", err)
	}

	_, automatic, err := loadLegacyReconciliations(context.Background(), db)
	if err != nil {
		t.Fatalf("load legacy data: %v", err)
	}
	if len(automatic) != 1 {
		t.Fatalf("parsed automatic records = %d, want 1 unique order", len(automatic))
	}
	want, err := parseLegacyTime(firstCreatedAt)
	if err != nil {
		t.Fatalf("解析首次对账时间失败: %v", err)
	}
	if !automatic[0].ReconciliationCreatedAt.Equal(want) {
		t.Fatalf("重复订单回退时间 = %v, want first seen %v", automatic[0].ReconciliationCreatedAt, want)
	}
	gotOccurredAt, err := legacyAutomaticOccurredAt(automatic[0])
	if err != nil {
		t.Fatalf("选择重复订单时间失败: %v", err)
	}
	wantOccurredAt, err := parseLegacyTime(firstOccurredAt)
	if err != nil {
		t.Fatalf("解析首次实际时间失败: %v", err)
	}
	if !gotOccurredAt.Equal(wantOccurredAt) {
		t.Fatalf("重复订单实际时间 = %v, want first valid %v", gotOccurredAt, wantOccurredAt)
	}
}

func TestLegacyAutomaticRecordWithoutOccurredAtUsesReconciliationCreatedAt(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())
	const createdAt = "2026-08-12T10:20:30Z"
	inputJSON := `{"manualRows":[],"automaticRecords":[{"source":"legacy","id":"stable-time","siteLabel":"HBY","amount":12.34}]}`
	if _, err := db.Exec(`UPDATE reconcile_runs SET input_json=?, created_at=? WHERE id='run-1'`, inputJSON, createdAt); err != nil {
		t.Fatalf("更新旧对账记录失败: %v", err)
	}

	_, automatic, err := loadLegacyReconciliations(context.Background(), db)
	if err != nil {
		t.Fatalf("读取旧数据失败: %v", err)
	}
	if len(automatic) != 1 {
		t.Fatalf("parsed automatic records = %d, want 1", len(automatic))
	}
	want, err := parseLegacyTime(createdAt)
	if err != nil {
		t.Fatalf("解析测试时间失败: %v", err)
	}
	if !automatic[0].ReconciliationCreatedAt.Equal(want) {
		t.Fatalf("回退时间 = %v, want %v", automatic[0].ReconciliationCreatedAt, want)
	}
}

func TestLegacyAutomaticOccurredAtRejectsMissingStableTime(t *testing.T) {
	if _, err := legacyAutomaticOccurredAt(legacyAutomaticRecord{OccurredAt: "invalid"}); err == nil {
		t.Fatal("实际时间和对账时间均不可用时必须拒绝导入")
	}
}

func TestLegacyReconciliationDetailsExcludeRawInput(t *testing.T) {
	item := legacyReconciliation{
		ID:         "run-sensitive",
		InputJSON:  `{"automaticRecords":[],"cookie":"secret-cookie"}`,
		ResultJSON: `{"status":"same","manualTotal":10,"automaticTotal":10,"differenceAmount":0}`,
	}
	details := legacyReconciliationDetails(item)
	serialized, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal reconciliation details: %v", err)
	}
	if strings.Contains(string(serialized), "secret-cookie") {
		t.Fatal("reconciliation details must not persist raw legacy input")
	}
	if !strings.Contains(string(serialized), `"status":"same"`) {
		t.Fatalf("sanitized details should preserve result summary: %s", serialized)
	}
}

func TestEvaluateLegacyManualExpressionKeepsOriginalCalculatorSemantics(t *testing.T) {
	cases := map[string]float64{
		"34+5+10+10+10+10": 79,
		"10+93×0.99":       102.07,
		"150+10=160":       160,
		"50×2+20×4+10+1":   191,
	}
	for expression, want := range cases {
		got, err := EvaluateLegacyManualExpression(expression)
		if err != nil {
			t.Fatalf("EvaluateLegacyManualExpression(%q) error: %v", expression, err)
		}
		if got != want {
			t.Fatalf("EvaluateLegacyManualExpression(%q) = %.2f, want %.2f", expression, got, want)
		}
	}
}

func TestLegacyProbeSupportedDisablesAIGWAndAIGC(t *testing.T) {
	if legacyProbeSupported(legacyBalanceSite{Name: "AIGW", Domain: "aigw.example"}) {
		t.Fatal("AIGW should be imported as historical-only")
	}
	if legacyProbeSupported(legacyBalanceSite{Name: "aigc", Domain: "aigc.example"}) {
		t.Fatal("aigc should be imported as historical-only")
	}
	if !legacyProbeSupported(legacyBalanceSite{Name: "HBY", Domain: "hubway.cc"}) {
		t.Fatal("ordinary upstream site should remain probe-supported")
	}
}

func TestParseBalanceCenterLiandongCurlRestrictsEndpointAndNormalizesBody(t *testing.T) {
	curl := `curl 'https://pay.ldxp.cn/shopApi/Order/list' -H 'cookie: session=secret' -H 'content-length: 123' -H 'x-requested-with: XMLHttpRequest' --data-raw '{"keywords":"13800000000","status":0,"current":9}'`

	request, err := ParseBalanceCenterLiandongCurl(curl)
	if err != nil {
		t.Fatalf("ParseBalanceCenterLiandongCurl returned error: %v", err)
	}
	if request.URL != "https://pay.ldxp.cn/shopApi/Order/list" {
		t.Fatalf("url = %q", request.URL)
	}
	if request.Keywords != "13800000000" {
		t.Fatalf("keywords = %q", request.Keywords)
	}
	if _, exists := request.Headers["content-length"]; exists {
		t.Fatal("content-length must not be replayed")
	}
	if request.Headers["cookie"] != "session=secret" {
		t.Fatal("cookie header should be preserved only in encrypted session storage")
	}
	if request.Body["status"] != float64(1) || request.Body["current"] != float64(1) || request.Body["pageSize"] != float64(999999) {
		t.Fatalf("request body was not normalized: %#v", request.Body)
	}
}

func TestParseBalanceCenterLiandongCurlRejectsUnexpectedEndpoint(t *testing.T) {
	_, err := ParseBalanceCenterLiandongCurl(`curl 'https://example.com/shopApi/Order/list' --data-raw '{"keywords":"138"}'`)
	if err == nil {
		t.Fatal("unexpected host should be rejected")
	}
	_, err = ParseBalanceCenterLiandongCurl(`curl 'https://pay.ldxp.cn/other' --data-raw '{"keywords":"138"}'`)
	if err == nil {
		t.Fatal("unexpected path should be rejected")
	}
}

func newLegacyBalanceSQLite(t *testing.T, withManualEvents bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE sites (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			base_url TEXT NOT NULL,
			alert_threshold REAL NOT NULL DEFAULT 0.6,
			economy_threshold REAL NOT NULL DEFAULT 2,
			hidden INTEGER NOT NULL DEFAULT 0,
			login_username TEXT,
			payment_scale REAL NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE site_keys (
			id TEXT PRIMARY KEY,
			site_id TEXT NOT NULL,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			secret_cipher TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE snapshots (
			id TEXT PRIMARY KEY,
			site_id TEXT NOT NULL,
			key_id TEXT,
			remaining REAL,
			unit TEXT,
			rate_multiplier REAL,
			recent_spend REAL,
			has_recent_usage INTEGER,
			status TEXT NOT NULL,
			error_code TEXT,
			error_message TEXT,
			fetched_at TEXT NOT NULL
		)`,
		`CREATE TABLE liandong_orders (
			trade_number TEXT PRIMARY KEY,
			goods_name TEXT NOT NULL,
			total_amount REAL NOT NULL,
			quantity INTEGER NOT NULL,
			status INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE reconcile_runs (
			id TEXT PRIMARY KEY,
			input_json TEXT NOT NULL,
			result_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	if withManualEvents {
		statements = append(statements, `CREATE TABLE manual_recharge_events (
			id TEXT PRIMARY KEY,
			row_id TEXT NOT NULL,
			site_label TEXT NOT NULL,
			amount REAL NOT NULL,
			created_at TEXT NOT NULL
		)`)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	return db
}

func seedLegacyBalanceSQLite(t *testing.T, db *sql.DB, manualRowsJSON string) {
	t.Helper()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for _, site := range []struct {
		id, name, provider, baseURL string
		scale                       float64
	}{
		{"site-hby", "HBY", "sub2api", "https://hubway.cc", 1},
		{"site-yigpt", "Yigpt", "newapi", "https://api.yigpt.cc", 0.1},
		{"site-aigw", "AIGW", "newapi", "https://aigw.example", 1},
	} {
		if _, err := db.Exec(`INSERT INTO sites (id, name, provider, base_url, payment_scale, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, site.id, site.name, site.provider, site.baseURL, site.scale, now, now); err != nil {
			t.Fatalf("insert site: %v", err)
		}
	}
	for _, key := range []struct {
		id, siteID, name string
		enabled          int
	}{
		{"key-hby", "site-hby", "HBY Key", 1},
		{"key-yigpt", "site-yigpt", "Yigpt Key", 1},
		{"key-aigw", "site-aigw", "AIGW Key", 0},
	} {
		if _, err := db.Exec(`INSERT INTO site_keys (id, site_id, name, enabled, secret_cipher, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, key.id, key.siteID, key.name, key.enabled, "v1.redacted.tag.cipher", now, now); err != nil {
			t.Fatalf("insert key: %v", err)
		}
	}
	for _, snapshot := range []struct {
		id, siteID, keyID, status string
		remaining, rate           float64
	}{
		{"snap-hby", "site-hby", "key-hby", "ok", 10, 1},
		{"snap-yigpt", "site-yigpt", "key-yigpt", "ok", 20, 0.2},
		{"snap-aigw", "site-aigw", "key-aigw", "error", 0, 0},
	} {
		if _, err := db.Exec(`INSERT INTO snapshots (id, site_id, key_id, remaining, unit, rate_multiplier, status, error_code, fetched_at) VALUES (?, ?, ?, ?, 'USD', ?, ?, '', ?)`, snapshot.id, snapshot.siteID, snapshot.keyID, snapshot.remaining, snapshot.rate, snapshot.status, now); err != nil {
			t.Fatalf("insert snapshot: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('calculator.manualRows', ?)`, manualRowsJSON); err != nil {
		t.Fatalf("insert manual rows setting: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO liandong_orders (trade_number, goods_name, total_amount, quantity, status, created_at) VALUES ('trade-1', 'Yigpt 兑换码', 60, 1, 1, ?)`, now); err != nil {
		t.Fatalf("insert liandong order: %v", err)
	}
	if legacySQLiteHasTable(context.Background(), db, "manual_recharge_events") {
		if _, err := db.Exec(`INSERT INTO manual_recharge_events (id, row_id, site_label, amount, created_at) VALUES ('manual-event-1', 'row-hby', 'HBY', 10, ?)`, now); err != nil {
			t.Fatalf("insert manual recharge event: %v", err)
		}
	}
	inputJSON := `{"manualRows":[{"id":"line-1","label":"Yigpt","expression":"60","total":60,"items":[{"amount":60}]}],"automaticRecords":[{"id":"trade-1","source":"liandong","siteLabel":"Yigpt","amount":60,"orderNo":"trade-1","occurredAt":"` + now + `","note":"联动小铺已支付订单"}]}`
	resultJSON := `{"status":"same","manualTotal":60,"automaticTotal":60,"differenceAmount":0}`
	if _, err := db.Exec(`INSERT INTO reconcile_runs (id, input_json, result_json, created_at) VALUES ('run-1', ?, ?, ?)`, inputJSON, resultJSON, now); err != nil {
		t.Fatalf("insert reconcile run: %v", err)
	}
}

func legacyManualBaselineRowsJSON() string {
	return `[
		{"id":"row-hby","label":"HBY","expression":"337"},
		{"id":"row-onebool","label":"OneBool","expression":"34+5+10+10+10+10"},
		{"id":"row-yigpt","label":"Yigpt","expression":"60"},
		{"id":"row-unknown","label":"不知名","expression":"10+93×0.99"},
		{"id":"row-zhiyuan","label":"智元","expression":"150+10=160"},
		{"id":"row-vovo","label":"VoVo","expression":"140+245"},
		{"id":"row-yuyu","label":"鱼鱼","expression":"693+70×0.99"},
		{"id":"row-fox","label":"Fox","expression":"10"},
		{"id":"row-biz","label":"BizDecipher","expression":"80"},
		{"id":"row-pite","label":"Pite","expression":"20+10=30"},
		{"id":"row-boft","label":"BOFT","expression":"10+5+10+20"},
		{"id":"row-wuwei","label":"无畏","expression":"50×2+20×4+10+1"},
		{"id":"row-aigc","label":"AIGC","expression":"10"},
		{"id":"row-aigw","label":"AIGW","expression":"10"},
		{"id":"row-hanhe","label":"寒鹤","expression":"1"}
	]`
}
