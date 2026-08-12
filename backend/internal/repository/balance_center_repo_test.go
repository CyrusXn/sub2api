package repository

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBalanceCenterRepositoryPersistSnapshotUsesOneTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO balance_center_sites")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO balance_center_account_bindings")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO balance_center_snapshots")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO balance_center_current_states")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewBalanceCenterRepository(db)
	snapshot := &service.BalanceCenterSnapshot{
		AccountID:        int64Ptr(17),
		SiteName:         "HBY",
		NormalizedDomain: "hubway.cc",
		BaseURL:          "https://hubway.cc",
		Source:           "probe",
		SourceKey:        "probe:17:1",
		Status:           "ok",
		ProbedAt:         now,
	}
	result, err := repo.PersistSnapshot(context.Background(), snapshot)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.SiteID)
	require.Equal(t, int64(9), result.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRepositoryGetAndSaveAlertState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT low_balance_active, multiplier_baseline")).
		WithArgs("account:17").
		WillReturnRows(sqlmock.NewRows([]string{"low_balance_active", "multiplier_baseline"}).AddRow(true, 0.1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO balance_center_alert_states")).
		WithArgs("account:17", int64(9), int64(17), true, 0.2, int64(21), service.BalanceCenterAlertMultiplierChanged, now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAlertRepository)
	state, err := repo.GetAlertState(context.Background(), "account:17", 9, int64Ptr(17))
	require.NoError(t, err)
	require.True(t, state.LowBalanceActive)
	require.Equal(t, 0.1, *state.MultiplierBaseline)

	decision := &service.BalanceCenterAlertDecision{Type: service.BalanceCenterAlertMultiplierChanged, NewValue: float64Ptr(0.2)}
	err = repo.SaveAlertState(context.Background(), "account:17", 9, int64Ptr(17), 21, service.BalanceCenterAlertState{
		LowBalanceActive: true, MultiplierBaseline: float64Ptr(0.2),
	}, decision, now)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRepositoryGetAlertStateReturnsEmptyWhenMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT low_balance_active, multiplier_baseline")).
		WithArgs("account:17").
		WillReturnError(sql.ErrNoRows)

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAlertRepository)
	state, err := repo.GetAlertState(context.Background(), "account:17", 9, int64Ptr(17))
	require.NoError(t, err)
	require.False(t, state.LowBalanceActive)
	require.Nil(t, state.MultiplierBaseline)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterRepositoryRecordsAlertDeliveryIdempotently(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO balance_center_alert_deliveries")).
		WithArgs("account:17", int64(9), int64(17), int64(21), service.BalanceCenterAlertLowBalance,
			"ops@example.com", "[余额中心]低余额告警 - HBY", nil, 4.0, 5.0,
			service.BalanceCenterAlertDeliveryAccepted, "", now, now).
		WillReturnResult(sqlmock.NewResult(1, 1))

	repo := NewBalanceCenterRepository(db).(service.BalanceCenterAlertRepository)
	err = repo.RecordAlertDelivery(context.Background(), &service.BalanceCenterAlertDeliveryInput{
		IdentityKey: "account:17", SiteID: 9, AccountID: int64Ptr(17), SnapshotID: 21,
		AlertType: service.BalanceCenterAlertLowBalance, Recipient: "ops@example.com", Subject: "[余额中心]低余额告警 - HBY",
		NewValue: float64Ptr(4), Threshold: float64Ptr(5), Status: service.BalanceCenterAlertDeliveryAccepted,
		AttemptedAt: now, AcceptedAt: &now,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCenterAlertDeliveryMigrationAddsAuditIdentity(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "226_extend_balance_center_alert_deliveries.sql"))
	require.NoError(t, err)
	sqlText := string(content)
	for _, fragment := range []string{
		"ADD COLUMN IF NOT EXISTS recipient_email",
		"ADD COLUMN IF NOT EXISTS subject",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_balance_center_alert_deliveries_identity",
		"(snapshot_id, alert_type, recipient_email)",
	} {
		require.True(t, strings.Contains(sqlText, fragment), "迁移缺少 %s", fragment)
	}
}

func int64Ptr(value int64) *int64       { return &value }
func float64Ptr(value float64) *float64 { return &value }
