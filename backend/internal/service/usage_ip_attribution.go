package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

const CCSwitchIPAttributionLeaseTTL = 2 * time.Minute

// UsageIPAttributionStore 保存 CC-Switch 轮换产生的短时 Key/IP 归因租约。
type UsageIPAttributionStore interface {
	SetUsageIPAttributionLease(ctx context.Context, apiKeyID int64, sourceIP string, targetIP string, ttl time.Duration) error
	GetUsageIPAttributionLease(ctx context.Context, apiKeyID int64, sourceIP string) (targetIP string, found bool, err error)
}

type UsageIPAttributionService struct {
	store UsageIPAttributionStore
}

func NewUsageIPAttributionService(store UsageIPAttributionStore) *UsageIPAttributionService {
	return &UsageIPAttributionService{store: store}
}

func (s *UsageIPAttributionService) CreateLease(ctx context.Context, apiKeyID int64, sourceIP string, targetIP string) error {
	if s == nil || s.store == nil {
		return errors.New("usage ip attribution store is unavailable")
	}
	sourceIP = strings.TrimSpace(sourceIP)
	targetIP = strings.TrimSpace(targetIP)
	if apiKeyID <= 0 || sourceIP == "" || targetIP == "" {
		return errors.New("invalid usage ip attribution lease")
	}
	return s.store.SetUsageIPAttributionLease(ctx, apiKeyID, sourceIP, targetIP, CCSwitchIPAttributionLeaseTTL)
}

func (s *UsageIPAttributionService) Resolve(ctx context.Context, apiKeyID int64, sourceIP string) (string, bool) {
	if s == nil || s.store == nil || apiKeyID <= 0 {
		return "", false
	}
	sourceIP = strings.TrimSpace(sourceIP)
	if sourceIP == "" {
		return "", false
	}
	targetIP, found, err := s.store.GetUsageIPAttributionLease(ctx, apiKeyID, sourceIP)
	if err != nil || !found {
		return "", false
	}
	targetIP = strings.TrimSpace(targetIP)
	return targetIP, targetIP != ""
}
