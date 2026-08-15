package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAlertEmailOutboxRepositoryEnqueueKeepsOriginalAggregationDeadline(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewAlertEmailOutboxRepository(db)
	createdAt := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	availableAt := createdAt.Add(time.Minute)

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO alert_email_outbox")).
		WithArgs("balance_center", "15", "snapshot:15:low_balance", "low_balance", "ops@example.com", "余额不足", "<p>余额不足</p>", availableAt, availableAt, createdAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.EnqueueAlertEmail(context.Background(), &service.AlertEmailOutboxInput{
		SourceType: "balance_center", SourceID: "15", SourceKey: "snapshot:15:low_balance",
		AlertType: "low_balance", Recipient: "ops@example.com", Subject: "余额不足",
		BodyHTML: "<p>余额不足</p>", CreatedAt: createdAt, AvailableAt: availableAt,
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAlertEmailOutboxRepositoryClaimsOneRecipientWindowWithSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewAlertEmailOutboxRepository(db)
	now := time.Date(2026, 8, 14, 10, 1, 0, 0, time.UTC)
	createdAt := now.Add(-time.Minute)
	rows := sqlmock.NewRows([]string{
		"id", "source_type", "source_id", "source_key", "alert_type",
		"recipient_email", "subject", "body_html", "attempt_count", "created_at",
	}).AddRow(1, "balance_center", "15", "snapshot:15:low_balance", "low_balance", "ops@example.com", "余额不足", "<p>余额不足</p>", 0, createdAt).
		AddRow(2, "ops_alert", "27", "event:27", "account_request_failure", "ops@example.com", "上游异常", "<p>上游异常</p>", 0, createdAt.Add(30*time.Second))
	mock.ExpectQuery("(?s)WITH anchor AS .*FOR UPDATE SKIP LOCKED.*UPDATE alert_email_outbox.*RETURNING").
		WithArgs(now, int64(120), 100).
		WillReturnRows(rows)

	items, err := repo.ClaimAlertEmailBatch(context.Background(), now, 2*time.Minute, 100)

	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "balance_center", items[0].SourceType)
	require.Equal(t, "ops@example.com", items[1].Recipient)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAlertEmailOutboxRepositoryMarksSentAndRetriesClaimedBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewAlertEmailOutboxRepository(db)
	now := time.Date(2026, 8, 14, 10, 1, 0, 0, time.UTC)

	mock.ExpectExec("(?s)WITH sent AS .*UPDATE balance_center_alert_deliveries.*UPDATE ops_alert_email_deliveries.*UPDATE ops_alert_events").
		WithArgs(sqlmock.AnyArg(), now).
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.MarkAlertEmailBatchSent(context.Background(), []int64{1, 2}, now))

	nextRetry := now.Add(2 * time.Minute)
	mock.ExpectExec(`(?s)WITH retried AS .*attempt_count = attempt_count \+ 1.*UPDATE balance_center_alert_deliveries.*UPDATE ops_alert_email_deliveries`).
		WithArgs(sqlmock.AnyArg(), "smtp timeout", nextRetry).
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.RetryAlertEmailBatch(context.Background(), []int64{3, 4}, "smtp timeout", nextRetry))
	require.NoError(t, mock.ExpectationsWereMet())
}
