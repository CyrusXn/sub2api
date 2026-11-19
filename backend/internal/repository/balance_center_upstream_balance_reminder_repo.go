package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewUpstreamSiteBalanceReminderRepository(db *sql.DB) service.UpstreamSiteBalanceReminderRepository {
	return &balanceCenterRepository{db: db}
}

func (r *balanceCenterRepository) EnsureUpstreamSiteBalanceObservations(ctx context.Context, items []service.UpstreamSiteBalanceObservation) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO upstream_site_balance_observations (host, display_name, base_url, next_probe_at)
VALUES ($1,$2,$3,NOW())
ON CONFLICT (host) DO UPDATE SET
 display_name=EXCLUDED.display_name,
 base_url=EXCLUDED.base_url,
 next_probe_at=CASE WHEN upstream_site_balance_observations.last_error='site_not_configured' THEN NOW() ELSE upstream_site_balance_observations.next_probe_at END,
 last_error=CASE WHEN upstream_site_balance_observations.last_error='site_not_configured' THEN '' ELSE upstream_site_balance_observations.last_error END,
 updated_at=NOW()`, item.Host, item.DisplayName, item.BaseURL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *balanceCenterRepository) ListDueUpstreamSiteBalanceObservations(ctx context.Context, now time.Time, limit int) ([]service.UpstreamSiteBalanceObservation, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT host, display_name, base_url, failure_count
FROM upstream_site_balance_observations
WHERE next_probe_at <= $1
ORDER BY next_probe_at, host
LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.UpstreamSiteBalanceObservation, 0)
	for rows.Next() {
		var item service.UpstreamSiteBalanceObservation
		if err := rows.Scan(&item.Host, &item.DisplayName, &item.BaseURL, &item.FailureCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *balanceCenterRepository) SaveUpstreamSiteBalanceObservation(ctx context.Context, item service.UpstreamSiteBalanceObservation) (_ *service.UpstreamSiteBalanceReminder, err error) {
	item.Currency = strings.TrimSpace(item.Currency)
	if item.Currency == "" {
		item.Currency = "USD"
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var previous sql.NullFloat64
	var previousCurrency, previousAlertCurrency string
	var previousAlertActive bool
	var previousAlertOldBalance, previousAlertDelta sql.NullFloat64
	var previousAlertDetectedAt sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT balance, currency, alert_active, alert_old_balance, alert_delta, alert_currency, alert_detected_at FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE`, item.Host).Scan(&previous, &previousCurrency, &previousAlertActive, &previousAlertOldBalance, &previousAlertDelta, &previousAlertCurrency, &previousAlertDetectedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	increased := false
	delta := 0.0
	// API 与网页可能使用不同单位，单位变化只能重建基线，不能当作充值。
	if previous.Valid && strings.EqualFold(strings.TrimSpace(previousCurrency), item.Currency) {
		increased, delta = service.EvaluateUpstreamBalanceIncrease(&previous.Float64, item.Balance)
	}
	observedDelta := delta
	var reminder *service.UpstreamSiteBalanceReminder
	var alertOldBalance, alertNewBalance, alertDelta, alertDetectedAt any
	// 非上涨采样也会先校验 INSERT 的非空约束，提醒币种不能传 NULL。
	alertCurrency := item.Currency
	if increased {
		oldBalance, detectedAt := previous.Float64, item.ObservedAt
		// 只累计同币种、仍待处理且达标的涨幅；小额波动与消费不混入累计金额。
		// old/new 保留首尾实测余额，delta 是各次达标增幅之和，可能不同于两者净差。
		if previousAlertActive && previousAlertDelta.Valid && previousAlertDelta.Float64 >= service.UpstreamSiteBalanceMinimumIncrease && strings.EqualFold(strings.TrimSpace(previousAlertCurrency), item.Currency) {
			if previousAlertOldBalance.Valid {
				oldBalance = previousAlertOldBalance.Float64
			}
			if previousAlertDetectedAt.Valid {
				detectedAt = previousAlertDetectedAt.Time
			}
			delta += previousAlertDelta.Float64
		}
		alertOldBalance = oldBalance
		alertNewBalance = item.Balance
		alertDelta = delta
		alertDetectedAt = detectedAt
		// 充值归属检测时间；审核时间仅用于 review 字段，绝不回写 occurred_at。
		amount := int64(observedDelta)
		if amount > 0 {
			sourceKey := fmt.Sprintf("%s:%s", strings.ToLower(item.Host), item.ObservedAt.UTC().Format(time.RFC3339Nano))
			_, err = tx.ExecContext(ctx, `
WITH site AS (
 INSERT INTO balance_center_sites (name, display_name, normalized_domain, base_url, source, probe_supported)
 VALUES ($5, $5, LOWER($4), $6, 'sub2api', TRUE)
 ON CONFLICT (normalized_domain) DO UPDATE SET
   display_name=CASE WHEN balance_center_sites.display_name='' THEN EXCLUDED.display_name ELSE balance_center_sites.display_name END,
   base_url=CASE WHEN balance_center_sites.base_url='' THEN EXCLUDED.base_url ELSE balance_center_sites.base_url END,
   updated_at=NOW()
 RETURNING id, COALESCE(NULLIF(display_name, ''), name) AS site_label
)
INSERT INTO balance_center_recharge_events (source, source_key, site_id, site_label, amount, currency, occurred_at, note, record_type, review_status)
SELECT 'upstream_auto', $1, site.id, site.site_label, $2, 'CNY', $3, '余额增加自动识别，待审核', 'recharge', 'pending' FROM site
ON CONFLICT (source, source_key) DO NOTHING`, sourceKey, amount, item.ObservedAt, item.Host, item.DisplayName, item.BaseURL)
			if err != nil {
				return nil, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO upstream_site_balance_observations (
 host, display_name, base_url, balance, currency, observed_at, next_probe_at,
 failure_count, last_error, alert_active, alert_old_balance, alert_new_balance,
 alert_delta, alert_currency, alert_detected_at, alert_version, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,0,'', $8,$9,$10,$11,$12,$13,CASE WHEN $8 THEN 1 ELSE 0 END,NOW())
ON CONFLICT (host) DO UPDATE SET
 display_name=EXCLUDED.display_name, base_url=EXCLUDED.base_url,
 balance=EXCLUDED.balance, currency=EXCLUDED.currency, observed_at=EXCLUDED.observed_at,
 next_probe_at=EXCLUDED.next_probe_at, failure_count=0, last_error='',
 alert_active=CASE WHEN EXCLUDED.alert_active THEN TRUE ELSE upstream_site_balance_observations.alert_active END,
 alert_old_balance=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_old_balance ELSE upstream_site_balance_observations.alert_old_balance END,
 alert_new_balance=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_new_balance ELSE upstream_site_balance_observations.alert_new_balance END,
 alert_delta=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_delta ELSE upstream_site_balance_observations.alert_delta END,
 alert_currency=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_currency ELSE upstream_site_balance_observations.alert_currency END,
 alert_detected_at=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_detected_at ELSE upstream_site_balance_observations.alert_detected_at END,
	alert_version=CASE WHEN EXCLUDED.alert_active THEN upstream_site_balance_observations.alert_version+1 ELSE upstream_site_balance_observations.alert_version END,
	updated_at=NOW()`, item.Host, item.DisplayName, item.BaseURL, item.Balance, item.Currency, item.ObservedAt, item.ObservedAt.Add(service.UpstreamSiteBalancePollInterval), increased, alertOldBalance, alertNewBalance, alertDelta, alertCurrency, alertDetectedAt)
	if err != nil {
		return nil, err
	}
	if increased {
		var persisted service.UpstreamSiteBalanceReminder
		err = tx.QueryRowContext(ctx, `
SELECT host, display_name, alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version
FROM upstream_site_balance_observations WHERE host=$1 AND alert_active=TRUE`, item.Host).Scan(
			&persisted.Host, &persisted.DisplayName, &persisted.OldBalance, &persisted.NewBalance,
			&persisted.Delta, &persisted.Currency, &persisted.DetectedAt, &persisted.Version,
		)
		if err != nil {
			return nil, err
		}
		reminder = &persisted
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return reminder, nil
}

func (r *balanceCenterRepository) ListActiveUpstreamSiteBalanceReminders(ctx context.Context) ([]service.UpstreamSiteBalanceReminder, error) {
	items, _, err := r.ListUpstreamSiteBalanceReminderSnapshot(ctx)
	return items, err
}

func (r *balanceCenterRepository) ListUpstreamSiteBalanceReminderSnapshot(ctx context.Context) ([]service.UpstreamSiteBalanceReminder, map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT host, display_name, alert_active, alert_old_balance, alert_new_balance, alert_delta,
       alert_currency, alert_detected_at, alert_version
FROM upstream_site_balance_observations ORDER BY host`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]service.UpstreamSiteBalanceReminder, 0)
	versions := make(map[string]int64)
	for rows.Next() {
		var item service.UpstreamSiteBalanceReminder
		var active bool
		var oldBalance, newBalance, delta sql.NullFloat64
		var detectedAt sql.NullTime
		if err := rows.Scan(&item.Host, &item.DisplayName, &active, &oldBalance, &newBalance, &delta, &item.Currency, &detectedAt, &item.Version); err != nil {
			return nil, nil, err
		}
		versions[item.Host] = item.Version
		if !active {
			continue
		}
		item.OldBalance, item.NewBalance, item.Delta = oldBalance.Float64, newBalance.Float64, delta.Float64
		if detectedAt.Valid {
			item.DetectedAt = detectedAt.Time
		}
		items = append(items, item)
	}
	return items, versions, rows.Err()
}

func (r *balanceCenterRepository) DeferUpstreamSiteBalanceObservation(ctx context.Context, host string, next time.Time, reason string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE upstream_site_balance_observations SET next_probe_at=$2, failure_count=LEAST(failure_count+1, 10), last_error=$3, updated_at=NOW() WHERE host=$1`, host, next, reason)
	return err
}

func (r *balanceCenterRepository) ResolveUpstreamSiteBalanceReminder(ctx context.Context, host string, now time.Time) (int64, bool, error) {
	var version int64
	err := r.db.QueryRowContext(ctx, `UPDATE upstream_site_balance_observations SET alert_active=FALSE, alert_old_balance=NULL, alert_new_balance=NULL, alert_delta=NULL, alert_detected_at=NULL, alert_version=alert_version+1, updated_at=$2 WHERE host=$1 AND alert_active=TRUE RETURNING alert_version`, host, now).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("resolve upstream balance reminder: %w", err)
	}
	return version, true, nil
}
