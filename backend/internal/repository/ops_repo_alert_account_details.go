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
  diagnosis, error_phase, status_code, occurred_at, error_log_id, user_id,
  user_email, api_key_id, api_key_name, request_id, client_request_id,
  error_reason, error_message, requested_model, upstream_model
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (alert_event_id, account_id) DO UPDATE SET
  account_name = EXCLUDED.account_name,
  platform = EXCLUDED.platform,
  group_id = EXCLUDED.group_id,
  group_name = EXCLUDED.group_name,
  diagnosis = EXCLUDED.diagnosis,
  error_phase = EXCLUDED.error_phase,
  status_code = EXCLUDED.status_code,
  occurred_at = EXCLUDED.occurred_at,
  error_log_id = EXCLUDED.error_log_id,
  user_id = EXCLUDED.user_id,
  user_email = EXCLUDED.user_email,
  api_key_id = EXCLUDED.api_key_id,
  api_key_name = EXCLUDED.api_key_name,
  request_id = EXCLUDED.request_id,
  client_request_id = EXCLUDED.client_request_id,
  error_reason = EXCLUDED.error_reason,
  error_message = EXCLUDED.error_message,
  requested_model = EXCLUDED.requested_model,
  upstream_model = EXCLUDED.upstream_model`

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
			opsAlertDetailOptionalID(detail.ErrorLogID),
			opsNullInt64(detail.UserID),
			strings.TrimSpace(detail.UserEmail),
			opsNullInt64(detail.APIKeyID),
			strings.TrimSpace(detail.APIKeyName),
			strings.TrimSpace(detail.RequestID),
			strings.TrimSpace(detail.ClientRequestID),
			strings.TrimSpace(detail.ErrorReason),
			strings.TrimSpace(detail.ErrorMessage),
			strings.TrimSpace(detail.RequestedModel),
			strings.TrimSpace(detail.UpstreamModel),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func opsAlertDetailOptionalID(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
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
  diagnosis, error_phase, status_code, occurred_at, COALESCE(error_log_id, 0),
  user_id, COALESCE(user_email, ''), api_key_id, COALESCE(api_key_name, ''),
  COALESCE(request_id, ''), COALESCE(client_request_id, ''),
  COALESCE(error_reason, ''), COALESCE(error_message, ''),
  COALESCE(requested_model, ''), COALESCE(upstream_model, ''), created_at
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
			&detail.ErrorLogID,
			&detail.UserID,
			&detail.UserEmail,
			&detail.APIKeyID,
			&detail.APIKeyName,
			&detail.RequestID,
			&detail.ClientRequestID,
			&detail.ErrorReason,
			&detail.ErrorMessage,
			&detail.RequestedModel,
			&detail.UpstreamModel,
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
