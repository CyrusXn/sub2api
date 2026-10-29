package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var openAIModelFailureStreakScript = redis.NewScript(`
if ARGV[1] == '1' then
  redis.call('DEL', KEYS[1])
  return 0
end
local count = redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return count
`)

func (c *gatewayCache) RecordOpenAIModelResult(ctx context.Context, accountID int64, model string, success bool, ttl time.Duration) (int, error) {
	// 对模型名取摘要，限制键长度；每个账号、模型独立累计，成功与失败均为原子操作。
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(model))))
	key := fmt.Sprintf("openai_model_failure_streak:%d:%x", accountID, digest)
	result, err := openAIModelFailureStreakScript.Run(ctx, c.rdb, []string{key}, success, ttl.Milliseconds()).Int()
	return result, err
}
