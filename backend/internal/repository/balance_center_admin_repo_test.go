package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBalanceCenterAdminOverviewReadsCurrentStatesOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("FROM balance_center_current_states cs")).
		WillReturnRows(sqlmock.NewRows([]string{
			"site_id", "site_name", "normalized_domain", "base_url", "account_id", "account_name",
			"status", "balance", "converted_balance", "rate_multiplier", "conversion_scale", "currency",
			"reason", "probed_at", "last_used_at", "source",
		}).AddRow(1, "站点", "example.com", "https://example.com", 7, "账号", "ok", 10, 5, 0.5, 0.5, "USD", "", now, now, "sub2api_probe"))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	items, err := repo.ListBalanceCenterOverview(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(7), *items[0].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterSitesOrderByHistoricalRechargeTotal(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery("LEFT JOIN balance_center_recharge_events[\\s\\S]+ORDER BY COALESCE\\(SUM\\(e.amount\\), 0\\) DESC").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "normalized_domain", "base_url", "source", "probe_supported", "historical_recharge_total", "updated_at",
		}).
			AddRow(2, "VoVo", "vovo.example", "https://vovo.example", "sub2api", true, 80.0, now).
			AddRow(1, "HBY", "hby.example", "https://hby.example", "sub2api", true, 20.0, now))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	items, err := repo.ListBalanceCenterSites(context.Background())
	require.NoError(t, err)
	require.Equal(t, "VoVo", items[0].Name)
	require.Equal(t, 80.0, items[0].HistoricalRechargeTotal)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterAdminSnapshotsUsesFiltersAndPagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	start := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	filter := service.BalanceCenterListFilter{Page: 2, PageSize: 100, SiteID: 3, AccountID: 7, Status: "ok", StartTime: &start, EndTime: &end}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM balance_center_snapshots s WHERE s.site_id=$1 AND s.account_id=$2 AND s.status=$3 AND s.probed_at >= $4 AND s.probed_at <= $5")).
		WithArgs(int64(3), int64(7), "ok", start, end).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("FROM balance_center_snapshots s[\\s\\S]+LIMIT \\$6 OFFSET \\$7").
		WithArgs(int64(3), int64(7), "ok", start, end, 100, 100).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "site_id", "account_id", "legacy_key_id", "name", "domain", "base_url", "source",
			"source_key", "status", "balance", "converted", "rate", "scale", "currency", "reason", "probed_at", "payload",
		}))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	page, err := repo.ListBalanceCenterSnapshots(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, 2, page.Page)
	require.Equal(t, 100, page.PageSize)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterAdminReplaceManualRowsUsesTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO balance_center_manual_rows")).
		WithArgs("manual", "HBY", nil, "HBY", "337", 337.0, 0).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	err = repo.ReplaceBalanceCenterManualRows(context.Background(), []service.BalanceCenterManualRow{{
		Source: "manual", SourceKey: "HBY", Label: "HBY", Expression: "337", Amount: 337,
	}})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterAdminRechargeUpsertIsIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (source, source_key) DO UPDATE")).
		WithArgs("manual", "tx-1", nil, "", nil, 10.0, "CNY", now, "备注").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	item, err := repo.CreateBalanceCenterRechargeEvent(context.Background(), &service.BalanceCenterRechargeEvent{
		Source: "manual", SourceKey: "tx-1", Amount: 10, Currency: "CNY", OccurredAt: now, Note: "备注",
	})
	require.NoError(t, err)
	require.Equal(t, int64(9), item.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRechargeSummaryAggregatesFullFilteredRangeBeforePagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	siteID := int64(3)
	newest := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	middle := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery("COALESCE\\(NULLIF\\(BTRIM\\(s.site_label\\)[\\s\\S]+FROM balance_center_recharge_events s[\\s\\S]+LEFT JOIN balance_center_sites bs[\\s\\S]+s.occurred_at >= \\$1[\\s\\S]+s.occurred_at <= \\$2[\\s\\S]+ORDER BY s.occurred_at DESC, s.id DESC").
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source", "source_key", "site_id", "account_id", "amount", "currency", "occurred_at", "note", "site_label", "site_name",
		}).
			AddRow(12, "manual", "tx-12", siteID, nil, 30.0, "CNY", newest, "第二笔", "", "站点 A").
			AddRow(11, "manual", "tx-11", siteID, nil, 20.0, "CNY", middle, "第一笔", "", "站点 A").
			AddRow(10, "legacy_opening", "opening-a", nil, nil, 50.0, "CNY", oldest, "旧系统期初累计，历史日期未知", "HX", "HX").
			AddRow(9, "legacy_opening", "opening-b", nil, nil, 25.0, "CNY", oldest.Add(-time.Hour), "旧系统期初累计，历史日期未知", "Fox", "Fox"))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	summary, err := repo.GetBalanceCenterRechargeSummary(context.Background(), service.BalanceCenterListFilter{
		Page: 2, PageSize: 1, StartTime: &start, EndTime: &end,
	})
	require.NoError(t, err)
	require.Equal(t, 125.0, summary.TotalAmount)
	require.Equal(t, int64(4), summary.Total)
	require.Equal(t, 2, summary.Page)
	require.Equal(t, 1, summary.PageSize)
	require.Len(t, summary.Items, 1)
	require.Equal(t, int64(11), summary.Items[0].ID)
	require.Len(t, summary.Sites, 3)
	require.Equal(t, &siteID, summary.Sites[0].SiteID)
	require.Equal(t, "站点 A", summary.Sites[0].SiteName)
	require.Equal(t, 50.0, summary.Sites[0].TotalAmount)
	require.Equal(t, int64(2), summary.Sites[0].RecordCount)
	require.Len(t, summary.Sites[0].Items, 2)
	require.Nil(t, summary.Sites[1].SiteID)
	require.Equal(t, "HX", summary.Sites[1].SiteName)
	require.Equal(t, 50.0, summary.Sites[1].TotalAmount)
	require.Equal(t, int64(1), summary.Sites[1].RecordCount)
	require.Equal(t, "Fox", summary.Sites[2].SiteName)
	require.Equal(t, 25.0, summary.Sites[2].TotalAmount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRechargeSummaryUsesDecimalForAmounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery("FROM balance_center_recharge_events s").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source", "source_key", "site_id", "account_id", "amount", "currency", "occurred_at", "note", "site_label", "site_name",
		}).
			AddRow(2, "manual", "tx-2", 3, nil, 0.1, "CNY", now, "", "", "站点 A").
			AddRow(1, "manual", "tx-1", 3, nil, 0.2, "CNY", now.Add(-time.Second), "", "", "站点 A"))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	summary, err := repo.GetBalanceCenterRechargeSummary(context.Background(), service.BalanceCenterListFilter{Page: 1, PageSize: 20})

	require.NoError(t, err)
	require.Equal(t, 0.3, summary.TotalAmount)
	require.Equal(t, 0.3, summary.Sites[0].TotalAmount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRechargeSummaryProtectsPaginationOverflow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery("FROM balance_center_recharge_events s").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source", "source_key", "site_id", "account_id", "amount", "currency", "occurred_at", "note", "site_label", "site_name",
		}).AddRow(1, "manual", "tx-1", 3, nil, 10.0, "CNY", now, "", "", "站点 A"))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	maxInt := int(^uint(0) >> 1)
	var summary *service.BalanceCenterRechargeSummary
	require.NotPanics(t, func() {
		summary, err = repo.GetBalanceCenterRechargeSummary(context.Background(), service.BalanceCenterListFilter{Page: maxInt, PageSize: maxInt})
	})
	require.NoError(t, err)
	require.Empty(t, summary.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRechargeSummaryCombinesSiteAndTimeFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	mock.ExpectQuery("s.site_id=\\$1[\\s\\S]+s.occurred_at >= \\$2[\\s\\S]+s.occurred_at <= \\$3").
		WithArgs(int64(7), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source", "source_key", "site_id", "account_id", "amount", "currency", "occurred_at", "note", "site_label", "site_name",
		}))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	_, err = repo.GetBalanceCenterRechargeSummary(context.Background(), service.BalanceCenterListFilter{
		Page: 1, PageSize: 20, SiteID: 7, StartTime: &start, EndTime: &end,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterLiandongSessionStoresEncryptedPayloadOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO balance_center_liandong_sessions")).
		WithArgs("opaque-ciphertext").
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterLiandongRepository)
	require.NoError(t, repo.SaveBalanceCenterLiandongSession(context.Background(), "opaque-ciphertext"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterLiandongOrdersUpsertByTransactionNumber(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	paidAt := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("ON CONFLICT (transaction_no) DO UPDATE")).
		WithArgs("trade-1", 55.5, paidAt, "paid", `{"goods_name":"Yigpt 60元兑换码","quantity":1}`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterLiandongRepository)
	synced, err := repo.UpsertBalanceCenterLiandongOrders(context.Background(), []service.BalanceCenterLiandongOrder{{
		TransactionNo: "trade-1", GoodsName: "Yigpt 60元兑换码", PaidAmount: 55.5,
		Quantity: 1, Status: "paid", PaidAt: paidAt,
	}})
	require.NoError(t, err)
	require.Equal(t, 1, synced)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterAutomaticRecordsSyncUsesPaidLiandongOrders(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("INSERT INTO balance_center_automatic_records[\\s\\S]+FROM balance_center_liandong_orders[\\s\\S]+WHERE status = 'paid'").
		WillReturnResult(sqlmock.NewResult(0, 3))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterLiandongRepository)
	synced, err := repo.SyncBalanceCenterAutomaticRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, synced)
	require.NoError(t, mock.ExpectationsWereMet())
}
