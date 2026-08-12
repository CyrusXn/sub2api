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

func (r *balanceCenterRepository) ListBalanceCenterOverview(ctx context.Context) ([]service.BalanceCenterOverviewItem, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT s.id, s.name, s.normalized_domain, s.base_url, cs.account_id,
       COALESCE(a.name, ''), cs.status, cs.balance, cs.converted_balance,
       cs.rate_multiplier, cs.conversion_scale, cs.currency, cs.reason,
       cs.probed_at, cs.last_used_at, cs.source
FROM balance_center_current_states cs
JOIN balance_center_sites s ON s.id = cs.site_id
LEFT JOIN accounts a ON a.id = cs.account_id
ORDER BY s.normalized_domain ASC, cs.account_id NULLS LAST`)
	if err != nil {
		return nil, fmt.Errorf("查询余额概览失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterOverviewItem, 0)
	for rows.Next() {
		var item service.BalanceCenterOverviewItem
		var accountID sql.NullInt64
		var balance, converted, rate sql.NullFloat64
		var lastUsed sql.NullTime
		if err := rows.Scan(&item.SiteID, &item.SiteName, &item.NormalizedDomain, &item.BaseURL,
			&accountID, &item.AccountName, &item.Status, &balance, &converted, &rate,
			&item.ConversionScale, &item.Currency, &item.Reason, &item.ProbedAt, &lastUsed, &item.Source); err != nil {
			return nil, err
		}
		item.AccountID = balanceCenterNullInt64Ptr(accountID)
		item.Balance = balanceCenterNullFloat64Ptr(balance)
		item.ConvertedBalance = balanceCenterNullFloat64Ptr(converted)
		item.RateMultiplier = balanceCenterNullFloat64Ptr(rate)
		item.LastUsedAt = balanceCenterNullTimePtr(lastUsed)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *balanceCenterRepository) ListBalanceCenterSites(ctx context.Context) ([]service.BalanceCenterSite, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, name, normalized_domain, base_url, source, probe_supported, updated_at
FROM balance_center_sites ORDER BY normalized_domain ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询余额站点失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterSite, 0)
	for rows.Next() {
		var item service.BalanceCenterSite
		if err := rows.Scan(&item.ID, &item.Name, &item.NormalizedDomain, &item.BaseURL, &item.Source, &item.ProbeSupported, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *balanceCenterRepository) ListBalanceCenterSnapshots(ctx context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterSnapshot], error) {
	where, args := balanceCenterFilterSQL(filter, true)
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM balance_center_snapshots s"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limitPos, offsetPos := len(args)+1, len(args)+2
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `
SELECT s.id, s.site_id, s.account_id, s.legacy_key_id, bs.name, bs.normalized_domain,
       bs.base_url, s.source, s.source_key, s.status, s.balance, s.converted_balance,
       s.rate_multiplier, s.conversion_scale, s.currency, s.reason, s.probed_at, s.payload
FROM balance_center_snapshots s
JOIN balance_center_sites bs ON bs.id = s.site_id`+where+`
ORDER BY s.probed_at DESC, s.id DESC
LIMIT $`+fmt.Sprint(limitPos)+` OFFSET $`+fmt.Sprint(offsetPos), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterSnapshot, 0, filter.PageSize)
	for rows.Next() {
		var item service.BalanceCenterSnapshot
		var accountID, legacyKeyID sql.NullInt64
		var balance, converted, rate sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.SiteID, &accountID, &legacyKeyID, &item.SiteName,
			&item.NormalizedDomain, &item.BaseURL, &item.Source, &item.SourceKey, &item.Status,
			&balance, &converted, &rate, &item.ConversionScale, &item.Currency, &item.Reason,
			&item.ProbedAt, &item.Payload); err != nil {
			return nil, err
		}
		item.AccountID, item.LegacyKeyID = balanceCenterNullInt64Ptr(accountID), balanceCenterNullInt64Ptr(legacyKeyID)
		item.Balance, item.ConvertedBalance, item.RateMultiplier = balanceCenterNullFloat64Ptr(balance), balanceCenterNullFloat64Ptr(converted), balanceCenterNullFloat64Ptr(rate)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &service.BalanceCenterPage[service.BalanceCenterSnapshot]{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (r *balanceCenterRepository) ListBalanceCenterManualRows(ctx context.Context) ([]service.BalanceCenterManualRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, source, source_key, site_id, label, expression, amount, sort_order FROM balance_center_manual_rows ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterManualRow, 0)
	for rows.Next() {
		var item service.BalanceCenterManualRow
		var siteID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Source, &item.SourceKey, &siteID, &item.Label, &item.Expression, &item.Amount, &item.SortOrder); err != nil {
			return nil, err
		}
		item.SiteID = balanceCenterNullInt64Ptr(siteID)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *balanceCenterRepository) ReplaceBalanceCenterManualRows(ctx context.Context, rows []service.BalanceCenterManualRow) (err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, item := range rows {
		if _, err = tx.ExecContext(ctx, `
INSERT INTO balance_center_manual_rows (source, source_key, site_id, label, expression, amount, sort_order)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (source, source_key) DO UPDATE SET site_id=EXCLUDED.site_id, label=EXCLUDED.label,
expression=EXCLUDED.expression, amount=EXCLUDED.amount, sort_order=EXCLUDED.sort_order, updated_at=NOW()`,
			item.Source, item.SourceKey, item.SiteID, item.Label, item.Expression, item.Amount, item.SortOrder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *balanceCenterRepository) ListBalanceCenterRechargeEvents(ctx context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterRechargeEvent], error) {
	where, args := balanceCenterFilterSQL(filter, false)
	return listBalanceCenterRechargeEvents(ctx, r.db, filter, where, args)
}

func listBalanceCenterRechargeEvents(ctx context.Context, db *sql.DB, filter service.BalanceCenterListFilter, where string, args []any) (*service.BalanceCenterPage[service.BalanceCenterRechargeEvent], error) {
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM balance_center_recharge_events s"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := db.QueryContext(ctx, `SELECT id, source, source_key, site_id, account_id, amount, currency, occurred_at, note
FROM balance_center_recharge_events s`+where+` ORDER BY occurred_at DESC, id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterRechargeEvent, 0, filter.PageSize)
	for rows.Next() {
		var item service.BalanceCenterRechargeEvent
		var siteID, accountID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Source, &item.SourceKey, &siteID, &accountID, &item.Amount, &item.Currency, &item.OccurredAt, &item.Note); err != nil {
			return nil, err
		}
		item.SiteID, item.AccountID = balanceCenterNullInt64Ptr(siteID), balanceCenterNullInt64Ptr(accountID)
		items = append(items, item)
	}
	return &service.BalanceCenterPage[service.BalanceCenterRechargeEvent]{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, rows.Err()
}

