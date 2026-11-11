package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const upstreamSiteBalanceReminderChannel = "balance:center:upstream-site-reminders"

type upstreamSiteBalanceReminderBus struct {
	client *redis.Client
}

func NewUpstreamSiteBalanceReminderBus(client *redis.Client) service.UpstreamSiteBalanceReminderBus {
	return &upstreamSiteBalanceReminderBus{client: client}
}

func (b *upstreamSiteBalanceReminderBus) PublishUpstreamSiteBalanceReminder(ctx context.Context, event service.UpstreamSiteBalanceReminderEvent) error {
	if b == nil || b.client == nil {
		return errors.New("上游余额提醒推送未初始化")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, upstreamSiteBalanceReminderChannel, payload).Err()
}

func (b *upstreamSiteBalanceReminderBus) SubscribeUpstreamSiteBalanceReminders(ctx context.Context) (<-chan service.UpstreamSiteBalanceReminderEvent, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("上游余额提醒推送未初始化")
	}
	pubsub := b.client.Subscribe(ctx, upstreamSiteBalanceReminderChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, err
	}
	events := make(chan service.UpstreamSiteBalanceReminderEvent, 16)
	go func() {
		defer close(events)
		defer pubsub.Close()
		for message := range pubsub.Channel() {
			var event service.UpstreamSiteBalanceReminderEvent
			if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
				continue
			}
			if !enqueueUpstreamSiteBalanceReminderEvent(ctx, events, event) {
				// 慢客户端不静默丢事件；结束订阅使 WebSocket 重连并从数据库快照恢复。
				return
			}
		}
	}()
	return events, nil
}

func enqueueUpstreamSiteBalanceReminderEvent(ctx context.Context, events chan<- service.UpstreamSiteBalanceReminderEvent, event service.UpstreamSiteBalanceReminderEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}
