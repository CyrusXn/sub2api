//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// 临时表遮蔽同名正式表，所有变更随测试事务回滚。
const businessRechargeFixtureSQL = `
CREATE TEMP TABLE users (id bigint PRIMARY KEY, email text, created_at timestamptz DEFAULT '2026-10-31 00:00:00+08');
CREATE TEMP TABLE accounts (id bigint PRIMARY KEY, rate_multiplier numeric, extra jsonb);
CREATE TEMP TABLE redeem_codes (id bigint PRIMARY KEY, used_by bigint, used_at timestamptz, status text, type text, value numeric);
CREATE TEMP TABLE usage_logs (
 id bigint PRIMARY KEY, user_id bigint, account_id bigint, created_at timestamptz,
 actual_cost numeric DEFAULT 0, total_cost numeric DEFAULT 0, account_stats_cost numeric,
 account_rate_multiplier numeric, upstream_cost_base numeric, upstream_group_rate_multiplier numeric
);
CREATE TEMP TABLE usage_dashboard_daily (
 bucket_date date PRIMARY KEY, total_requests bigint DEFAULT 0,
 input_tokens bigint DEFAULT 0, output_tokens bigint DEFAULT 0,
 cache_creation_tokens bigint DEFAULT 0, cache_read_tokens bigint DEFAULT 0,
 total_cost numeric DEFAULT 0, actual_cost numeric DEFAULT 0, account_cost numeric DEFAULT 0,
 total_duration_ms bigint DEFAULT 0, active_users bigint DEFAULT 0
);
CREATE TEMP TABLE usage_dashboard_daily_users (bucket_date date, user_id bigint, PRIMARY KEY (bucket_date, user_id));
CREATE TEMP TABLE usage_dashboard_hourly (bucket_start timestamptz PRIMARY KEY, total_requests bigint, active_users bigint);
CREATE TEMP TABLE usage_dashboard_hourly_users (bucket_start timestamptz, user_id bigint, PRIMARY KEY (bucket_start, user_id));
CREATE TEMP TABLE dashboard_business_daily (
 bucket_date date PRIMARY KEY, recharge_amount numeric DEFAULT 0,
 total_requests bigint DEFAULT 0, input_tokens bigint DEFAULT 0, output_tokens bigint DEFAULT 0,
 cache_creation_tokens bigint DEFAULT 0, cache_read_tokens bigint DEFAULT 0,
 total_cost numeric DEFAULT 0, actual_cost numeric DEFAULT 0, account_cost numeric DEFAULT 0,
 admin_actual_cost numeric DEFAULT 0, admin_account_cost numeric DEFAULT 0,
 upstream_cost numeric DEFAULT 0, admin_upstream_cost numeric DEFAULT 0,
 total_duration_ms bigint DEFAULT 0, computed_at timestamptz, finalized_at timestamptz
);
INSERT INTO users (id, email) VALUES (1, ' CYRUSXN@163.COM '), (2, 'normal@example.com'), (3, 'admin@example.com'), (4, 'cyrusxn+test@163.com');
INSERT INTO redeem_codes VALUES
 (1, 1, '2026-11-01 00:00:00+08', 'used', 'admin_balance', 1000),
 (2, 2, '2026-11-01 00:00:00+08', 'used', 'balance', 50),
 (3, 3, '2026-11-01 00:00:00+08', 'used', 'balance', 500),
 (4, 2, '2026-11-01 00:00:00+08', 'used', 'balance', 1),
 (5, 1, '2026-11-02 00:00:00+08', 'used', 'balance', 300),
 (6, 4, '2026-11-01 00:00:00+08', 'used', 'balance', 10);
INSERT INTO usage_logs (id, user_id, created_at, actual_cost) VALUES
 (1, 1, '2026-11-01 23:59:59.999999+08', 0.5),
 (2, 1, '2026-11-02 00:00:00+08', 2.5),
 (3, 2, '2026-11-01 12:00:00+08', 30);
INSERT INTO dashboard_business_daily (bucket_date, recharge_amount, total_requests, actual_cost, finalized_at) VALUES
 ('2026-09-01', 1234, 99, 99, '2026-10-01 00:00:00+08'),
 ('2026-11-01', 1060, 2, 30.5, '2026-11-03 00:00:00+08'),
 ('2026-11-02', 300, 1, 2.5, NULL),
 ('2026-11-03', 999, 0, 0, NULL);
INSERT INTO usage_dashboard_daily (bucket_date, total_requests, actual_cost) VALUES
 ('2026-11-01', 2, 30.5), ('2026-11-02', 1, 2.5);
`

