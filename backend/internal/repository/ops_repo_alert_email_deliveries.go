package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *opsRepository) InsertAlertEmailDelivery(ctx context.Context, input *service.OpsAlertEmailDeliveryInput) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if input == nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return fmt.Errorf("invalid alert email delivery")
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO ops_alert_email_deliveries (
  alert_event_id, idempotency_key, recipient_email, status, subject, rule_name,
  severity, target_site, account_summary, failure_reason, detail_html, error_ids, is_digest, sent_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (idempotency_key) DO NOTHING`,
		input.AlertEventID,
		strings.TrimSpace(input.IdempotencyKey),
		strings.TrimSpace(input.RecipientEmail),
		strings.TrimSpace(input.Status),
		strings.TrimSpace(input.Subject),
		strings.TrimSpace(input.RuleName),
		strings.TrimSpace(input.Severity),
		strings.TrimSpace(input.TargetSite),
		strings.TrimSpace(input.AccountSummary),
		strings.TrimSpace(input.FailureReason),
		strings.TrimSpace(input.DetailHTML),
		pq.Array(input.ErrorIDs),
		input.IsDigest,
		input.SentAt,
	)
	return err
}

func (r *opsRepository) ListAlertEmailDeliveries(ctx context.Context, filter *service.OpsAlertEmailDeliveryFilter) (*service.OpsAlertEmailDeliveryList, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		filter = &service.OpsAlertEmailDeliveryFilter{}
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	args := make([]any, 0, 3)
	where := ""
	if status := strings.TrimSpace(filter.Status); status != "" {
		args = append(args, status)
		where = " WHERE status = $1"
	}

	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_alert_email_deliveries"+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	args = append(args, pageSize, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)
	rows, err := r.db.QueryContext(ctx, `
SELECT
  id, alert_event_id, recipient_email, status, subject, rule_name, severity,
  target_site, account_summary, failure_reason, detail_html, error_ids, is_digest,
  digested_at, sent_at, created_at
FROM ops_alert_email_deliveries`+where+`
ORDER BY created_at DESC, id DESC
LIMIT $`+itoa(limitPos)+` OFFSET $`+itoa(offsetPos), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]*service.OpsAlertEmailDelivery, 0, pageSize)
	for rows.Next() {
		item, scanErr := scanOpsAlertEmailDelivery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &service.OpsAlertEmailDeliveryList{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (r *opsRepository) ListPendingQuietAlertEmails(ctx context.Context, since time.Time) ([]*service.OpsAlertEmailDelivery, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT
  id, alert_event_id, recipient_email, status, subject, rule_name, severity,
  target_site, account_summary, failure_reason, detail_html, error_ids, is_digest,
  digested_at, sent_at, created_at
FROM ops_alert_email_deliveries
WHERE status = $1 AND digested_at IS NULL AND created_at >= $2
ORDER BY recipient_email ASC, created_at ASC, id ASC`, service.OpsAlertEmailStatusQuietHours, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]*service.OpsAlertEmailDelivery, 0)
	for rows.Next() {
		item, scanErr := scanOpsAlertEmailDelivery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *opsRepository) MarkQuietAlertEmailsDigested(ctx context.Context, ids []int64, digestedAt time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE ops_alert_email_deliveries
SET digested_at = $2
WHERE id = ANY($1) AND status = $3 AND digested_at IS NULL`, pq.Array(ids), digestedAt, service.OpsAlertEmailStatusQuietHours)
	return err
}

type opsAlertEmailDeliveryScanner interface {
	Scan(dest ...any) error
}

func scanOpsAlertEmailDelivery(scanner opsAlertEmailDeliveryScanner) (*service.OpsAlertEmailDelivery, error) {
	var item service.OpsAlertEmailDelivery
	var eventID sql.NullInt64
	var digestedAt sql.NullTime
	var sentAt sql.NullTime
	var errorIDs pq.Int64Array
	if err := scanner.Scan(
		&item.ID,
		&eventID,
		&item.RecipientEmail,
		&item.Status,
		&item.Subject,
		&item.RuleName,
		&item.Severity,
		&item.TargetSite,
		&item.AccountSummary,
		&item.FailureReason,
		&item.DetailHTML,
		&errorIDs,
		&item.IsDigest,
		&digestedAt,
		&sentAt,
		&item.CreatedAt,
	); err != nil {
		return nil, err
	}
	if eventID.Valid {
		value := eventID.Int64
		item.AlertEventID = &value
	}
	if digestedAt.Valid {
		value := digestedAt.Time
		item.DigestedAt = &value
	}
	if sentAt.Valid {
		value := sentAt.Time
		item.SentAt = &value
	}
	item.ErrorIDs = append([]int64(nil), errorIDs...)
	return &item, nil
}
