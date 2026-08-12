package service

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	balanceCenterEventMergeWindow = time.Minute
	balanceCenterEventBatchSize   = int64(20)
)

type BalanceCenterEventQueue interface {
	Schedule(context.Context, int64, time.Time) error
	PopDue(context.Context, time.Time, int64) ([]int64, error)
}

type BalanceCenterAccountProber interface {
	ProbeAccount(context.Context, int64) (*UpstreamBillingProbeSnapshot, error)
}

type BalanceCenterUsageEventPublisher interface {
	PublishAccountUsed(context.Context, int64)
}

type BalanceCenterEventService struct {
	queue       BalanceCenterEventQueue
	settingRepo SettingRepository
	prober      BalanceCenterAccountProber
	enabled     atomic.Bool
	now         func() time.Time
}

func NewBalanceCenterEventService(queue BalanceCenterEventQueue, settingRepo SettingRepository, prober BalanceCenterAccountProber) *BalanceCenterEventService {
	return &BalanceCenterEventService{queue: queue, settingRepo: settingRepo, prober: prober, now: time.Now}
}

func (s *BalanceCenterEventService) RefreshSettings(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyBalanceCenterEnabled,
		SettingKeyBalanceCenterEventProbeEnabled,
	})
	if err != nil {
		return err
	}
	s.enabled.Store(parseBalanceCenterBool(values[SettingKeyBalanceCenterEnabled]) && parseBalanceCenterBool(values[SettingKeyBalanceCenterEventProbeEnabled]))
	return nil
}

func (s *BalanceCenterEventService) PublishAccountUsed(ctx context.Context, accountID int64) {
	if s == nil || s.queue == nil || accountID <= 0 || !s.enabled.Load() {
		return
	}
	if err := s.queue.Schedule(ctx, accountID, s.now().Add(balanceCenterEventMergeWindow)); err != nil {
		slog.Warn("余额中心事件投递失败", "account_id", accountID, "error", err)
	}
}

func (s *BalanceCenterEventService) ConsumeDue(ctx context.Context) error {
	if s == nil || s.queue == nil || s.prober == nil || !s.enabled.Load() {
		return nil
	}
	accountIDs, err := s.queue.PopDue(ctx, s.now(), balanceCenterEventBatchSize)
	if err != nil {
		return err
	}
	for _, accountID := range accountIDs {
		if _, probeErr := s.prober.ProbeAccount(ctx, accountID); probeErr != nil {
			slog.Warn("余额中心事件探测失败", "account_id", accountID, "error", probeErr)
		}
	}
	return nil
}
