package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLegacyLowRateStrictOrder(t *testing.T) {
	for _, path := range []struct {
		name      string
		loadBatch bool
		loadErr   error
		wait      bool
		sticky    bool
	}{
		{name: "load_batch", loadBatch: true, sticky: true},
		{name: "without_load_batch", sticky: true},
		{name: "load_lookup_failure", loadBatch: true, loadErr: errors.New("load unavailable"), sticky: true},
		{name: "fallback_wait", loadBatch: true, wait: true, sticky: true},
		{name: "first_load_batch", loadBatch: true},
		{name: "first_without_load_batch"},
		{name: "first_load_lookup_failure", loadBatch: true, loadErr: errors.New("load unavailable")},
		{name: "first_fallback_wait", loadBatch: true, wait: true},
	} {
		for _, rule := range []string{"rate", "priority", "last_used", "failover"} {
			t.Run(path.name+"/"+rule, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				ctx := context.Background()
				now := time.Now()
				older, newer := now.Add(-time.Hour), now.Add(-time.Minute)
				cooldownUntil := now.Add(time.Hour)
				accounts := []Account{
					{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateMultiplier: floatPtr(0.3), LastUsedAt: &older},
					{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, RateMultiplier: floatPtr(0.3), LastUsedAt: &newer},
					{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, RateMultiplier: floatPtr(0.1), TempUnschedulableUntil: &cooldownUntil},
					{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, RateMultiplier: floatPtr(0.1), Credentials: map[string]any{"model_mapping": map[string]any{"other-model": "other-model"}}},
				}
				var excluded map[int64]struct{}
				switch rule {
				case "rate":
					accounts[1].Priority = 0
					accounts[1].RateMultiplier = floatPtr(0.8)
				case "last_used":
					accounts[1].Priority = accounts[0].Priority
				case "failover":
					accounts = append(accounts, Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, RateMultiplier: floatPtr(0.2)})
					excluded = map[int64]struct{}{5: {}}
				}
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = path.loadBatch
				loads := map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 10}, 2: {AccountID: 2, LoadRate: 10}}
				acquire := map[int64]bool{}
				if path.wait {
					loads[1].LoadRate, loads[2].LoadRate = 100, 100
					acquire[1], acquire[2] = false, false
				}
				svc := &OpenAIGatewayService{
					accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
					cache:       &schedulerTestGatewayCache{},
					cfg:         cfg,
					rateLimitService: &RateLimitService{settingService: NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
						openAIAdvancedSchedulerSettingKey: "false", SettingKeyOpenAILowUpstreamRatePriorityEnabled: "true",
					}}, cfg)},
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: loads, loadBatchErr: path.loadErr, acquireResults: acquire}),
				}
				sessionHash := ""
				if path.sticky {
					sessionHash = "strict-low-rate"
					require.NoError(t, svc.setStickySessionAccountID(ctx, nil, sessionHash, 2, time.Hour))
				}
				selection, _, err := svc.SelectAccountWithScheduler(ctx, nil, "", sessionHash, "gpt-test", excluded, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				if selection.ReleaseFunc != nil {
					defer selection.ReleaseFunc()
				}
				require.Equal(t, int64(1), selection.Account.ID)
				if path.wait {
					require.NotNil(t, selection.WaitPlan)
					require.Equal(t, int64(1), selection.WaitPlan.AccountID)
				}
			})
		}
	}
}

func TestLegacyLowRateStrictOrderUsesLoadBeforeLastUsed(t *testing.T) {
	for _, wait := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquire", true: "fallback_wait"}[wait], func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			ctx := context.Background()
			older := time.Now().Add(-time.Hour)
			lowLoad, highLoad := 10, 90
			if wait {
				lowLoad, highLoad = 100, 150
			}
			accounts := []Account{
				{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateMultiplier: floatPtr(0.3), LastUsedAt: &older},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateMultiplier: floatPtr(0.3)},
			}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = true
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:       &schedulerTestGatewayCache{},
				cfg:         cfg,
				rateLimitService: &RateLimitService{settingService: NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
					openAIAdvancedSchedulerSettingKey: "false", SettingKeyOpenAILowUpstreamRatePriorityEnabled: "true",
				}}, cfg)},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{
					1: {AccountID: 1, LoadRate: lowLoad}, 2: {AccountID: 2, LoadRate: highLoad},
				}}),
			}
			require.NoError(t, svc.setStickySessionAccountID(ctx, nil, "strict-low-rate", 2, time.Hour))
			selection, _, err := svc.SelectAccountWithScheduler(ctx, nil, "", "strict-low-rate", "gpt-test", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			if selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			require.Equal(t, int64(1), selection.Account.ID)
			if wait {
				require.NotNil(t, selection.WaitPlan)
				require.Equal(t, int64(1), selection.WaitPlan.AccountID)
			}
		})
	}
}

type lowRateRefreshingConcurrencyCache struct {
	schedulerTestConcurrencyCache
	refreshedLoad map[int64]*AccountLoadInfo
	loadCalls     int
}

func (c *lowRateRefreshingConcurrencyCache) GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.loadCalls++
	if c.loadCalls > 1 {
		return c.refreshedLoad, nil
	}
	return c.schedulerTestConcurrencyCache.GetAccountsLoadBatch(ctx, accounts)
}

func TestLegacyLowRateStrictOrderWaitUsesRefreshedLoad(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	older := time.Now().Add(-time.Hour)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateMultiplier: floatPtr(0.3)},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateMultiplier: floatPtr(0.3), LastUsedAt: &older},
	}
	cache := &lowRateRefreshingConcurrencyCache{
		schedulerTestConcurrencyCache: schedulerTestConcurrencyCache{
			loadMap:        map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 10}, 2: {AccountID: 2, LoadRate: 90}},
			acquireResults: map[int64]bool{1: false, 2: false},
		},
		refreshedLoad: map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 150}, 2: {AccountID: 2, LoadRate: 100}},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cfg:         cfg,
		rateLimitService: &RateLimitService{settingService: NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
			openAIAdvancedSchedulerSettingKey: "false", SettingKeyOpenAILowUpstreamRatePriorityEnabled: "true",
		}}, cfg)},
		concurrencyService: NewConcurrencyService(cache),
	}
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), nil, "", "", "gpt-test", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, 2, cache.loadCalls)
	require.Equal(t, int64(2), selection.Account.ID, "等待排序须使用抢槽失败后刷新的负载快照")
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(2), selection.WaitPlan.AccountID)
}
