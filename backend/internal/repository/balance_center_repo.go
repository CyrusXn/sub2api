package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type balanceCenterRepository struct {
	db *sql.DB
}

func NewBalanceCenterRepository(db *sql.DB) service.BalanceCenterRepository {
	return &balanceCenterRepository{db: db}
}

func (r *balanceCenterRepository) PersistSnapshot(ctx context.Context, snapshot *service.BalanceCenterSnapshot) (_ *service.BalanceCenterSnapshot, err error) {
	if r == nil || r.db == nil {
		return nil, errors.New("余额中心仓储未初始化")
	}
	if err := validateBalanceCenterSnapshot(snapshot); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始余额快照事务失败: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var siteID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO balance_center_sites (name, display_name, normalized_domain, base_url, source, probe_supported)
VALUES ($1, $1, $2, $3, $4, $5)
ON CONFLICT (normalized_domain) DO UPDATE SET
    name = EXCLUDED.name,
	display_name = CASE WHEN balance_center_sites.display_name = '' THEN EXCLUDED.display_name ELSE balance_center_sites.display_name END,
    base_url = CASE WHEN EXCLUDED.base_url <> '' THEN EXCLUDED.base_url ELSE balance_center_sites.base_url END,
    updated_at = NOW()
RETURNING id`, snapshot.SiteName, snapshot.NormalizedDomain, snapshot.BaseURL, snapshot.Source, snapshot.Status != "unsupported").Scan(&siteID)
	if err != nil {
		return nil, fmt.Errorf("写入余额站点失败: %w", err)
	}

	if snapshot.AccountID != nil {
		if _, err = tx.ExecContext(ctx, `
INSERT INTO balance_center_account_bindings (site_id, account_id, source)
VALUES ($1, $2, $3)
ON CONFLICT (site_id, account_id) DO UPDATE SET
    source = EXCLUDED.source,
    updated_at = NOW()`, siteID, *snapshot.AccountID, snapshot.Source); err != nil {
			return nil, fmt.Errorf("写入余额账号绑定失败: %w", err)
		}
	}

	payload := snapshot.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	conversionScale := snapshot.ConversionScale
	if conversionScale == 0 {
		conversionScale = 1
	}
	var snapshotID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO balance_center_snapshots (
    site_id, account_id, legacy_key_id, source, source_key, status,
    balance, converted_balance, rate_multiplier, conversion_scale,
    currency, reason, probed_at, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)
ON CONFLICT (source, source_key) DO UPDATE SET source_key = EXCLUDED.source_key
RETURNING id`,
		siteID, snapshot.AccountID, snapshot.LegacyKeyID, snapshot.Source, snapshot.SourceKey,
		snapshot.Status, snapshot.Balance, snapshot.ConvertedBalance, snapshot.RateMultiplier,
		conversionScale, snapshot.Currency, snapshot.Reason, snapshot.ProbedAt, []byte(payload),
	).Scan(&snapshotID)
	if err != nil {
		return nil, fmt.Errorf("写入永久余额快照失败: %w", err)
	}

	identityKey := balanceCenterIdentityKey(snapshot)
	if _, err = tx.ExecContext(ctx, `
INSERT INTO balance_center_current_states (
    identity_key, site_id, account_id, legacy_key_id, snapshot_id, status,
    balance, converted_balance, rate_multiplier, conversion_scale,
    currency, reason, probed_at, last_used_at, source
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (identity_key) DO UPDATE SET
    site_id = EXCLUDED.site_id,
    account_id = EXCLUDED.account_id,
    legacy_key_id = EXCLUDED.legacy_key_id,
    snapshot_id = EXCLUDED.snapshot_id,
    status = EXCLUDED.status,
    balance = EXCLUDED.balance,
    converted_balance = EXCLUDED.converted_balance,
    rate_multiplier = EXCLUDED.rate_multiplier,
    conversion_scale = EXCLUDED.conversion_scale,
    currency = EXCLUDED.currency,
    reason = EXCLUDED.reason,
    probed_at = EXCLUDED.probed_at,
    last_used_at = COALESCE(EXCLUDED.last_used_at, balance_center_current_states.last_used_at),
    source = EXCLUDED.source,
    updated_at = NOW()`,
		identityKey, siteID, snapshot.AccountID, snapshot.LegacyKeyID, snapshotID,
		snapshot.Status, snapshot.Balance, snapshot.ConvertedBalance, snapshot.RateMultiplier,
		conversionScale, snapshot.Currency, snapshot.Reason, snapshot.ProbedAt,
		snapshot.LastUsedAt, snapshot.Source,
	); err != nil {
		return nil, fmt.Errorf("更新当前余额状态失败: %w", err)
	}

	if err = recordBalanceCenterCashIncrease(ctx, tx, siteID, snapshotID, conversionScale, snapshot); err != nil {
		return nil, fmt.Errorf("记录现金余额充值增量失败: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交余额快照事务失败: %w", err)
	}
	result := *snapshot
	result.ID = snapshotID
	result.SiteID = siteID
	result.ConversionScale = conversionScale
	return &result, nil
}

