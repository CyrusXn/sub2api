package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveUpstreamCostRateMultiplier_UsesAccountProbeEffectiveRate(t *testing.T) {
	requestedAt := time.Date(2026, 8, 25, 21, 30, 0, 0, time.UTC)
	accountRate := 1.0
	account := &Account{
		RateMultiplier: &accountRate,
		Extra: map[string]any{
			UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{
				Status: UpstreamBillingProbeStatusOK,
				Data: map[string]any{
					"billing_scope":             "token",
					"resolved_rate_multiplier":  0.07,
					"peak_rate_enabled":         false,
					"effective_rate_multiplier": 0.07,
				},
			},
		},
	}

	got := resolveUpstreamCostRateMultiplier(account, requestedAt)
	require.InDelta(t, 0.07, got, 1e-12)
}

func TestResolveAdminUsageSettlementMultiplier(t *testing.T) {
	userMultiplier := 10.0

	tests := []struct {
		name  string
		user  *User
		group *Group
		want  float64
	}{
		{
			name:  "用户配置优先于分组配置",
			user:  &User{AdminUsageMultiplier: &userMultiplier},
			group: &Group{AdminUsageMultiplier: 3},
			want:  10,
		},
		{
			name:  "用户未配置时使用分组配置",
			user:  &User{},
			group: &Group{AdminUsageMultiplier: 3},
			want:  3,
		},
		{
			name: "没有分组时使用默认值一",
			user: &User{},
			want: 1,
		},
		{
			name:  "已加载的分组零倍率保持为零",
			user:  &User{},
			group: &Group{AdminUsageMultiplierConfigured: true},
			want:  0,
		},
		{
			name:  "未加载的分组零值回退默认值一",
			user:  &User{},
			group: &Group{},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.InDelta(t, tt.want, resolveAdminUsageSettlementMultiplier(tt.user, tt.group), 1e-12)
		})
	}
}

func TestResolveAdminUsageSettlementMultiplierForAccount_ComposesAccountMultiplier(t *testing.T) {
	userMultiplier := 1.2
	accountMultiplier := 1.5

	got := resolveAdminUsageSettlementMultiplierForAccount(
		&User{AdminUsageMultiplier: &userMultiplier},
		&Group{AdminUsageMultiplier: 3},
		&Account{AdminUsageMultiplier: &accountMultiplier},
	)

	require.InDelta(t, 1.8, got, 1e-12)
}

func TestApplyAdminUsageSettlementMultiplier(t *testing.T) {
	accountRate := 1.25
	log := &UsageLog{
		InputTokens:           1447,
		OutputTokens:          130,
		CacheCreationTokens:   20,
		CacheReadTokens:       30,
		CacheCreation5mTokens: 12,
		CacheCreation1hTokens: 8,
		ImageInputTokens:      4,
		ImageOutputTokens:     5,
		InputCost:             0.007235,
		OutputCost:            0.000390,
		CacheCreationCost:     0.000020,
		CacheReadCost:         0.000030,
		ImageInputCost:        0.000004,
		ImageOutputCost:       0.000005,
		TotalCost:             0.007684,
		ActualCost:            0.0007684,
		RateMultiplier:        0.10,
		AccountRateMultiplier: &accountRate,
	}
	cost := &CostBreakdown{
		InputCost:         log.InputCost,
		OutputCost:        log.OutputCost,
		CacheCreationCost: log.CacheCreationCost,
		CacheReadCost:     log.CacheReadCost,
		ImageInputCost:    log.ImageInputCost,
		ImageOutputCost:   log.ImageOutputCost,
		TotalCost:         log.TotalCost,
		ActualCost:        log.ActualCost,
		BillingMode:       string(BillingModeToken),
	}

	captureUpstreamCostSnapshot(log, log.TotalCost, log.RateMultiplier)
	applyAdminUsageSettlementMultiplier(log, cost, 10)

	require.Equal(t, 14470, log.InputTokens)
	require.Equal(t, 1300, log.OutputTokens)
	require.Equal(t, 200, log.CacheCreationTokens)
	require.Equal(t, 300, log.CacheReadTokens)
	require.Equal(t, 120, log.CacheCreation5mTokens)
	require.Equal(t, 80, log.CacheCreation1hTokens)
	require.Equal(t, 40, log.ImageInputTokens)
	require.Equal(t, 50, log.ImageOutputTokens)
	require.InDelta(t, 0.07235, log.InputCost, 1e-12)
	require.InDelta(t, 0.00390, log.OutputCost, 1e-12)
	require.InDelta(t, 0.07684, log.TotalCost, 1e-12)
	require.InDelta(t, 0.007684, log.ActualCost, 1e-12)
	require.InDelta(t, 0.10, log.RateMultiplier, 1e-12, "原始有效倍率不能被附加倍率覆盖")
	require.NotNil(t, log.UpstreamCostBase)
	require.NotNil(t, log.UpstreamGroupRateMultiplier)
	require.InDelta(t, 0.007684, *log.UpstreamCostBase, 1e-12, "上游原始消费费用必须在附加倍率前固化")
	require.InDelta(t, 0.10, *log.UpstreamGroupRateMultiplier, 1e-12, "上游分组倍率必须保留本次请求的实际倍率")
	require.InDelta(t, accountRate, *log.AccountRateMultiplier, 1e-12)
	require.InDelta(t, log.InputCost, cost.InputCost, 1e-12)
	require.InDelta(t, log.OutputCost, cost.OutputCost, 1e-12)
	require.InDelta(t, log.TotalCost, cost.TotalCost, 1e-12)
	require.InDelta(t, log.ActualCost, cost.ActualCost, 1e-12)
	require.Equal(t, string(BillingModeToken), cost.BillingMode)
}
