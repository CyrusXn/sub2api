package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const balanceCenterEventQueueKey = "balance:center:probe:due"

var balanceCenterPopDueScript = redis.NewScript(`
local members = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
if #members > 0 then
  redis.call('ZREM', KEYS[1], unpack(members))
end
return members
`)

type balanceCenterEventQueue struct {
	rdb *redis.Client
}

func NewBalanceCenterEventQueue(rdb *redis.Client) service.BalanceCenterEventQueue {
	return &balanceCenterEventQueue{rdb: rdb}
}

func (q *balanceCenterEventQueue) Schedule(ctx context.Context, accountID int64, dueAt time.Time) error {
	return q.ScheduleBatch(ctx, []int64{accountID}, dueAt)
}

func (q *balanceCenterEventQueue) ScheduleBatch(ctx context.Context, accountIDs []int64, dueAt time.Time) error {
	if len(accountIDs) == 0 {
		return nil
	}
	members := make([]redis.Z, len(accountIDs))
	for i, accountID := range accountIDs {
		members[i] = redis.Z{Score: float64(dueAt.UnixMilli()), Member: strconv.FormatInt(accountID, 10)}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	client := q.rdb
	if deadline, ok := ctx.Deadline(); ok {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
		// 共享客户端默认不把 context deadline 用于 socket；局部副本保留连接池，
		// 仅收紧本批读写等待，避免 Redis 故障仍占用全局秒级超时。
		for _, configured := range []time.Duration{q.rdb.Options().ReadTimeout, q.rdb.Options().WriteTimeout} {
			if configured > 0 && configured < timeout {
				timeout = configured
			}
		}
		client = q.rdb.WithTimeout(timeout)
	}
	// 连续请求只合并事件，不延后首次到期时间，避免活跃账号一直无法探测。
	return client.ZAddNX(ctx, balanceCenterEventQueueKey, members...).Err()
}

func (q *balanceCenterEventQueue) PopDue(ctx context.Context, now time.Time, limit int64) ([]int64, error) {
	values, err := balanceCenterPopDueScript.Run(ctx, q.rdb, []string{balanceCenterEventQueueKey}, now.UnixMilli(), limit).StringSlice()
	if err != nil {
		return nil, err
	}
	accountIDs := make([]int64, 0, len(values))
	for _, value := range values {
		accountID, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr == nil && accountID > 0 {
			accountIDs = append(accountIDs, accountID)
		}
	}
	return accountIDs, nil
}
