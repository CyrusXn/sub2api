package service

import (
	"context"
	"log/slog"
	"time"
)

// ChannelMonitorPassiveCard 直接以业务分组为卡片，不再保存探针凭据或伪造 PING。
type ChannelMonitorPassiveCard struct {
	ID               int64                      `json:"id"`
	Name             string                     `json:"name"`
	Provider         string                     `json:"provider"`
	Passive          bool                       `json:"passive"`
	PrimaryStatus    string                     `json:"primary_status"`
	PrimaryLatencyMs *int64                     `json:"primary_latency_ms"`
	SuccessRate      float64                    `json:"success_rate"`
	Availability7d   float64                    `json:"availability_7d"`
	Availability15d  float64                    `json:"availability_15d"`
	Availability30d  float64                    `json:"availability_30d"`
	Timeline         []UserMonitorTimelinePoint `json:"timeline"`
}

func (s *ChannelMonitorV2Service) PassiveCards(ctx context.Context, filter ChannelMonitorV2Filter) ([]ChannelMonitorPassiveCard, error) {
	if s.settings == nil || !s.settings.GetChannelMonitorRuntime(ctx).Enabled {
		return nil, ErrChannelMonitorDisabled
	}
	return s.repo.GetPassiveCards(ctx, filter)
}

// 独立于旧 V1/V2 展示配置，每分钟读取一次已完成分钟，并重算近期窗口接纳延迟写入。
func (s *ChannelMonitorV2Aggregator) passiveLoop() {
	for {
		s.runPassiveOnce()
		timer := time.NewTimer(time.Until(time.Now().Truncate(time.Minute).Add(time.Minute)))
		select {
		case <-s.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *ChannelMonitorV2Aggregator) runPassiveOnce() {
	s.mu.Lock()
	parent := s.ctx
	s.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 55*time.Second)
	defer cancel()
	if s.settings == nil || !s.settings.GetChannelMonitorRuntime(ctx).PassiveAggregationAllowed() {
		return
	}
	if err := s.repo.RefreshPassiveMinutes(ctx, time.Now().UTC().Truncate(time.Minute)); err != nil {
		// 失败不写成渠道故障：这是采集失败，不是用户请求失败。
		slog.Error("渠道被动状态采集失败", "error", err)
	}
}
