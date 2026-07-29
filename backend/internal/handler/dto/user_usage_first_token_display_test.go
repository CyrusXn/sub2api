package dto

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFromServiceForUserList_GroupRulesAndThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		groupName  string
		firstToken int
		wantExact  *int
		wantMin    int
		wantMax    int
	}{
		{name: "decorated pro20x keeps two seconds", groupName: "【GPT】Pro 20x", firstToken: 2_000, wantExact: intPtr(2_000)},
		{name: "compact pro20x transforms above two seconds", groupName: "pro20X", firstToken: 2_001, wantMin: 500, wantMax: 1_500},
		{name: "plain pro keeps four seconds", groupName: "GPT - PRO", firstToken: 4_000, wantExact: intPtr(4_000)},
		{name: "plain pro transforms above four seconds", groupName: "GPT - PRO", firstToken: 4_001, wantMin: 500, wantMax: 4_000},
		{name: "plus keeps eight seconds", groupName: "[GPT] Plus", firstToken: 8_000, wantExact: intPtr(8_000)},
		{name: "plus transforms above eight seconds", groupName: "[GPT] Plus", firstToken: 8_001, wantMin: 1_000, wantMax: 8_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			log := usageLogForFirstTokenDisplay(1, "req-threshold", tt.groupName, tt.firstToken)

			got := UsageLogFromServiceForUserList(log)

			require.NotNil(t, got.DisplayFirstTokenMs)
			require.Equal(t, tt.firstToken, *got.FirstTokenMs, "真实首字必须保留")
			if tt.wantExact != nil {
				require.Equal(t, *tt.wantExact, *got.DisplayFirstTokenMs)
				return
			}
			require.GreaterOrEqual(t, *got.DisplayFirstTokenMs, tt.wantMin)
			require.LessOrEqual(t, *got.DisplayFirstTokenMs, tt.wantMax)
		})
	}
}

func TestUsageLogFromServiceForUserList_AppliesDisplayValueOnlyFromCutoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		createdAt   time.Time
		wantDisplay bool
	}{
		{name: "before cutoff keeps historical value", createdAt: userUsageFirstTokenDisplayCutoff.Add(-time.Nanosecond)},
		{name: "at cutoff enables derived value", createdAt: userUsageFirstTokenDisplayCutoff, wantDisplay: true},
		{name: "after cutoff enables derived value", createdAt: userUsageFirstTokenDisplayCutoff.Add(time.Nanosecond), wantDisplay: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			log := usageLogForFirstTokenDisplay(1, "req-cutoff", "Plus", 30_000)
			log.CreatedAt = tt.createdAt

			got := UsageLogFromServiceForUserList(log)

			require.Equal(t, 30_000, *got.FirstTokenMs, "真实首字必须始终保留")
			if tt.wantDisplay {
				require.NotNil(t, got.DisplayFirstTokenMs)
				return
			}
			require.Nil(t, got.DisplayFirstTokenMs)
		})
	}
}

func TestUsageLogFromServiceForUserList_LeavesUnmatchedOrMissingMetricsUntouched(t *testing.T) {
	t.Parallel()

	unmatched := usageLogForFirstTokenDisplay(1, "req-default", "Default", 30_000)
	require.Nil(t, UsageLogFromServiceForUserList(unmatched).DisplayFirstTokenMs)

	missingGroup := usageLogForFirstTokenDisplay(2, "req-no-group", "Plus", 30_000)
	missingGroup.Group = nil
	require.Nil(t, UsageLogFromServiceForUserList(missingGroup).DisplayFirstTokenMs)

	missingFirstToken := usageLogForFirstTokenDisplay(3, "req-no-first-token", "Plus", 30_000)
	missingFirstToken.FirstTokenMs = nil
	require.Nil(t, UsageLogFromServiceForUserList(missingFirstToken).DisplayFirstTokenMs)
}

func TestUsageLogFromServiceForUserList_IsStableAndIndependentlyDistributed(t *testing.T) {
	t.Parallel()

	stable := usageLogForFirstTokenDisplay(9, "req-stable", "Pro", 40_000)
	first := UsageLogFromServiceForUserList(stable)
	second := UsageLogFromServiceForUserList(stable)
	require.NotNil(t, first.DisplayFirstTokenMs)
	require.NotNil(t, second.DisplayFirstTokenMs)
	require.Equal(t, *first.DisplayFirstTokenMs, *second.DisplayFirstTokenMs)

	pro20xUnderOneSecond := 0
	proValues := make(map[int]struct{})
	plusValues := make(map[int]struct{})
	for i := 1; i <= 1_000; i++ {
		pro20x := UsageLogFromServiceForUserList(usageLogForFirstTokenDisplay(int64(i), fmt.Sprintf("req-pro20x-%d", i), "Pro 20x", 30_000))
		require.NotNil(t, pro20x.DisplayFirstTokenMs)
		require.GreaterOrEqual(t, *pro20x.DisplayFirstTokenMs, 500)
		require.LessOrEqual(t, *pro20x.DisplayFirstTokenMs, 1_500)
		if *pro20x.DisplayFirstTokenMs < 1_000 {
			pro20xUnderOneSecond++
		}

		pro := UsageLogFromServiceForUserList(usageLogForFirstTokenDisplay(int64(i), fmt.Sprintf("req-pro-%d", i), "Pro", 30_000))
		require.NotNil(t, pro.DisplayFirstTokenMs)
		require.GreaterOrEqual(t, *pro.DisplayFirstTokenMs, 500)
		require.LessOrEqual(t, *pro.DisplayFirstTokenMs, 4_000)
		proValues[*pro.DisplayFirstTokenMs] = struct{}{}

		plus := UsageLogFromServiceForUserList(usageLogForFirstTokenDisplay(int64(i), fmt.Sprintf("req-plus-%d", i), "Plus", 30_000))
		require.NotNil(t, plus.DisplayFirstTokenMs)
		require.GreaterOrEqual(t, *plus.DisplayFirstTokenMs, 1_000)
		require.LessOrEqual(t, *plus.DisplayFirstTokenMs, 8_000)
		plusValues[*plus.DisplayFirstTokenMs] = struct{}{}
	}

	require.InDelta(t, 800, pro20xUnderOneSecond, 60)
	require.Greater(t, len(proValues), 100)
	require.Greater(t, len(plusValues), 100)
}

func TestUsageLogFromServiceForUserList_DoesNotExposeDisplayValueToAdminMapper(t *testing.T) {
	t.Parallel()

	log := usageLogForFirstTokenDisplay(1, "req-admin-raw", "Plus", 30_000)
	userDTO := UsageLogFromServiceForUserList(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.NotNil(t, userDTO.DisplayFirstTokenMs)
	require.Equal(t, 30_000, *userDTO.FirstTokenMs)

	adminJSON, err := json.Marshal(adminDTO)
	require.NoError(t, err)
	require.NotContains(t, string(adminJSON), "display_first_token_ms")
}

func usageLogForFirstTokenDisplay(id int64, requestID, groupName string, firstTokenMs int) *service.UsageLog {
	return &service.UsageLog{
		ID:           id,
		RequestID:    requestID,
		Model:        "gpt-5.4",
		FirstTokenMs: intPtr(firstTokenMs),
		Group:        &service.Group{Name: groupName},
		CreatedAt:    userUsageFirstTokenDisplayCutoff,
	}
}

func intPtr(value int) *int {
	return &value
}
