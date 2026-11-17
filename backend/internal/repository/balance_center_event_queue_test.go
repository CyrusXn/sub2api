package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redisserver "github.com/alicebob/miniredis/v2/server"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBalanceCenterEventQueueScheduleBatchUsesOneCommandAndKeepsFirstDue(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	queue := NewBalanceCenterEventQueue(client)
	ctx := context.Background()
	first := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	require.NoError(t, queue.Schedule(ctx, 17, first))
	before := server.CommandCount()
	require.NoError(t, queue.ScheduleBatch(ctx, []int64{17, 23, 23, 29}, first.Add(time.Minute)))
	require.Equal(t, before+1, server.CommandCount())
	require.NoError(t, queue.ScheduleBatch(ctx, nil, first))
	require.Equal(t, before+1, server.CommandCount())
	due, err := queue.PopDue(ctx, first, 20)
	require.NoError(t, err)
	require.Equal(t, []int64{17}, due)
	due, err = queue.PopDue(ctx, first.Add(time.Minute), 20)
	require.NoError(t, err)
	require.Equal(t, []int64{23, 29}, due)
}

func TestBalanceCenterEventQueueScheduleBatchBoundsSocketWaitLocally(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(context.Background()).Err())
	queue := NewBalanceCenterEventQueue(client)
	server.Server().SetPreHook(func(_ *redisserver.Peer, cmd string, _ ...string) bool {
		return cmd == "ZADD"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := queue.ScheduleBatch(ctx, []int64{17, 23}, time.Now())
	require.Error(t, err)
	require.Less(t, time.Since(started), 400*time.Millisecond, "默认Redis不遵循context socket deadline，须局部限制读写等待")
	require.Equal(t, time.Second, client.Options().ReadTimeout, "不能修改共享Redis客户端选项")
	require.Equal(t, time.Second, client.Options().WriteTimeout)
	require.False(t, client.Options().ContextTimeoutEnabled)
}

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
