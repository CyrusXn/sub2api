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
