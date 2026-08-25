package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
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
SELECT s.id, s.name, s.normalized_domain, s.base_url, s.source, s.probe_supported,
       COALESCE(SUM(e.amount), 0) AS historical_recharge_total, s.updated_at
FROM balance_center_sites s
LEFT JOIN balance_center_recharge_events e ON e.site_id = s.id
GROUP BY s.id, s.name, s.normalized_domain, s.base_url, s.source, s.probe_supported, s.updated_at
ORDER BY COALESCE(SUM(e.amount), 0) DESC, s.normalized_domain ASC, s.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询余额站点失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterSite, 0)
	for rows.Next() {
		var item service.BalanceCenterSite
		if err := rows.Scan(&item.ID, &item.Name, &item.NormalizedDomain, &item.BaseURL, &item.Source, &item.ProbeSupported, &item.HistoricalRechargeTotal, &item.UpdatedAt); err != nil {
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

func (r *balanceCenterRepository) GetBalanceCenterRechargeSummary(ctx context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterRechargeSummary, error) {
	where, args := balanceCenterRechargeFilterSQL(filter)
	rows, err := r.db.QueryContext(ctx, `
SELECT s.id, s.source, s.source_key, s.site_id, s.account_id, s.amount, s.currency,
       s.occurred_at, s.note, s.site_label,
       CASE WHEN s.site_id IS NULL THEN COALESCE(NULLIF(BTRIM(s.site_label), ''), '未归属站点')
            ELSE COALESCE(NULLIF(BTRIM(bs.name), ''), '未归属站点') END AS site_name
FROM balance_center_recharge_events s
LEFT JOIN balance_center_sites bs ON bs.id = s.site_id`+where+`
ORDER BY s.occurred_at DESC, s.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	summary := &service.BalanceCenterRechargeSummary{
		Items:    make([]service.BalanceCenterRechargeEvent, 0),
		Sites:    make([]service.BalanceCenterRechargeSiteSummary, 0),
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}
	allItems := make([]service.BalanceCenterRechargeEvent, 0)
	siteIndexes := make(map[string]int)
	siteAmounts := make(map[string]decimal.Decimal)
	totalAmount := decimal.Zero
	for rows.Next() {
		var item service.BalanceCenterRechargeEvent
		var siteID, accountID sql.NullInt64
		var siteName string
		if err := rows.Scan(&item.ID, &item.Source, &item.SourceKey, &siteID, &accountID, &item.Amount, &item.Currency, &item.OccurredAt, &item.Note, &item.SiteLabel, &siteName); err != nil {
			return nil, err
		}
		item.SiteID, item.AccountID = balanceCenterNullInt64Ptr(siteID), balanceCenterNullInt64Ptr(accountID)
		allItems = append(allItems, item)
		amount := decimal.NewFromFloat(item.Amount)
		totalAmount = totalAmount.Add(amount)
		summary.TotalAmount = totalAmount.InexactFloat64()

		groupKey := "label:" + canonicalBalanceCenterSiteLabel(siteName)
		if item.SiteID != nil {
			groupKey = "site:" + fmt.Sprint(*item.SiteID)
		}
		index, exists := siteIndexes[groupKey]
		if !exists {
			index = len(summary.Sites)
			siteIndexes[groupKey] = index
			summary.Sites = append(summary.Sites, service.BalanceCenterRechargeSiteSummary{
				SiteID: item.SiteID, SiteName: siteName, Items: make([]service.BalanceCenterRechargeEvent, 0),
			})
		}
		site := &summary.Sites[index]
		currentAmount, exists := siteAmounts[groupKey]
		if !exists {
			currentAmount = decimal.Zero
		}
		siteAmounts[groupKey] = currentAmount.Add(amount)
		site.TotalAmount = siteAmounts[groupKey].InexactFloat64()
		site.RecordCount++
		site.Items = append(site.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(summary.Sites, func(i, j int) bool {
		return summary.Sites[i].TotalAmount > summary.Sites[j].TotalAmount
	})

	summary.Total = int64(len(allItems))
	if len(allItems) == 0 || filter.Page < 1 || filter.PageSize < 1 {
		return summary, nil
	}
	pageIndex := filter.Page - 1
	if pageIndex > (len(allItems)-1)/filter.PageSize {
		return summary, nil
	}
	start := pageIndex * filter.PageSize
	end := start + filter.PageSize
	if end < start || end > len(allItems) {
		end = len(allItems)
	}
	summary.Items = allItems[start:end]
	return summary, nil
}

func listBalanceCenterRechargeEvents(ctx context.Context, db *sql.DB, filter service.BalanceCenterListFilter, where string, args []any) (*service.BalanceCenterPage[service.BalanceCenterRechargeEvent], error) {
	var total int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM balance_center_recharge_events s"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := db.QueryContext(ctx, `SELECT id, source, source_key, site_id, account_id, amount, currency, occurred_at, note, site_label
FROM balance_center_recharge_events s`+where+` ORDER BY occurred_at DESC, id DESC LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.BalanceCenterRechargeEvent, 0, filter.PageSize)
	for rows.Next() {
		var item service.BalanceCenterRechargeEvent
		var siteID, accountID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Source, &item.SourceKey, &siteID, &accountID, &item.Amount, &item.Currency, &item.OccurredAt, &item.Note, &item.SiteLabel); err != nil {
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
INSERT INTO balance_center_recharge_events (source, source_key, site_id, site_label, account_id, amount, currency, occurred_at, note)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (source, source_key) DO UPDATE SET site_id=EXCLUDED.site_id, site_label=EXCLUDED.site_label, account_id=EXCLUDED.account_id,
amount=EXCLUDED.amount, currency=EXCLUDED.currency, occurred_at=EXCLUDED.occurred_at, note=EXCLUDED.note, updated_at=NOW()
RETURNING id`, item.Source, item.SourceKey, item.SiteID, item.SiteLabel, item.AccountID, item.Amount, item.Currency, item.OccurredAt, item.Note).Scan(&result.ID)
	return &result, err
}

func canonicalBalanceCenterSiteLabel(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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

func (r *balanceCenterRepository) SaveBalanceCenterLiandongSession(ctx context.Context, ciphertext string) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO balance_center_liandong_sessions (id, request_encrypted)
VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET request_encrypted=EXCLUDED.request_encrypted, updated_at=NOW()`, ciphertext)
	return err
}

func (r *balanceCenterRepository) GetBalanceCenterLiandongSession(ctx context.Context) (string, error) {
	var ciphertext string
	err := r.db.QueryRowContext(ctx, `SELECT request_encrypted FROM balance_center_liandong_sessions WHERE id=1`).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.ErrBalanceCenterLiandongSessionNotFound
	}
	return ciphertext, err
}

func (r *balanceCenterRepository) UpsertBalanceCenterLiandongOrders(ctx context.Context, orders []service.BalanceCenterLiandongOrder) (synced int, err error) {
	if len(orders) == 0 {
		return 0, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, order := range orders {
		payload, marshalErr := json.Marshal(map[string]any{
			"goods_name": order.GoodsName,
			"quantity":   order.Quantity,
		})
		if marshalErr != nil {
			return synced, marshalErr
		}
		result, execErr := tx.ExecContext(ctx, `
INSERT INTO balance_center_liandong_orders
    (source, source_key, transaction_no, paid_amount, currency, paid_at, status, payload)
VALUES ('liandong',$1,$1,$2,'CNY',$3,$4,$5::jsonb)
ON CONFLICT (transaction_no) DO UPDATE SET
    paid_amount=EXCLUDED.paid_amount,
    paid_at=EXCLUDED.paid_at,
    status=EXCLUDED.status,
    payload=EXCLUDED.payload,
    updated_at=NOW()`, order.TransactionNo, order.PaidAmount, order.PaidAt, order.Status, string(payload))
		if execErr != nil {
			return synced, execErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr == nil {
			synced += int(affected)
		}
	}
	if err = tx.Commit(); err != nil {
		return synced, err
	}
	return synced, nil
}

func (r *balanceCenterRepository) SyncBalanceCenterAutomaticRecords(ctx context.Context) (int, error) {
	result, err := r.db.ExecContext(ctx, `
INSERT INTO balance_center_automatic_records
    (source, source_key, site_id, amount, currency, occurred_at, record_type, metadata)
SELECT 'liandong', transaction_no, site_id, paid_amount, currency,
       COALESCE(paid_at, created_at), 'recharge', payload
FROM balance_center_liandong_orders
WHERE status = 'paid'
ON CONFLICT (source, source_key) DO UPDATE SET
    site_id=EXCLUDED.site_id,
    amount=EXCLUDED.amount,
    currency=EXCLUDED.currency,
    occurred_at=EXCLUDED.occurred_at,
    record_type=EXCLUDED.record_type,
    metadata=EXCLUDED.metadata`)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
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

func balanceCenterRechargeFilterSQL(filter service.BalanceCenterListFilter) (string, []any) {
	conditions, args := make([]string, 0, 3), make([]any, 0, 3)
	if filter.SiteID > 0 {
		args = append(args, filter.SiteID)
		conditions = append(conditions, "s.site_id=$"+fmt.Sprint(len(args)))
	}
	if filter.StartTime != nil {
		args = append(args, *filter.StartTime)
		conditions = append(conditions, "s.occurred_at >= $"+fmt.Sprint(len(args)))
	}
	if filter.EndTime != nil {
		args = append(args, *filter.EndTime)
		conditions = append(conditions, "s.occurred_at <= $"+fmt.Sprint(len(args)))
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
