package repository

import (
	"context"
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGeminiInteractionRepositoryOwnerQuery(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectQuery(`WHERE public_id = \$1 AND api_key_id = \$2 AND user_id = \$3 AND group_id = \$4 AND deleted_at IS NULL`).WithArgs("id", int64(1), int64(2), int64(3)).WillReturnError(sql.ErrNoRows)
	_, err = repo.GetOwned(context.Background(), 1, 2, 3, "id")
	require.ErrorIs(t, err, service.ErrGeminiInteractionNotFound)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestGeminiInteractionRepositoryResponseCannotResetSettled(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectQuery(`UPDATE gemini_interactions SET upstream_id.*CASE WHEN status IN.*RETURNING upstream_id, status, usage, settled`).WithArgs(int64(1), "up", "in_progress", sqlmock.AnyArg(), "").WillReturnRows(sqlmock.NewRows([]string{"upstream_id", "status", "usage", "settled", "review_reason"}).AddRow("up", "completed", `{"input_tokens":5,"output_tokens":3}`, true, ""))
	v := &service.GeminiInteraction{ID: 1, UpstreamID: "up", Status: "in_progress", Usage: &service.ClaudeUsage{InputTokens: 2}}
	require.NoError(t, repo.Update(context.Background(), v))
	require.True(t, v.Settled)
	require.Equal(t, "completed", v.Status)
	require.Equal(t, 5, v.Usage.InputTokens)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionRepositoryCreateLimitsDurableConcurrentTasks(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT concurrency FROM users.*FOR UPDATE`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"concurrency"}).AddRow(2))
	m.ExpectQuery(`SELECT id FROM groups.*FOR SHARE`).WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(0))
	m.ExpectQuery(`SELECT user_id, group_id FROM api_keys.*FOR UPDATE`).WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id"}).AddRow(2, 0))
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM gemini_interactions WHERE user_id`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	m.ExpectRollback()
	err = repo.Create(context.Background(), &service.GeminiInteraction{PublicID: "new", UserID: 2, AccountID: 3, UserConcurrency: 2, AccountConcurrency: 1})
	require.ErrorIs(t, err, service.ErrGeminiInteractionConcurrency)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionRepositoryDeleteRequiresSettledRow(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectExec(`UPDATE gemini_interactions SET deleted_at = NOW\(\) WHERE id = \$1 AND settled = TRUE`).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.Error(t, repo.SoftDelete(context.Background(), 1))
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionRepositoryFindsCommittedBillingReceipt(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectQuery(`SELECT EXISTS.*usage_billing_dedup.*usage_billing_dedup_archive`).WithArgs("ginteraction:local", int64(2)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	ok, err := repo.HasUsageBilling(context.Background(), "ginteraction:local", 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionRepositoryBackgroundBalanceHoldIsAtomic(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT concurrency FROM users.*FOR UPDATE`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"concurrency"}).AddRow(0))
	m.ExpectQuery(`SELECT id FROM groups.*FOR SHARE`).WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(0))
	m.ExpectQuery(`SELECT user_id, group_id FROM api_keys.*FOR UPDATE`).WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id"}).AddRow(2, 0))
	m.ExpectQuery(`SELECT concurrency FROM accounts.*FOR UPDATE`).WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"concurrency"}).AddRow(0))
	m.ExpectExec(`UPDATE users SET balance = balance - \$1, frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1.*balance >= \$1`).WithArgs(1.5, int64(2)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectRollback()
	err = repo.Create(context.Background(), &service.GeminiInteraction{PublicID: "local", UserID: 2, AccountID: 3, Background: true, BalanceHold: 1.5})
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionRepositoryCreateRejectsRevokedSubscription(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT concurrency FROM users.*FOR UPDATE`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"concurrency"}).AddRow(0))
	m.ExpectQuery(`SELECT id FROM groups.*FOR SHARE`).WithArgs(int64(4)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(4))
	m.ExpectQuery(`SELECT id FROM user_subscriptions.*user_id = \$2 AND group_id = \$3.*FOR SHARE`).WithArgs(int64(5), int64(2), int64(4)).WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	err = repo.Create(context.Background(), &service.GeminiInteraction{PublicID: "local", UserID: 2, GroupID: 4, AccountID: 3, SubscriptionID: 5})
	require.ErrorIs(t, err, service.ErrGeminiInteractionNotFound)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionRepositoryCreateDoesNotWaitForBusyDependencies(t *testing.T) {
	for _, busy := range []string{"group", "subscription", "key", "account"} {
		t.Run(busy, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := NewGeminiInteractionRepository(db)
			m.ExpectBegin()
			m.ExpectQuery(`SELECT concurrency FROM users.*FOR UPDATE`).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"concurrency"}).AddRow(0))
			locks := []struct {
				name, query string
				columns     []string
				values      []any
			}{
				{"group", `SELECT id FROM groups.*FOR SHARE NOWAIT`, []string{"id"}, []any{int64(4)}},
				{"subscription", `SELECT id FROM user_subscriptions.*FOR SHARE NOWAIT`, []string{"id"}, []any{int64(6)}},
				{"key", `SELECT user_id, group_id FROM api_keys.*FOR UPDATE NOWAIT`, []string{"user_id", "group_id"}, []any{int64(2), int64(4)}},
				{"account", `SELECT concurrency FROM accounts.*FOR UPDATE NOWAIT`, []string{"concurrency"}, []any{int64(0)}},
			}
			for _, lock := range locks {
				q := m.ExpectQuery(lock.query)
				if lock.name == busy {
					q.WillReturnError(&pq.Error{Code: "55P03"})
					break
				}
				rows := sqlmock.NewRows(lock.columns)
				if len(lock.values) == 1 {
					rows.AddRow(lock.values[0])
				} else {
					rows.AddRow(lock.values[0], lock.values[1])
				}
				q.WillReturnRows(rows)
			}
			m.ExpectRollback()
			err = repo.Create(context.Background(), &service.GeminiInteraction{PublicID: "local", UserID: 2, GroupID: 4, AccountID: 3, APIKeyID: 5, SubscriptionID: 6})
			require.ErrorIs(t, err, service.ErrGeminiInteractionBusy)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestGeminiInteractionRepositorySettlementReleasesHoldOnce(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewGeminiInteractionRepository(db)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT user_id, hold_amount, settled FROM gemini_interactions.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "hold_amount", "settled"}).AddRow(2, 1.5, false))
	m.ExpectExec(`UPDATE users SET balance = balance \+ \$1, frozen_balance = COALESCE\(frozen_balance, 0\) - \$1`).WithArgs(1.5, int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec(`UPDATE gemini_interactions SET settled = TRUE`).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	require.NoError(t, repo.MarkSettled(context.Background(), 1))
	m.ExpectBegin()
	m.ExpectQuery(`SELECT user_id, hold_amount, settled FROM gemini_interactions.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "hold_amount", "settled"}).AddRow(2, 1.5, true))
	m.ExpectCommit()
	require.NoError(t, repo.MarkSettled(context.Background(), 1))
	require.NoError(t, m.ExpectationsWereMet())
}
