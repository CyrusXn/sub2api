package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseBalanceCenterSubscriptionUsesBoundID(t *testing.T) {
	body := []byte(`{"code":0,"data":[{"id":7,"expires_at":"2026-09-04T20:37:25+08:00"},{"id":9,"expires_at":"2026-09-28T16:27:44+08:00"}]}`)
	expires, reason := parseBalanceCenterSubscription(body, 9)
	require.Empty(t, reason)
	require.Equal(t, "2026-09-28T16:27:44+08:00", expires.Format(time.RFC3339))
	// 重置只改变同一订阅有效期，不能误取另一个过期套餐。
	_, reason = parseBalanceCenterSubscription(body, 10)
	require.NotEmpty(t, reason)
	_, reason = parseBalanceCenterSubscription([]byte(`{"code":0,"data":[{"id":9}]}`), 9)
	require.NotEmpty(t, reason)
}

func TestBalanceCenterEmailEnablesHiddenCollectionSwitches(t *testing.T) {
	repo := &balanceCenterSettingRepoStub{values: map[string]string{SettingKeyBalanceCenterEmailEnabled: "true", SettingKeyBalanceCenterEnabled: "false", SettingKeyBalanceCenterEventProbeEnabled: "false"}}
	svc := NewBalanceCenterService(nil, repo)
	settings, err := svc.GetSettings(t.Context())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.True(t, settings.EventProbeEnabled)
	queue := &balanceCenterEventQueueStub{}
	events := NewBalanceCenterEventService(queue, repo, nil)
	require.NoError(t, events.RefreshSettings(t.Context()))
	events.PublishAccountUsed(t.Context(), 17)
	require.Equal(t, []int64{17}, queue.published)
}

// 日额度提醒必须与人民币订阅资产分离，字段缺失时不误报为零。
func TestSubscriptionQuotaObservation(t *testing.T) {
	now := time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, fields string
		want         *float64
	}{
		{"低于一刀", `"status":"active","daily_usage_usd":999.5,"daily_window_start":"2026-09-09T08:00:00Z","group":{"daily_limit_usd":1000}`, float64Ptr(0.5)},
		{"等于一刀", `"status":"active","daily_usage_usd":999,"daily_window_start":"2026-09-09T08:00:00Z","group":{"daily_limit_usd":1000}`, float64Ptr(1)},
		{"缺少用量", `"status":"active","group":{"daily_limit_usd":1000}`, nil},
		{"已过期", `"status":"expired","daily_usage_usd":1000,"group":{"daily_limit_usd":1000}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, reason := parseBalanceCenterSubscriptionObservation([]byte(`{"code":0,"data":[{"id":9,"expires_at":"2026-09-28T16:27:44+08:00",`+tc.fields+`}]}`), 9, now)
			require.Empty(t, reason)
			require.Equal(t, tc.want, o.RemainingUSD)
		})
	}
}

type subscriptionAssetRepoStub struct {
	balanceCenterServiceRepositoryStub
	messages []AlertEmailOutboxInput
}

func (r *subscriptionAssetRepoStub) ListBalanceCenterAssets(context.Context) ([]BalanceCenterAsset, error) {
	return nil, nil
}
func (r *subscriptionAssetRepoStub) SaveBalanceCenterAsset(context.Context, *BalanceCenterAsset) error {
	return nil
}
func (r *subscriptionAssetRepoStub) SaveBalanceCenterSubscriptionObservation(_ context.Context, _, _ int64, _ BalanceCenterSubscriptionObservation, _ time.Time, _ string, m []AlertEmailOutboxInput) error {
	r.messages = m
	return nil
}

// 检测边界、开关和未知状态直接验证入队行为，防止资产余额误入告警。
func TestFishSubscriptionAlertThreshold(t *testing.T) {
	for _, tc := range []struct {
		name            string
		remaining       *float64
		enabled, reason string
		want            int
	}{
		{"低于阈值", float64Ptr(0.99), "true", "", 1},
		{"等于阈值", float64Ptr(1), "true", "", 0},
		{"缺失额度", nil, "true", "", 0},
		{"关闭邮件", float64Ptr(0), "false", "", 0},
		{"同步失败", float64Ptr(0), "true", "查询失败", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &subscriptionAssetRepoStub{}
			settings := &balanceCenterSettingRepoStub{values: map[string]string{SettingKeyBalanceCenterEmailEnabled: tc.enabled, SettingKeyOpsEmailNotificationConfig: `{"alert":{"recipients":["ops@example.com"]}}`}}
			svc := NewBalanceCenterService(r, settings)
			now := time.Now()
			require.NoError(t, svc.SaveSubscriptionObservation(t.Context(), 13, 944, BalanceCenterSubscriptionObservation{ExpiresAt: now.Add(time.Hour), RemainingUSD: tc.remaining, WindowKey: "944:window:expiry"}, now, tc.reason))
			require.Len(t, r.messages, tc.want)
			if tc.want > 0 {
				require.Equal(t, now, r.messages[0].AvailableAt)
				require.Contains(t, r.messages[0].BodyHTML, "手动重置")
				require.Equal(t, "balance_center_subscription", r.messages[0].SourceType)
			}
		})
	}
}
