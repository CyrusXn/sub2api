package service

import "math"

// resolveAdminUsageSettlementMultiplier 解析仅由管理端配置的结算附加倍率。
// 用户配置优先；用户未配置时继承分组；缺少分组时回退为 1。
func ResolveAdminUsageSettlementMultiplier(user *User, group *Group) float64 {
	if user != nil && user.AdminUsageMultiplier != nil {
		return *user.AdminUsageMultiplier
	}
	if group != nil {
		if group.AdminUsageMultiplier == 0 && !group.AdminUsageMultiplierConfigured {
			return 1
		}
		return group.AdminUsageMultiplier
	}
	return 1
}

func resolveAdminUsageSettlementMultiplier(user *User, group *Group) float64 {
	return ResolveAdminUsageSettlementMultiplier(user, group)
}

// ResolveAdminUsageSettlementMultiplierForAccount 在用户/分组附加倍率基础上叠加账号附加倍率。
// 账号倍率只在实际选中该账号后生效，确保用户余额、用量明细和 token 快照使用同一结果。
func ResolveAdminUsageSettlementMultiplierForAccount(user *User, group *Group, account *Account) float64 {
	return ResolveAdminUsageSettlementMultiplier(user, group) * account.AdminUsageRateMultiplier()
}

func resolveAdminUsageSettlementMultiplierForAccount(user *User, group *Group, account *Account) float64 {
	return ResolveAdminUsageSettlementMultiplierForAccount(user, group, account)
}

func scaleSettlementToken(value int, multiplier float64) int {
	return int(math.Round(float64(value) * multiplier))
}

// applyAdminUsageSettlementMultiplier 将附加倍率固化到本次请求的计量和结算结果。
// RateMultiplier、AccountRateMultiplier、BillingMode 等原始计费元数据保持不变。
func applyAdminUsageSettlementMultiplier(log *UsageLog, cost *CostBreakdown, multiplier float64) {
	if log == nil || cost == nil || multiplier == 1 {
		return
	}

	log.InputTokens = scaleSettlementToken(log.InputTokens, multiplier)
	log.OutputTokens = scaleSettlementToken(log.OutputTokens, multiplier)
	log.CacheCreationTokens = scaleSettlementToken(log.CacheCreationTokens, multiplier)
	log.CacheReadTokens = scaleSettlementToken(log.CacheReadTokens, multiplier)
	log.CacheCreation5mTokens = scaleSettlementToken(log.CacheCreation5mTokens, multiplier)
	log.CacheCreation1hTokens = scaleSettlementToken(log.CacheCreation1hTokens, multiplier)
	log.ImageInputTokens = scaleSettlementToken(log.ImageInputTokens, multiplier)
	log.ImageOutputTokens = scaleSettlementToken(log.ImageOutputTokens, multiplier)

	cost.InputCost *= multiplier
	cost.OutputCost *= multiplier
	cost.CacheCreationCost *= multiplier
	cost.CacheReadCost *= multiplier
	cost.ImageInputCost *= multiplier
	cost.ImageOutputCost *= multiplier
	cost.TotalCost *= multiplier
	cost.ActualCost *= multiplier

	log.InputCost = cost.InputCost
	log.OutputCost = cost.OutputCost
	log.CacheCreationCost = cost.CacheCreationCost
	log.CacheReadCost = cost.CacheReadCost
	log.ImageInputCost = cost.ImageInputCost
	log.ImageOutputCost = cost.ImageOutputCost
	log.TotalCost = cost.TotalCost
	log.ActualCost = cost.ActualCost
}
