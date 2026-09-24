package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// 按请求去重，并以终态错误覆盖已计费但最终失败的请求；只输出聚合数值。
const channelMonitorPassiveAggregateSQL = `
WITH terminal_errors AS MATERIALIZED (
  SELECT DISTINCT ON (group_id, COALESCE(user_id, 0), COALESCE(NULLIF(request_id, ''), 'error:' || id::text))
    group_id, user_id, request_id, created_at
  FROM ops_error_logs
  WHERE created_at >= $1::timestamptz - INTERVAL '90 minutes' AND created_at < $2
    AND group_id IS NOT NULL AND NOT is_count_tokens
    AND (status_code >= 400 OR error_type = 'cyber_policy')
  ORDER BY group_id, COALESCE(user_id, 0), COALESCE(NULLIF(request_id, ''), 'error:' || id::text), created_at DESC, id DESC
), usage_events AS MATERIALIZED (
  SELECT ul.group_id, ul.user_id, ul.request_id, ul.created_at, ul.first_token_ms,
    FALSE AS failed,
    (COALESCE(ul.request_type, 0) NOT IN (4, 6)
      AND (ul.actual_cost > 0 OR ul.total_cost > 0 OR ul.input_tokens > 0 OR ul.output_tokens > 0
        OR ul.cache_creation_tokens > 0 OR ul.cache_read_tokens > 0 OR ul.image_count > 0)) AS succeeded
  FROM usage_logs ul
  LEFT JOIN terminal_errors e ON e.group_id = ul.group_id AND e.user_id = ul.user_id
    AND e.request_id = NULLIF(ul.request_id, '')
  WHERE ul.created_at >= $1 AND ul.created_at < $2 AND ul.group_id IS NOT NULL
    AND e.request_id IS NULL
), events AS (
  SELECT group_id, created_at, first_token_ms, failed, succeeded FROM usage_events
  UNION ALL
  SELECT e.group_id, e.created_at, NULL::bigint, TRUE, FALSE
  FROM terminal_errors e
  WHERE e.created_at >= $1 AND e.created_at < $2
), minutes AS (
  SELECT group_id, date_trunc('minute', created_at) AS bucket_start,
    COUNT(*) AS requests, COUNT(*) FILTER (WHERE succeeded) AS successes,
    COUNT(*) FILTER (WHERE failed) AS errors,
    MIN(first_token_ms) FILTER (WHERE succeeded AND first_token_ms >= 0) AS first_token_ms
  FROM events GROUP BY 1, 2
)
INSERT INTO channel_monitor_passive_minutes
  (group_id, bucket_start, request_count, success_count, error_count, first_token_ms, status)
SELECT m.group_id, m.bucket_start, m.requests, m.successes, m.errors, m.first_token_ms,
  CASE WHEN m.successes > 0 AND m.first_token_ms < 10000 THEN 'operational'
       WHEN m.errors = m.requests THEN 'error'
       ELSE 'degraded' END
FROM minutes m JOIN groups g ON g.id = m.group_id
ON CONFLICT (group_id, bucket_start) DO UPDATE SET
  request_count = EXCLUDED.request_count, success_count = EXCLUDED.success_count,
  error_count = EXCLUDED.error_count, first_token_ms = EXCLUDED.first_token_ms, status = EXCLUDED.status`