const businessRechargeUnrelatedMissingHistorySQL = `
INSERT INTO dashboard_business_daily (bucket_date, recharge_amount, total_requests) VALUES ('2026-10-31', 88, 5);
INSERT INTO usage_dashboard_daily (bucket_date, total_requests, active_users) VALUES ('2026-10-31', 5, 1);
INSERT INTO usage_dashboard_daily_users VALUES ('2026-10-31', 2);
INSERT INTO usage_dashboard_hourly VALUES ('2026-10-31 10:00:00+08', 3, 1), ('2026-10-31 11:00:00+08', 2, 1);
INSERT INTO usage_dashboard_hourly_users VALUES ('2026-10-31 10:00:00+08', 2), ('2026-10-31 11:00:00+08', 2);
`

func TestBusinessRechargeMigrationUsesCompleteHistoricalActivityIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  string
		blocked bool
	}{
		{name: "其他用户缺失且完整小时索引证明测试账号未使用"},
		{name: "日活索引残缺但小时记录显示测试账号曾使用则拒绝回填", change: `INSERT INTO usage_dashboard_hourly_users VALUES ('2026-10-31 10:00:00+08', 1); UPDATE usage_dashboard_hourly SET active_users=2 WHERE bucket_start='2026-10-31 10:00:00+08'`, blocked: true},
		{name: "小时活跃索引缺失则拒绝回填", change: `DELETE FROM usage_dashboard_hourly_users WHERE bucket_start='2026-10-31 10:00:00+08'`, blocked: true},
		{name: "小时汇总请求少于固化历史则拒绝回填", change: `UPDATE usage_dashboard_hourly SET total_requests=1 WHERE bucket_start='2026-10-31 10:00:00+08'`, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tx, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.ExecContext(ctx, businessRechargeFixtureSQL+businessRechargeUnrelatedMissingHistorySQL+tc.change)
			require.NoError(t, err)
			migration, err := migrations.FS.ReadFile("266_business_test_account_recharge.sql")
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, string(migration))
			if tc.blocked {
				require.ErrorContains(t, err, "经营历史使用明细不完整")
			} else {
				require.NoError(t, err)
				var amount float64
				require.NoError(t, tx.QueryRowContext(ctx, `SELECT recharge_amount FROM dashboard_business_daily WHERE bucket_date='2026-10-31'`).Scan(&amount))
				require.Equal(t, float64(88), amount)
			}
		})
	}
}

func TestBusinessRechargeMigrationAndAggregation(t *testing.T) {
	previousTimezone := timezone.Name()
	require.NoError(t, timezone.Init("Asia/Shanghai"))
	t.Cleanup(func() { require.NoError(t, timezone.Init(previousTimezone)) })
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, businessRechargeFixtureSQL)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("266_business_test_account_recharge.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	assertRecharge := func(date string, expected float64) {
		t.Helper()
		var amount float64
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT recharge_amount FROM dashboard_business_daily WHERE bucket_date = $1::date`, date).Scan(&amount))
		require.InDelta(t, expected, amount, 1e-8)
	}
	assertRecharge("2026-11-01", 60.5)
	assertRecharge("2026-11-02", 2.5)
	assertRecharge("2026-11-03", 999)
	assertRecharge("2026-09-01", 1234)
	var originalRecharge float64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM redeem_codes WHERE id = 1`).Scan(&originalRecharge))
	require.Equal(t, float64(1000), originalRecharge)
	var finalized bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT finalized_at IS NOT NULL FROM dashboard_business_daily WHERE bucket_date = '2026-11-01'`).Scan(&finalized))
	require.True(t, finalized)

	// 普通聚合和迁移必须计算相同的人民币金额；已固化日不能被残余明细覆盖。
	_, err = tx.ExecContext(ctx, `UPDATE dashboard_business_daily SET finalized_at = NULL WHERE bucket_date >= '2026-11-01'`)
	require.NoError(t, err)
	repo := newDashboardAggregationRepositoryWithSQL(tx)
	loc := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	require.NoError(t, repo.upsertBusinessDailyAggregates(ctx, start, start.AddDate(0, 0, 2)))
	assertRecharge("2026-11-01", 60.5)
	assertRecharge("2026-11-02", 2.5)
	_, err = tx.ExecContext(ctx, `UPDATE dashboard_business_daily SET finalized_at = NOW() WHERE bucket_date = '2026-11-01'`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `DELETE FROM usage_logs WHERE id = 1`)
	require.NoError(t, err)
	require.NoError(t, repo.upsertBusinessDailyAggregates(ctx, start, start.AddDate(0, 0, 1)))
	assertRecharge("2026-11-01", 60.5)
	_, err = tx.ExecContext(ctx, string(migration))
	require.ErrorContains(t, err, "经营历史使用明细不完整")
}