func (r *balanceCenterRepository) CreateBalanceCenterRechargeEvent(ctx context.Context, item *service.BalanceCenterRechargeEvent) (*service.BalanceCenterRechargeEvent, error) {
	result := *item
	err := r.db.QueryRowContext(ctx, `
INSERT INTO balance_center_recharge_events (source, source_key, site_id, account_id, amount, currency, occurred_at, note)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (source, source_key) DO UPDATE SET site_id=EXCLUDED.site_id, account_id=EXCLUDED.account_id,
amount=EXCLUDED.amount, currency=EXCLUDED.currency, occurred_at=EXCLUDED.occurred_at, note=EXCLUDED.note, updated_at=NOW()
RETURNING id`, item.Source, item.SourceKey, item.SiteID, item.AccountID, item.Amount, item.Currency, item.OccurredAt, item.Note).Scan(&result.ID)
	return &result, err
}

func (r *balanceCenterRepository) DeleteBalanceCenterRechargeEvent(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM balance_center_recharge_events WHERE id=$1`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("充值记录不存在")
	}
	return nil
}

func (r *balanceCenterRepository) ListBalanceCenterReconciliations(ctx context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterReconciliation], error) {
	filter.Status = strings.TrimSpace(filter.Status)
	where, args := "", []any{}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = " WHERE status=$1"
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM balance_center_reconciliations"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT id, source, source_key, period_start, period_end, expected_amount, actual_amount, difference_amount, status, created_at
FROM balance_center_reconciliations`+where+` ORDER BY created_at DESC, id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterReconciliation, 0, filter.PageSize)
	for rows.Next() {
		var item service.BalanceCenterReconciliation
		var start, end sql.NullTime
		var expected, actual, difference sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.Source, &item.SourceKey, &start, &end, &expected, &actual, &difference, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.PeriodStart, item.PeriodEnd = balanceCenterNullTimePtr(start), balanceCenterNullTimePtr(end)
		item.ExpectedAmount, item.ActualAmount, item.DifferenceAmount = balanceCenterNullFloat64Ptr(expected), balanceCenterNullFloat64Ptr(actual), balanceCenterNullFloat64Ptr(difference)
		items = append(items, item)
	}
	return &service.BalanceCenterPage[service.BalanceCenterReconciliation]{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, rows.Err()
}

func (r *balanceCenterRepository) CreateBalanceCenterReconciliation(ctx context.Context, item *service.BalanceCenterReconciliation) (*service.BalanceCenterReconciliation, error) {
	result := *item
	err := r.db.QueryRowContext(ctx, `
INSERT INTO balance_center_reconciliations (source, source_key, period_start, period_end, expected_amount, actual_amount, difference_amount, status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (source, source_key) DO UPDATE SET period_start=EXCLUDED.period_start, period_end=EXCLUDED.period_end,
expected_amount=EXCLUDED.expected_amount, actual_amount=EXCLUDED.actual_amount, difference_amount=EXCLUDED.difference_amount, status=EXCLUDED.status
RETURNING id, created_at`, item.Source, item.SourceKey, item.PeriodStart, item.PeriodEnd, item.ExpectedAmount, item.ActualAmount, item.DifferenceAmount, item.Status).Scan(&result.ID, &result.CreatedAt)
	return &result, err
}

func (r *balanceCenterRepository) ListBalanceCenterAlerts(ctx context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterAlertDelivery], error) {
	where, args := balanceCenterFilterSQL(filter, false)
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM balance_center_alert_deliveries s"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT id, site_id, account_id, snapshot_id, alert_type, recipient_email,
old_value, new_value, threshold, status, failure_reason, attempted_at, accepted_at, created_at
FROM balance_center_alert_deliveries s`+where+` ORDER BY created_at DESC, id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterAlertDelivery, 0, filter.PageSize)
	for rows.Next() {
		var item service.BalanceCenterAlertDelivery
		var accountID, snapshotID sql.NullInt64
		var oldValue, newValue, threshold sql.NullFloat64
		var attempted, accepted sql.NullTime
		if err := rows.Scan(&item.ID, &item.SiteID, &accountID, &snapshotID, &item.AlertType, &item.Recipient,
			&oldValue, &newValue, &threshold, &item.Status, &item.FailureReason, &attempted, &accepted, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.AccountID, item.SnapshotID = balanceCenterNullInt64Ptr(accountID), balanceCenterNullInt64Ptr(snapshotID)
		item.OldValue, item.NewValue, item.Threshold = balanceCenterNullFloat64Ptr(oldValue), balanceCenterNullFloat64Ptr(newValue), balanceCenterNullFloat64Ptr(threshold)
		item.AttemptedAt, item.AcceptedAt = balanceCenterNullTimePtr(attempted), balanceCenterNullTimePtr(accepted)
		items = append(items, item)
	}
	return &service.BalanceCenterPage[service.BalanceCenterAlertDelivery]{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, rows.Err()
}

func balanceCenterFilterSQL(filter service.BalanceCenterListFilter, includeTime bool) (string, []any) {
	conditions, args := make([]string, 0, 5), make([]any, 0, 5)
	add := func(column string, value any) {
		args = append(args, value)
		conditions = append(conditions, column+"=$"+fmt.Sprint(len(args)))
	}
	if filter.SiteID > 0 {
		add("s.site_id", filter.SiteID)
	}
	if filter.AccountID > 0 {
		add("s.account_id", filter.AccountID)
	}
	if filter.Status != "" {
		add("s.status", filter.Status)
	}
	if includeTime && filter.StartTime != nil {
		args = append(args, *filter.StartTime)
		conditions = append(conditions, "s.probed_at >= $"+fmt.Sprint(len(args)))
	}
	if includeTime && filter.EndTime != nil {
		args = append(args, *filter.EndTime)
		conditions = append(conditions, "s.probed_at <= $"+fmt.Sprint(len(args)))
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func balanceCenterNullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
func balanceCenterNullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}
func balanceCenterNullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
