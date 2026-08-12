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
	return q.rdb.ZAdd(ctx, balanceCenterEventQueueKey, redis.Z{
		Score:  float64(dueAt.UnixMilli()),
		Member: strconv.FormatInt(accountID, 10),
	}).Err()
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
