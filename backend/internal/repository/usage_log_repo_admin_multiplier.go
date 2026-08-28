package repository

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 管理端和用户端都直接汇总请求结算时已经固化的用量；保留该恒等表达式，
// 让现有管理统计查询结构无需分叉，同时禁止按当前配置追溯重算历史记录。
const adminUsageMultiplierSQLExpr = "1::double precision"

func usageLogColumn(alias, name string) string {
	if alias == "" {
		return name
	}
	return alias + "." + name
}

// upstreamCostRateMultiplierSQLExpr 从账号保存的上游探测快照按请求发生时刻计算倍率。
// 本站用户分组倍率、专属倍率和管理附加倍率均不属于上游实际消耗，不能参与该计算。
// 临时探测失败时沿用快照保留的最近成功声明值；没有可用声明值时才回退账号静态倍率。
func upstreamCostRateMultiplierSQLExpr(logAlias, accountAlias string) string {
	logCreatedAt := usageLogColumn(logAlias, "created_at")
	snapshot := accountAlias + ".extra -> 'upstream_billing_probe'"
	data := "(" + snapshot + " -> 'data')"

	return fmt.Sprintf(`
		COALESCE(
			CASE
				WHEN %[1]s ->> 'status' = 'failed'
					AND jsonb_typeof(%[1]s -> 'manual_rate_multiplier') = 'number'
				THEN (%[1]s ->> 'manual_rate_multiplier')::numeric
				WHEN %[1]s ->> 'status' IN ('ok', 'failed')
					AND %[2]s ->> 'billing_scope' = 'token'
					AND jsonb_typeof(%[2]s -> 'resolved_rate_multiplier') = 'number'
				THEN (%[2]s ->> 'resolved_rate_multiplier')::numeric * CASE
					WHEN %[2]s ->> 'peak_rate_enabled' = 'false' THEN 1
					WHEN %[2]s ->> 'peak_rate_enabled' = 'true'
						AND jsonb_typeof(%[2]s -> 'peak_rate_multiplier') = 'number'
						AND NULLIF(%[2]s ->> 'peak_start', '') IS NOT NULL
						AND NULLIF(%[2]s ->> 'peak_end', '') IS NOT NULL
						AND NULLIF(%[2]s ->> 'timezone', '') IS NOT NULL
						AND (%[3]s AT TIME ZONE (%[2]s ->> 'timezone'))::time >= (%[2]s ->> 'peak_start')::time
						AND (%[3]s AT TIME ZONE (%[2]s ->> 'timezone'))::time < (%[2]s ->> 'peak_end')::time
						THEN (%[2]s ->> 'peak_rate_multiplier')::numeric
					WHEN %[2]s ->> 'peak_rate_enabled' = 'true' THEN 1
					ELSE NULL
				END
			END,
			COALESCE(%[4]s.rate_multiplier, 1)
		)`, snapshot, data, logCreatedAt, accountAlias)
}

func upstreamCostSQLExpr(logAlias, accountAlias string) string {
	return fmt.Sprintf(
		"COALESCE(%[1]s, %[2]s) * COALESCE(%[3]s, %[4]s)",
		usageLogColumn(logAlias, "upstream_cost_base"),
		usageLogColumn(logAlias, "total_cost"),
		usageLogColumn(logAlias, "upstream_group_rate_multiplier"),
		upstreamCostRateMultiplierSQLExpr(logAlias, accountAlias),
	)
}

func adminScaledTokenSum(column string) string {
	return fmt.Sprintf("COALESCE(SUM(ROUND(%s * %s)::bigint), 0)", column, adminUsageMultiplierSQLExpr)
}

func adminScaledCostSum(column string) string {
	return fmt.Sprintf("COALESCE(SUM(%s * %s), 0)", column, adminUsageMultiplierSQLExpr)
}

func adminScaledTotalTokensSum(alias string) string {
	return fmt.Sprintf(
		"COALESCE(SUM(ROUND(%[1]s.input_tokens * %[2]s)::bigint + ROUND(%[1]s.output_tokens * %[2]s)::bigint + ROUND(%[1]s.cache_creation_tokens * %[2]s)::bigint + ROUND(%[1]s.cache_read_tokens * %[2]s)::bigint), 0)",
		alias,
		adminUsageMultiplierSQLExpr,
	)
}

// applyAdminUsageMultiplierToUsageLogs 保留为查询兼容入口；倍率已经在请求结算时固化，
// 此处必须保持恒等，避免管理端对新记录二次乘算或修改历史展示。
func applyAdminUsageMultiplierToUsageLogs(logs []service.UsageLog) {
	_ = logs
}
