package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
INSERT INTO balance_center_sites (name, normalized_domain, base_url, source, probe_supported)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (normalized_domain) DO UPDATE SET
    name = EXCLUDED.name,
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

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交余额快照事务失败: %w", err)
	}
	result := *snapshot
	result.ID = snapshotID
	result.SiteID = siteID
	result.ConversionScale = conversionScale
	return &result, nil
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
