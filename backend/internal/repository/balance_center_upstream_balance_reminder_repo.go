package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	if err = tx.QueryRowContext(ctx, `SELECT balance FROM upstream_site_balance_observations WHERE host=$1 FOR UPDATE`, item.Host).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	increased, delta := false, float64(0)
	if previous.Valid {
		increased, delta = service.EvaluateUpstreamBalanceIncrease(&previous.Float64, item.Balance)
	}
	var reminder *service.UpstreamSiteBalanceReminder
	if increased {
		reminder = &service.UpstreamSiteBalanceReminder{
			Host: item.Host, DisplayName: item.DisplayName, OldBalance: previous.Float64,
			NewBalance: item.Balance, Delta: delta, Currency: item.Currency, DetectedAt: item.ObservedAt,
		}
	}
	var alertOldBalance, alertNewBalance, alertDelta, alertCurrency, alertDetectedAt any
	if increased {
		alertOldBalance = previous.Float64
		alertNewBalance = item.Balance
		alertDelta = delta
		alertCurrency = item.Currency
		alertDetectedAt = item.ObservedAt
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
 alert_old_balance=CASE WHEN EXCLUDED.alert_active THEN COALESCE(upstream_site_balance_observations.alert_old_balance, EXCLUDED.alert_old_balance) ELSE upstream_site_balance_observations.alert_old_balance END,
 alert_new_balance=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_new_balance ELSE upstream_site_balance_observations.alert_new_balance END,
 alert_delta=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_new_balance - COALESCE(upstream_site_balance_observations.alert_old_balance, EXCLUDED.alert_old_balance) ELSE upstream_site_balance_observations.alert_delta END,
 alert_currency=CASE WHEN EXCLUDED.alert_active THEN EXCLUDED.alert_currency ELSE upstream_site_balance_observations.alert_currency END,
 alert_detected_at=CASE WHEN EXCLUDED.alert_active THEN COALESCE(upstream_site_balance_observations.alert_detected_at, EXCLUDED.alert_detected_at) ELSE upstream_site_balance_observations.alert_detected_at END,
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
