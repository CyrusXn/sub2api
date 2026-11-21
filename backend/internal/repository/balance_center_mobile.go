package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *balanceCenterRepository) CaptureMobileBalanceAlerts(ctx context.Context, snapshot *service.BalanceCenterSnapshot, threshold float64) error {
	if err := r.captureMobileBalanceAlerts(ctx, snapshot, threshold); err != nil {
		return fmt.Errorf("写入手机余额告警失败: %w", err)
	}
	return nil
}

func (r *balanceCenterRepository) captureMobileBalanceAlerts(ctx context.Context, snapshot *service.BalanceCenterSnapshot, threshold float64) error {
	key := balanceCenterIdentityKey(snapshot)
	if snapshot.AccountID == nil && snapshot.LegacyKeyID == nil {
		key = fmt.Sprintf("site:%d", snapshot.SiteID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO mobile_balance_alert_states (identity_key) VALUES ($1) ON CONFLICT DO NOTHING`, key); err != nil {
		return err
	}
	var state service.BalanceCenterAlertState
	var lastSnapshotID int64
	err = tx.QueryRowContext(ctx, `
SELECT snapshot_id, low_balance_active, low_balance_threshold, multiplier_baseline
FROM mobile_balance_alert_states WHERE identity_key = $1 FOR UPDATE`, key).
		Scan(&lastSnapshotID, &state.LowBalanceActive, &state.LowBalanceThreshold, &state.MultiplierBaseline)
	if err != nil || lastSnapshotID >= snapshot.ID {
		return err
	}
	decisions, next := service.EvaluateBalanceCenterAlerts(state, service.BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: snapshot.ConvertedBalance, RateMultiplier: snapshot.RateMultiplier,
	}, threshold)
	for _, decision := range decisions {
		alert := service.NewMobileBalanceAlert(snapshot, decision)
		if _, err = tx.ExecContext(ctx, `SELECT append_mobile_alert($1, $2, $3)`, alert.Kind, alert.Title, alert.Body); err != nil {
			return err
		}
		next = service.AcceptBalanceCenterAlert(next, decision)
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE mobile_balance_alert_states
SET snapshot_id = $2, low_balance_active = $3, low_balance_threshold = $4, multiplier_baseline = $5
WHERE identity_key = $1`, key, snapshot.ID, next.LowBalanceActive, next.LowBalanceThreshold, next.MultiplierBaseline); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *balanceCenterRepository) ListMobileAlerts(ctx context.Context, after int64) ([]service.MobileAlert, error) {
	const columns = `id, kind, title, body, created_at`
	var rows *sql.Rows
	var err error
	if after == 0 {
		// 首次连接从近 24 小时的第一条开始，历史积压沿同一游标分批补收。
		rows, err = r.db.QueryContext(ctx, `SELECT `+columns+`
FROM mobile_alerts WHERE created_at >= now() - interval '24 hours' ORDER BY id LIMIT 100`)
	} else {
		rows, err = r.db.QueryContext(ctx, `SELECT `+columns+` FROM mobile_alerts WHERE id > $1 ORDER BY id LIMIT 100`, after)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	alerts := make([]service.MobileAlert, 0)
	for rows.Next() {
		var alert service.MobileAlert
		if err := rows.Scan(&alert.ID, &alert.Kind, &alert.Title, &alert.Body, &alert.CreatedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}
