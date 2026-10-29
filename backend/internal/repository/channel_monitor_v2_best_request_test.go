package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2BestRequestsMergeOnlySelectedModelsAndGroups(t *testing.T) {
	for _, groupBy := range []service.ChannelMonitorV2GroupBy{
		service.ChannelMonitorV2GroupByPlatform, service.ChannelMonitorV2GroupByPlatformGroup,
		service.ChannelMonitorV2GroupByPlatformModel, service.ChannelMonitorV2GroupByPlatformGroupModel,
	} {
		t.Run(string(groupBy), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			start := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
			filter := service.ChannelMonitorV2Filter{
				Start: start, End: start.Add(time.Minute), Models: []string{service.ChannelMonitorV2OtherModel},
				RestrictGroups: true, AllowedGroupIDs: []int64{7},
			}
			cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true, Models: []string{"named"}}}}
			columns := []string{"bucket_start", "platform", "group_id", "group_name", "model", "succeeded", "first_token_ms", "duration_ms", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens"}
			mock.ExpectQuery("WITH terminal_errors AS MATERIALIZED").WithArgs(start, filter.End, pq.Array([]string{"openai"}), pq.Array([]int64{7})).
				WillReturnRows(sqlmock.NewRows(columns).
					AddRow(start, "openai", 7, "组", "named", true, 1, 1, 1, 1, 0, 0).
					AddRow(start, "openai", 7, "组", "other-a", true, 2000, 9000, 10, 20, 0, 90).
					AddRow(start, "openai", 7, "组", "other-b", true, 1000, 9500, 30, 40, 0, 70))
			got, err := (&channelMonitorV2Repository{db: db}).loadBestRequests(context.Background(), filter, cfg, groupBy)
			require.NoError(t, err)
			require.Len(t, got, 1)
			key := channelMonitorV2MatrixDimensionKey(groupBy, cfg, "openai", 7, "other-b")
			require.Len(t, got[key], 1)
			require.Equal(t, "other-b", got[key][0].Model)
			require.EqualValues(t, 70, got[key][0].metric().CacheReadTokens)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestChannelMonitorV2MatrixKeepsFullSummaryAndScoresRawMinutes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-24 * time.Hour)
	filter := service.ChannelMonitorV2Filter{Start: start, End: end, Bucket: time.Hour}
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}
	mock.ExpectQuery("SELECT usage_coverage_start").WillReturnRows(sqlmock.NewRows([]string{"usage", "error", "through", "computed", "backfill"}).AddRow(start, start, end, end, start))
	factColumns := []string{"bucket", "platform", "group", "name", "model", "success", "error", "affected", "attempts", "input", "output", "creation", "read", "ttft_sum", "ttft_count", "duration_sum", "duration_count"}
	mock.ExpectQuery("SELECT MIN.*FROM channel_monitor_v2_metrics_rollup").WithArgs(start, end, pq.Array([]string{"openai"}), 3600).
		WillReturnRows(sqlmock.NewRows(factColumns).AddRow(start, "openai", 7, "组", "model", 200, 10, 0, 0, 100, 200, 0, 900, 200000, 200, 2000000, 200))
	mock.ExpectQuery("SELECT MIN.*FROM channel_monitor_v2_latency_histograms_rollup").
		WillReturnRows(sqlmock.NewRows([]string{"bucket", "platform", "group", "model", "user", "metric", "bound", "count"}))
	columns := []string{"bucket_start", "platform", "group_id", "group_name", "model", "succeeded", "first_token_ms", "duration_ms", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens"}
	mock.ExpectQuery("WITH terminal_errors AS MATERIALIZED").WithArgs(end.Add(-360*time.Minute), end, pq.Array([]string{"openai"})).
		WillReturnRows(sqlmock.NewRows(columns).
			AddRow(end.Add(-2*time.Minute), "openai", 7, "组", "model", true, 1234, 9000, 10, 20, 0, 90).
			AddRow(end.Add(-time.Minute), "openai", 7, "组", "model", false, nil, nil, 0, 0, 0, 0))
	got, err := (&channelMonitorV2Repository{db: db}).GetMatrix(context.Background(), filter, cfg, service.ChannelMonitorV2GroupByPlatform, true)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.EqualValues(t, 210, got.Items[0].Metrics.RequestCount, "完整 24 小时汇总不能被 6 小时分钟窗口替代")
	require.Equal(t, end.Add(-360*time.Minute), got.Coverage.RequestedStart)
	require.Equal(t, start, got.MinuteWindow.RangeStart)
	require.Equal(t, 60, got.Coverage.BucketSeconds)
	buckets := got.Items[0].Buckets
	require.Len(t, buckets, 2)
	require.Equal(t, "healthy", buckets[0].Health.Overall)
	require.EqualValues(t, 1, buckets[0].Health.MinimumSample)
	require.EqualValues(t, 1234, *buckets[0].Metrics.TTFT.P50Ms)
	require.Equal(t, "critical", buckets[1].Health.Overall)
	require.True(t, buckets[1].Health.HasTraffic)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorV2BestRequestWindowIsBoundedAndBrowsable(t *testing.T) {
	end := time.Date(2026, 10, 27, 8, 30, 0, 0, time.UTC)
	for _, minutes := range []int{90, 24 * 60, 30 * 24 * 60} {
		filter := service.ChannelMonitorV2Filter{Start: end.Add(-time.Duration(minutes) * time.Minute), End: end, MinuteOffset: 99999}
		got, window := channelMonitorV2BestRequestWindow(filter, end)
		require.Equal(t, time.Minute, got.Bucket)
		require.LessOrEqual(t, got.End.Sub(got.Start), 360*time.Minute)
		require.Equal(t, filter.Start, got.Start, "最早窗口可到达完整时间范围起点")
		require.Equal(t, max(0, minutes-360), window.Offset)
	}
}

func TestChannelMonitorV2BestRequestKeepsOneRealRequest(t *testing.T) {
	fast := channelMonitorV2BestRequest{
		Success: true, TTFT: sql.NullInt64{Int64: 1234, Valid: true},
		Duration: sql.NullInt64{Int64: 9000, Valid: true},
		Input:    10, Output: 20, CacheRead: 90,
	}
	slow := fast
	slow.TTFT.Int64, slow.Duration.Int64, slow.CacheRead = 5000, 2000, 999
	require.True(t, fast.betterThan(slow))
	require.False(t, slow.betterThan(fast))
	failed := fast
	failed.Success, failed.TTFT.Int64 = false, 1
	require.True(t, slow.betterThan(failed), "成功优先，不能选最快的失败")
	missing := fast
	missing.TTFT = sql.NullInt64{}
	require.True(t, fast.betterThan(missing))
	tied := fast
	tied.Duration.Int64 = 10000
	require.True(t, fast.betterThan(tied))
	m := fast.metric()
	require.EqualValues(t, 1, m.RequestCount)
	require.EqualValues(t, 1, m.SuccessRequests)
	require.EqualValues(t, 1234, *m.TTFT.P50Ms)
	thresholds := service.DefaultChannelMonitorV2HealthThresholds()
	require.Equal(t, "healthy", service.ChannelMonitorV2BestRequestHealth(m, thresholds).Overall)
	slow.TTFT.Int64 = 10001
	require.EqualValues(t, 10001, *slow.metric().TTFT.P50Ms, "首字直接取使用记录，不做桶上界量化")
	require.Equal(t, "warning", service.ChannelMonitorV2BestRequestHealth(slow.metric(), thresholds).Overall)
	require.EqualValues(t, 9000, *m.Duration.P50Ms)
	require.InDelta(t, 0.9, m.CacheRate, 0.0001)
	require.EqualValues(t, 120, m.TokenCount)
	require.NotNil(t, m.OutputTPS)
	require.InDelta(t, 20.0/9, *m.OutputTPS, 0.0001)
	require.Nil(t, failed.metric().OutputTPS)
	require.EqualValues(t, 1, failed.metric().ErrorRequests)
}
