package repository

import (
	"fmt"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const adminUsageMultiplierSQLExpr = "COALESCE(u.admin_usage_multiplier, g.admin_usage_multiplier, 1)::double precision"

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

func adminUsageMultiplierForLog(log *service.UsageLog) float64 {
	if log == nil {
		return 1
	}
	if log.User != nil && log.User.AdminUsageMultiplier != nil {
		return *log.User.AdminUsageMultiplier
	}
	if log.Group != nil {
		return log.Group.AdminUsageMultiplier
	}
	return 1
}

func scaleUsageToken(value int, multiplier float64) int {
	return int(math.Round(float64(value) * multiplier))
}

func applyAdminUsageMultiplierToUsageLogs(logs []service.UsageLog) {
	for i := range logs {
		multiplier := adminUsageMultiplierForLog(&logs[i])
		logs[i].InputTokens = scaleUsageToken(logs[i].InputTokens, multiplier)
		logs[i].OutputTokens = scaleUsageToken(logs[i].OutputTokens, multiplier)
		logs[i].CacheCreationTokens = scaleUsageToken(logs[i].CacheCreationTokens, multiplier)
		logs[i].CacheReadTokens = scaleUsageToken(logs[i].CacheReadTokens, multiplier)
		logs[i].CacheCreation5mTokens = scaleUsageToken(logs[i].CacheCreation5mTokens, multiplier)
		logs[i].CacheCreation1hTokens = scaleUsageToken(logs[i].CacheCreation1hTokens, multiplier)
		logs[i].ImageInputTokens = scaleUsageToken(logs[i].ImageInputTokens, multiplier)
		logs[i].ImageOutputTokens = scaleUsageToken(logs[i].ImageOutputTokens, multiplier)

		logs[i].InputCost *= multiplier
		logs[i].OutputCost *= multiplier
		logs[i].CacheCreationCost *= multiplier
		logs[i].CacheReadCost *= multiplier
		logs[i].ImageInputCost *= multiplier
		logs[i].ImageOutputCost *= multiplier
		logs[i].TotalCost *= multiplier
		logs[i].ActualCost *= multiplier
		logs[i].RateMultiplier *= multiplier
		if logs[i].AccountStatsCost != nil {
			value := *logs[i].AccountStatsCost * multiplier
			logs[i].AccountStatsCost = &value
		}
	}
}
