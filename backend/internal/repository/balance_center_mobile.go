package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *balanceCenterRepository) WatchMobileAlerts(ctx context.Context) (<-chan struct{}, error) {
	if r.listenDSN == "" {
		return nil, errors.New("手机告警推送未配置数据库监听")
	}
	listener := pq.NewListener(r.listenDSN, time.Second, 10*time.Second, nil)
	ready := make(chan error, 1)
	go func() { ready <- listener.Listen("sub2api_mobile_alerts") }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-ready:
		if err != nil {
			_ = listener.Close()
			return nil, err
		}
	case <-timer.C:
		_ = listener.Close()
		return nil, context.DeadlineExceeded
	case <-ctx.Done():
		_ = listener.Close()
		return nil, ctx.Err()
	}
	wake := make(chan struct{}, 1)
	go func() {
		defer close(wake)
		defer listener.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-listener.Notify:
				if !ok {
					return
				}
				// 合并唤醒信号。重连信号也触发按游标补读。
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	return wake, nil
}

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
	// 账号快照仍跟踪独立的倍率变化。钱包告警仅使用专门的站点探测和耗尽事件。
	decisions, next := service.EvaluateBalanceCenterAlerts(state, service.BalanceCenterSuccessfulSnapshot{
		RateMultiplier: snapshot.RateMultiplier,
	}, threshold)
	for _, decision := range decisions {
		change := fmt.Sprintf("%g → %g", *decision.OldValue, *decision.NewValue)
		if _, err = tx.ExecContext(ctx, `SELECT append_mobile_upstream_rate($1, $2)`, snapshot.NormalizedDomain, change); err != nil {
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

func (r *balanceCenterRepository) CaptureMobileSiteWallet(ctx context.Context, item service.UpstreamSiteBalanceObservation, threshold float64) error {
	_, err := r.db.ExecContext(ctx, `SELECT refresh_mobile_upstream_balance($1, $2, $3, $4, $5, NULL)`, item.Host, threshold, item.Balance, item.RechargeConversionScale, item.ObservedAt)
	return err
}

func (r *balanceCenterRepository) ListMobileAlerts(ctx context.Context, after int64) ([]service.MobileAlert, error) {
	const columns = `id, kind, title, body, created_at`
	const visible = `upstream_host IS NOT NULL`
	var rows *sql.Rows
	var err error
	if after == 0 {
		// 首次连接从近 24 小时的第一条开始，历史积压沿同一游标分批补收。
		rows, err = r.db.QueryContext(ctx, `SELECT `+columns+`
FROM mobile_alerts WHERE created_at >= now() - interval '24 hours' AND`+visible+` ORDER BY id LIMIT 100`)
	} else {
		rows, err = r.db.QueryContext(ctx, `SELECT `+columns+` FROM mobile_alerts WHERE id > $1 AND`+visible+` ORDER BY id LIMIT 100`, after)
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
