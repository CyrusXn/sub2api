package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 使用独立的临时 PostgreSQL，绝不读取项目数据库连接配置。
// SUB2API_PASSIVE_TEST_PG_BIN=/path/to/postgres/bin go test ./internal/repository -run TestChannelMonitorPassivePostgres -v
func TestChannelMonitorPassivePostgres(t *testing.T) {
	bin := os.Getenv("SUB2API_PASSIVE_TEST_PG_BIN")
	if bin == "" {
		t.Skip("设置 SUB2API_PASSIVE_TEST_PG_BIN 后执行真实 PostgreSQL 回归")
	}
	dir, err := os.MkdirTemp("/tmp", "channel-passive-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	run := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(filepath.Join(bin, name), args...).CombinedOutput()
		require.NoError(t, err, "%s: %s", name, out)
	}
	data := filepath.Join(dir, "data")
	run("initdb", "-D", data, "-A", "trust", "-U", "postgres", "--no-locale")
	run("pg_ctl", "-D", data, "-l", filepath.Join(dir, "postgres.log"), "-o", "-k "+dir+" -c listen_addresses=''", "-w", "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "fast", "-w", "stop") })
	db, err := sql.Open("postgres", fmt.Sprintf("host=%s user=postgres dbname=postgres sslmode=disable TimeZone=UTC", dir))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	execSQL := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	execSQL(`CREATE TABLE groups (id bigint PRIMARY KEY, name text NOT NULL, platform text NOT NULL, deleted_at timestamptz);
CREATE TABLE usage_logs (id bigserial PRIMARY KEY, group_id bigint, user_id bigint DEFAULT 1, request_id text,
 created_at timestamptz, first_token_ms bigint, request_type smallint DEFAULT 2, actual_cost numeric DEFAULT 0,
 total_cost numeric DEFAULT 0, input_tokens integer DEFAULT 0, output_tokens integer DEFAULT 0,
 cache_creation_tokens integer DEFAULT 0, cache_read_tokens integer DEFAULT 0, image_count integer DEFAULT 0);
CREATE TABLE ops_error_logs (id bigserial PRIMARY KEY, group_id bigint, user_id bigint DEFAULT 1, request_id text,
 created_at timestamptz, status_code integer, error_type text DEFAULT 'upstream_error', is_count_tokens boolean DEFAULT false);`)
	migration, err := os.ReadFile("../../migrations/249_channel_monitor_passive_minutes.sql")
	require.NoError(t, err)
	execSQL(string(migration))
	// 迁移允许安全重试。
	execSQL(string(migration))
	repo := &channelMonitorV2Repository{db: db}
	end := time.Now().UTC().Truncate(time.Minute)
	minute := end.Add(-time.Minute)
	for id := 1; id <= 12; id++ {
		execSQL(`INSERT INTO groups VALUES ($1, $2, 'openai', NULL)`, id, fmt.Sprintf("group-%02d", id))
	}
	usage := func(group int, request string, at time.Time, ttft any, tokens int, requestType int) {
		execSQL(`INSERT INTO usage_logs (group_id, request_id, created_at, first_token_ms, output_tokens, request_type)
VALUES ($1, $2, $3, $4, $5, $6)`, group, request, at, ttft, tokens, requestType)
	}
	failure := func(group int, request string, at time.Time, code int) {
		execSQL(`INSERT INTO ops_error_logs (group_id, request_id, created_at, status_code) VALUES ($1, $2, $3, $4)`, group, request, at, code)
	}
	// 免费但有输出的成功请求也算成功；同分钟的失败不能盖掉快成功。
	usage(1, "fast", minute, 9999, 5, 2)
	failure(1, "failed", minute, 502)
	usage(2, "boundary", minute, 10000, 5, 2)
	failure(3, "duplicate-error", minute, 500)
	failure(3, "duplicate-error", minute.Add(time.Second), 502)
	usage(4, "no-ttft", minute, nil, 5, 2)
	usage(5, "unknown", minute, 100, 0, 2)
	usage(6, "paid-failure", minute, 100, 5, 2)
	failure(6, "paid-failure", minute, 502)
	usage(7, "recovered", minute, 100, 5, 2)
	failure(7, "recovered", minute, 200)
	// 分组 8 从未使用；9 只有 count_tokens 错误，均不生成点。
	failure(9, "count-only", minute, 500)
	execSQL(`UPDATE ops_error_logs SET is_count_tokens = true WHERE group_id = 9`)
	usage(10, "cyber", minute, 100, 0, 4)
	failure(10, "cyber", minute, 200)
	execSQL(`UPDATE ops_error_logs SET error_type = 'cyber_policy' WHERE group_id = 10`)
	usage(11, "negative", minute, -1, 5, 2)
	failure(12, "empty-placeholder", minute, 500)
	usage(12, "empty-placeholder", minute, nil, 0, 2)
	// 图片专用分组即使有记录也不展示，大小写和中文命名均生效。
	for index, name := range []string{"【GPT】image2", "DALL-E", "生图专用", "绘图"} {
		id := 13 + index
		execSQL(`INSERT INTO groups VALUES ($1, $2, 'openai', NULL)`, id, name)
		usage(id, "image-request", minute, nil, 5, 2)
	}
	require.NoError(t, repo.RefreshPassiveMinutes(ctx, end))
	require.NoError(t, repo.RefreshPassiveMinutes(ctx, end))
	filter := service.ChannelMonitorV2Filter{Start: end.Add(-7 * 24 * time.Hour), End: end}
	cards, err := repo.GetPassiveCards(ctx, filter)
	require.NoError(t, err)
	require.Len(t, cards, 10)
	require.Equal(t, int64(1), cards[0].ID, "按真实请求量降序")
	want := map[int64]string{1: "operational", 2: "degraded", 3: "error", 4: "degraded", 5: "degraded", 6: "error", 7: "operational", 10: "error", 11: "degraded", 12: "error"}
	for _, card := range cards {
		require.Equal(t, want[card.ID], card.PrimaryStatus, "group %d", card.ID)
		require.True(t, card.Passive)
		require.Len(t, card.Timeline, 1, "重复采集不重复记点")
	}
	require.Equal(t, 0.5, cards[0].SuccessRate)
	require.Equal(t, 100.0, cards[0].Availability7d)
	var count int
	require.NoError(t, db.QueryRow(`SELECT request_count FROM channel_monitor_passive_minutes WHERE group_id = 3`).Scan(&count))
	require.Equal(t, 1, count, "同一失败不能重复统计")
	filter.RestrictGroups = true
	cards, err = repo.GetPassiveCards(ctx, filter)
	require.NoError(t, err)
	require.Empty(t, cards)
	filter.AllowedGroupIDs = []int64{7, 8}
	cards, err = repo.GetPassiveCards(ctx, filter)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.Equal(t, int64(7), cards[0].ID)
	filter.Platforms = []string{"anthropic"}
	cards, err = repo.GetPassiveCards(ctx, filter)
	require.NoError(t, err)
	require.Empty(t, cards)

	// 延迟到达的计费记录与已有错误合并，只保留最终报错分钟。
	failure(8, "late", minute, 500)
	require.NoError(t, repo.RefreshPassiveMinutes(ctx, end))
	usage(8, "late", minute.Add(-time.Minute), 50, 5, 2)
	require.NoError(t, repo.RefreshPassiveMinutes(ctx, end))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channel_monitor_passive_minutes WHERE group_id = 8`).Scan(&count))
	require.Equal(t, 1, count)
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM channel_monitor_passive_minutes WHERE group_id = 8`).Scan(&status))
	require.Equal(t, "error", status)
	// 长请求在较早分钟计费后才失败，当前错误不能因旧使用记录而消失。
	usage(9, "long-failure", end.Add(-20*time.Minute), 50, 5, 2)
	failure(9, "long-failure", minute, 502)
	require.NoError(t, repo.RefreshPassiveMinutes(ctx, end))
	require.NoError(t, db.QueryRow(`SELECT status FROM channel_monitor_passive_minutes WHERE group_id = 9 AND bucket_start = $1`, minute).Scan(&status))
	require.Equal(t, "error", status)

	// 停机补采旧小时期间，最新分钟必须同步产生状态；空分钟不补点。
	execSQL(`UPDATE channel_monitor_passive_watermark SET data_through = $1`, end.Add(-3*time.Hour))
	usage(8, "latest", end, 20, 2, 2)
	require.NoError(t, repo.RefreshPassiveMinutes(ctx, end.Add(time.Minute)))
	require.NoError(t, db.QueryRow(`SELECT status FROM channel_monitor_passive_minutes WHERE group_id = 8 AND bucket_start = $1`, end).Scan(&status))
	require.Equal(t, "operational", status)

	// 最近 60 条有流量记录与三个可用率窗口，不受空分钟影响。
	execSQL(`TRUNCATE channel_monitor_passive_minutes`)
	execSQL(`INSERT INTO channel_monitor_passive_minutes
SELECT 1, $1::timestamptz - n * interval '1 minute', 1, 1, 0, 30, 'operational' FROM generate_series(1, 61) AS n`, end)
	execSQL(`INSERT INTO channel_monitor_passive_minutes VALUES
(1, $1::timestamptz - interval '8 days', 1, 0, 1, NULL, 'error'),
(1, $1::timestamptz - interval '20 days', 1, 0, 1, NULL, 'error'),
(2, $1::timestamptz - interval '8 days', 100, 1, 99, 30, 'operational')`, end)
	filter = service.ChannelMonitorV2Filter{Start: end.Add(-7 * 24 * time.Hour), End: end}
	cards, err = repo.GetPassiveCards(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), cards[0].ID)
	require.Len(t, cards[0].Timeline, 60)
	require.True(t, cards[0].Timeline[0].CheckedAt.After(cards[0].Timeline[59].CheckedAt))
	require.Equal(t, 100.0, cards[0].Availability7d)
	require.InDelta(t, 100.0*61/62, cards[0].Availability15d, 0.001)
	require.InDelta(t, 100.0*61/63, cards[0].Availability30d, 0.001)
	filter.Start = end.Add(-15 * 24 * time.Hour)
	cards, err = repo.GetPassiveCards(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(2), cards[0].ID, "排序使用所选时间窗口")
}
