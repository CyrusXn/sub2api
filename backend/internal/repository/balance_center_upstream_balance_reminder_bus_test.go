package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEnqueueUpstreamSiteBalanceReminderEventReportsSlowSubscriber(t *testing.T) {
	events := make(chan service.UpstreamSiteBalanceReminderEvent, 1)
	events <- service.UpstreamSiteBalanceReminderEvent{Type: "resolved", Host: "existing.example"}
	newEvent := service.UpstreamSiteBalanceReminderEvent{Type: "resolved", Host: "new.example"}

	delivered := enqueueUpstreamSiteBalanceReminderEvent(context.Background(), events, newEvent)

	require.False(t, delivered)
	require.Equal(t, "existing.example", (<-events).Host)
}
