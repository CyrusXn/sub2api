//go:build unit

package repository

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 跨实例去重和失败释放必须使用所有权令牌，旧请求不能释放新请求的记录。
func TestEmailDedupReservation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	first := &emailCache{rdb: client}
	second := &emailCache{rdb: client}
	ctx := context.Background()
	ok, err := first.AcquireEmailDelivery(ctx, "hash", "first", 5*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = second.AcquireEmailDelivery(ctx, "hash", "second", 5*time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, second.ReleaseEmailDelivery(ctx, "hash", "second"))
	ok, _ = second.AcquireEmailDelivery(ctx, "hash", "second", 5*time.Minute)
	require.False(t, ok)
	server.FastForward(5 * time.Minute)
	ok, err = second.AcquireEmailDelivery(ctx, "hash", "second", 5*time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, first.ReleaseEmailDelivery(ctx, "hash", "first"))
	ok, _ = first.AcquireEmailDelivery(ctx, "hash", "third", 5*time.Minute)
	require.False(t, ok)
	require.NoError(t, second.ReleaseEmailDelivery(ctx, "hash", "second"))
	ok, _ = first.AcquireEmailDelivery(ctx, "hash", "third", 5*time.Minute)
	require.True(t, ok)
}