func (r *balanceCenterRepository) GetAlertState(ctx context.Context, identityKey string, _ int64, _ *int64) (service.BalanceCenterAlertState, error) {
	if r == nil || r.db == nil {
		return service.BalanceCenterAlertState{}, errors.New("余额中心仓储未初始化")
	}
	var lowBalanceActive bool
	var multiplier sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `
SELECT low_balance_active, multiplier_baseline
FROM balance_center_alert_states
WHERE identity_key = $1`, identityKey).Scan(&lowBalanceActive, &multiplier)
	if errors.Is(err, sql.ErrNoRows) {
		return service.BalanceCenterAlertState{}, nil
	}
	if err != nil {
		return service.BalanceCenterAlertState{}, fmt.Errorf("读取余额告警状态失败: %w", err)
	}
	state := service.BalanceCenterAlertState{LowBalanceActive: lowBalanceActive}
	if multiplier.Valid {
		value := multiplier.Float64
		state.MultiplierBaseline = &value
	}
	return state, nil
}

func (r *balanceCenterRepository) SaveAlertState(
	ctx context.Context,
	identityKey string,
	siteID int64,
	accountID *int64,
	snapshotID int64,
	state service.BalanceCenterAlertState,
	decision *service.BalanceCenterAlertDecision,
	now time.Time,
) error {
	if r == nil || r.db == nil {
		return errors.New("余额中心仓储未初始化")
	}
	var alertType any
	if decision != nil && strings.TrimSpace(decision.Type) != "" {
		alertType = decision.Type
	}
	// CASE 的时间参数必须显式声明类型，避免 PostgreSQL 按文本推断而阻断后续邮件入队。
	_, err := r.db.ExecContext(ctx, `
INSERT INTO balance_center_alert_states (
    identity_key, site_id, account_id, low_balance_active, multiplier_baseline,
    last_success_snapshot_id, low_balance_notified_at, multiplier_notified_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    CASE WHEN $7::text = 'low_balance' THEN $8::timestamptz ELSE NULL END,
    CASE WHEN $7::text = 'multiplier_changed' THEN $8::timestamptz ELSE NULL END
)
ON CONFLICT (identity_key) DO UPDATE SET
    site_id = EXCLUDED.site_id,
    account_id = EXCLUDED.account_id,
    low_balance_active = EXCLUDED.low_balance_active,
    multiplier_baseline = EXCLUDED.multiplier_baseline,
    last_success_snapshot_id = EXCLUDED.last_success_snapshot_id,
    low_balance_notified_at = CASE WHEN $7::text = 'low_balance' THEN $8::timestamptz ELSE balance_center_alert_states.low_balance_notified_at END,
    multiplier_notified_at = CASE WHEN $7::text = 'multiplier_changed' THEN $8::timestamptz ELSE balance_center_alert_states.multiplier_notified_at END,
    updated_at = NOW()`,
		identityKey, siteID, accountID, state.LowBalanceActive, state.MultiplierBaseline,
		snapshotID, alertType, now,
	)
	if err != nil {
		return fmt.Errorf("保存余额告警状态失败: %w", err)
	}
	return nil
}

func (r *balanceCenterRepository) RecordAlertDelivery(ctx context.Context, input *service.BalanceCenterAlertDeliveryInput) error {
	if r == nil || r.db == nil {
		return errors.New("余额中心仓储未初始化")
	}
	if input == nil || strings.TrimSpace(input.IdentityKey) == "" || strings.TrimSpace(input.AlertType) == "" {
		return errors.New("余额告警投递记录不能为空")
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO balance_center_alert_deliveries (
    identity_key, site_id, account_id, snapshot_id, alert_type,
    recipient_email, subject, old_value, new_value, threshold,
    status, failure_reason, attempted_at, accepted_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (snapshot_id, alert_type, recipient_email) DO UPDATE SET
    status = EXCLUDED.status,
    failure_reason = EXCLUDED.failure_reason,
    attempted_at = EXCLUDED.attempted_at,
    accepted_at = EXCLUDED.accepted_at`,
		input.IdentityKey, input.SiteID, input.AccountID, input.SnapshotID, input.AlertType,
		strings.TrimSpace(input.Recipient), strings.TrimSpace(input.Subject), input.OldValue,
		input.NewValue, input.Threshold, input.Status, input.FailureReason,
		input.AttemptedAt, input.AcceptedAt,
	)
	if err != nil {
		return fmt.Errorf("记录余额告警投递失败: %w", err)
	}
	return nil
}

func validateBalanceCenterSnapshot(snapshot *service.BalanceCenterSnapshot) error {
	if snapshot == nil {
		return errors.New("余额快照不能为空")
	}
	if strings.TrimSpace(snapshot.NormalizedDomain) == "" {
		return errors.New("余额快照站点域名不能为空")
	}
	if strings.TrimSpace(snapshot.Source) == "" || strings.TrimSpace(snapshot.SourceKey) == "" {
		return errors.New("余额快照幂等来源不能为空")
	}
	if snapshot.ProbedAt.IsZero() {
		return errors.New("余额快照时间不能为空")
	}
	return nil
}

func balanceCenterIdentityKey(snapshot *service.BalanceCenterSnapshot) string {
	if snapshot.AccountID != nil {
		return fmt.Sprintf("account:%d", *snapshot.AccountID)
	}
	if snapshot.LegacyKeyID != nil {
		return fmt.Sprintf("legacy:%d", *snapshot.LegacyKeyID)
	}
	return "source:" + snapshot.Source + ":" + snapshot.SourceKey
}
