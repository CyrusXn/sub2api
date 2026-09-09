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
			COALESCE((SELECT SUM(amount) FROM balance_center_recharge_events), 0),
			COALESCE((SELECT SUM(balance) FROM users WHERE deleted_at IS NULL AND LOWER(TRIM(COALESCE(email, ''))) <> 'admin@example.com' AND total_recharged > 1), 0),
			COALESCE((
				SELECT SUM(site_balance)
				FROM (
					SELECT
						COALESCE(NULLIF(TRIM(credentials ->> 'base_url'), ''), NULLIF(TRIM(platform), ''), 'unknown') AS site_key,
						MIN((extra #>> '{upstream_billing_probe,balance,amount}')::double precision) AS site_balance
					FROM accounts
					WHERE deleted_at IS NULL
					  -- 余额有独立成功状态，倍率失败不代表余额无效。
					  AND extra #>> '{upstream_billing_probe,balance,status}' = 'ok'
					  AND extra #>> '{upstream_billing_probe,balance,amount}' IS NOT NULL
					  -- 只用单反斜杠：raw string 里写 \\. 会被 Postgres 解释为"匹配一个反斜杠"，
					  -- 从而把所有带小数点的余额全部过滤掉，这里必须是转义小数点。
					  AND extra #>> '{upstream_billing_probe,balance,amount}' ~ '^-?[0-9]+(\.[0-9]+)?$'
					GROUP BY 1
				) upstream_site_balances
			), 0),
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
			COALESCE(SUM(upstream_cost), 0),
			COALESCE(SUM(admin_upstream_cost), 0),
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
			COALESCE(SUM(admin_account_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(upstream_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE(SUM(admin_upstream_cost) FILTER (WHERE bucket_date >= $1::date AND bucket_date < $2::date), 0),
			COALESCE((
				SELECT SUM(amount)
				FROM balance_center_recharge_events
				WHERE occurred_at >= $3::timestamptz AND occurred_at < $4::timestamptz
			), 0),
			(
				SELECT b.user_balance_total
				FROM dashboard_business_daily b
				WHERE b.bucket_date = $2::date - 1 AND b.balance_captured_at IS NOT NULL
				ORDER BY b.bucket_date DESC
				LIMIT 1
			),
			(
				SELECT b.upstream_balance_total
				FROM dashboard_business_daily b
				WHERE b.bucket_date = $2::date - 1 AND b.balance_captured_at IS NOT NULL
				ORDER BY b.bucket_date DESC
				LIMIT 1
			),
			(
				SELECT b.bucket_date
				FROM dashboard_business_daily b
				WHERE b.bucket_date = $2::date - 1 AND b.balance_captured_at IS NOT NULL
				ORDER BY b.bucket_date DESC
				LIMIT 1
			)
		FROM dashboard_business_daily
	`
	result := &service.DashboardBusinessSummary{Daily: make([]service.DashboardBusinessDailyPoint, 0)}
	var lifetimeAdminActual, lifetimeAdminAccount float64
	var rangeAdminActual, rangeAdminAccount float64
	var lifetimeAdminUpstream, rangeAdminUpstream float64
	// 余额类指标只有每日快照，区间内可能一天都没采集到，用 NULL 承载"暂无数据"。
	var rangeUserBalance, rangeUpstreamBalance sql.NullFloat64
	var rangeBalanceSnapshotDate sql.NullTime
	values := []any{
		&result.UpstreamRechargeTotal,
		&result.UserBalanceTotal,
		&result.UpstreamBalanceTotal,
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
		&result.Lifetime.UpstreamCost,
		&lifetimeAdminUpstream,
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
		&result.Range.UpstreamCost,
		&rangeAdminUpstream,
		&result.RangeUpstreamRechargeTotal,
		&rangeUserBalance,
		&rangeUpstreamBalance,
		&rangeBalanceSnapshotDate,
	}
	// 日桶与充值事件使用独立参数，避免 PostgreSQL 将同一参数推断成 date 后丢失北京时间偏移。
	startDate, endDate := start.Format("2006-01-02"), end.Format("2006-01-02")
	if err := scanSingleRow(ctx, r.sql, query, []any{startDate, endDate, start, end}, values...); err != nil {
		return nil, err
	}
	result.Lifetime.TotalTokens = result.Lifetime.InputTokens + result.Lifetime.OutputTokens + result.Lifetime.CacheCreationTokens + result.Lifetime.CacheReadTokens
	result.Lifetime.ActualCostExcludingAdmin = result.Lifetime.ActualCost - lifetimeAdminActual
	result.Lifetime.AccountCostExcludingAdmin = result.Lifetime.AccountCost - lifetimeAdminAccount
	result.Lifetime.UpstreamCostExcludingAdmin = result.Lifetime.UpstreamCost - lifetimeAdminUpstream
	result.Range.TotalTokens = result.Range.InputTokens + result.Range.OutputTokens + result.Range.CacheCreationTokens + result.Range.CacheReadTokens
	result.Range.ActualCostExcludingAdmin = result.Range.ActualCost - rangeAdminActual
	result.Range.AccountCostExcludingAdmin = result.Range.AccountCost - rangeAdminAccount
	result.Range.UpstreamCostExcludingAdmin = result.Range.UpstreamCost - rangeAdminUpstream
	result.RangeUserBalanceTotal = nullableFloat64(rangeUserBalance)
	result.RangeUpstreamBalanceTotal = nullableFloat64(rangeUpstreamBalance)
	if rangeBalanceSnapshotDate.Valid {
		snapshotDate := rangeBalanceSnapshotDate.Time
		result.RangeBalanceSnapshotDate = &snapshotDate
	}

	rows, err := r.sql.QueryContext(ctx, `
		SELECT
			bucket_date,
			recharge_amount,
			total_requests,
			input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens AS total_tokens,
			actual_cost,
			actual_cost - admin_actual_cost AS actual_cost_excluding_admin,
			account_cost,
			account_cost - admin_account_cost AS account_cost_excluding_admin,
			upstream_cost,
			upstream_cost - admin_upstream_cost AS upstream_cost_excluding_admin
		FROM dashboard_business_daily
		WHERE bucket_date >= $1::date AND bucket_date < $2::date
		ORDER BY bucket_date ASC
	`, startDate, endDate)
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
			&point.UpstreamCost,
			&point.UpstreamCostExcludingAdmin,
		); err != nil {
			return nil, err
		}
		result.Daily = append(result.Daily, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 先释放明细游标，实账查询可在单连接事务中安全继续。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := r.populateBusinessLedger(ctx, start, end, result); err != nil {
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
	// 滚动窗口必须直接读取明细；小时聚合可能尚未回填，混用会漏掉完整小时。
	query := `
		SELECT
			` + adminScaledTotalTokensSum("ul") + ` AS tokens,
			` + adminScaledCostSum("ul.actual_cost") + ` AS actual_cost
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN groups g ON g.id = ul.group_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`
	err := scanSingleRow(ctx, r.sql, query, []any{start, end}, &tokens, &actualCost)
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
