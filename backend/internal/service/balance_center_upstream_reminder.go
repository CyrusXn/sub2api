package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type balanceCenterUpstreamReminderRepository interface {
	UpstreamSiteBalanceReminderRepository
	CreateBalanceCenterRechargeEventWithReminder(context.Context, *BalanceCenterRechargeEvent) (*BalanceCenterRechargeEvent, *UpstreamSiteBalanceReminder, error)
}

func (s *BalanceCenterService) SetUpstreamSiteBalanceReminderBus(bus UpstreamSiteBalanceReminderBus) {
	if s != nil {
		s.upstreamBalanceBus = bus
	}
}

func (s *BalanceCenterService) upstreamReminderRepository() (UpstreamSiteBalanceReminderRepository, error) {
	r, ok := s.repository.(UpstreamSiteBalanceReminderRepository)
	if !ok {
		return nil, errors.New("上游余额提醒仓储不可用")
	}
	return r, nil
}

func (s *BalanceCenterService) EnsureUpstreamSiteBalanceObservations(ctx context.Context, items []UpstreamSiteBalanceObservation) error {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return err
	}
	return r.EnsureUpstreamSiteBalanceObservations(ctx, items)
}

func (s *BalanceCenterService) ListDueUpstreamSiteBalanceObservations(ctx context.Context, now time.Time, limit int) ([]UpstreamSiteBalanceObservation, error) {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return nil, err
	}
	return r.ListDueUpstreamSiteBalanceObservations(ctx, now, limit)
}

func (s *BalanceCenterService) SaveUpstreamSiteBalanceObservation(ctx context.Context, item UpstreamSiteBalanceObservation) (*UpstreamSiteBalanceReminder, error) {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return nil, err
	}
	reminder, err := r.SaveUpstreamSiteBalanceObservation(ctx, item)
	if err != nil || reminder == nil {
		return reminder, err
	}
	s.publishUpstreamBalanceReminder(ctx, UpstreamSiteBalanceReminderEvent{Type: "upsert", Reminder: reminder})
	return reminder, nil
}

func (s *BalanceCenterService) DeferUpstreamSiteBalanceObservation(ctx context.Context, host string, next time.Time, reason string) error {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return err
	}
	return r.DeferUpstreamSiteBalanceObservation(ctx, host, next, reason)
}

func (s *BalanceCenterService) ListActiveUpstreamSiteBalanceReminders(ctx context.Context) ([]UpstreamSiteBalanceReminder, error) {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return nil, err
	}
	return r.ListActiveUpstreamSiteBalanceReminders(ctx)
}

func (s *BalanceCenterService) ListUpstreamSiteBalanceReminderSnapshot(ctx context.Context) ([]UpstreamSiteBalanceReminder, map[string]int64, error) {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return nil, nil, err
	}
	return r.ListUpstreamSiteBalanceReminderSnapshot(ctx)
}

func (s *BalanceCenterService) ResolveUpstreamSiteBalanceReminder(ctx context.Context, host string, now time.Time) (bool, error) {
	r, err := s.upstreamReminderRepository()
	if err != nil {
		return false, err
	}
	version, resolved, err := r.ResolveUpstreamSiteBalanceReminder(ctx, host, now)
	if err != nil || !resolved {
		return resolved, err
	}
	s.publishUpstreamBalanceReminder(ctx, UpstreamSiteBalanceReminderEvent{Type: "resolved", Host: host, Version: version})
	return true, nil
}

func (s *BalanceCenterService) publishUpstreamBalanceReminder(ctx context.Context, event UpstreamSiteBalanceReminderEvent) {
	if err := s.PublishUpstreamSiteBalanceReminder(ctx, event); err != nil {
		slog.Warn("balance_center_upstream_reminder_publish_failed", "event_type", event.Type, "error_type", fmt.Sprintf("%T", err))
	}
}

func (s *BalanceCenterService) PublishUpstreamSiteBalanceReminder(ctx context.Context, event UpstreamSiteBalanceReminderEvent) error {
	if s == nil || s.upstreamBalanceBus == nil {
		return nil
	}
	return s.upstreamBalanceBus.PublishUpstreamSiteBalanceReminder(ctx, event)
}

func (s *BalanceCenterService) SubscribeUpstreamSiteBalanceReminders(ctx context.Context) (<-chan UpstreamSiteBalanceReminderEvent, error) {
	if s == nil || s.upstreamBalanceBus == nil {
		return nil, errors.New("上游余额提醒推送不可用")
	}
	return s.upstreamBalanceBus.SubscribeUpstreamSiteBalanceReminders(ctx)
}

func (s *BalanceCenterService) CreateBalanceCenterRechargeEventWithReminder(ctx context.Context, item *BalanceCenterRechargeEvent) (*BalanceCenterRechargeEvent, *UpstreamSiteBalanceReminder, error) {
	r, ok := s.repository.(balanceCenterUpstreamReminderRepository)
	if !ok {
		return nil, nil, errors.New("充值提醒事务仓储不可用")
	}
	created, reminder, err := r.CreateBalanceCenterRechargeEventWithReminder(ctx, item)
	if err != nil || reminder == nil {
		return created, reminder, err
	}
	s.publishUpstreamBalanceReminder(ctx, UpstreamSiteBalanceReminderEvent{Type: "resolved", Host: reminder.Host, Version: reminder.Version})
	return created, reminder, nil
}
