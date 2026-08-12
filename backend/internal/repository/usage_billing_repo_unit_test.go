//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	conditionalBalanceDeductSQL = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance`
	overdraftBalanceDeductSQL   = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING balance`
	reserveBatchImageHoldSQL    = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL    = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL    = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	userExistsForBillingSQL     = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`
)

func TestDeductUsageBillingBalance_UsesSufficientBalanceGuard(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(7.5))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 2.5)
	require.NoError(t, err)
	require.True(t, sufficient)
	require.InDelta(t, 7.5, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_RecordsOverdraftWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(-5.0))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 10)
	require.NoError(t, err)
	require.False(t, sufficient)
	require.InDelta(t, -5.0, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_FlagsBalanceOverdraft(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(-5.0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -5.0, *result.NewBalance, 0.000001)
	require.True(t, result.BalanceOverdrafted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingRepositoryApply_PublishesBalanceCenterEventAfterCommit(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	publisher := &balanceCenterUsageEventPublisherSpy{}
	repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
	cmd := &service.UsageBillingCommand{
		RequestID:   "req-balance-center-success",
		APIKeyID:    7,
		UserID:      42,
		AccountID:   17,
		BalanceCost: 1.25,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).
		WithArgs(cmd.RequestID, cmd.APIKeyID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(`SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
		WithArgs(cmd.RequestID, cmd.APIKeyID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(1.25, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectCommit()

	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, []int64{17}, publisher.accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingRepositoryApply_DoesNotPublishBalanceCenterEventForDuplicateOrFailedTransaction(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		ctx := context.Background()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		publisher := &balanceCenterUsageEventPublisherSpy{}
		repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
		cmd := &service.UsageBillingCommand{
			RequestID:          "req-balance-center-duplicate",
			APIKeyID:           7,
			UserID:             42,
			AccountID:          17,
			BalanceCost:        1.25,
			RequestFingerprint: "stable-fingerprint",
		}

		mock.ExpectBegin()
		mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).
			WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`SELECT request_fingerprint\s+FROM usage_billing_dedup\s+WHERE request_id`).
			WithArgs(cmd.RequestID, cmd.APIKeyID).
			WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(cmd.RequestFingerprint))
		mock.ExpectRollback()

		result, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, result.Applied)
		require.Empty(t, publisher.accountIDs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("effect_failure", func(t *testing.T) {
		ctx := context.Background()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		publisher := &balanceCenterUsageEventPublisherSpy{}
		repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
		cmd := &service.UsageBillingCommand{
			RequestID:   "req-balance-center-failure",
			APIKeyID:    7,
			UserID:      42,
			AccountID:   17,
			BalanceCost: 1.25,
		}

		mock.ExpectBegin()
		mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).
			WithArgs(cmd.RequestID, cmd.APIKeyID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
		mock.ExpectQuery(`SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
			WithArgs(cmd.RequestID, cmd.APIKeyID).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(conditionalBalanceDeductSQL).
			WithArgs(1.25, cmd.UserID).
			WillReturnError(errors.New("write failed"))
		mock.ExpectRollback()

		_, err = repo.Apply(ctx, cmd)
		require.Error(t, err)
		require.Empty(t, publisher.accountIDs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit_failure", func(t *testing.T) {
		ctx := context.Background()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		publisher := &balanceCenterUsageEventPublisherSpy{}
		repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
		cmd := &service.UsageBillingCommand{
			RequestID:   "req-balance-center-commit-failure",
			APIKeyID:    7,
			UserID:      42,
			AccountID:   17,
			BalanceCost: 1.25,
		}

		mock.ExpectBegin()
		mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).
			WithArgs(cmd.RequestID, cmd.APIKeyID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
		mock.ExpectQuery(`SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
			WithArgs(cmd.RequestID, cmd.APIKeyID).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(conditionalBalanceDeductSQL).
			WithArgs(1.25, cmd.UserID).
			WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

		_, err = repo.Apply(ctx, cmd)
		require.Error(t, err)
		require.Empty(t, publisher.accountIDs)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestReserveUsageBillingBatchImageBalance_DoesNotPublishBalanceCenterEvent(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	publisher := &balanceCenterUsageEventPublisherSpy{}
	repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
	cmd := &service.BatchImageBalanceHoldCommand{
		RequestID:  "batch-hold-balance-center",
		APIKeyID:   7,
		UserID:     42,
		BatchID:    "imgbatch_balance_center",
		HoldAmount: 2.5,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).
		WithArgs(cmd.RequestID, cmd.APIKeyID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(`SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
		WithArgs(cmd.RequestID, cmd.APIKeyID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(97.5, 2.5))
	mock.ExpectCommit()

	result, err := repo.ReserveBatchImageBalance(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Empty(t, publisher.accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

type balanceCenterUsageEventPublisherSpy struct {
	accountIDs []int64
}

func (p *balanceCenterUsageEventPublisherSpy) PublishAccountUsed(_ context.Context, accountID int64) {
	p.accountIDs = append(p.accountIDs, accountID)
}

func TestDeductUsageBillingBalance_ReturnsUserNotFoundWhenNoUserUpdated(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, _, err = deductUsageBillingBalance(ctx, tx, 42, 10)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 2.5))
	mock.ExpectCommit()

	result, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.75, 0.0))
	mock.ExpectCommit()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(releaseBatchImageHoldSQL).
		WithArgs(1.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_release", HoldAmount: 1})
	require.NoError(t, err)
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与归档表均无 hold claim：说明该 job 从未成功冻结，
	// 释放必须跳过，不得从他人冻结资金池中凭空生成余额。
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_phantom", HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
