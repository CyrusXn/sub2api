package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteBalanceObservationPostgres(t *testing.T) {
	dsn := os.Getenv("SUB2API_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("设置 SUB2API_TEST_POSTGRES_DSN 后使用隔离测试库验证真实 PostgreSQL 约束")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		var err error
		dsn, err = pq.ParseURL(dsn)
		require.NoError(t, err)
	}
	// 启动参数覆盖每条物理连接的默认 schema，断线重连后也不能回落到业务表。
	connector, err := pq.NewConnector(dsn + " search_path=pg_temp")
	require.NoError(t, err)
	for _, configured := range []bool{false, true} {
		name := "new_host"
		if configured {
			name = "configured_host"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			db := sql.OpenDB(connector)
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			var database string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&database))
			require.True(t, strings.HasSuffix(database, "_test") || strings.HasSuffix(database, "_preview"), "只允许隔离测试库")
			// 复用真实迁移中的约束，只操作当前连接的临时表，避免接触测试库已有业务数据。
			migration, err := os.ReadFile("../../migrations/269_upstream_site_balance_reminders.sql")
			require.NoError(t, err)
			create, _, ok := strings.Cut(string(migration), ");")
			require.True(t, ok)
			create = strings.Replace(create, "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE", 1) + ");"
			_, err = db.ExecContext(ctx, "SET search_path TO pg_temp")
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, create)
			require.NoError(t, err)
			// 自动入账也使用临时表，沿用生产迁移的字段与约束。
			_, err = db.ExecContext(ctx, `CREATE TEMP TABLE accounts (id BIGINT PRIMARY KEY);
CREATE TEMP TABLE upstream_site_credentials (id BIGINT PRIMARY KEY);`)
			require.NoError(t, err)
			schema, err := os.ReadFile("../../migrations/225_add_balance_center.sql")
			require.NoError(t, err)
			for _, table := range []string{"balance_center_sites", "balance_center_recharge_events"} {
				_, definition, found := strings.Cut(string(schema), "CREATE TABLE IF NOT EXISTS "+table+" (")
				require.True(t, found)
				definition, _, found = strings.Cut(definition, ");")
				require.True(t, found)
				_, err = db.ExecContext(ctx, "CREATE TEMP TABLE "+table+" ("+definition+");")
				require.NoError(t, err)
			}
			for _, filename := range []string{"230_add_recharge_event_site_label.sql", "238_add_balance_center_display_name_record_type.sql", "275_balance_center_recharge_review.sql"} {
				migration, err := os.ReadFile("../../migrations/" + filename)
				require.NoError(t, err)
				_, err = db.ExecContext(ctx, string(migration))
				require.NoError(t, err)
			}
			var temporary bool
			require.NoError(t, db.QueryRowContext(ctx, "SELECT relnamespace = pg_my_temp_schema() FROM pg_class WHERE oid = 'upstream_site_balance_observations'::regclass").Scan(&temporary))
			require.True(t, temporary)

			repo := NewUpstreamSiteBalanceReminderRepository(db)
			now := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
			observation := service.UpstreamSiteBalanceObservation{
				Host: "balance.example", DisplayName: "余额回归测试", BaseURL: "https://balance.example",
			}
			if configured {
				require.NoError(t, repo.EnsureUpstreamSiteBalanceObservations(ctx, []service.UpstreamSiteBalanceObservation{observation}))
				require.NoError(t, repo.DeferUpstreamSiteBalanceObservation(ctx, observation.Host, now.Add(time.Hour), "observation_save_failed"))
			}
			cases := []struct {
				name         string
				balance      float64
				currency     string
				wantIncrease bool
				version      int64
				oldBalance   float64
				delta        float64
				amount       int64
			}{
				{name: "首次采样建立基线", balance: 20, currency: "USD"},
				{name: "余额不变仍可保存", balance: 20, currency: "USD"},
				{name: "小额上涨只更新基线", balance: 20.5, currency: "USD"},
				{name: "连续小额上涨不累计触发", balance: 21, currency: "USD"},
				{name: "余额下降仍可保存", balance: 18, currency: "USD"},
				{name: "余额上涨生成提醒", balance: 25, currency: "USD", wantIncrease: true, version: 1, oldBalance: 18, delta: 7, amount: 10},
				{name: "小额上涨保留合法待处理提醒", balance: 25.5, currency: "USD", version: 1},
				{name: "连续小额上涨不混入待处理提醒", balance: 26, currency: "USD", version: 1},
				{name: "下降保留待处理提醒", balance: 24, currency: "USD", version: 1},
				// old/new 是提醒首尾实测余额，delta 只累计达标的单次上涨，不计小额变动或消费。
				{name: "再次上涨只累加合格增量", balance: 30, currency: "USD", wantIncrease: true, version: 2, oldBalance: 18, delta: 13, amount: 10},
				{name: "币种变化只重建基线", balance: 200, currency: "CNY", version: 2},
				{name: "新币种上涨替换提醒", balance: 210, currency: "CNY", wantIncrease: true, version: 3, oldBalance: 200, delta: 10, amount: 10},
				{name: "空币种默认美元", balance: 20, currency: "", version: 3},
			}
			var active *service.UpstreamSiteBalanceReminder
			for index, item := range cases {
				if !t.Run(item.name, func(t *testing.T) {
					observation.Balance, observation.Currency = item.balance, item.currency
					observation.ObservedAt = now.Add(time.Duration(index) * service.UpstreamSiteBalancePollInterval)
					reminder, err := repo.SaveUpstreamSiteBalanceObservation(ctx, observation)
					require.NoError(t, err)
					if item.wantIncrease {
						require.NotNil(t, reminder)
						require.Equal(t, item.oldBalance, reminder.OldBalance)
						require.Equal(t, item.balance, reminder.NewBalance)
						require.Equal(t, item.delta, reminder.Delta)
						require.Equal(t, item.currency, reminder.Currency)
						require.Equal(t, item.version, reminder.Version)
						if active != nil && active.Currency == reminder.Currency {
							require.Equal(t, active.DetectedAt, reminder.DetectedAt)
						} else {
							require.Equal(t, observation.ObservedAt, reminder.DetectedAt)
						}
						active = reminder
						var amount int64
						var occurredAt time.Time
						var reviewStatus string
						err = db.QueryRowContext(ctx, `SELECT amount::bigint, occurred_at, review_status
FROM balance_center_recharge_events WHERE source='upstream_auto' AND occurred_at=$1`, observation.ObservedAt).
							Scan(&amount, &occurredAt, &reviewStatus)
						require.NoError(t, err)
						require.Equal(t, item.amount, amount)
						require.True(t, occurredAt.Equal(observation.ObservedAt))
						require.Equal(t, "pending", reviewStatus)
					} else {
						require.Nil(t, reminder)
					}
					var balance float64
					var currency, lastError string
					var failures int
					var observedAt, nextProbeAt time.Time
					err = db.QueryRowContext(ctx, "SELECT balance, currency, observed_at, next_probe_at, failure_count, last_error FROM upstream_site_balance_observations WHERE host=$1", observation.Host).
						Scan(&balance, &currency, &observedAt, &nextProbeAt, &failures, &lastError)
					require.NoError(t, err)
					require.Equal(t, item.balance, balance)
					wantCurrency := item.currency
					if wantCurrency == "" {
						wantCurrency = "USD"
					}
					require.Equal(t, wantCurrency, currency)
					require.True(t, observation.ObservedAt.Equal(observedAt))
					require.True(t, observation.ObservedAt.Add(service.UpstreamSiteBalancePollInterval).Equal(nextProbeAt))
					require.Zero(t, failures)
					require.Empty(t, lastError)
					reminders, versions, err := repo.ListUpstreamSiteBalanceReminderSnapshot(ctx)
					require.NoError(t, err)
					require.Equal(t, item.version, versions[observation.Host])
					if active == nil {
						require.Empty(t, reminders)
					} else {
						require.Equal(t, []service.UpstreamSiteBalanceReminder{*active}, reminders)
					}
				}) {
					t.FailNow()
				}
			}
			version, changed, err := repo.ResolveUpstreamSiteBalanceReminder(ctx, observation.Host, observation.ObservedAt)
			require.NoError(t, err)
			require.True(t, changed)
			require.EqualValues(t, 4, version)
			reminders, err := repo.ListActiveUpstreamSiteBalanceReminders(ctx)
			require.NoError(t, err)
			require.Empty(t, reminders)
			t.Run("旧小额提醒迁移清理且不复用旧值", func(t *testing.T) {
				_, err := db.ExecContext(ctx, `
INSERT INTO upstream_site_balance_observations (
 host, display_name, base_url, balance, currency, alert_active,
 alert_old_balance, alert_new_balance, alert_delta, alert_currency, alert_detected_at, alert_version
) VALUES
 ('small.example', 'HBY', 'https://small.example', 20.5, 'USD', TRUE, 20, 20.5, 0.5, 'USD', $1, 7),
 ('valid.example', '智学', 'https://valid.example', 28.96, 'USD', TRUE, 20, 28.96, 8.96, 'USD', $1, 4),
 ('one.example', '边界', 'https://one.example', 21, 'USD', TRUE, 20, 21, 1, 'USD', $1, 1),
 ('stale.example', '已解决', 'https://stale.example', 20.5, 'USD', FALSE, 20, 20.5, 0.5, 'USD', $1, 3)`, now)
				require.NoError(t, err)
				migration, err := os.ReadFile("../../migrations/272_upstream_site_balance_minimum_increase.sql")
				require.NoError(t, err)
				_, err = db.ExecContext(ctx, string(migration))
				require.NoError(t, err)
				_, err = db.ExecContext(ctx, string(migration))
				require.NoError(t, err)
				var baseline float64
				var active bool
				var oldBalance, newBalance, delta sql.NullFloat64
				var detectedAt sql.NullTime
				var version int64
				err = db.QueryRowContext(ctx, `SELECT balance, alert_active, alert_old_balance, alert_new_balance, alert_delta, alert_detected_at, alert_version
FROM upstream_site_balance_observations WHERE host='small.example'`).Scan(&baseline, &active, &oldBalance, &newBalance, &delta, &detectedAt, &version)
				require.NoError(t, err)
				require.Equal(t, 20.5, baseline)
				require.False(t, active)
				require.False(t, oldBalance.Valid || newBalance.Valid || delta.Valid || detectedAt.Valid)
				require.EqualValues(t, 8, version)
				reminders, versions, err := repo.ListUpstreamSiteBalanceReminderSnapshot(ctx)
				require.NoError(t, err)
				require.Len(t, reminders, 2)
				require.EqualValues(t, 4, versions["valid.example"])
				require.EqualValues(t, 1, versions["one.example"])
				for _, reminder := range reminders {
					if reminder.Host == "valid.example" {
						require.InDelta(t, 8.96, reminder.Delta, 1e-8)
					}
				}
				for _, host := range []string{"small.example", "stale.example"} {
					reminder, err := repo.SaveUpstreamSiteBalanceObservation(ctx, service.UpstreamSiteBalanceObservation{
						Host: host, DisplayName: host, BaseURL: "https://" + host,
						Balance: 21.5, Currency: "USD", ObservedAt: now.Add(time.Hour),
					})
					require.NoError(t, err)
					require.NotNil(t, reminder)
					require.Equal(t, 20.5, reminder.OldBalance)
					require.Equal(t, 21.5, reminder.NewBalance)
					require.Equal(t, 1.0, reminder.Delta)
					require.True(t, now.Add(time.Hour).Equal(reminder.DetectedAt))
				}
			})
			connection, err := db.Conn(ctx)
			require.NoError(t, err)
			require.ErrorIs(t, connection.Raw(func(any) error { return driver.ErrBadConn }), driver.ErrBadConn)
			var searchPath string
			var missing bool
			require.NoError(t, db.QueryRowContext(ctx, "SELECT current_setting('search_path'), to_regclass('upstream_site_balance_observations') IS NULL").Scan(&searchPath, &missing))
			require.Equal(t, "pg_temp", searchPath)
			require.True(t, missing, "重连后临时表消失，不能命中业务表")
		})
	}
}
