package repository

import (
	"context"
	"regexp"
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

func int64Ptr(value int64) *int64 { return &value }
