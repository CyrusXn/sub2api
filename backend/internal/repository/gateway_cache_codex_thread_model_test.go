package repository

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheOpenAICodexThreadModel(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache, ok := NewGatewayCache(client).(service.OpenAICodexThreadModelCache)
	require.True(t, ok)

	ctx := context.Background()
	model, err := cache.GetOpenAICodexThreadModel(ctx, 101, "thread-a")
	require.NoError(t, err)
	require.Empty(t, model)

	require.NoError(t, cache.SetOpenAICodexThreadModel(ctx, 101, "thread-a", "gpt-5.6-sol", time.Hour))
	model, err = cache.GetOpenAICodexThreadModel(ctx, 101, "thread-a")
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", model)

	model, err = cache.GetOpenAICodexThreadModel(ctx, 202, "thread-a")
	require.NoError(t, err)
	require.Empty(t, model, "相同 thread_id 必须按 API Key 隔离")

	key := buildOpenAICodexThreadModelKey(101, "thread-a")
	require.Equal(t, time.Hour, redisServer.TTL(key))
	require.NotContains(t, key, "thread-a", "客户端 thread_id 不应原样进入 Redis key")
	require.Len(t, strings.TrimPrefix(key, openAICodexThreadModelPrefix+"101:"), sha256.Size*2)
}
