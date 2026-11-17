//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUsageBillingRepositoryApplyBatch_PublishesUniqueAccountsOnce(t *testing.T) {
	db, mock := newSQLMock(t)
	publisher := &balanceCenterUsageEventPublisherSpy{}
	repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
	cmds := make([]*service.UsageBillingCommand, 128)
	accountIDs := []int64{17, 23, 17, 29, 0, -1}
	for i := range cmds {
		cmds[i] = &service.UsageBillingCommand{RequestID: fmt.Sprintf("batch-%d", i), UserID: 42, APIKeyID: 7, AccountID: accountIDs[i%len(accountIDs)]}
	}
	mock.ExpectBegin()
	expectFreshUsageBillingBatch(mock, cmds...)
	mock.ExpectCommit()

	outcomes, err := repo.ApplyBatch(context.Background(), cmds)
	require.NoError(t, err)
	require.Len(t, outcomes, len(cmds))
	require.Equal(t, [][]int64{{17, 23, 29}}, publisher.accountBatches)
	require.Zero(t, publisher.singleCalls, "批量提交不得逐条重建事件超时预算")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingRepositoryApplyBatch_PublishesOnlyCommittedAppliedUsage(t *testing.T) {
	db, mock := newSQLMock(t)
	publisher := &balanceCenterUsageEventPublisherSpy{}
	repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
	cmds := []*service.UsageBillingCommand{
		{RequestID: "batch-fresh", UserID: 42, APIKeyID: 7, AccountID: 17, RequestFingerprint: "fresh"},
		{RequestID: "batch-duplicate", UserID: 42, APIKeyID: 7, AccountID: 18, RequestFingerprint: "duplicate"},
		{RequestID: "batch-archived", UserID: 42, APIKeyID: 7, AccountID: 19, RequestFingerprint: "archived"},
		{RequestID: "batch-conflict", UserID: 42, APIKeyID: 7, AccountID: 20, RequestFingerprint: "changed"},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "request_id", "api_key_id"}).
			AddRow(1, cmds[0].RequestID, cmds[0].APIKeyID).
			AddRow(2, cmds[2].RequestID, cmds[2].APIKeyID))
	mock.ExpectQuery(`(?s)FROM usage_billing_dedup d.*JOIN unnest`).
		WillReturnRows(sqlmock.NewRows([]string{"request_id", "api_key_id", "request_fingerprint"}).
			AddRow(cmds[1].RequestID, cmds[1].APIKeyID, cmds[1].RequestFingerprint).
			AddRow(cmds[3].RequestID, cmds[3].APIKeyID, "original"))
	mock.ExpectQuery(`(?s)FROM usage_billing_dedup_archive a.*JOIN unnest`).
		WillReturnRows(sqlmock.NewRows([]string{"request_id", "api_key_id", "request_fingerprint"}).
			AddRow(cmds[2].RequestID, cmds[2].APIKeyID, cmds[2].RequestFingerprint))
	mock.ExpectExec(`DELETE FROM usage_billing_dedup`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	outcomes, err := repo.ApplyBatch(context.Background(), cmds)
	require.NoError(t, err)
	require.Len(t, outcomes, len(cmds))
	require.True(t, outcomes[0].Result.Applied)
	require.False(t, outcomes[1].Result.Applied)
	require.False(t, outcomes[2].Result.Applied)
	require.ErrorIs(t, outcomes[3].Err, service.ErrUsageBillingRequestConflict)
	require.Equal(t, []int64{17}, publisher.accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingRepositoryApplyBatch_DoesNotPublishFailedTransaction(t *testing.T) {
	for _, phase := range []string{"effects", "commit"} {
		t.Run(phase, func(t *testing.T) {
			db, mock := newSQLMock(t)
			publisher := &balanceCenterUsageEventPublisherSpy{}
			repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
			cmd := &service.UsageBillingCommand{RequestID: "batch-failure", UserID: 42, APIKeyID: 7, AccountID: 17, BalanceCost: 1}
			writeErr := errors.New("transaction failed")

			mock.ExpectBegin()
			expectFreshUsageBillingBatch(mock, cmd)
			balanceUpdate := mock.ExpectQuery(`(?s)UPDATE users.*RETURNING balance`)
			if phase == "effects" {
				balanceUpdate.WillReturnError(writeErr)
				mock.ExpectRollback()
			} else {
				balanceUpdate.WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("99"))
				mock.ExpectCommit().WillReturnError(writeErr)
			}

			_, err := repo.ApplyBatch(context.Background(), []*service.UsageBillingCommand{cmd})
			require.ErrorIs(t, err, writeErr)
			require.Empty(t, publisher.accountIDs)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageBillingRepositoryApplyBatch_GeminiInteractionRespectsSettlementState(t *testing.T) {
	for _, settled := range []bool{true, false} {
		name := "pending"
		if settled {
			name = "settled"
		}
		t.Run(name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			publisher := &balanceCenterUsageEventPublisherSpy{}
			repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
			cmd := &service.UsageBillingCommand{
				RequestID: service.GeminiInteractionUsageRequestIDPrefix + name, UserID: 42, APIKeyID: 7, AccountID: 17, BalanceCost: 9,
			}

			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT settled, usage FROM gemini_interactions.*FOR UPDATE`).
				WithArgs(name, cmd.APIKeyID).
				WillReturnRows(sqlmock.NewRows([]string{"settled", "usage"}).AddRow(settled, nil))
			if !settled {
				expectFreshUsageBillingBatch(mock, cmd)
				mock.ExpectQuery(`(?s)UPDATE users.*RETURNING balance`).
					WithArgs("9", int64(42)).
					WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("91"))
			}
			mock.ExpectCommit()

			outcomes, err := repo.ApplyBatch(context.Background(), []*service.UsageBillingCommand{cmd})
			require.NoError(t, err)
			require.Len(t, outcomes, 1)
			require.NotNil(t, outcomes[0].Result)
			require.Equal(t, !settled, outcomes[0].Result.Applied)
			if settled {
				require.Empty(t, publisher.accountIDs)
			} else {
				require.InDelta(t, 91, *outcomes[0].Result.NewBalance, 1e-9)
				require.Equal(t, []int64{17}, publisher.accountIDs)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageBillingRepositoryApplyBatch_LocksGeminiTasksBeforeDedupAndKeepsOutcomeOrder(t *testing.T) {
	db, mock := newSQLMock(t)
	publisher := &balanceCenterUsageEventPublisherSpy{}
	repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
	cmds := []*service.UsageBillingCommand{
		{RequestID: service.GeminiInteractionUsageRequestIDPrefix + "z", UserID: 42, APIKeyID: 7, AccountID: 17, BalanceCost: 1},
		{RequestID: "ordinary", UserID: 42, APIKeyID: 7, AccountID: 18, BalanceCost: 2},
		{RequestID: service.GeminiInteractionUsageRequestIDPrefix + "a", UserID: 42, APIKeyID: 7, AccountID: 19, BalanceCost: 9},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT settled, usage FROM gemini_interactions.*FOR UPDATE`).
		WithArgs("a", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"settled", "usage"}).AddRow(true, nil))
	mock.ExpectQuery(`SELECT settled, usage FROM gemini_interactions.*FOR UPDATE`).
		WithArgs("z", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"settled", "usage"}).AddRow(false, []byte(`{"input_tokens":1}`)))
	expectFreshUsageBillingBatch(mock, cmds[0], cmds[1])
	mock.ExpectQuery(`(?s)UPDATE users.*RETURNING balance`).
		WithArgs("3", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("97"))
	mock.ExpectCommit()

	outcomes, err := repo.ApplyBatch(context.Background(), cmds)
	require.NoError(t, err)
	require.Len(t, outcomes, len(cmds))
	require.True(t, outcomes[0].Result.Applied)
	require.InDelta(t, 99, *outcomes[0].Result.NewBalance, 1e-9)
	require.True(t, outcomes[1].Result.Applied)
	require.InDelta(t, 97, *outcomes[1].Result.NewBalance, 1e-9)
	require.False(t, outcomes[2].Result.Applied)
	require.Equal(t, []int64{17, 18}, publisher.accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingRepositoryApplyBatch_GeminiLockFailureRollsBackBeforeOtherCharges(t *testing.T) {
	db, mock := newSQLMock(t)
	publisher := &balanceCenterUsageEventPublisherSpy{}
	repo := &usageBillingRepository{db: db, balanceCenterPublisher: publisher}
	cmds := []*service.UsageBillingCommand{
		{RequestID: "ordinary", UserID: 42, APIKeyID: 7, AccountID: 17, BalanceCost: 2},
		{RequestID: service.GeminiInteractionUsageRequestIDPrefix + "missing", UserID: 42, APIKeyID: 7, AccountID: 18, BalanceCost: 1},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT settled, usage FROM gemini_interactions.*FOR UPDATE`).
		WithArgs("missing", int64(7)).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.ApplyBatch(context.Background(), cmds)
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.Empty(t, publisher.accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectFreshUsageBillingBatch(mock sqlmock.Sqlmock, cmds ...*service.UsageBillingCommand) {
	claimed := sqlmock.NewRows([]string{"id", "request_id", "api_key_id"})
	for i, cmd := range cmds {
		claimed.AddRow(i+1, cmd.RequestID, cmd.APIKeyID)
	}
	mock.ExpectQuery(`INSERT INTO usage_billing_dedup`).WillReturnRows(claimed)
	mock.ExpectQuery(`(?s)FROM usage_billing_dedup_archive a.*JOIN unnest`).
		WillReturnRows(sqlmock.NewRows([]string{"request_id", "api_key_id", "request_fingerprint"}))
}
