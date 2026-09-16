package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func fishSubscriptionAccount(t *testing.T, now time.Time) *Account {
	t.Helper()
	cash := 0.15
	a := &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		RateMultiplier: &cash, Credentials: map[string]any{"api_key": "sk-fish-test", "base_url": "https://sub.anzhiyu.com/v1"},
		Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true}}
	require.NoError(t, configureSubscriptionRate(a, &UpstreamSubscriptionRateConfig{Enabled: true, SubscriptionID: 944, Price: 808, QuotaUSD: 30000, GroupMultiplier: 4}))
	c := subscriptionRateConfig(a)
	remaining := 50.0
	a.Extra[UpstreamBillingProbeExtraKey] = &UpstreamBillingProbeSnapshot{
		Status: UpstreamBillingProbeStatusOK, ReceivedAt: &now, FreshUntil: probeTimePtr(now.Add(2 * time.Minute)),
		Data: map[string]any{"resolved_rate_multiplier": cash, "effective_rate_multiplier": cash, "peak_rate_enabled": false},
		Subscription: &UpstreamSubscriptionRateObservation{KeyFingerprint: c.KeyFingerprint, SubscriptionID: c.SubscriptionID, ConfigFingerprint: subscriptionRateConfigFingerprint(c),
			RemainingUSD: &remaining, ObservedAt: now, FreshUntil: now.Add(2 * time.Minute), ExpiresAt: now.Add(time.Hour)},
	}
	return a
}

func TestFishSubscriptionRateSharedByBillingCostAndScheduling(t *testing.T) {
	now := time.Now().Add(-time.Second)
	a := fishSubscriptionAccount(t, now)
	want := 808.0 / 30000 * 4
	require.InDelta(t, 0.10773333333, want, 1e-10)
	rate, ok := a.SubscriptionRateMultiplier(now)
	require.True(t, ok)
	require.Equal(t, want, rate)
	require.Equal(t, want, a.BillingRateMultiplier())
	require.Equal(t, want, resolveUpstreamCostRateMultiplier(a, now))
	rate, ok = openAIFreshUpstreamBillingRate(a, now)
	require.True(t, ok)
	require.Equal(t, want, rate)
	require.Equal(t, 0.15, a.BaseRateMultiplier(), "现金倍率必须保留供回退")
	// 现金分组倍率变化不改变与此 Key 绑定的订阅规则。
	cash := 0.8
	a.RateMultiplier = &cash
	require.Equal(t, want, a.BillingRateMultiplier())
}

func TestFishSubscriptionRateRejectsUncertainOrMismatchedState(t *testing.T) {
	now := time.Now().Add(-time.Second)
	for _, scenario := range []string{"关闭", "更换Key", "更换站点", "更换套餐参数", "额度耗尽", "未知额度", "订阅过期", "快照过期", "观察失败", "未来快照", "关闭探测"} {
		t.Run(scenario, func(t *testing.T) {
			a := fishSubscriptionAccount(t, now)
			c := *subscriptionRateConfig(a)
			snapshot := decodeUpstreamBillingProbeSnapshot(a.Extra)
			switch scenario {
			case "关闭":
				c.Enabled = false
			case "更换Key":
				a.Credentials["api_key"] = "sk-other-test"
			case "更换站点":
				a.Credentials["base_url"] = "https://other.example/v1"
			case "更换套餐参数":
				c.GroupMultiplier = 5
			case "额度耗尽":
				snapshot.Subscription.RemainingUSD = float64Ptr(0)
			case "未知额度":
				snapshot.Subscription.RemainingUSD = nil
			case "订阅过期":
				snapshot.Subscription.ExpiresAt = now
			case "快照过期":
				snapshot.Subscription.FreshUntil = now
			case "观察失败":
				snapshot.Subscription.Error = "失败"
			case "未来快照":
				snapshot.Subscription.ObservedAt = now.Add(time.Hour)
			case "关闭探测":
				a.Extra[UpstreamBillingProbeEnabledExtraKey] = false
			}
			a.Extra[upstreamSubscriptionRateKey] = c
			a.Extra[UpstreamBillingProbeExtraKey] = snapshot
			_, ok := a.SubscriptionRateMultiplier(now)
			require.False(t, ok)
			require.Equal(t, 0.15, resolveUpstreamCostRateMultiplier(a, now))
		})
	}
}

type fishSubscriptionHTTP struct{ webAccountRateHTTPStub }

func (u *fishSubscriptionHTTP) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	if req.URL.Path == "/api/v1/subscriptions" {
		return jsonResponse(http.StatusOK, `{"code":0,"data":[{"id":944,"status":"active","expires_at":"2026-09-28T16:27:44+08:00","daily_window_start":"2026-09-16T00:00:00Z","daily_usage_usd":950,"group":{"daily_limit_usd":1000}}]}`), nil
	}
	return u.webAccountRateHTTPStub.Do(req, proxy, id, concurrency)
}
func (u *fishSubscriptionHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestFishSubscriptionProbePersistsIndependentObservation(t *testing.T) {
	now := time.Date(2026, 9, 16, 2, 0, 0, 0, time.UTC)
	a := fishSubscriptionAccount(t, now)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}
	svc := newUpstreamBillingProbeTestService(repo, &fishSubscriptionHTTP{}, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "sub.anzhiyu.com")
	snapshot := &UpstreamBillingProbeSnapshot{Status: UpstreamBillingProbeStatusOK, LastAttemptAt: now, Data: map[string]any{"resolved_rate_multiplier": 0.15}}
	require.NoError(t, svc.updateSnapshot(context.Background(), a, snapshot, nil))
	require.NotNil(t, snapshot.Subscription)
	require.Empty(t, snapshot.Subscription.Error)
	require.Equal(t, 50.0, *snapshot.Subscription.RemainingUSD)
	require.Equal(t, now.Add(time.Minute), snapshot.NextProbeAt)
	require.Equal(t, 0.15, snapshot.Data["resolved_rate_multiplier"])
	rate, ok := repo.accounts[a.ID].SubscriptionRateMultiplier(now)
	require.True(t, ok)
	require.InDelta(t, 808.0/30000*4, rate, 1e-12)
}

func TestUpdateFishSubscriptionRequiresExplicitConfigAndNewKeyReconfirmation(t *testing.T) {
	now := time.Now()
	a := fishSubscriptionAccount(t, now)
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), a.ID, &UpdateAccountInput{Extra: map[string]any{upstreamSubscriptionRateKey: map[string]any{"enabled": false}}})
	require.NoError(t, err)
	require.NotNil(t, subscriptionRateConfig(updated), "普通 extra 更新不应修改专用配置")
	config := *subscriptionRateConfig(updated)
	_, err = svc.UpdateAccount(context.Background(), a.ID, &UpdateAccountInput{Credentials: map[string]any{"api_key": "sk-new-test"}, UpstreamSubscriptionRate: &config})
	require.ErrorContains(t, err, "更换 API Key")
}
