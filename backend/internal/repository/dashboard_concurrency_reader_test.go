package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestDashboardTotalConcurrencyIncludesRegularAndLiveSlots(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewConcurrencyCache(client, 15, 900)
	reader, ok := cache.(service.DashboardConcurrencyReader)
	require.True(t, ok)
	live, ok := cache.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	acquired, err := cache.AcquireAccountSlot(ctx, 10, 3, "regular-10")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = cache.AcquireAccountSlot(ctx, 11, 2, "regular-11")
	require.NoError(t, err)
	require.True(t, acquired)
	liveAcquired, err := live.AcquireLiveLease(ctx, 10, 3, 20, 3, 30, "live-10", true)
	require.NoError(t, err)
	require.True(t, liveAcquired)

	total, err := reader.GetTotalActiveConcurrency(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, total)
}

func TestDashboardTotalConcurrencyKeepsLiveSessionAfterRegularSlotReleased(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewConcurrencyCache(client, 15, 900)
	reader := cache.(service.DashboardConcurrencyReader)
	live := cache.(service.LiveConcurrencyCache)
	ctx := context.Background()

	acquired, err := cache.AcquireAccountSlot(ctx, 10, 2, "regular-10")
	require.NoError(t, err)
	require.True(t, acquired)
	liveAcquired, err := live.AcquireLiveLease(ctx, 10, 2, 20, 2, 30, "live-only-10", true)
	require.NoError(t, err)
	require.True(t, liveAcquired)
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 10, "regular-10"))

	total, err := reader.GetTotalActiveConcurrency(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, total)

	require.NoError(t, live.ReleaseLiveLease(ctx, 10, 20, 30, "live-only-10"))
	total, err = reader.GetTotalActiveConcurrency(ctx)
	require.NoError(t, err)
	require.Zero(t, total)
}
