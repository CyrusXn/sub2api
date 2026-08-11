package handler

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMapUsageLogsForUserListUsesRealFirstTokenOnly(t *testing.T) {
	t.Parallel()

	firstTokenMs := 45_000
	records := []service.UsageLog{{
		ID:           1,
		RequestID:    "req-user-real-first-token",
		FirstTokenMs: &firstTokenMs,
		Group:        &service.Group{Name: "Plus"},
	}}

	result := mapUsageLogsForUserList(records)

	require.Len(t, result, 1)
	require.NotNil(t, result[0].FirstTokenMs)
	require.Equal(t, firstTokenMs, *result[0].FirstTokenMs)
	payload, err := json.Marshal(result[0])
	require.NoError(t, err)
	require.NotContains(t, string(payload), "display_first_token_ms")
}
