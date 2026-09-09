package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type balanceCenterSubscriptionStore interface {
	ListAssets(context.Context) ([]BalanceCenterAsset, error)
	GetAsset(context.Context, int64) (*BalanceCenterAsset, error)
	SaveSubscriptionObservation(context.Context, int64, int64, BalanceCenterSubscriptionObservation, time.Time, string) error
}

// SyncBalanceCenterSubscription 只查询已绑定的订阅，绝不调用重置或续费接口。
func (s *UpstreamBillingProbeService) SyncBalanceCenterSubscription(ctx context.Context, siteID int64) (*BalanceCenterAsset, error) {
	store, ok := s.balanceCenterStore.(balanceCenterSubscriptionStore)
	if !ok {
		return nil, errors.New("订阅资产服务不可用")
	}
	// 在读取配置前记录观察起点，整个请求期间的人工修改都必须优先。
	observed := time.Now().UTC()
	asset, err := store.GetAsset(ctx, siteID)
	if err != nil {
		return nil, err
	}
	if !asset.SubscriptionAutoSync || asset.SubscriptionID == nil || asset.AccountID == nil || asset.Domain != "sub.anzhiyu.com" {
		return nil, errors.New("请先配置鱼鱼订阅 ID 并开启自动同步")
	}
	account, err := s.accountRepo.GetByID(ctx, *asset.AccountID)
	if err != nil {
		return nil, err
	}
	expires, reason := s.fetchBalanceCenterSubscription(ctx, account, *asset.SubscriptionID, observed)
	if err := store.SaveSubscriptionObservation(ctx, siteID, *asset.SubscriptionID, expires, observed, reason); err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, errors.New(reason)
	}
	return store.GetAsset(ctx, siteID)
}

func (s *UpstreamBillingProbeService) fetchBalanceCenterSubscription(ctx context.Context, account *Account, id int64, now time.Time) (BalanceCenterSubscriptionObservation, string) {
	baseURL, err := s.accountTestService.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return BalanceCenterSubscriptionObservation{}, "订阅站点地址无效"
	}
	host, _, err := normalizeUpstreamSite(baseURL)
	if err != nil || host != "sub.anzhiyu.com" {
		return BalanceCenterSubscriptionObservation{}, "订阅绑定账号域名不匹配"
	}
	credential := s.resolveWebAccountCredential(ctx, baseURL)
	if credential == nil {
		return BalanceCenterSubscriptionObservation{}, "请在上游站点账号配置鱼鱼登录信息"
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return BalanceCenterSubscriptionObservation{}, "上游账号代理不可用"
		}
		proxyURL = account.Proxy.URL()
	}
	var profile *tlsfingerprint.Profile
	if s.accountTestService.tlsFPProfileService != nil {
		profile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	token, _, reason, _ := s.webAccountAccessToken(ctx, account, baseURL, credential.Username, credential.Password, proxyURL, profile, now)
	if reason != "" {
		return BalanceCenterSubscriptionObservation{}, "鱼鱼登录失败，请检查上游站点账号配置"
	}
	// 此路径及 expires_at/id 字段已通过用户打开的订阅页实际响应核对。
	req, cancel, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/v1/subscriptions", nil, nil)
	if err != nil {
		return BalanceCenterSubscriptionObservation{}, "构建订阅查询失败"
	}
	defer cancel()
	req.Header.Set("Authorization", "Bearer "+token)
	_, _, reason, _, body := s.doWebAccountRequest(account, req, proxyURL, profile, now)
	if reason != "" {
		if reason == "web_auth_failed" {
			s.clearWebAccountToken(baseURL, credential.Username)
		}
		return BalanceCenterSubscriptionObservation{}, "订阅查询失败，下次同步重试；可切换手工控制"
	}
	return parseBalanceCenterSubscriptionObservation(body, id, now)
}

// 额度字段来自鱼鱼当前订阅页面；缺失字段保留未知，绝不能按零额度报警。
type BalanceCenterSubscriptionObservation struct {
	ExpiresAt    time.Time
	RemainingUSD *float64
	WindowKey    string
}

func parseBalanceCenterSubscription(body []byte, id int64) (time.Time, string) {
	o, reason := parseBalanceCenterSubscriptionObservation(body, id, time.Now())
	return o.ExpiresAt, reason
}
func parseBalanceCenterSubscriptionObservation(body []byte, id int64, now time.Time) (BalanceCenterSubscriptionObservation, string) {
	var root struct {
		Code int `json:"code"`
		Data []struct {
			ID         int64      `json:"id"`
			ExpiresAt  time.Time  `json:"expires_at"`
			Status     string     `json:"status"`
			DailyUsage *float64   `json:"daily_usage_usd"`
			Window     *time.Time `json:"daily_window_start"`
			Group      struct {
				Limit *float64 `json:"daily_limit_usd"`
			} `json:"group"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &root); err != nil || root.Code != 0 {
		return BalanceCenterSubscriptionObservation{}, "订阅响应格式无效"
	}
	for _, item := range root.Data {
		if item.ID != id || item.ExpiresAt.IsZero() {
			continue
		}
		o := BalanceCenterSubscriptionObservation{ExpiresAt: item.ExpiresAt}
		if item.Status == "active" && now.Before(item.ExpiresAt) && item.DailyUsage != nil && item.Group.Limit != nil && item.Window != nil && !item.Window.IsZero() && !item.Window.After(now) {
			limit, used := *item.Group.Limit, *item.DailyUsage
			if limit > 0 && used >= 0 && !math.IsNaN(limit) && !math.IsInf(limit, 0) && !math.IsNaN(used) && !math.IsInf(used, 0) {
				remaining := math.Max(0, limit-used)
				o.RemainingUSD = &remaining
				// 重置会更新日窗口并扣减到期时间；两者共同标识一次可用额度，重启也不重复提醒。
				o.WindowKey = fmt.Sprintf("%d:%s:%s", id, item.Window.UTC().Format(time.RFC3339Nano), item.ExpiresAt.UTC().Format(time.RFC3339Nano))
			}
		}
		return o, ""
	}
	return BalanceCenterSubscriptionObservation{}, "未找到绑定订阅，请核对订阅 ID；不自动切换到其他套餐"
}

func (s *UpstreamBillingProbeService) syncDueBalanceCenterSubscriptions(ctx context.Context) {
	store, ok := s.balanceCenterStore.(balanceCenterSubscriptionStore)
	if !ok {
		return
	}
	// 按站点调度，成功或失败均间隔一分钟，不依赖请求或某个账号的探测开关。
	assets, err := store.ListAssets(ctx)
	if err != nil {
		return
	}
	for _, a := range assets {
		if a.SubscriptionAutoSync && a.AccountID != nil && (a.SubscriptionSyncedAt == nil || time.Since(*a.SubscriptionSyncedAt) >= time.Minute) {
			_, _ = s.SyncBalanceCenterSubscription(ctx, a.SiteID)
		}
	}
}
