package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	if report.Snapshots != 3 {
		t.Fatalf("snapshots = %d, want 3", report.Snapshots)
	}
	if report.ManualRows != 15 {
		t.Fatalf("manual rows = %d, want 15", report.ManualRows)
	}
	if report.ManualTotal != BalanceCenterLegacyExpectedManual {
		t.Fatalf("manual total = %.2f, want %.2f", report.ManualTotal, BalanceCenterLegacyExpectedManual)
	}
	if report.ManualRechargeEvents != 1 {
		t.Fatalf("manual recharge events = %d, want 1", report.ManualRechargeEvents)
	}
	if report.AutomaticRecords != 1 {
		t.Fatalf("automatic records = %d, want 1", report.AutomaticRecords)
	}
	if report.LiandongOrders != 1 {
		t.Fatalf("liandong orders = %d, want 1", report.LiandongOrders)
	}
	if report.Reconciliations != 1 {
		t.Fatalf("reconciliations = %d, want 1", report.Reconciliations)
	}
}

func TestImportLegacyBalanceCenterSQLiteRejectsWrongManualBaseline(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, `[{"id":"bad","label":"HBY","expression":"1"}]`)

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{})
	if err == nil {
		t.Fatal("expected wrong manual baseline to fail")
	}
	if report == nil || report.ManualTotal != 1 {
		t.Fatalf("report should expose the parsed wrong total, got %#v", report)
	}
}

func TestImportLegacyBalanceCenterSQLiteAllowsMissingManualRechargeTable(t *testing.T) {
	db := newLegacyBalanceSQLite(t, false)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())

	report, err := ImportLegacyBalanceCenterSQLite(context.Background(), db, nil, BalanceCenterLegacyImportOptions{})
	if err != nil {
		t.Fatalf("missing manual recharge table should be compatible: %v", err)
	}
	if report.ManualRechargeEvents != 0 {
		t.Fatalf("manual recharge events = %d, want 0", report.ManualRechargeEvents)
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
	if report.ManualTotal != BalanceCenterLegacyExpectedManual {
		t.Fatalf("manual total = %.2f, want %.2f", report.ManualTotal, BalanceCenterLegacyExpectedManual)
	}
}

func TestLegacyAutomaticRecordsDeduplicateRepeatedOrdersAcrossReconciliations(t *testing.T) {
	db := newLegacyBalanceSQLite(t, true)
	seedLegacyBalanceSQLite(t, db, legacyManualBaselineRowsJSON())
	var original string
	if err := db.QueryRow(`SELECT input_json FROM reconcile_runs WHERE id='run-1'`).Scan(&original); err != nil {
		t.Fatalf("read reconcile input: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reconcile_runs (id, input_json, result_json, created_at) VALUES ('run-2', ?, '{"status":"same"}', '2026-08-12T10:00:00Z')`, original); err != nil {
		t.Fatalf("insert repeated reconcile: %v", err)
	}

	data, err := loadLegacyBalanceCenterData(context.Background(), db)
	if err != nil {
		t.Fatalf("load legacy data: %v", err)
	}
	if len(data.AutomaticRecords) != 1 {
		t.Fatalf("automatic records = %d, want 1 unique order", len(data.AutomaticRecords))
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
