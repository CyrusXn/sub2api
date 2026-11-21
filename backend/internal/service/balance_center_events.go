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
	// 余额事件属于请求主链路的旁路观察；Redis 异常时最多等待一个很短的固定窗口。
	balanceCenterEventPublishTimeout = 200 * time.Millisecond
)

type BalanceCenterEventQueue interface {
	Schedule(context.Context, int64, time.Time) error
	ScheduleBatch(context.Context, []int64, time.Time) error
	PopDue(context.Context, time.Time, int64) ([]int64, error)
}

type BalanceCenterAccountProber interface {
	ProbeAccount(context.Context, int64) (*UpstreamBillingProbeSnapshot, error)
}

type BalanceCenterUsageEventPublisher interface {
	PublishAccountUsed(context.Context, int64)
	PublishAccountsUsed(context.Context, []int64)
}

type BalanceCenterEventService struct {
	queue       BalanceCenterEventQueue
	settingRepo SettingRepository
	prober      BalanceCenterAccountProber
	enabled     atomic.Bool
	now         func() time.Time
}

func NewBalanceCenterEventService(queue BalanceCenterEventQueue, settingRepo SettingRepository, probers ...BalanceCenterAccountProber) *BalanceCenterEventService {
	var prober BalanceCenterAccountProber
	if len(probers) > 0 {
		prober = probers[0]
	}
	return &BalanceCenterEventService{queue: queue, settingRepo: settingRepo, prober: prober, now: time.Now}
}

func (s *BalanceCenterEventService) SetProber(prober BalanceCenterAccountProber) {
	if s == nil {
		return
	}
	s.prober = prober
}

func (s *BalanceCenterEventService) RefreshSettings(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyBalanceCenterEnabled,
		SettingKeyBalanceCenterEventProbeEnabled,
		SettingKeyBalanceCenterEmailEnabled,
	})
	if err != nil {
		return err
	}
	// 手机告警启用后也需要采集活跃账号余额，即使管理员关闭了邮件。
	_, mobileEnabled := mobileAlertDigest()
	s.enabled.Store(mobileEnabled || parseBalanceCenterBool(values[SettingKeyBalanceCenterEmailEnabled]) || (parseBalanceCenterBool(values[SettingKeyBalanceCenterEnabled]) && parseBalanceCenterBool(values[SettingKeyBalanceCenterEventProbeEnabled])))
	return nil
}

func (s *BalanceCenterEventService) PublishAccountUsed(ctx context.Context, accountID int64) {
	s.PublishAccountsUsed(ctx, []int64{accountID})
}

func (s *BalanceCenterEventService) PublishAccountsUsed(ctx context.Context, accountIDs []int64) {
	if s == nil || s.queue == nil || len(accountIDs) == 0 || !s.enabled.Load() {
		return
	}
	unique := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, exists := seen[accountID]; accountID <= 0 || exists {
			continue
		}
		seen[accountID] = struct{}{}
		unique = append(unique, accountID)
	}
	if len(unique) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), balanceCenterEventPublishTimeout)
	defer cancel()
	// 一批账号共用一次预算和一条队列命令，不能在结算提交后逐条累加等待。
	if err := s.queue.ScheduleBatch(publishCtx, unique, s.now().Add(balanceCenterEventMergeWindow)); err != nil {
		slog.Warn("余额中心事件投递失败", "account_count", len(unique), "error", err)
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
