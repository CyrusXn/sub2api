package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGeminiInteractionBillingLocksBeforeClaimAndSkipsReleased(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := &usageBillingRepository{db: db}
	m.ExpectBegin()
	m.ExpectQuery(`SELECT settled, usage FROM gemini_interactions.*FOR UPDATE`).WithArgs("local", int64(2)).WillReturnRows(sqlmock.NewRows([]string{"settled", "usage"}).AddRow(true, nil))
	m.ExpectRollback()
	result, err := r.Apply(context.Background(), &service.UsageBillingCommand{RequestID: "ginteraction:local", APIKeyID: 2})
	require.NoError(t, err)
	require.False(t, result.Applied, "人工解冻后，旧的自动结算快照不得继续扣款")
	require.NoError(t, m.ExpectationsWereMet())
}

func reviewRow(settled bool, usage any) *sqlmock.Rows {
	return sqlmock.NewRows(strings.Split(strings.ReplaceAll(interactionColumns, " ", ""), ",")).AddRow(1, "local", "up", 2, 3, 4, 5, `{"Model":"gemini-2.5-pro"}`, "cancelled", usage, settled, time.Now(), 1.5, "terminal_usage_missing")
}

func TestGeminiInteractionReviewReleaseCommitsWithAuditOrRollsBack(t *testing.T) {
	for _, auditFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "audit failure"}[auditFails], func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			r := &geminiInteractionRepository{db: db}
			m.ExpectBegin()
			m.ExpectQuery(`SELECT .* FROM gemini_interactions.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(reviewRow(false, nil))
			m.ExpectQuery(`SELECT EXISTS.*usage_billing_dedup.*usage_billing_dedup_archive`).WithArgs("ginteraction:local", int64(2)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			m.ExpectExec(`UPDATE users SET balance = balance \+.*frozen_balance.*>=`).WithArgs(1.5, int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec(`UPDATE gemini_interactions SET settled = TRUE, resolution`).WithArgs(int64(1), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			audit := m.ExpectExec(`INSERT INTO audit_logs`).WithArgs(int64(9), "admin.gemini_interactions.release", "/api/v1/admin/balance-center/interactions/1/resolve", sqlmock.AnyArg())
			if auditFails {
				audit.WillReturnError(errors.New("audit offline"))
				m.ExpectRollback()
			} else {
				audit.WillReturnResult(sqlmock.NewResult(1, 1))
				m.ExpectCommit()
			}
			v, err := r.ResolveReview(context.Background(), 1, 9, service.GeminiInteractionResolution{Action: "release", Reason: "已核实上游无费用"})
			if auditFails {
				require.Error(t, err)
				require.Nil(t, v)
			} else {
				require.NoError(t, err)
				require.True(t, v.Settled)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestGeminiInteractionReviewCannotOverwriteUsageOrReleaseTwice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		settled bool
		usage   any
	}{{"released", true, nil}, {"authoritative usage", false, `{"input_tokens":5,"output_tokens":2}`}} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			r := &geminiInteractionRepository{db: db}
			m.ExpectBegin()
			m.ExpectQuery(`SELECT .* FROM gemini_interactions.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(reviewRow(tc.settled, tc.usage))
			m.ExpectRollback()
			_, err = r.ResolveReview(context.Background(), 1, 9, service.GeminiInteractionResolution{Action: "release", Reason: "已核实上游无费用"})
			require.ErrorIs(t, err, service.ErrGeminiInteractionReviewConflict)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestGeminiInteractionReviewManualUsageDoesNotReleaseHoldBeforeBilling(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := &geminiInteractionRepository{db: db}
	m.ExpectBegin()
	m.ExpectQuery(`SELECT .* FROM gemini_interactions.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(reviewRow(false, nil))
	m.ExpectQuery(`SELECT EXISTS.*usage_billing_dedup`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	m.ExpectExec(`UPDATE gemini_interactions SET usage = .*manual_usage_pending.*next_poll_at`).WithArgs(int64(1), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec(`INSERT INTO audit_logs`).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	v, err := r.ResolveReview(context.Background(), 1, 9, service.GeminiInteractionResolution{Action: "settle", Reason: "已核实上游实际用量", Usage: &service.ClaudeUsage{InputTokens: 5, OutputTokens: 3}})
	require.NoError(t, err)
	require.False(t, v.Settled)
	require.Equal(t, 1.5, v.BalanceHold)
	require.Equal(t, 5, v.Usage.InputTokens)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionBillingAllowsSettlementMultiplier(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := &usageBillingRepository{db: db}
	m.ExpectBegin()
	m.ExpectQuery(`SELECT settled, usage FROM gemini_interactions.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"settled", "usage"}).AddRow(false, `{"input_tokens":5,"output_tokens":3}`))
	m.ExpectQuery(`INSERT INTO usage_billing_dedup`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	m.ExpectQuery(`SELECT request_fingerprint FROM usage_billing_dedup_archive`).WillReturnError(sql.ErrNoRows)
	m.ExpectCommit()
	result, err := r.Apply(context.Background(), &service.UsageBillingCommand{RequestID: "ginteraction:local", APIKeyID: 2, InputTokens: 10, OutputTokens: 6})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.NoError(t, m.ExpectationsWereMet())
}
