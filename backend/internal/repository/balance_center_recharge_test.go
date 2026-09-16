package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRecordCashIncreasePreservesBaselineAndDeduplicatesSiteKeys(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		baseline       bool
		oldBalance     float64
		oldAccount     int64
		oldScale       float64
		oldCurrency    string
		oldTime        time.Time
		representative int64
		wantBaseline   bool
		wantRecharge   bool
		oldFingerprint string
	}{
		{"首次只建立基线", false, 0, 17, 1, "USD", now.Add(-time.Minute), 17, true, false, "current"},
		{"正增量入账并发送", true, 8, 17, 1, "USD", now.Add(-time.Minute), 17, true, true, "current"},
		{"消费不记充值", true, 11, 17, 1, "USD", now.Add(-time.Minute), 17, true, false, "current"},
		{"换Key只重建", true, 8, 17, 1, "USD", now.Add(-time.Minute), 17, true, false, "previous"},
		{"相同余额不记充值", true, 10, 17, 1, "USD", now.Add(-time.Minute), 17, true, false, "current"},
		{"换算改变只重建", true, 8, 17, 0.1, "USD", now.Add(-time.Minute), 17, true, false, "current"},
		{"币种改变只重建", true, 8, 17, 1, "CNY", now.Add(-time.Minute), 17, true, false, "current"},
		{"代表账号改变只重建", true, 8, 16, 1, "USD", now.Add(-time.Minute), 17, true, false, "current"},
		{"其他Key不记第二次", true, 8, 16, 1, "USD", now.Add(-time.Minute), 16, false, false, "current"},
		{"乱序余额不回退", true, 8, 17, 1, "USD", now.Add(time.Minute), 17, false, false, "current"},
		{"同一快照不重复", true, 8, 17, 1, "USD", now, 17, false, false, "current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT a.id,md5").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint"}).AddRow(tc.representative, "current"))
			if tc.representative == 17 {
				q := mock.ExpectQuery("SELECT account_id,balance,conversion_scale,currency,probed_at").WithArgs(int64(3))
				if tc.baseline {
					q.WillReturnRows(sqlmock.NewRows([]string{"account_id", "balance", "conversion_scale", "currency", "probed_at", "key_fingerprint"}).AddRow(tc.oldAccount, tc.oldBalance, tc.oldScale, tc.oldCurrency, tc.oldTime, tc.oldFingerprint))
				} else {
					q.WillReturnError(sql.ErrNoRows)
				}
			}
			if tc.wantBaseline {
				mock.ExpectExec("INSERT INTO balance_center_recharge_baselines").WithArgs(int64(3), int64(17), 10.0, 1.0, "USD", now, "current").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if tc.wantRecharge {
				mock.ExpectExec("INSERT INTO balance_center_recharge_events").WithArgs("site:3:snapshot:9", int64(3), int64(17), 2.0, now).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectQuery("SELECT COALESCE").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("鱼&鱼"))
				mock.ExpectExec("INSERT INTO alert_email_outbox").WithArgs("3", "site:3:snapshot:9", "ops@example.com", "鱼&鱼-充值成功2.00元", "鱼&amp;鱼-充值成功2.00元", now).WillReturnResult(sqlmock.NewResult(1, 1))
			}
			mock.ExpectCommit()
			tx, err := db.Begin()
			require.NoError(t, err)
			err = recordBalanceCenterCashIncrease(context.Background(), tx, 3, 9, 1, &service.BalanceCenterSnapshot{Source: "sub2api_probe", RechargeKeyFingerprint: "current", AccountID: int64Ptr(17), ConvertedBalance: float64Ptr(10), Currency: "USD", ProbedAt: now, RechargeRecipients: []string{"ops@example.com"}})
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCashRechargeFailureRollsBackSnapshotTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO balance_center_sites").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectExec("INSERT INTO balance_center_account_bindings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO balance_center_snapshots").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectExec("INSERT INTO balance_center_current_states").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT a.id,md5").WillReturnError(errors.New("基线读取失败"))
	mock.ExpectRollback()
	_, err = NewBalanceCenterRepository(db).PersistSnapshot(context.Background(), &service.BalanceCenterSnapshot{Source: "sub2api_probe", SourceKey: "probe:17:1", AccountID: int64Ptr(17), ConvertedBalance: float64Ptr(10), NormalizedDomain: "example.com", ProbedAt: time.Now()})
	require.ErrorContains(t, err, "记录现金余额充值增量失败")
	require.NoError(t, mock.ExpectationsWereMet())
}
