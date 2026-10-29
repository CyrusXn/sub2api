package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 最佳请求保留同一条记录的指标，不能把不同请求的最低延迟、最高缓存率拼在一起。
type channelMonitorV2BestRequest struct {
	BucketStart                             time.Time
	Platform, GroupName, Model              string
	GroupID                                 int64
	Success                                 bool
	TTFT, Duration                          sql.NullInt64
	Input, Output, CacheCreation, CacheRead int64
}

const channelMonitorV2MinuteWindowSize = 360

func channelMonitorV2BestRequestWindow(filter service.ChannelMonitorV2Filter, now time.Time) (service.ChannelMonitorV2Filter, service.ChannelMonitorV2MinuteWindow) {
	end := filter.End
	if current := now.UTC().Truncate(time.Minute).Add(time.Minute); current.Before(end) {
		end = current
	}
	maxOffset := max(0, int(end.Sub(filter.Start).Minutes())-channelMonitorV2MinuteWindowSize)
	offset := min(max(0, filter.MinuteOffset), maxOffset)
	window := service.ChannelMonitorV2MinuteWindow{RangeStart: filter.Start, RangeEnd: end, Offset: offset, MaxOffset: maxOffset}
	filter.End = end.Add(-time.Duration(offset) * time.Minute)
	filter.Start = maxTime(filter.Start, filter.End.Add(-channelMonitorV2MinuteWindowSize*time.Minute))
	filter.Bucket = time.Minute
	return filter, window
}

func (r channelMonitorV2BestRequest) betterThan(other channelMonitorV2BestRequest) bool {
	if r.Success != other.Success {
		return r.Success
	}
	for _, pair := range [][2]sql.NullInt64{{r.TTFT, other.TTFT}, {r.Duration, other.Duration}} {
		if pair[0].Valid != pair[1].Valid {
			return pair[0].Valid
		}
		if pair[0].Valid && pair[0].Int64 != pair[1].Int64 {
			return pair[0].Int64 < pair[1].Int64
		}
	}
	return false
}

func (r channelMonitorV2BestRequest) metric() service.ChannelMonitorV2Metric {
	a := newMetricAccumulator()
	if r.Success {
		a.success = 1
	} else {
		a.errors = 1
	}
	a.input, a.output, a.cacheCreation, a.cacheRead = r.Input, r.Output, r.CacheCreation, r.CacheRead
	if r.TTFT.Valid {
		a.ttftSum, a.ttftCount = r.TTFT.Int64, 1
		a.hist["ttft"][r.TTFT.Int64] = 1
	}
	if r.Duration.Valid {
		a.durationSum, a.durationCount = r.Duration.Int64, 1
		a.hist["duration"][r.Duration.Int64] = 1
	}
	m := a.metric(1, false)
	if r.Success && r.Duration.Valid && r.Duration.Int64 > 0 {
		tps := float64(r.Output) * 1000 / float64(r.Duration.Int64)
		m.OutputTPS = &tps
	}
	return m
}

