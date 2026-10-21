package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamBillingProbePersistsLastRateChange(t *testing.T) {
	ctx := context.Background()
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{}}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}
	svc := &UpstreamBillingProbeService{accountRepo: repo}
	now := time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
	readChange := func(snapshot *UpstreamBillingProbeSnapshot) any {
		t.Helper()
		payload, err := json.Marshal(snapshot)
		require.NoError(t, err)
		var value map[string]any
		require.NoError(t, json.Unmarshal(payload, &value))
		return value["rate_change"]
	}
	success := func(rate float64) *UpstreamBillingProbeSnapshot {
		t.Helper()
		current, err := repo.GetByID(ctx, 1)
		require.NoError(t, err)
		snapshot, err := svc.persistProbeSuccess(ctx, current, 30, now, http.StatusOK,
			map[string]any{"effective_rate_multiplier": rate}, nil)
		require.NoError(t, err)
		now = now.Add(time.Minute)
		return snapshot
	}
	require.Nil(t, readChange(success(0.25)), "首次探测没有基线，不能编造变化")
	down := readChange(success(0.23))
	require.Equal(t, map[string]any{"from": 0.25, "to": 0.23, "changed_at": "2026-10-21T01:01:00Z"}, down)
	require.Equal(t, down, readChange(success(0.23)), "相同探测不能清除或刷新变化时间")
	current, err := repo.GetByID(ctx, 1)
	require.NoError(t, err)
	failed, err := svc.persistProbeFailure(ctx, current, 30, now, 502, "http_error", 0)
	require.NoError(t, err)
	require.Equal(t, down, readChange(failed), "失败探测保留最后一次变化")
	require.Equal(t, down, readChange(success(0.23)), "失败恢复后没有变化则保留")
	require.Equal(t, map[string]any{"from": 0.23, "to": 0.26, "changed_at": "2026-10-21T01:04:00Z"}, readChange(success(0.26)))
	zero := readChange(success(0))
	require.Equal(t, map[string]any{"from": 0.26, "to": 0.0, "changed_at": "2026-10-21T01:05:00Z"}, zero)
	current, err = repo.GetByID(ctx, 1)
	require.NoError(t, err)
	cleared, err := svc.persistProbeFailureCleared(ctx, current, 30, now, 502, "web_auth_failed", 0, nil)
	require.NoError(t, err)
	require.Nil(t, cleared.Data)
	require.Equal(t, zero, readChange(cleared), "网页探测清空自动数据时仍保留变化记录")
	require.Equal(t, map[string]any{"from": 0.0, "to": 0.3, "changed_at": "2026-10-21T01:06:00Z"}, readChange(success(0.3)))
	current, err = repo.GetByID(ctx, 1)
	require.NoError(t, err)
	unsupported, err := svc.persistProbeFailure(ctx, current, 30, now, 404, "unsupported", 0)
	require.NoError(t, err)
	require.Nil(t, readChange(unsupported), "不再支持的来源不展示旧倍率变化")
	require.Nil(t, readChange(success(0.4)), "来源恢复后没有可比较基线，不编造变化")
}
