package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

// TrendDataPoint represents a single point in trend data
type TrendDataPoint = usagestats.TrendDataPoint

// ModelStat represents usage statistics for a single model
type ModelStat = usagestats.ModelStat

// UserUsageTrendPoint represents user usage trend data point
type UserUsageTrendPoint = usagestats.UserUsageTrendPoint

// UserSpendingRankingItem represents a user spending ranking row.
type UserSpendingRankingItem = usagestats.UserSpendingRankingItem
type UserSpendingRankingResponse = usagestats.UserSpendingRankingResponse

// APIKeyUsageTrendPoint represents API key usage trend data point
type APIKeyUsageTrendPoint = usagestats.APIKeyUsageTrendPoint

// GetAPIKeyUsageTrend returns usage trend data grouped by API key and date
func (r *usageLogRepository) GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) (results []APIKeyUsageTrendPoint, err error) {
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		WITH top_keys AS (
			SELECT ul.api_key_id
			FROM usage_logs ul
			LEFT JOIN users u ON u.id = ul.user_id
			LEFT JOIN groups g ON g.id = ul.group_id
			WHERE ul.created_at >= $1 AND ul.created_at < $2
			GROUP BY ul.api_key_id
			ORDER BY %s DESC
			LIMIT $3
		)
		SELECT
			TO_CHAR(ul.created_at, '%s') as date,
			ul.api_key_id,
			COALESCE(k.name, '') as key_name,
			COUNT(*) as requests,
			%s as tokens
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN api_keys k ON ul.api_key_id = k.id
		WHERE ul.api_key_id IN (SELECT api_key_id FROM top_keys)
		  AND ul.created_at >= $4 AND ul.created_at < $5
		GROUP BY date, ul.api_key_id, k.name
		ORDER BY date ASC, tokens DESC
	`, adminScaledTotalTokensSum("ul"), dateFormat, adminScaledTotalTokensSum("ul"))

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]APIKeyUsageTrendPoint, 0)
	for rows.Next() {
		var row APIKeyUsageTrendPoint
		if err = rows.Scan(&row.Date, &row.APIKeyID, &row.KeyName, &row.Requests, &row.Tokens); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// GetUserUsageTrend returns usage trend data grouped by user and date
func (r *usageLogRepository) GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) (results []UserUsageTrendPoint, err error) {
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		WITH top_users AS (
			SELECT ul.user_id
			FROM usage_logs ul
			LEFT JOIN users u ON u.id = ul.user_id
			LEFT JOIN groups g ON g.id = ul.group_id
			WHERE ul.created_at >= $1 AND ul.created_at < $2
			GROUP BY ul.user_id
			ORDER BY %s DESC
			LIMIT $3
		)
		SELECT
			TO_CHAR(ul.created_at, '%s') as date,
			ul.user_id,
			COALESCE(u.email, '') as email,
			COALESCE(u.username, '') as username,
			COUNT(*) as requests,
			%s as tokens,
			%s as cost,
			%s as actual_cost
		FROM usage_logs ul
		LEFT JOIN users u ON ul.user_id = u.id
		LEFT JOIN groups g ON g.id = ul.group_id
		WHERE ul.user_id IN (SELECT user_id FROM top_users)
		  AND ul.created_at >= $4 AND ul.created_at < $5
		GROUP BY date, ul.user_id, u.email, u.username
		ORDER BY date ASC, tokens DESC
	`, adminScaledTotalTokensSum("ul"), dateFormat, adminScaledTotalTokensSum("ul"), adminScaledCostSum("ul.total_cost"), adminScaledCostSum("ul.actual_cost"))

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]UserUsageTrendPoint, 0)
	for rows.Next() {
		var row UserUsageTrendPoint
		if err = rows.Scan(&row.Date, &row.UserID, &row.Email, &row.Username, &row.Requests, &row.Tokens, &row.Cost, &row.ActualCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// GetUserSpendingRanking returns user spending ranking aggregated within the time range.
func (r *usageLogRepository) GetUserSpendingRanking(ctx context.Context, startTime, endTime time.Time, limit int) (result *UserSpendingRankingResponse, err error) {
	if limit <= 0 {
		limit = 12
	}

	query := `
		WITH user_spend AS (
			SELECT
				ul.user_id,
				COALESCE(u.email, '') as email,
				COALESCE(u.username, '') as username,
				` + adminScaledCostSum("ul.actual_cost") + ` as actual_cost,
				COUNT(*) as requests,
				` + adminScaledTotalTokensSum("ul") + ` as tokens
			FROM usage_logs ul
			LEFT JOIN users u ON ul.user_id = u.id
			LEFT JOIN groups g ON g.id = ul.group_id
			WHERE ul.created_at >= $1 AND ul.created_at < $2
			GROUP BY ul.user_id, u.email, u.username
		),
		ranked AS (
			SELECT
				user_id,
				email,
				username,
				actual_cost,
				requests,
				tokens,
				COALESCE(SUM(actual_cost) OVER (), 0) as total_actual_cost,
				COALESCE(SUM(requests) OVER (), 0) as total_requests,
				COALESCE(SUM(tokens) OVER (), 0) as total_tokens
			FROM user_spend
			ORDER BY actual_cost DESC, tokens DESC, user_id ASC
			LIMIT $3
		)
		SELECT
			user_id,
			email,
			username,
			actual_cost,
			requests,
			tokens,
			total_actual_cost,
			total_requests,
			total_tokens
		FROM ranked
		ORDER BY actual_cost DESC, tokens DESC, user_id ASC
	`

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			result = nil
		}
	}()

	ranking := make([]UserSpendingRankingItem, 0)
	totalActualCost := 0.0
	totalRequests := int64(0)
	totalTokens := int64(0)
	for rows.Next() {
		var row UserSpendingRankingItem
		if err = rows.Scan(&row.UserID, &row.Email, &row.Username, &row.ActualCost, &row.Requests, &row.Tokens, &totalActualCost, &totalRequests, &totalTokens); err != nil {
			return nil, err
		}
		ranking = append(ranking, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return &UserSpendingRankingResponse{
		Ranking:         ranking,
		TotalActualCost: totalActualCost,
		TotalRequests:   totalRequests,
		TotalTokens:     totalTokens,
	}, nil
}

// GetUserUsageTrendByUserID 获取指定用户的使用趋势
func (r *usageLogRepository) GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) (results []TrendDataPoint, err error) {
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		SELECT
			TO_CHAR(created_at, '%s') as date,
			COUNT(*) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(total_cost), 0) as cost,
			COALESCE(SUM(actual_cost), 0) as actual_cost
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY date
		ORDER BY date ASC
	`, dateFormat)

	rows, err := r.sql.QueryContext(ctx, query, userID, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetUserModelStats 获取指定用户的模型统计
func (r *usageLogRepository) GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, 0, 0, 0, "", nil, nil, nil, usagestats.ModelSourceRequested, "", false, nil)
}

// GetUsageTrendWithFilters returns usage trend data with optional filters
func (r *usageLogRepository) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) (results []TrendDataPoint, err error) {
	return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, userID, apiKeyID, accountID, groupID, model, "", requestType, stream, billingType, "", false, nil)
}

func (r *usageLogRepository) GetUsageTrendWithUsageFilters(ctx context.Context, startTime, endTime time.Time, granularity string, filters UsageLogFilters) (results []TrendDataPoint, err error) {
	return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.ModelFilterSource, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.AdminView, filters.UpstreamModelMismatch, &filters)
}

func (r *usageLogRepository) getUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, modelSource string, requestType *int16, stream *bool, billingType *int8, billingMode string, adminView bool, upstreamModelMismatch *bool, sharedFilters ...*UsageLogFilters) (results []TrendDataPoint, err error) {
	if !adminView && shouldUsePreaggregatedTrend(granularity, userID, apiKeyID, accountID, groupID, model, requestType, stream, billingType, billingMode, upstreamModelMismatch) {
		aggregated, aggregatedErr := r.getUsageTrendFromAggregates(ctx, startTime, endTime, granularity)
		if aggregatedErr == nil && len(aggregated) > 0 {
			return aggregated, nil
		}
	}

	dateFormat := safeDateFormat(granularity)
	alias := ""
	fromClause := "usage_logs"
	dateColumn := "created_at"
	inputTokensExpr := "COALESCE(SUM(input_tokens), 0)"
	outputTokensExpr := "COALESCE(SUM(output_tokens), 0)"
	cacheCreationTokensExpr := "COALESCE(SUM(cache_creation_tokens), 0)"
	cacheReadTokensExpr := "COALESCE(SUM(cache_read_tokens), 0)"
	totalTokensExpr := "COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0)"
	costExpr := "COALESCE(SUM(total_cost), 0)"
	actualCostExpr := "COALESCE(SUM(actual_cost), 0)"
	if adminView {
		alias = "ul"
		fromClause = "usage_logs ul LEFT JOIN users u ON u.id = ul.user_id LEFT JOIN groups g ON g.id = ul.group_id"
		dateColumn = "ul.created_at"
		inputTokensExpr = adminScaledTokenSum("ul.input_tokens")
		outputTokensExpr = adminScaledTokenSum("ul.output_tokens")
		cacheCreationTokensExpr = adminScaledTokenSum("ul.cache_creation_tokens")
		cacheReadTokensExpr = adminScaledTokenSum("ul.cache_read_tokens")
		totalTokensExpr = adminScaledTotalTokensSum("ul")
		costExpr = adminScaledCostSum("ul.total_cost")
		actualCostExpr = adminScaledCostSum("ul.actual_cost")
	}

	query := fmt.Sprintf(`
		SELECT
			TO_CHAR(%s, '%s') as date,
			COUNT(*) as requests,
			%s as input_tokens,
			%s as output_tokens,
			%s as cache_creation_tokens,
			%s as cache_read_tokens,
			%s as total_tokens,
			%s as cost,
			%s as actual_cost
		FROM %s
		WHERE %s >= $1 AND %s < $2
	`, dateColumn, dateFormat, inputTokensExpr, outputTokensExpr, cacheCreationTokensExpr, cacheReadTokensExpr, totalTokensExpr, costExpr, actualCostExpr, fromClause, dateColumn, dateColumn)

	args := []any{startTime, endTime}
	if userID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "user_id"), len(args)+1)
		args = append(args, userID)
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "api_key_id"), len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "account_id"), len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "group_id"), len(args)+1)
		args = append(args, groupID)
	}
	if len(sharedFilters) > 0 && sharedFilters[0] != nil {
		query, args = appendUsageLogSharedListQuery(query, args, *sharedFilters[0], alias)
	}
	query, args = appendUsageLogModelQueryFilterWithAlias(query, args, model, modelSource, alias)
	query, args = appendRequestTypeOrStreamQueryFilterWithAlias(query, args, requestType, stream, alias)
	if billingType != nil {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "billing_type"), len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, alias)
	if upstreamModelMismatch != nil {
		query += " AND " + upstreamModelMismatchCondition(usageLogColumn(alias, "upstream_model_mismatch"), *upstreamModelMismatch)
	}
	query += " GROUP BY date ORDER BY date ASC"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func shouldUsePreaggregatedTrend(granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, billingMode string, upstreamModelMismatch *bool) bool {
	if granularity != "day" && granularity != "hour" {
		return false
	}
	return userID == 0 &&
		apiKeyID == 0 &&
		accountID == 0 &&
		groupID == 0 &&
		model == "" &&
		requestType == nil &&
		stream == nil &&
		billingType == nil &&
		billingMode == "" &&
		upstreamModelMismatch == nil
}

func (r *usageLogRepository) getUsageTrendFromAggregates(ctx context.Context, startTime, endTime time.Time, granularity string) (results []TrendDataPoint, err error) {
	dateFormat := safeDateFormat(granularity)
	query := ""
	args := []any{startTime, endTime}

	switch granularity {
	case "hour":
		query = fmt.Sprintf(`
			SELECT
				TO_CHAR(bucket_start, '%s') as date,
				total_requests as requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) as total_tokens,
				total_cost as cost,
				actual_cost
			FROM usage_dashboard_hourly
			WHERE bucket_start >= $1 AND bucket_start < $2
			ORDER BY bucket_start ASC
		`, dateFormat)
	case "day":
		query = fmt.Sprintf(`
			SELECT
				TO_CHAR(bucket_date::timestamp, '%s') as date,
				total_requests as requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) as total_tokens,
				total_cost as cost,
				actual_cost
			FROM usage_dashboard_daily
			WHERE bucket_date >= $1::date AND bucket_date < $2::date
			ORDER BY bucket_date ASC
		`, dateFormat)
	default:
		return nil, nil
	}

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetModelStatsWithFilters returns model statistics with optional filters
func (r *usageLogRepository) GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, "", requestType, stream, billingType, usagestats.ModelSourceRequested, "", false, nil)
}

// GetModelStatsWithFiltersBySource returns model statistics with optional filters and model source dimension.
// source: requested | upstream | mapping.
func (r *usageLogRepository) GetModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8, source string) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, "", requestType, stream, billingType, source, "", false, nil)
}

func (r *usageLogRepository) GetModelStatsWithUsageFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters, source string) (results []ModelStat, err error) {
	return r.getModelStatsWithUsageFiltersBySource(ctx, startTime, endTime, filters, source)
}

func (r *usageLogRepository) getModelStatsWithUsageFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters, source string) (results []ModelStat, err error) {
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, source, filters.BillingMode, filters.AdminView, filters.UpstreamModelMismatch, &filters)
}

func (r *usageLogRepository) getModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, source string, billingMode string, adminView bool, upstreamModelMismatch *bool, sharedFilters ...*UsageLogFilters) (results []ModelStat, err error) {
	alias := ""
	fromClause := "usage_logs"
	createdAtColumn := "created_at"
	actualCostExpr := "COALESCE(SUM(actual_cost), 0) as actual_cost"
	// 当仅按 account_id 聚合时，实际费用使用账号倍率（total_cost * account_rate_multiplier）。
	if accountID > 0 && userID == 0 && apiKeyID == 0 {
		actualCostExpr = "COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) as actual_cost"
	}
	accountCostExpr := "COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) as account_cost"
	modelExpr := resolveModelDimensionExpression(source)
	inputTokensExpr := "COALESCE(SUM(input_tokens), 0)"
	outputTokensExpr := "COALESCE(SUM(output_tokens), 0)"
	cacheCreationTokensExpr := "COALESCE(SUM(cache_creation_tokens), 0)"
	cacheReadTokensExpr := "COALESCE(SUM(cache_read_tokens), 0)"
	totalTokensExpr := "COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0)"
	costExpr := "COALESCE(SUM(total_cost), 0)"
	if adminView {
		alias = "ul"
		fromClause = "usage_logs ul LEFT JOIN users u ON u.id = ul.user_id LEFT JOIN groups g ON g.id = ul.group_id"
		createdAtColumn = "ul.created_at"
		modelExpr = resolveModelDimensionExpressionWithAlias(source, alias)
		inputTokensExpr = adminScaledTokenSum("ul.input_tokens")
		outputTokensExpr = adminScaledTokenSum("ul.output_tokens")
		cacheCreationTokensExpr = adminScaledTokenSum("ul.cache_creation_tokens")
		cacheReadTokensExpr = adminScaledTokenSum("ul.cache_read_tokens")
		totalTokensExpr = adminScaledTotalTokensSum("ul")
		costExpr = adminScaledCostSum("ul.total_cost")
		actualCostExpr = adminScaledCostSum("ul.actual_cost") + " as actual_cost"
		if accountID > 0 && userID == 0 && apiKeyID == 0 {
			actualCostExpr = fmt.Sprintf("COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1) * %s), 0) as actual_cost", adminUsageMultiplierSQLExpr)
		}
		accountCostExpr = fmt.Sprintf("COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1) * %s), 0) as account_cost", adminUsageMultiplierSQLExpr)
	}

	query := fmt.Sprintf(`
		SELECT
			%s as model,
			COUNT(*) as requests,
			%s as input_tokens,
			%s as output_tokens,
			%s as cache_creation_tokens,
			%s as cache_read_tokens,
			%s as total_tokens,
			%s as cost,
			%s,
			%s
		FROM %s
		WHERE %s >= $1 AND %s < $2
	`, modelExpr, inputTokensExpr, outputTokensExpr, cacheCreationTokensExpr, cacheReadTokensExpr, totalTokensExpr, costExpr, actualCostExpr, accountCostExpr, fromClause, createdAtColumn, createdAtColumn)

	args := []any{startTime, endTime}
	if userID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "user_id"), len(args)+1)
		args = append(args, userID)
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "api_key_id"), len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "account_id"), len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "group_id"), len(args)+1)
		args = append(args, groupID)
	}
	if len(sharedFilters) > 0 && sharedFilters[0] != nil {
		query, args = appendUsageLogSharedListQuery(query, args, *sharedFilters[0], alias)
	}
	if strings.TrimSpace(model) != "" {
		query += fmt.Sprintf(" AND %s = $%d", modelExpr, len(args)+1)
		args = append(args, model)
	}
	query, args = appendRequestTypeOrStreamQueryFilterWithAlias(query, args, requestType, stream, alias)
	if billingType != nil {
		query += fmt.Sprintf(" AND %s = $%d", usageLogColumn(alias, "billing_type"), len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, alias)
	if upstreamModelMismatch != nil {
		query += " AND " + upstreamModelMismatchCondition(usageLogColumn(alias, "upstream_model_mismatch"), *upstreamModelMismatch)
	}
	query += fmt.Sprintf(" GROUP BY %s ORDER BY total_tokens DESC", modelExpr)

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanModelStatsRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetGroupStatsWithFilters returns group usage statistics with optional filters
func (r *usageLogRepository) GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) (results []usagestats.GroupStat, err error) {
	return r.getGroupStatsWithFilters(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, "", requestType, stream, billingType, "", false, nil)
}

func (r *usageLogRepository) GetGroupStatsWithUsageFilters(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters) (results []usagestats.GroupStat, err error) {
	return r.getGroupStatsWithFilters(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.AdminView, filters.UpstreamModelMismatch, &filters)
}

func (r *usageLogRepository) getGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, billingMode string, adminView bool, upstreamModelMismatch *bool, sharedFilters ...*UsageLogFilters) (results []usagestats.GroupStat, err error) {
	totalTokensExpr := "COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0)"
	costExpr := "COALESCE(SUM(ul.total_cost), 0)"
	actualCostExpr := "COALESCE(SUM(ul.actual_cost), 0)"
	accountCostExpr := "COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1)), 0)"
	userJoin := ""
	if adminView {
		totalTokensExpr = adminScaledTotalTokensSum("ul")
		costExpr = adminScaledCostSum("ul.total_cost")
		actualCostExpr = adminScaledCostSum("ul.actual_cost")
		accountCostExpr = fmt.Sprintf("COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1) * %s), 0)", adminUsageMultiplierSQLExpr)
		userJoin = "LEFT JOIN users u ON u.id = ul.user_id"
	}
	query := fmt.Sprintf(`
		SELECT
			COALESCE(ul.group_id, 0) as group_id,
			COALESCE(g.name, '') as group_name,
			COUNT(*) as requests,
			%s as total_tokens,
			%s as cost,
			%s as actual_cost,
			%s as account_cost
		FROM usage_logs ul
		LEFT JOIN groups g ON g.id = ul.group_id
		%s
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`, totalTokensExpr, costExpr, actualCostExpr, accountCostExpr, userJoin)

	args := []any{startTime, endTime}
	if userID > 0 {
		query += fmt.Sprintf(" AND ul.user_id = $%d", len(args)+1)
		args = append(args, userID)
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND ul.account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND ul.group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	if len(sharedFilters) > 0 && sharedFilters[0] != nil {
		query, args = appendUsageLogSharedListQuery(query, args, *sharedFilters[0], "ul")
	}
	if strings.TrimSpace(model) != "" {
		modelExpr := resolveModelDimensionExpressionWithAlias(usagestats.ModelSourceRequested, "ul")
		query += fmt.Sprintf(" AND %s = $%d", modelExpr, len(args)+1)
		args = append(args, model)
	}
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	if billingType != nil {
		query += fmt.Sprintf(" AND ul.billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "ul")
	if upstreamModelMismatch != nil {
		query += " AND " + upstreamModelMismatchCondition("ul.upstream_model_mismatch", *upstreamModelMismatch)
	}
	query += " GROUP BY ul.group_id, g.name ORDER BY total_tokens DESC"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usagestats.GroupStat, 0)
	for rows.Next() {
		var row usagestats.GroupStat
		if err := rows.Scan(
			&row.GroupID,
			&row.GroupName,
			&row.Requests,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetUserBreakdownStats returns per-user usage breakdown within a specific dimension.
func (r *usageLogRepository) GetUserBreakdownStats(ctx context.Context, startTime, endTime time.Time, dim usagestats.UserBreakdownDimension, limit int) (results []usagestats.UserBreakdownItem, err error) {
	query := `
		SELECT
			COALESCE(ul.user_id, 0) as user_id,
			COALESCE(u.email, '') as email,
			COUNT(*) as requests,
			` + adminScaledTokenSum("ul.input_tokens") + ` as input_tokens,
			` + adminScaledTokenSum("ul.output_tokens") + ` as output_tokens,
			(` + adminScaledTokenSum("ul.cache_creation_tokens") + ` + ` + adminScaledTokenSum("ul.cache_read_tokens") + `) as cache_tokens,
			` + adminScaledTotalTokensSum("ul") + ` as total_tokens,
			` + adminScaledCostSum("ul.total_cost") + ` as cost,
			` + adminScaledCostSum("ul.actual_cost") + ` as actual_cost,
			COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1) * ` + adminUsageMultiplierSQLExpr + `), 0) as account_cost
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN groups g ON g.id = ul.group_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`
	args := []any{startTime, endTime}

	if dim.GroupID > 0 {
		query += fmt.Sprintf(" AND ul.group_id = $%d", len(args)+1)
		args = append(args, dim.GroupID)
	}
	if len(dim.GroupIDs) > 0 {
		query += fmt.Sprintf(" AND ul.group_id = ANY($%d)", len(args)+1)
		args = append(args, pq.Array(dim.GroupIDs))
	}
	if dim.Model != "" {
		query += fmt.Sprintf(" AND %s = $%d", resolveModelDimensionExpressionWithAlias(dim.ModelType, "ul"), len(args)+1)
		args = append(args, dim.Model)
	}
	if len(dim.Models) > 0 {
		query += fmt.Sprintf(" AND %s = ANY($%d)", resolveModelDimensionExpressionWithAlias(dim.ModelType, "ul"), len(args)+1)
		args = append(args, pq.Array(dim.Models))
	}
	if dim.Endpoint != "" {
		col := resolveEndpointColumn(dim.EndpointType)
		query += fmt.Sprintf(" AND %s = $%d", col, len(args)+1)
		args = append(args, dim.Endpoint)
	}
	if dim.UserID > 0 {
		query += fmt.Sprintf(" AND ul.user_id = $%d", len(args)+1)
		args = append(args, dim.UserID)
	}
	if len(dim.UserIDs) > 0 {
		query += fmt.Sprintf(" AND ul.user_id = ANY($%d)", len(args)+1)
		args = append(args, pq.Array(dim.UserIDs))
	}
	if dim.APIKeyID > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = $%d", len(args)+1)
		args = append(args, dim.APIKeyID)
	}
	if len(dim.APIKeyIDs) > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = ANY($%d)", len(args)+1)
		args = append(args, pq.Array(dim.APIKeyIDs))
	}
	if dim.AccountID > 0 {
		query += fmt.Sprintf(" AND ul.account_id = $%d", len(args)+1)
		args = append(args, dim.AccountID)
	}
	if len(dim.AccountIDs) > 0 {
		query += fmt.Sprintf(" AND ul.account_id = ANY($%d)", len(args)+1)
		args = append(args, pq.Array(dim.AccountIDs))
	}
	if dim.RequestType != nil {
		condition, conditionArgs := buildRequestTypeFilterConditionWithAlias(len(args)+1, *dim.RequestType, "ul")
		query += " AND " + condition
		args = append(args, conditionArgs...)
	}
	if len(dim.RequestTypes) > 0 {
		conditions := make([]string, 0, len(dim.RequestTypes))
		for _, requestType := range dim.RequestTypes {
			condition, conditionArgs := buildRequestTypeFilterConditionWithAlias(len(args)+1, requestType, "ul")
			conditions = append(conditions, condition)
			args = append(args, conditionArgs...)
		}
		query += " AND (" + strings.Join(conditions, " OR ") + ")"
	}
	if dim.Stream != nil {
		query += fmt.Sprintf(" AND ul.stream = $%d", len(args)+1)
		args = append(args, *dim.Stream)
	}
	if dim.BillingType != nil {
		query += fmt.Sprintf(" AND ul.billing_type = $%d", len(args)+1)
		args = append(args, *dim.BillingType)
	}
	if len(dim.BillingTypes) > 0 {
		query += fmt.Sprintf(" AND ul.billing_type = ANY($%d)", len(args)+1)
		args = append(args, pq.Array(dim.BillingTypes))
	}

	// ORDER BY 列来自固定 allowlist(非用户原样字符串),避免 SQL 注入。
	orderBy := "actual_cost"
	switch dim.SortBy {
	case "total_tokens", "input_tokens", "output_tokens", "cache_tokens", "requests", "cost", "actual_cost":
		orderBy = dim.SortBy
	}
	query += " GROUP BY ul.user_id, u.email ORDER BY " + orderBy + " DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usagestats.UserBreakdownItem, 0)
	for rows.Next() {
		var row usagestats.UserBreakdownItem
		if err := rows.Scan(
			&row.UserID,
			&row.Email,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetAllGroupUsageSummary 返回所有分组在服务端配置时区内的今日、昨日与当前保留记录累计金额。
func (r *usageLogRepository) GetAllGroupUsageSummary(ctx context.Context, todayStart time.Time) ([]usagestats.GroupUsageSummary, error) {
	return r.getAllGroupUsageSummaryFromRollups(ctx, todayStart)
}

// resolveModelDimensionExpression maps model source type to a safe SQL expression.
func resolveModelDimensionExpression(modelType string) string {
	return resolveModelDimensionExpressionWithAlias(modelType, "")
}

func resolveModelDimensionExpressionWithAlias(modelType, alias string) string {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	requestedExpr := fmt.Sprintf("COALESCE(NULLIF(TRIM(%s), ''), %s)", column("requested_model"), column("model"))
	upstreamExpr := fmt.Sprintf("COALESCE(NULLIF(TRIM(%s), ''), %s)", column("upstream_model"), column("model"))
	switch usagestats.NormalizeModelSource(modelType) {
	case usagestats.ModelSourceUpstream:
		return upstreamExpr
	case usagestats.ModelSourceMapping:
		return fmt.Sprintf("(%s || ' -> ' || %s)", requestedExpr, upstreamExpr)
	default:
		return requestedExpr
	}
}

func scanTrendRows(rows *sql.Rows) ([]TrendDataPoint, error) {
	results := make([]TrendDataPoint, 0)
	for rows.Next() {
		var row TrendDataPoint
		if err := rows.Scan(
			&row.Date,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func scanModelStatsRows(rows *sql.Rows) ([]ModelStat, error) {
	results := make([]ModelStat, 0)
	for rows.Next() {
		var row ModelStat
		if err := rows.Scan(
			&row.Model,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