func (r *channelMonitorV2Repository) RefreshPassiveMinutes(ctx context.Context, end time.Time) (err error) {
	end = end.UTC().Truncate(time.Minute)
	latestEnd := end
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 多实例只允许一个采集者；唯一键和水位保证重试不会重复记点。
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(24920260921)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	var through time.Time
	err = tx.QueryRowContext(ctx, `SELECT data_through FROM channel_monitor_passive_watermark WHERE id = 1`).Scan(&through)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	start := end.Add(-time.Hour)
	if !through.IsZero() {
		start = through.Add(-5 * time.Minute)
	}
	if start.Before(end.Add(-30 * 24 * time.Hour)) {
		start = end.Add(-30 * 24 * time.Hour)
	}
	// 长时间停机后的补采按小时推进，避免一次扫描整月原始日志。
	if end.Sub(start) > time.Hour {
		end = start.Add(time.Hour)
	}
	if !start.Before(end) {
		return nil
	}
	// 延迟写入可能改变请求所属分钟，重算窗口先清除旧点，避免留下已不存在的错误点。
	if _, err = tx.ExecContext(ctx, `DELETE FROM channel_monitor_passive_minutes WHERE bucket_start >= $1 AND bucket_start < $2`, start, end); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, channelMonitorPassiveAggregateSQL, start, end); err != nil {
		return err
	}
	// 补采旧数据时仍刷新最新窗口，避免当前流量被历史追赶阻塞。
	if end.Before(latestEnd) {
		latestStart := latestEnd.Add(-5 * time.Minute)
		if latestStart.Before(end) {
			latestStart = end
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM channel_monitor_passive_minutes WHERE bucket_start >= $1 AND bucket_start < $2`, latestStart, latestEnd); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, channelMonitorPassiveAggregateSQL, latestStart, latestEnd); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_passive_watermark (id, data_through) VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET data_through = GREATEST(channel_monitor_passive_watermark.data_through, EXCLUDED.data_through)`, end); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM channel_monitor_passive_minutes WHERE bucket_start < $1`, latestEnd.Add(-30*24*time.Hour)); err != nil {
		return err
	}
	return tx.Commit()
}

const channelMonitorPassiveCardsSQL = `
WITH ranked AS (
  SELECT m.*, g.name, g.platform,
    ROW_NUMBER() OVER (PARTITION BY m.group_id ORDER BY m.bucket_start DESC) AS position
  FROM channel_monitor_passive_minutes m JOIN groups g ON g.id = m.group_id
  WHERE m.bucket_start >= $1::timestamptz - INTERVAL '30 days' AND m.bucket_start < $1
    AND g.deleted_at IS NULL
    -- 渠道状态只公开非专属分组，管理员和已授权用户也遵守此展示范围。
    AND NOT g.is_exclusive
    -- 隐藏名称明确标注图片生成的专用分组，不因普通分组允许生图而将其隐藏。
    AND g.name !~* '(image|dall-e|生图|绘图)'
    AND (NOT $2 OR m.group_id = ANY($3))
    AND (cardinality($4::text[]) = 0 OR g.platform = ANY($4))
)
SELECT group_id, name, platform,
  MAX(status) FILTER (WHERE position = 1), MAX(first_token_ms) FILTER (WHERE position = 1),
  MAX(success_count::float8 / request_count) FILTER (WHERE position = 1),
  COALESCE(100.0 * COUNT(*) FILTER (WHERE bucket_start >= $1 - INTERVAL '7 days' AND success_count > 0)
    / NULLIF(COUNT(*) FILTER (WHERE bucket_start >= $1 - INTERVAL '7 days'), 0), 0),
  COALESCE(100.0 * COUNT(*) FILTER (WHERE bucket_start >= $1 - INTERVAL '15 days' AND success_count > 0)
    / NULLIF(COUNT(*) FILTER (WHERE bucket_start >= $1 - INTERVAL '15 days'), 0), 0),
  100.0 * COUNT(*) FILTER (WHERE success_count > 0) / COUNT(*),
  jsonb_agg(jsonb_build_object('status', status, 'latency_ms', first_token_ms,
    'checked_at', bucket_start) ORDER BY bucket_start DESC) FILTER (WHERE position <= 60)
FROM ranked GROUP BY group_id, name, platform
ORDER BY COALESCE(SUM(request_count) FILTER (WHERE bucket_start >= $5), 0) DESC, name, group_id`

func (r *channelMonitorV2Repository) GetPassiveCards(ctx context.Context, filter service.ChannelMonitorV2Filter) ([]service.ChannelMonitorPassiveCard, error) {
	items := make([]service.ChannelMonitorPassiveCard, 0)
	if filter.RestrictGroups && len(filter.AllowedGroupIDs) == 0 {
		return items, nil
	}
	// 空数组使用显式空 slice，避免 PostgreSQL cardinality(NULL) 导致全部平台被过滤。
	platforms := append([]string{}, filter.Platforms...)
	groups := append([]int64{}, filter.AllowedGroupIDs...)
	rows, err := r.db.QueryContext(ctx, channelMonitorPassiveCardsSQL, filter.End,
		filter.RestrictGroups, pq.Array(groups), pq.Array(platforms), filter.Start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var card service.ChannelMonitorPassiveCard
		var timeline []byte
		if err := rows.Scan(&card.ID, &card.Name, &card.Provider, &card.PrimaryStatus, &card.PrimaryLatencyMs,
			&card.SuccessRate, &card.Availability7d, &card.Availability15d, &card.Availability30d, &timeline); err != nil {
			return nil, err
		}
		card.Passive = true
		if err := json.Unmarshal(timeline, &card.Timeline); err != nil {
			return nil, err
		}
		items = append(items, card)
	}
	return items, rows.Err()
}
