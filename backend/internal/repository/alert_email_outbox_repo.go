package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type alertEmailOutboxRepository struct {
	db *sql.DB
}

func NewAlertEmailOutboxRepository(db *sql.DB) service.AlertEmailOutboxRepository {
	return &alertEmailOutboxRepository{db: db}
}

// EnqueueAlertEmail 保留首次写入的聚合截止时间，重复业务事件不会重开时间窗。
func (r *alertEmailOutboxRepository) EnqueueAlertEmail(ctx context.Context, input *service.AlertEmailOutboxInput) error {
	if r == nil || r.db == nil {
		return errors.New("告警邮件队列仓储未初始化")
	}
	if input == nil || input.CreatedAt.IsZero() || input.AvailableAt.IsZero() {
		return errors.New("告警邮件队列内容无效")
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO alert_email_outbox (
    source_type, source_id, source_key, alert_type, recipient_email,
    subject, body_html, aggregate_until, available_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (source_type, source_key, recipient_email) DO NOTHING`,
		strings.TrimSpace(input.SourceType), strings.TrimSpace(input.SourceID), strings.TrimSpace(input.SourceKey),
		strings.TrimSpace(input.AlertType), strings.ToLower(strings.TrimSpace(input.Recipient)),
		strings.TrimSpace(input.Subject), strings.TrimSpace(input.BodyHTML), input.AvailableAt.UTC(),
		input.AvailableAt.UTC(), input.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("写入告警邮件队列失败: %w", err)
	}
	return nil
}

// ClaimAlertEmailBatch 只领取最早到期收件人的一个原始窗口，并通过行锁支持多实例并发。
func (r *alertEmailOutboxRepository) ClaimAlertEmailBatch(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*service.AlertEmailOutboxItem, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("告警邮件队列仓储未初始化")
	}
	if limit <= 0 {
		limit = 100
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 120
	}
	rows, err := r.db.QueryContext(ctx, `
WITH anchor AS (
    SELECT id, recipient_email, aggregate_until
    FROM alert_email_outbox
    WHERE status = 'pending'
      AND available_at <= $1
      AND (claimed_at IS NULL OR claimed_at < $1 - ($2 * INTERVAL '1 second'))
    ORDER BY available_at ASC, id ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
), candidates AS (
    SELECT queued.id
    FROM alert_email_outbox AS queued
    JOIN anchor ON anchor.recipient_email = queued.recipient_email
    WHERE queued.status = 'pending'
      -- 同批候选也必须到达重试时间，避免反复认领未到期或暂停的旧邮件。
      AND queued.available_at <= $1
      AND queued.created_at <= anchor.aggregate_until
      AND (queued.claimed_at IS NULL OR queued.claimed_at < $1 - ($2 * INTERVAL '1 second'))
    ORDER BY queued.created_at ASC, queued.id ASC
    LIMIT $3
    FOR UPDATE OF queued SKIP LOCKED
), claimed AS (
    UPDATE alert_email_outbox AS queued
    SET claimed_at = $1, updated_at = $1
    FROM candidates
    WHERE queued.id = candidates.id
    RETURNING queued.id, queued.source_type, queued.source_id, queued.source_key,
              queued.alert_type, queued.recipient_email, queued.subject, queued.body_html,
              queued.attempt_count, queued.created_at
)
SELECT id, source_type, source_id, source_key, alert_type, recipient_email,
       subject, body_html, attempt_count, created_at
FROM claimed
ORDER BY created_at ASC, id ASC`, now.UTC(), leaseSeconds, limit)
	if err != nil {
		return nil, fmt.Errorf("认领告警邮件队列失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]*service.AlertEmailOutboxItem, 0, limit)
	for rows.Next() {
		var item service.AlertEmailOutboxItem
		if err := rows.Scan(
			&item.ID, &item.SourceType, &item.SourceID, &item.SourceKey, &item.AlertType,
			&item.Recipient, &item.Subject, &item.BodyHTML, &item.AttemptCount, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("读取告警邮件队列失败: %w", err)
		}
		items = append(items, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历告警邮件队列失败: %w", err)
	}
	return items, nil
}

func (r *alertEmailOutboxRepository) MarkAlertEmailBatchSent(ctx context.Context, ids []int64, sentAt time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("告警邮件队列仓储未初始化")
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
WITH sent AS (
    UPDATE alert_email_outbox
    SET status = 'sent', sent_at = $2, claimed_at = NULL, last_error = '', updated_at = $2
    WHERE id = ANY($1) AND status = 'pending'
    RETURNING source_type, source_id, alert_type, recipient_email
), balance_audit AS (
    UPDATE balance_center_alert_deliveries AS delivery
    SET status = 'accepted', failure_reason = '', attempted_at = $2, accepted_at = $2
    FROM sent
    WHERE sent.source_type = 'balance_center'
      AND sent.source_id ~ '^[0-9]+$'
      AND delivery.snapshot_id = sent.source_id::BIGINT
      AND delivery.alert_type = sent.alert_type
      AND delivery.recipient_email = sent.recipient_email
    RETURNING delivery.id
), ops_audit AS (
    UPDATE ops_alert_email_deliveries AS delivery
    SET status = 'sent', failure_reason = '', sent_at = $2
    FROM sent
    WHERE sent.source_type = 'ops_alert'
      AND sent.source_id ~ '^[0-9]+$'
      AND delivery.alert_event_id = sent.source_id::BIGINT
      AND delivery.recipient_email = sent.recipient_email
      AND delivery.status IN ('queued', 'failed')
    RETURNING delivery.id
), ops_events AS (
    UPDATE ops_alert_events AS event
    SET email_sent = TRUE
    WHERE event.id IN (
        SELECT source_id::BIGINT
        FROM sent
        WHERE source_type = 'ops_alert' AND source_id ~ '^[0-9]+$'
    )
    RETURNING event.id
)
SELECT
    (SELECT COUNT(*) FROM sent),
    (SELECT COUNT(*) FROM balance_audit),
    (SELECT COUNT(*) FROM ops_audit),
    (SELECT COUNT(*) FROM ops_events)`, pq.Array(ids), sentAt.UTC())
	if err != nil {
		return fmt.Errorf("确认告警邮件发送成功失败: %w", err)
	}
	return nil
}

func (r *alertEmailOutboxRepository) RetryAlertEmailBatch(ctx context.Context, ids []int64, reason string, nextRetry time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("告警邮件队列仓储未初始化")
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
WITH retried AS (
    UPDATE alert_email_outbox
    SET attempt_count = attempt_count + 1, available_at = $3, claimed_at = NULL,
        last_error = $2, updated_at = NOW()
    WHERE id = ANY($1) AND status = 'pending'
    RETURNING source_type, source_id, alert_type, recipient_email
), balance_audit AS (
    UPDATE balance_center_alert_deliveries AS delivery
    SET status = 'failed', failure_reason = $2, attempted_at = NOW(), accepted_at = NULL
    FROM retried
    WHERE retried.source_type = 'balance_center'
      AND retried.source_id ~ '^[0-9]+$'
      AND delivery.snapshot_id = retried.source_id::BIGINT
      AND delivery.alert_type = retried.alert_type
      AND delivery.recipient_email = retried.recipient_email
    RETURNING delivery.id
), ops_audit AS (
    UPDATE ops_alert_email_deliveries AS delivery
    SET status = 'failed', failure_reason = $2, sent_at = NULL
    FROM retried
    WHERE retried.source_type = 'ops_alert'
      AND retried.source_id ~ '^[0-9]+$'
      AND delivery.alert_event_id = retried.source_id::BIGINT
      AND delivery.recipient_email = retried.recipient_email
      AND delivery.status IN ('queued', 'failed')
    RETURNING delivery.id
)
SELECT
    (SELECT COUNT(*) FROM retried),
    (SELECT COUNT(*) FROM balance_audit),
    (SELECT COUNT(*) FROM ops_audit)`, pq.Array(ids), strings.TrimSpace(reason), nextRetry.UTC())
	if err != nil {
		return fmt.Errorf("释放失败告警邮件批次失败: %w", err)
	}
	return nil
}