// 时间条件直接约束原始索引列；SQL 先为每个平台/分组/模型选一分钟的一条，
// Go 再合并用户所选展示维度，避免把全部请求传回应用层。
const channelMonitorV2BestRequestsSQL = `
WITH terminal_errors AS MATERIALIZED (
  SELECT DISTINCT ON (COALESCE(group_id, 0), COALESCE(user_id, 0), COALESCE(NULLIF(request_id, ''), 'error:' || id::text))
    id, group_id, user_id, request_id, account_id, created_at, platform, requested_model, model
  FROM ops_error_logs
  WHERE created_at >= $1::timestamptz - INTERVAL '90 minutes' AND created_at < $2
    AND NOT is_count_tokens AND (status_code >= 400 OR error_type = 'cyber_policy')
  ORDER BY COALESCE(group_id, 0), COALESCE(user_id, 0), COALESCE(NULLIF(request_id, ''), 'error:' || id::text), created_at DESC, id DESC
), events AS (
  SELECT date_trunc('minute', ul.created_at) AS bucket_start,
    ` + channelMonitorV2PlatformSQL + ` AS platform, COALESCE(ul.group_id, 0) AS group_id,
    COALESCE(g.name, '') AS group_name, ` + channelMonitorV2ModelSQL + ` AS model,
    ul.id,
    (ul.actual_cost > 0 OR ul.total_cost > 0 OR ul.input_tokens > 0 OR ul.output_tokens > 0
      OR ul.cache_creation_tokens > 0 OR ul.cache_read_tokens > 0 OR ul.image_count > 0) AS succeeded,
    CASE WHEN ul.first_token_ms >= 0 THEN ul.first_token_ms END AS first_token_ms,
    CASE WHEN ul.duration_ms >= 0 THEN ul.duration_ms END AS duration_ms,
    ul.input_tokens, ul.output_tokens, ul.cache_creation_tokens, ul.cache_read_tokens
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $1 AND ul.created_at < $2 AND COALESCE(ul.request_type, 0) NOT IN (4, 6)
    AND NOT EXISTS (
      SELECT 1 FROM terminal_errors e
      WHERE e.group_id IS NOT DISTINCT FROM ul.group_id AND e.user_id IS NOT DISTINCT FROM ul.user_id
        AND e.request_id = NULLIF(ul.request_id, '')
    )
  UNION ALL
  SELECT date_trunc('minute', e.created_at),
    lower(CASE WHEN g.platform = 'composite'
      THEN COALESCE(NULLIF(TRIM(a.platform), ''), NULLIF(NULLIF(lower(TRIM(e.platform)), ''), 'composite'), 'unknown')
      ELSE COALESCE(NULLIF(TRIM(e.platform), ''), NULLIF(TRIM(g.platform), ''), 'unknown') END),
    COALESCE(e.group_id, 0), COALESCE(g.name, ''),
    COALESCE(NULLIF(TRIM(e.requested_model), ''), NULLIF(TRIM(e.model), ''), 'unknown'),
    e.id, FALSE, NULL::bigint, NULL::bigint, 0, 0, 0, 0
  FROM terminal_errors e
  LEFT JOIN groups g ON g.id = e.group_id
  LEFT JOIN accounts a ON a.id = e.account_id
  WHERE e.created_at >= $1 AND e.created_at < $2
)
SELECT DISTINCT ON (m.bucket_start, m.platform, m.group_id, m.model)
  m.bucket_start, m.platform, m.group_id, m.group_name, m.model, m.succeeded,
  m.first_token_ms, m.duration_ms, COALESCE(m.input_tokens, 0) AS input_tokens, COALESCE(m.output_tokens, 0) AS output_tokens,
  COALESCE(m.cache_creation_tokens, 0) AS cache_creation_tokens, COALESCE(m.cache_read_tokens, 0) AS cache_read_tokens
FROM events m %s
ORDER BY m.bucket_start, m.platform, m.group_id, m.model,
  m.succeeded DESC, m.first_token_ms ASC NULLS LAST, m.duration_ms ASC NULLS LAST, m.id DESC`

func (r *channelMonitorV2Repository) loadBestRequests(ctx context.Context, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, groupBy service.ChannelMonitorV2GroupBy) (map[channelMonitorV2MatrixKey][]channelMonitorV2BestRequest, error) {
	where, args := channelMonitorV2Where(filter, cfg, "m")
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(channelMonitorV2BestRequestsSQL, where), args...)
	if err != nil {
		return nil, fmt.Errorf("读取分钟最佳请求: %w", err)
	}
	defer rows.Close()
	byDimension := map[channelMonitorV2MatrixKey]map[time.Time]channelMonitorV2BestRequest{}
	for rows.Next() {
		var candidate channelMonitorV2BestRequest
		if err := rows.Scan(&candidate.BucketStart, &candidate.Platform, &candidate.GroupID, &candidate.GroupName, &candidate.Model,
			&candidate.Success, &candidate.TTFT, &candidate.Duration, &candidate.Input, &candidate.Output, &candidate.CacheCreation, &candidate.CacheRead); err != nil {
			return nil, err
		}
		if !channelMonitorV2ModelSelected(filter, cfg, candidate.Platform, candidate.Model) {
			continue
		}
		if (groupBy == service.ChannelMonitorV2GroupByPlatformGroup || groupBy == service.ChannelMonitorV2GroupByPlatformGroupModel) && candidate.GroupID <= 0 {
			continue
		}
		key := channelMonitorV2MatrixDimensionKey(groupBy, cfg, candidate.Platform, candidate.GroupID, candidate.Model)
		if byDimension[key] == nil {
			byDimension[key] = map[time.Time]channelMonitorV2BestRequest{}
		}
		candidate.BucketStart = candidate.BucketStart.UTC()
		old, found := byDimension[key][candidate.BucketStart]
		if !found || candidate.betterThan(old) {
			byDimension[key][candidate.BucketStart] = candidate
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make(map[channelMonitorV2MatrixKey][]channelMonitorV2BestRequest, len(byDimension))
	for key, byMinute := range byDimension {
		for _, candidate := range byMinute {
			result[key] = append(result[key], candidate)
		}
		sort.Slice(result[key], func(i, j int) bool { return result[key][i].BucketStart.Before(result[key][j].BucketStart) })
	}
	return result, nil
}
