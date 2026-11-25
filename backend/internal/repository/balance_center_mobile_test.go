package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListMobileAlertsSeparatesSiteAlertsFromEmailMirrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &balanceCenterRepository{db: db}
	const prefix = "SELECT id, kind, title, body, created_at FROM mobile_alerts WHERE "
	for _, tc := range []struct {
		after     int64
		condition string
		mirror    bool
		filter    string
	}{
		{0, "created_at >= now() - interval '24 hours'", false, "upstream_host IS NOT NULL"},
		{42, "id > $1", false, "upstream_host IS NOT NULL"},
		{0, "created_at >= now() - interval '24 hours'", true, "kind = 'email_mirror'"},
		{42, "id > $1", true, "kind = 'email_mirror'"},
	} {
		query := mock.ExpectQuery(regexp.QuoteMeta(prefix + tc.condition + " AND " + tc.filter + " ORDER BY id LIMIT 100"))
		if tc.after != 0 {
			query.WithArgs(tc.after)
		}
		query.WillReturnRows(sqlmock.NewRows([]string{"id", "kind", "title", "body", "created_at"}))
		_, err := repo.ListMobileAlerts(context.Background(), tc.after, tc.mirror)
		require.NoError(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMobileRateEventAdvancesStateOnlyAfterSave(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "commit"
		if fail {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			accountID, balance, multiplier := int64(3), 1.0, 0.2
			snapshot := &service.BalanceCenterSnapshot{
				ID: 22, AccountID: &accountID, AccountName: "示例账号", SiteName: "示例站点",
				ConvertedBalance: &balance, RateMultiplier: &multiplier, NormalizedDomain: "test.example",
			}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO mobile_balance_alert_states").WithArgs("account:3").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("FROM mobile_balance_alert_states WHERE identity_key = .1 FOR UPDATE").WithArgs("account:3").
				WillReturnRows(sqlmock.NewRows([]string{"snapshot_id", "low_balance_active", "low_balance_threshold", "multiplier_baseline"}).
					AddRow(int64(21), false, nil, 0.1))
			rate := mock.ExpectExec("SELECT append_mobile_upstream_rate").WithArgs("test.example", "0.1 → 0.2")
			if fail {
				rate.WillReturnError(errors.New("写入失败"))
				mock.ExpectRollback()
			} else {
				rate.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE mobile_balance_alert_states").WithArgs("account:3", int64(22), false, nil, 0.2).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err = (&balanceCenterRepository{db: db}).CaptureMobileBalanceAlerts(context.Background(), snapshot, 5)
			if fail {
				require.ErrorContains(t, err, "写入失败")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
