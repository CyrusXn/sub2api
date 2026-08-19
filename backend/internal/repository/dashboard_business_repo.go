package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const dashboardSystemMetricMaxPoints = 360

func scanDashboardBusinessTotals(scanner interface{ Scan(...any) error }, out *service.DashboardBusinessTotals, adminActual, adminAccount *float64) error {
	if err := scanner.Scan(
		&out.RechargeAmount,
		&out.TotalRequests,
		&out.InputTokens,
		&out.OutputTokens,
		&out.CacheCreationTokens,
		&out.CacheReadTokens,
		&out.TotalCost,
		&out.ActualCost,
		&out.AccountCost,
		adminActual,
		adminAccount,
	); err != nil {
		return err
	}
	out.TotalTokens = out.InputTokens + out.OutputTokens + out.CacheCreationTokens + out.CacheReadTokens
	out.ActualCostExcludingAdmin = out.ActualCost - *adminActual
	out.AccountCostExcludingAdmin = out.AccountCost - *adminAccount
	return nil
}

func (r *dashboardAggregationRepository) GetDashboardBusinessSummary(ctx context.Context, start, end time.Time) (*service.DashboardBusinessSummary, error) {
	if r == nil || r.sql == nil {
		return nil, fmt.Errorf("仪表盘经营汇总仓储不可用")
	}
	query := `
		SELECT
			COALESCE(SUM(recharge_amount), 0),
			COALESCE(SUM(total_requests), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(account_cost), 0),
			COALESCE(SUM(admin_actual_cost), 0),
			COALESCE(SUM(admin_account_cost), 0),
			COALESCE(SUM(recharge_amount) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(total_requests) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(input_tokens) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(account_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(admin_actual_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(admin_account_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0)
		FROM dashboard_business_daily
	`
	result := &service.DashboardBusinessSummary{Daily: make([]service.DashboardBusinessDailyPoint, 0)}
	var lifetimeAdminActual, lifetimeAdminAccount float64
	var rangeAdminActual, rangeAdminAccount float64
	values := []any{
		&result.Lifetime.RechargeAmount,
		&result.Lifetime.TotalRequests,
		&result.Lifetime.InputTokens,
		&result.Lifetime.OutputTokens,
		&result.Lifetime.CacheCreationTokens,
		&result.Lifetime.CacheReadTokens,
		&result.Lifetime.TotalCost,
		&result.Lifetime.ActualCost,
		&result.Lifetime.AccountCost,
		&lifetimeAdminActual,
		&lifetimeAdminAccount,
		&result.Range.RechargeAmount,
		&result.Range.TotalRequests,
		&result.Range.InputTokens,
		&result.Range.OutputTokens,
		&result.Range.CacheCreationTokens,
		&result.Range.CacheReadTokens,
		&result.Range.TotalCost,
		&result.Range.ActualCost,
		&result.Range.AccountCost,
		&rangeAdminActual,
		&rangeAdminAccount,
	}
	if err := scanSingleRow(ctx, r.sql, query, []any{start, end}, values...); err != nil {
		return nil, err
	}
	result.Lifetime.TotalTokens = result.Lifetime.InputTokens + result.Lifetime.OutputTokens + result.Lifetime.CacheCreationTokens + result.Lifetime.CacheReadTokens
	result.Lifetime.ActualCostExcludingAdmin = result.Lifetime.ActualCost - lifetimeAdminActual
	result.Lifetime.AccountCostExcludingAdmin = result.Lifetime.AccountCost - lifetimeAdminAccount
	result.Range.TotalTokens = result.Range.InputTokens + result.Range.OutputTokens + result.Range.CacheCreationTokens + result.Range.CacheReadTokens
	result.Range.ActualCostExcludingAdmin = result.Range.ActualCost - rangeAdminActual
	result.Range.AccountCostExcludingAdmin = result.Range.AccountCost - rangeAdminAccount

	rows, err := r.sql.QueryContext(ctx, `
		SELECT
			bucket_date,
			recharge_amount,
			total_requests,
			input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens AS total_tokens,
			actual_cost,
			actual_cost - admin_actual_cost AS actual_cost_excluding_admin,
			account_cost,
			account_cost - admin_account_cost AS account_cost_excluding_admin
		FROM dashboard_business_daily
		WHERE bucket_date >= $1::date AND bucket_date < $2::date
		ORDER BY bucket_date ASC
	`, start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var point service.DashboardBusinessDailyPoint
		if err := rows.Scan(
			&point.BucketDate,
			&point.RechargeAmount,
			&point.TotalRequests,
			&point.TotalTokens,
			&point.ActualCost,
			&point.ActualCostExcludingAdmin,
			&point.AccountCost,
			&point.AccountCostExcludingAdmin,
		); err != nil {
			return nil, err
		}
		result.Daily = append(result.Daily, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *dashboardAggregationRepository) ListDashboardLowBalanceAccounts(ctx context.Context, threshold float64, limit int) ([]service.DashboardLowBalanceAccount, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT
			id,
			name,
			platform,
			(extra #>> '{upstream_billing_probe,balance,amount}')::double precision AS balance,
			COALESCE(extra #>> '{upstream_billing_probe,balance,unit}', ''),
			NULLIF(extra #>> '{upstream_billing_probe,balance,received_at}', '')::timestamptz
		FROM accounts
		WHERE deleted_at IS NULL
		  AND extra #>> '{upstream_billing_probe,balance,status}' = 'ok'
		  AND NULLIF(extra #>> '{upstream_billing_probe,balance,amount}', '') IS NOT NULL
		  AND (extra #>> '{upstream_billing_probe,balance,amount}')::double precision < $1
		ORDER BY balance ASC, id ASC
		LIMIT $2
	`, threshold, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.DashboardLowBalanceAccount, 0)
	for rows.Next() {
		var item service.DashboardLowBalanceAccount
		if err := rows.Scan(&item.ID, &item.Name, &item.Platform, &item.Balance, &item.Unit, &item.ReceivedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *dashboardAggregationRepository) GetDashboardLast24HourUsage(ctx context.Context, start, end time.Time) (int64, float64, error) {
	var tokens int64
	var actualCost float64
	err := scanSingleRow(ctx, r.sql, `
		WITH bounds AS (
			SELECT
				$1::timestamptz AS start_time,
				$2::timestamptz AS end_time,
				CASE
					WHEN $1::timestamptz = date_trunc('hour', $1::timestamptz) THEN $1::timestamptz
					ELSE date_trunc('hour', $1::timestamptz) + INTERVAL '1 hour'
				END AS full_hour_start,
				date_trunc('hour', $2::timestamptz) AS full_hour_end
		),
		hourly AS (
			SELECT
				COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS tokens,
				COALESCE(SUM(actual_cost), 0) AS actual_cost
			FROM usage_dashboard_hourly, bounds
			WHERE bucket_start >= full_hour_start AND bucket_start < full_hour_end
		),
		boundary AS (
			SELECT
				COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS tokens,
				COALESCE(SUM(actual_cost), 0) AS actual_cost
			FROM usage_logs, bounds
			WHERE created_at >= start_time AND created_at < end_time
			  AND (created_at < full_hour_start OR created_at >= full_hour_end)
		)
		SELECT
			hourly.tokens + boundary.tokens,
			hourly.actual_cost + boundary.actual_cost
		FROM hourly, boundary
	`, []any{start, end}, &tokens, &actualCost)
	return tokens, actualCost, err
}

func dashboardMetricBucketSize(start, end time.Time, maxPoints int) time.Duration {
	if maxPoints <= 0 || maxPoints > dashboardSystemMetricMaxPoints {
		maxPoints = 240
	}
	duration := end.Sub(start)
	if duration <= 0 {
		return time.Minute
	}
	bucket := duration / time.Duration(maxPoints)
	if bucket < time.Minute {
		return time.Minute
	}
	return bucket.Round(time.Minute)
}

func (r *dashboardAggregationRepository) GetDashboardSystemMetricTrend(ctx context.Context, start, end time.Time, maxPoints int) (*service.DashboardSystemMetricTrend, error) {
	bucket := dashboardMetricBucketSize(start, end, maxPoints)
	rows, err := r.sql.QueryContext(ctx, `
		SELECT
			date_bin($3::interval, created_at, TIMESTAMPTZ '1970-01-01 00:00:00+00') AS bucket,
			AVG(cpu_usage_percent),
			AVG(memory_used_mb)::bigint,
			AVG(memory_total_mb)::bigint,
			AVG(memory_usage_percent),
			AVG(network_receive_bytes_per_second),
			AVG(network_transmit_bytes_per_second),
			AVG(disk_used_bytes)::bigint,
			AVG(disk_total_bytes)::bigint,
			AVG(disk_usage_percent),
			COALESCE(MAX(resource_source), '')
		FROM ops_system_metrics
		WHERE created_at >= $1 AND created_at < $2
		  AND window_minutes = 1
		  AND platform IS NULL
		  AND group_id IS NULL
		GROUP BY bucket
		ORDER BY bucket ASC
		LIMIT 360
	`, start, end, bucket.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := &service.DashboardSystemMetricTrend{
		Points:       make([]service.DashboardSystemMetricPoint, 0),
		NetworkDaily: make([]service.DashboardNetworkTrafficDailyPoint, 0),
	}
	for rows.Next() {
		var point service.DashboardSystemMetricPoint
		var cpu, memoryPct, receive, transmit, diskPct sql.NullFloat64
		var memoryUsed, memoryTotal, diskUsed, diskTotal sql.NullInt64
		if err := rows.Scan(
			&point.Time,
			&cpu,
			&memoryUsed,
			&memoryTotal,
			&memoryPct,
			&receive,
			&transmit,
			&diskUsed,
			&diskTotal,
			&diskPct,
			&point.ResourceSource,
		); err != nil {
			return nil, err
		}
		point.CPUUsagePercent = nullableFloat64(cpu)
		point.MemoryUsedMB = nullableInt64(memoryUsed)
		point.MemoryTotalMB = nullableInt64(memoryTotal)
		point.MemoryUsagePercent = nullableFloat64(memoryPct)
		point.NetworkReceiveBytesPerSecond = nullableFloat64(receive)
		point.NetworkTransmitBytesPerSecond = nullableFloat64(transmit)
		point.DiskUsedBytes = nullableInt64(diskUsed)
		point.DiskTotalBytes = nullableInt64(diskTotal)
		point.DiskUsagePercent = nullableFloat64(diskPct)
		if point.ResourceSource == "host" {
			result.Source = "host"
		} else if result.Source == "" && point.ResourceSource != "" {
			result.Source = point.ResourceSource
		}
		result.Points = append(result.Points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	dailyRows, err := r.sql.QueryContext(ctx, `
		SELECT bucket_date, receive_bytes, transmit_bytes
		FROM ops_network_traffic_daily
		WHERE bucket_date >= ($1 AT TIME ZONE 'Asia/Shanghai')::date
		  AND bucket_date < ($2 AT TIME ZONE 'Asia/Shanghai')::date
		ORDER BY bucket_date ASC
	`, start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dailyRows.Close() }()
	for dailyRows.Next() {
		var bucketDate time.Time
		var point service.DashboardNetworkTrafficDailyPoint
		if err := dailyRows.Scan(&bucketDate, &point.ReceiveBytes, &point.TransmitBytes); err != nil {
			return nil, err
		}
		point.BucketDate = bucketDate.Format("2006-01-02")
		point.TotalBytes = point.ReceiveBytes + point.TransmitBytes
		result.NetworkTotals.ReceiveBytes += point.ReceiveBytes
		result.NetworkTotals.TransmitBytes += point.TransmitBytes
		result.NetworkDaily = append(result.NetworkDaily, point)
	}
	if err := dailyRows.Err(); err != nil {
		return nil, err
	}
	result.NetworkTotals.TotalBytes = result.NetworkTotals.ReceiveBytes + result.NetworkTotals.TransmitBytes
	return result, nil
}

func nullableFloat64(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	v := value.Float64
	return &v
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}
