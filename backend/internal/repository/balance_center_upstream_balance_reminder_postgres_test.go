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
			}{
				{name: "首次采样建立基线", balance: 20, currency: "USD"},
				{name: "余额不变仍可保存", balance: 20, currency: "USD"},
				{name: "余额下降仍可保存", balance: 18, currency: "USD"},
				{name: "余额上涨生成提醒", balance: 25, currency: "USD", wantIncrease: true, version: 1, oldBalance: 18, delta: 7},
				{name: "下降保留待处理提醒", balance: 24, currency: "USD", version: 1},
				{name: "再次上涨累加提醒", balance: 30, currency: "USD", wantIncrease: true, version: 2, oldBalance: 18, delta: 12},
				{name: "币种变化只重建基线", balance: 200, currency: "CNY", version: 2},
				{name: "新币种上涨替换提醒", balance: 210, currency: "CNY", wantIncrease: true, version: 3, oldBalance: 200, delta: 10},
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
