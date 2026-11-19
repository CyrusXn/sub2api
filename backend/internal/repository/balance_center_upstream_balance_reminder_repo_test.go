package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteBalanceObservationFirstSampleOnlyEstablishesBaseline(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 20.0, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), false, nil, nil, nil, "USD", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewUpstreamSiteBalanceReminderRepository(db)
	reminder, err := repo.SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 20,
		Currency: "USD", ObservedAt: now,
	})

	require.NoError(t, err)
	require.Nil(t, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationIncreasePersistsReminderAndFiveMinuteNextProbe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).AddRow(20.0, "USD", false, nil, nil, "USD", nil))
	expectAutomaticRechargeReview(mock, "site.example", now, 5)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 25.0, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), true, 20.0, 25.0, 5.0, "USD", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT host, display_name, alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"host", "display_name", "alert_old_balance", "alert_new_balance", "alert_delta", "alert_currency", "alert_detected_at", "alert_version"}).
			AddRow("site.example", "站点", 20.0, 25.0, 5.0, "USD", now, 1))
	mock.ExpectCommit()

	repo := NewUpstreamSiteBalanceReminderRepository(db)
	reminder, err := repo.SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 25,
		Currency: "USD", ObservedAt: now,
	})

	require.NoError(t, err)
	require.Equal(t, &service.UpstreamSiteBalanceReminder{
		Host: "site.example", DisplayName: "站点", OldBalance: 20, NewBalance: 25,
		Delta: 5, Currency: "USD", DetectedAt: now, Version: 1,
	}, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationIncreaseUpdatesExistingReminderFromOriginalBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 11, 9, 5, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).AddRow(25.0, "USD", true, 20.0, 5.0, "USD", now.Add(-5*time.Minute)))
	expectAutomaticRechargeReview(mock, "site.example", now, 5)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 30.0, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), true, 20.0, 30.0, 10.0, "USD", now.Add(-5*time.Minute)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT host, display_name, alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"host", "display_name", "alert_old_balance", "alert_new_balance", "alert_delta", "alert_currency", "alert_detected_at", "alert_version"}).
			AddRow("site.example", "站点", 20.0, 30.0, 10.0, "USD", now.Add(-5*time.Minute), 2))
	mock.ExpectCommit()

	repo := NewUpstreamSiteBalanceReminderRepository(db)
	reminder, err := repo.SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 30,
		Currency: "USD", ObservedAt: now,
	})

	require.NoError(t, err)
	require.Equal(t, &service.UpstreamSiteBalanceReminder{
		Host: "site.example", DisplayName: "站点", OldBalance: 20, NewBalance: 30,
		Delta: 10, Currency: "USD", DetectedAt: now.Add(-5 * time.Minute), Version: 2,
	}, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationCurrencyChangeOnlyResetsBaseline(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).AddRow(10.0, "USD", false, nil, nil, "USD", nil))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 70.0, "CNY", now, now.Add(service.UpstreamSiteBalancePollInterval), false, nil, nil, nil, "CNY", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	reminder, err := NewUpstreamSiteBalanceReminderRepository(db).SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 70,
		Currency: "CNY", ObservedAt: now,
	})
	require.NoError(t, err)
	require.Nil(t, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationReplacesDifferentCurrencyReminder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 16, 2, 5, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).
			AddRow(70.0, "CNY", true, 10.0, 5.0, "USD", now.Add(-time.Hour)))
	expectAutomaticRechargeReview(mock, "site.example", now, 5)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 75.0, "CNY", now, now.Add(service.UpstreamSiteBalancePollInterval), true, 70.0, 75.0, 5.0, "CNY", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT host, display_name, alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"host", "display_name", "alert_old_balance", "alert_new_balance", "alert_delta", "alert_currency", "alert_detected_at", "alert_version"}).
			AddRow("site.example", "站点", 70.0, 75.0, 5.0, "CNY", now, 3))
	mock.ExpectCommit()

	reminder, err := NewUpstreamSiteBalanceReminderRepository(db).SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 75,
		Currency: "CNY", ObservedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, &service.UpstreamSiteBalanceReminder{
		Host: "site.example", DisplayName: "站点", OldBalance: 70, NewBalance: 75,
		Delta: 5, Currency: "CNY", DetectedAt: now, Version: 3,
	}, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationSmallIncreaseStillUpdatesBaseline(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).AddRow(20.0, "USD", false, nil, nil, "USD", nil))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 20.5, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), false, nil, nil, nil, "USD", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	reminder, err := NewUpstreamSiteBalanceReminderRepository(db).SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 20.5,
		Currency: "USD", ObservedAt: now,
	})
	require.NoError(t, err)
	require.Nil(t, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationPendingReminderExcludesSmallIncrease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 11, 17, 9, 10, 0, 0, time.UTC)
	detectedAt := now.Add(-10 * time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).AddRow(25.5, "USD", true, 20.0, 5.0, "USD", detectedAt))
	expectAutomaticRechargeReview(mock, "site.example", now, 5)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 30.5, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), true, 20.0, 30.5, 10.0, "USD", detectedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT host, display_name, alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"host", "display_name", "alert_old_balance", "alert_new_balance", "alert_delta", "alert_currency", "alert_detected_at", "alert_version"}).
			AddRow("site.example", "站点", 20.0, 30.5, 10.0, "USD", detectedAt, 2))
	mock.ExpectCommit()

	reminder, err := NewUpstreamSiteBalanceReminderRepository(db).SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 30.5,
		Currency: "USD", ObservedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, &service.UpstreamSiteBalanceReminder{
		Host: "site.example", DisplayName: "站点", OldBalance: 20, NewBalance: 30.5,
		Delta: 10, Currency: "USD", DetectedAt: detectedAt, Version: 2,
	}, reminder)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteBalanceObservationDoesNotReuseInactiveOrSmallReminder(t *testing.T) {
	for _, tt := range []struct {
		name   string
		active bool
		delta  float64
	}{
		{name: "已解决提醒即使残留金额也不合并", delta: 5},
		{name: "旧小额提醒不合并", active: true, delta: 0.5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			now := time.Date(2026, 11, 17, 9, 15, 0, 0, time.UTC)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta("SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
				WithArgs("site.example").
				WillReturnRows(sqlmock.NewRows([]string{"balance", "currency", "alert_active", "alert_old_balance", "alert_delta", "alert_currency", "alert_detected_at"}).
					AddRow(25.5, "USD", tt.active, 20.0, tt.delta, "USD", now.Add(-time.Hour)))
			expectAutomaticRechargeReview(mock, "site.example", now, 1)
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
				WithArgs("site.example", "站点", "https://site.example", 26.5, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), true, 25.5, 26.5, 1.0, "USD", now).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT host, display_name, alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version")).
				WithArgs("site.example").
				WillReturnRows(sqlmock.NewRows([]string{"host", "display_name", "alert_old_balance", "alert_new_balance", "alert_delta", "alert_currency", "alert_detected_at", "alert_version"}).
					AddRow("site.example", "站点", 25.5, 26.5, 1.0, "USD", now, 3))
			mock.ExpectCommit()

			reminder, err := NewUpstreamSiteBalanceReminderRepository(db).SaveUpstreamSiteBalanceObservation(context.Background(), service.UpstreamSiteBalanceObservation{
				Host: "site.example", DisplayName: "站点", BaseURL: "https://site.example", Balance: 26.5,
				Currency: "USD", ObservedAt: now,
			})
			require.NoError(t, err)
			require.Equal(t, &service.UpstreamSiteBalanceReminder{
				Host: "site.example", DisplayName: "站点", OldBalance: 25.5, NewBalance: 26.5,
				Delta: 1, Currency: "USD", DetectedAt: now, Version: 3,
			}, reminder)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func expectAutomaticRechargeReview(mock sqlmock.Sqlmock, host string, observedAt time.Time, amount int64) {
	sourceKey := host + ":" + observedAt.UTC().Format(time.RFC3339Nano)
	mock.ExpectExec(regexp.QuoteMeta("WITH site AS (")).
		WithArgs(sourceKey, amount, observedAt, host, "站点", "https://site.example").
		WillReturnResult(sqlmock.NewResult(1, 1))
}
