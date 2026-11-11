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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 20.0, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), false, nil, nil, nil, nil, nil).
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(20.0))
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT balance FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE")).
		WithArgs("site.example").
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(25.0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_site_balance_observations")).
		WithArgs("site.example", "站点", "https://site.example", 30.0, "USD", now, now.Add(service.UpstreamSiteBalancePollInterval), true, 25.0, 30.0, 5.0, "USD", now).
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
