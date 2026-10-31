//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProductionSyntheticMarkerCannotFakeConnectionSuccess(t *testing.T) {
	account := Account{
		ID: 91, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{"synthetic_ui_test": true},
	}
	svc := &AccountTestService{accountRepo: &stubOpenAIAccountRepo{accounts: []Account{account}}}
	ctx, recorder := newTestContext()
	err := svc.TestAccountConnection(ctx, account.ID, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "No API key available")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestProductionPassiveUsageCannotOverrideRealStatsWithSyntheticValues(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	account := &Account{
		ID: 92, Platform: PlatformAnthropic, Type: AccountTypeOAuth,
		SessionWindowEnd: &reset,
		Extra: map[string]any{
			"synthetic_ui_test":      true,
			"synthetic_window_stats": map[string]any{"requests": 9999, "tokens": 999999, "cost": 999},
		},
	}
	svc := &AccountUsageService{cache: &UsageCache{}}
	svc.cache.windowStatsCache.Store(account.ID, &windowStatsCache{
		stats: &WindowStats{Requests: 7, Tokens: 120, Cost: 0.25}, timestamp: time.Now(),
	})
	usage, err := svc.getPassiveUsageForAccount(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, usage.FiveHour)
	require.Equal(t, int64(7), usage.FiveHour.WindowStats.Requests)
	require.Equal(t, int64(120), usage.FiveHour.WindowStats.Tokens)
	require.Equal(t, 0.25, usage.FiveHour.WindowStats.Cost)
}

func TestProductionSyntheticMarkerCannotBypassActiveUsageProbe(t *testing.T) {
	account := &Account{
		ID: 93, Platform: PlatformAnthropic, Type: AccountTypeOAuth,
		Extra: map[string]any{"synthetic_ui_test": true},
	}
	svc := &AccountUsageService{cache: &UsageCache{}, usageLogRepo: &usageBatchLogRepoStub{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	usage, err := svc.getUsageForAccount(ctx, account, true)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, usage)
}
