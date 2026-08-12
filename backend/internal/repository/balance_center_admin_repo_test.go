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
		WithArgs("manual", "tx-1", nil, nil, 10.0, "CNY", now, "备注").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAdminRepository)
	item, err := repo.CreateBalanceCenterRechargeEvent(context.Background(), &service.BalanceCenterRechargeEvent{
		Source: "manual", SourceKey: "tx-1", Amount: 10, Currency: "CNY", OccurredAt: now, Note: "备注",
	})
	require.NoError(t, err)
	require.Equal(t, int64(9), item.ID)
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
