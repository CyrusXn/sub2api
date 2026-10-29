package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAIModelFailureStreakSharedResetAndExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	a, b := &gatewayCache{rdb: client}, &gatewayCache{rdb: client}
	ctx := context.Background()
	for i, cache := range []*gatewayCache{a, b, a} {
		count, err := cache.RecordOpenAIModelResult(ctx, 42, "gpt-test", false, time.Minute)
		require.NoError(t, err)
		require.Equal(t, i+1, count)
	}
	count, err := b.RecordOpenAIModelResult(ctx, 42, "gpt-other", false, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	_, err = b.RecordOpenAIModelResult(ctx, 42, "gpt-test", true, time.Minute)
	require.NoError(t, err)
	count, err = a.RecordOpenAIModelResult(ctx, 42, "gpt-test", false, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	server.FastForward(2 * time.Minute)
	count, err = b.RecordOpenAIModelResult(ctx, 42, "gpt-test", false, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
