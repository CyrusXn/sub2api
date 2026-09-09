package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"time"
)

// BalanceCenterAsset 将现金资产与已经计入充值的订阅剩余价值分开，避免重复算账。
type BalanceCenterAsset struct {
	SiteID                        int64      `json:"site_id"`
	SiteName                      string     `json:"site_name"`
	Domain                        string     `json:"domain"`
	AccountID                     *int64     `json:"account_id"`
	ManualBalance                 *float64   `json:"manual_balance"`
	CashBalance                   *float64   `json:"cash_balance"`
	SubscriptionID                *int64     `json:"subscription_id"`
	SubscriptionPrice             float64    `json:"subscription_price"`
	SubscriptionDays              int        `json:"subscription_days"`
	SubscriptionExpiresAt         *time.Time `json:"subscription_expires_at"`
	SubscriptionAutoSync          bool       `json:"subscription_auto_sync"`
	SubscriptionSyncedAt          *time.Time `json:"subscription_synced_at"`
	SubscriptionSyncError         string     `json:"subscription_sync_error"`
	SubscriptionDailyRemainingUSD *float64   `json:"subscription_daily_remaining_usd"`
	SubscriptionBalance           *float64   `json:"subscription_balance"`
	TotalBalance                  *float64   `json:"total_balance"`
	BalanceKnown                  bool       `json:"balance_known"`
}

type BalanceCenterAssetRepository interface {
	ListBalanceCenterAssets(context.Context) ([]BalanceCenterAsset, error)
	SaveBalanceCenterAsset(context.Context, *BalanceCenterAsset) error
	SaveBalanceCenterSubscriptionObservation(context.Context, int64, int64, BalanceCenterSubscriptionObservation, time.Time, string, []AlertEmailOutboxInput) error
}

func (s *BalanceCenterService) assetRepository() (BalanceCenterAssetRepository, error) {
	r, ok := s.repository.(BalanceCenterAssetRepository)
	if !ok {
		return nil, errors.New("资产仓储不可用")
	}
	return r, nil
}

func (s *BalanceCenterService) ListAssets(ctx context.Context) ([]BalanceCenterAsset, error) {
	r, err := s.assetRepository()
	if err != nil {
		return nil, err
	}
	return r.ListBalanceCenterAssets(ctx)
}

func (s *BalanceCenterService) GetAsset(ctx context.Context, id int64) (*BalanceCenterAsset, error) {
	items, err := s.ListAssets(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].SiteID == id {
			return &items[i], nil
		}
	}
	return nil, errors.New("站点资产不存在")
}

func (s *BalanceCenterService) SaveAsset(ctx context.Context, a *BalanceCenterAsset) error {
	if a == nil || a.SiteID <= 0 || a.SubscriptionDays < 1 || a.SubscriptionDays > 3660 || math.IsNaN(a.SubscriptionPrice) || math.IsInf(a.SubscriptionPrice, 0) || a.SubscriptionPrice < 0 {
		return errors.New("订阅价格或周期无效")
	}
	if a.ManualBalance != nil && (math.IsNaN(*a.ManualBalance) || math.IsInf(*a.ManualBalance, 0) || *a.ManualBalance < 0) {
		return errors.New("手工现金余额必须是非负数")
	}
	if a.SubscriptionID != nil && *a.SubscriptionID <= 0 {
		return errors.New("订阅 ID 必须为正整数")
	}
	if a.SubscriptionPrice > 0 && a.SubscriptionExpiresAt == nil && !a.SubscriptionAutoSync {
		return errors.New("手工订阅需要到期时间")
	}
	current, err := s.GetAsset(ctx, a.SiteID)
	if err != nil {
		return err
	}
	// 仅对已实际核对接口的站点启用自动同步，其余站点保留手工控制。
	if a.SubscriptionAutoSync && (current.Domain != "sub.anzhiyu.com" || a.SubscriptionID == nil) {
		return errors.New("自动同步仅支持鱼鱼站点，且必须指定订阅 ID")
	}
	r, err := s.assetRepository()
	if err != nil {
		return err
	}
	return r.SaveBalanceCenterAsset(ctx, a)
}

// 保存额度观察和提醒时使用同一事务，避免多实例同步或故障重试重复发信。
func (s *BalanceCenterService) SaveSubscriptionObservation(ctx context.Context, siteID, subscriptionID int64, o BalanceCenterSubscriptionObservation, observed time.Time, reason string) error {
	r, err := s.assetRepository()
	if err != nil {
		return err
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	var messages []AlertEmailOutboxInput
	if reason == "" && o.RemainingUSD != nil && *o.RemainingUSD < 1 && o.WindowKey != "" && settings.EmailEnabled {
		recipients, err := s.balanceCenterAlertRecipients(ctx)
		if err != nil {
			return err
		}
		for _, recipient := range recipients {
			messages = append(messages, AlertEmailOutboxInput{
				SourceType: "balance_center_subscription", SourceID: fmt.Sprint(siteID), SourceKey: fmt.Sprintf("site:%d:%s", siteID, o.WindowKey),
				AlertType: "subscription_quota_low", Recipient: recipient,
				Subject:   "[余额中心]鱼鱼订阅额度不足，请及时重置",
				BodyHTML:  fmt.Sprintf(`<h2>鱼鱼订阅额度不足</h2><p>当前日剩余额度：$%.4f；提醒阈值：低于 $1。</p><p>请前往 <a href="https://sub.anzhiyu.com/subscriptions">鱼鱼订阅页</a>手动重置。每次手动重置会扣减 1 天有效期，本系统不会自动重置。</p><p>检测时间：%s</p>`, *o.RemainingUSD, html.EscapeString(observed.Format(time.RFC3339))),
				CreatedAt: observed, AvailableAt: observed,
			})
		}
	}
	return r.SaveBalanceCenterSubscriptionObservation(ctx, siteID, subscriptionID, o, observed, reason, messages)
}
