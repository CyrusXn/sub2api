//go:build unit

package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIIdentityRouting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		links    map[int64]any
		inactive []int64
		foreign  []int64
		want     []int64
	}{
		{name: "目标优先且不回原账号", links: map[int64]any{1: float64(2)}, want: []int64{2, 4, 3}},
		{name: "目标不可用按倍率与优先级回退", links: map[int64]any{1: 2}, inactive: []int64{2}, want: []int64{4, 3}},
		{name: "链式配置", links: map[int64]any{1: 2, 2: 3}, want: []int64{3, 4}},
		{name: "双向环和重复引用", links: map[int64]any{1: 2, 2: 1, 4: 2}, want: []int64{3}},
		{name: "自引用", links: map[int64]any{1: 1, 4: 4}, want: []int64{3, 2}},
		{name: "不存在目标", links: map[int64]any{1: 999}, want: []int64{4, 3, 2}},
		{name: "目标跨组不能使用", links: map[int64]any{1: 2}, foreign: []int64{2}, want: []int64{4, 3}},
		{name: "全部成环后停止", links: map[int64]any{1: 2, 2: 1, 3: 4, 4: 3}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(9)
			accounts := make([]Account, 4)
			for i := range accounts {
				id := int64(i + 1)
				rate := []float64{0.1, 0.9, 0.5, 0.5}[i]
				accounts[i] = Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1,
					Priority: []int{0, 1, 8, 2}[i], RateMultiplier: &rate, GroupIDs: []int64{groupID}, Extra: map[string]any{}}
				if link, ok := tc.links[id]; ok {
					accounts[i].Extra["identity_account_id"] = link
				}
			}
			for _, id := range tc.inactive {
				accounts[id-1].Status = StatusDisabled
			}
			for _, id := range tc.foreign {
				accounts[id-1].GroupIDs = []int64{99}
			}
			svc := newLegacySchedulerDecisionTestService(accounts, false, schedulerTestConcurrencyCache{})
			ctx := WithOpenAIIdentityRouting(context.Background(), true)
			failed := map[int64]struct{}{}
			var got []int64
			exhausted := false
			for i := 0; i < 6; i++ {
				selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-test", failed,
					OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true)
				if err != nil {
					require.ErrorIs(t, err, ErrNoAvailableAccounts)
					exhausted = true
					break
				}
				require.NotNil(t, selection)
				got = append(got, selection.Account.ID)
				failed[selection.Account.ID] = struct{}{}
				releaseLegacySchedulerDecisionSelection(selection)
			}
			require.Equal(t, tc.want, got)
			require.True(t, exhausted, "应在有限次尝试后耗尽")
			require.True(t, IsOpenAIIdentityRoutingActive(ctx))
		})
	}
}

func TestOpenAIIdentityRoutingUnconfiguredAndOrdinary(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "普通请求不切换", true: "未配置不切换"}[enabled], func(t *testing.T) {
			groupID := int64(9)
			accounts := newLegacySchedulerDecisionTestAccounts(groupID, true)
			if !enabled {
				accounts[0].Extra["identity_account_id"] = accounts[1].ID
			}
			svc := newLegacySchedulerDecisionTestService(accounts, false, schedulerTestConcurrencyCache{})
			ctx := WithOpenAIIdentityRouting(context.Background(), enabled)
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-test", nil,
				OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true)
			require.NoError(t, err)
			require.Equal(t, accounts[0].ID, selection.Account.ID)
			require.False(t, IsOpenAIIdentityRoutingActive(ctx))
			releaseLegacySchedulerDecisionSelection(selection)
		})
	}
}

func TestOpenAIIdentityRoutingBusyAndSticky(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(9)
	accounts := newLegacySchedulerDecisionTestAccounts(groupID, true)
	accounts[0].Extra[accountIdentityAccountIDKey] = accounts[1].ID
	third := accounts[1]
	third.ID = 38103
	third.Priority = 10
	third.Extra = map[string]any{}
	accounts = append(accounts, third)
	var acquired, released []int64
	svc := newLegacySchedulerDecisionTestService(accounts, false, schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{38102: false}, acquiredIDs: &acquired, releasedIDs: &released,
	})
	require.NoError(t, svc.setStickySessionAccountID(context.Background(), &groupID, "ordinary-session", accounts[0].ID, time.Hour))
	ctx := WithOpenAIIdentityRouting(context.Background(), true)
	selection, decision, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "ordinary-session", "gpt-test", nil,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true)
	require.NoError(t, err)
	require.Equal(t, third.ID, selection.Account.ID, "身份目标并发满时应跳过")
	require.Equal(t, "identity_account", decision.Layer)
	releaseLegacySchedulerDecisionSelection(selection)
	require.Equal(t, []int64{38101, 38102, 38103}, acquired)
	require.ElementsMatch(t, []int64{38101, 38103}, released)
	boundID, err := svc.getStickySessionAccountID(context.Background(), &groupID, "ordinary-session")
	require.NoError(t, err)
	require.Equal(t, accounts[0].ID, boundID, "后续普通对话仍绑定原账号")
}

func TestIdentityAccountIDInvalidValues(t *testing.T) {
	for _, value := range []any{nil, true, -1, 0, 1.5, math.Inf(1), math.NaN(), "invalid", "1.5", "9223372036854775808"} {
		require.Zero(t, identityAccountID(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{accountIdentityAccountIDKey: value}}))
	}
}
