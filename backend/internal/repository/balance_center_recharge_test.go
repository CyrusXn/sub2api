package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBalanceIncreaseOnlyPersistsSnapshots(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewBalanceCenterRepository(db)
	now := time.Now()
	for i, balance := range []float64{9, 108.75, 108.75, 105} {
		mock.ExpectBegin()
		mock.ExpectQuery("INSERT INTO balance_center_sites").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
		mock.ExpectExec("INSERT INTO balance_center_account_bindings").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("INSERT INTO balance_center_snapshots").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(i + 1))
		mock.ExpectExec("INSERT INTO balance_center_current_states").WillReturnResult(sqlmock.NewResult(0, 1))
		// 余额上涨、重复或下降均只保存余额，不读写充值基线、充值流水或充值邮件。
		mock.ExpectCommit()
		persisted, err := repo.PersistSnapshot(context.Background(), &service.BalanceCenterSnapshot{
			Source: "sub2api_probe", SourceKey: now.Add(time.Duration(i) * time.Second).String(),
			AccountID: int64Ptr(17), ConvertedBalance: &balance,
			NormalizedDomain: "example.com", ProbedAt: now.Add(time.Duration(i) * time.Second),
		})
		require.NoError(t, err)
		require.Equal(t, balance, *persisted.ConvertedBalance)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
