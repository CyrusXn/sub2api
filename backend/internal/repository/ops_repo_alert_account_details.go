package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) InsertAlertAccountDetails(ctx context.Context, details []*service.OpsAlertAccountDetail) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if len(details) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const query = `
INSERT INTO ops_alert_account_details (
  alert_event_id, account_id, account_name, platform, group_id, group_name,
  diagnosis, error_phase, status_code, occurred_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (alert_event_id, account_id) DO UPDATE SET
  account_name = EXCLUDED.account_name,
  platform = EXCLUDED.platform,
  group_id = EXCLUDED.group_id,
  group_name = EXCLUDED.group_name,
  diagnosis = EXCLUDED.diagnosis,
  error_phase = EXCLUDED.error_phase,
  status_code = EXCLUDED.status_code,
  occurred_at = EXCLUDED.occurred_at`

	for _, detail := range details {
		if detail == nil || detail.AlertEventID <= 0 || detail.AccountID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			query,
			detail.AlertEventID,
			detail.AccountID,
			strings.TrimSpace(detail.AccountName),
			strings.TrimSpace(detail.Platform),
			detail.GroupID,
			strings.TrimSpace(detail.GroupName),
			strings.TrimSpace(detail.Diagnosis),
			strings.TrimSpace(detail.ErrorPhase),
			detail.StatusCode,
			detail.OccurredAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *opsRepository) ListAlertAccountDetails(ctx context.Context, eventID int64) ([]*service.OpsAlertAccountDetail, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if eventID <= 0 {
		return nil, fmt.Errorf("invalid event id")
	}

	rows, err := r.db.QueryContext(ctx, `
SELECT
  id, alert_event_id, account_id, account_name, platform, group_id, group_name,
  diagnosis, error_phase, status_code, occurred_at, created_at
FROM ops_alert_account_details
WHERE alert_event_id = $1
ORDER BY account_id ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	details := make([]*service.OpsAlertAccountDetail, 0)
	for rows.Next() {
		var detail service.OpsAlertAccountDetail
		if err := rows.Scan(
			&detail.ID,
			&detail.AlertEventID,
			&detail.AccountID,
			&detail.AccountName,
			&detail.Platform,
			&detail.GroupID,
			&detail.GroupName,
			&detail.Diagnosis,
			&detail.ErrorPhase,
			&detail.StatusCode,
			&detail.OccurredAt,
			&detail.CreatedAt,
		); err != nil {
			return nil, err
		}
		details = append(details, &detail)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return details, nil
}
