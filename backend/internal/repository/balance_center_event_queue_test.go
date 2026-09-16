package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBalanceCenterEventQueueMergesSameAccount(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	queue := NewBalanceCenterEventQueue(client)
	ctx := context.Background()
	first := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, queue.Schedule(ctx, 17, first.Add(time.Minute)))
	require.NoError(t, queue.Schedule(ctx, 17, first.Add(2*time.Minute)))
	require.NoError(t, queue.Schedule(ctx, 23, first.Add(time.Minute)))

	due, err := queue.PopDue(ctx, first.Add(90*time.Second), 20)
	require.NoError(t, err)
	require.Equal(t, []int64{17, 23}, due)

	due, err = queue.PopDue(ctx, first.Add(3*time.Minute), 20)
	require.NoError(t, err)
	require.Empty(t, due)
	require.NoError(t, queue.Schedule(ctx, 17, first.Add(4*time.Minute)))
	due, err = queue.PopDue(ctx, first.Add(4*time.Minute), 20)
	require.NoError(t, err)
	require.Equal(t, []int64{17}, due)
}
