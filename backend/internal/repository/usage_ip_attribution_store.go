package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const usageIPAttributionLeasePrefix = "usage_ip_attribution:"

type usageIPAttributionStore struct {
	rdb *redis.Client
}

func NewUsageIPAttributionStore(rdb *redis.Client) service.UsageIPAttributionStore {
	return &usageIPAttributionStore{rdb: rdb}
}

func usageIPAttributionLeaseKey(apiKeyID int64, sourceIP string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sourceIP)))
	return fmt.Sprintf("%s%d:%s", usageIPAttributionLeasePrefix, apiKeyID, hex.EncodeToString(sum[:]))
}

func (s *usageIPAttributionStore) SetUsageIPAttributionLease(ctx context.Context, apiKeyID int64, sourceIP string, targetIP string, ttl time.Duration) error {
	return s.rdb.Set(ctx, usageIPAttributionLeaseKey(apiKeyID, sourceIP), strings.TrimSpace(targetIP), ttl).Err()
}

func (s *usageIPAttributionStore) GetUsageIPAttributionLease(ctx context.Context, apiKeyID int64, sourceIP string) (string, bool, error) {
	value, err := s.rdb.Get(ctx, usageIPAttributionLeaseKey(apiKeyID, sourceIP)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
