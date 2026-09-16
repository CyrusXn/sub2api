package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const upstreamSubscriptionRateKey = "upstream_subscription_rate"

// 分组倍率由管理员按当前 API Key 配置，不从可能变化的现金倍率推断。
type UpstreamSubscriptionRateConfig struct {
	Enabled         bool    `json:"enabled"`
	SubscriptionID  int64   `json:"subscription_id"`
	Price           float64 `json:"price"`
	QuotaUSD        float64 `json:"quota_usd"`
	GroupMultiplier float64 `json:"group_multiplier"`
	KeyFingerprint  string  `json:"key_fingerprint,omitempty"`
}

type UpstreamSubscriptionRateObservation struct {
	KeyFingerprint    string    `json:"key_fingerprint"`
	SubscriptionID    int64     `json:"subscription_id"`
	ConfigFingerprint string    `json:"config_fingerprint"`
	Rate              float64   `json:"rate"`
	RemainingUSD      *float64  `json:"remaining_usd,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
	ObservedAt        time.Time `json:"observed_at"`
	FreshUntil        time.Time `json:"fresh_until"`
	Error             string    `json:"error,omitempty"`
}

func subscriptionRateKeyFingerprint(a *Account) string {
	if a == nil || a.GetCredential("api_key") == "" {
		return ""
	}
	host, _, err := normalizeUpstreamSite(a.GetCredential("base_url"))
	if err != nil || host != "sub.anzhiyu.com" || a.Type != AccountTypeAPIKey {
		return ""
	}
	sum := sha256.Sum256([]byte(host + "\x00" + a.GetCredential("api_key")))
	return hex.EncodeToString(sum[:])
}

func subscriptionRateConfig(a *Account) *UpstreamSubscriptionRateConfig {
	if a == nil {
		return nil
	}
	b, err := json.Marshal(a.Extra[upstreamSubscriptionRateKey])
	if err != nil {
		return nil
	}
	var c UpstreamSubscriptionRateConfig
	if json.Unmarshal(b, &c) != nil || !c.Enabled || c.KeyFingerprint == "" || c.KeyFingerprint != subscriptionRateKeyFingerprint(a) || !validSubscriptionRateConfig(c) {
		return nil
	}
	return &c
}

func validSubscriptionRateConfig(c UpstreamSubscriptionRateConfig) bool {
	if c.SubscriptionID <= 0 {
		return false
	}
	for _, v := range []float64{c.Price, c.QuotaUSD, c.GroupMultiplier, c.Price / c.QuotaUSD * c.GroupMultiplier} {
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func subscriptionRateConfigFingerprint(c *UpstreamSubscriptionRateConfig) string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func configureSubscriptionRate(account *Account, config *UpstreamSubscriptionRateConfig) error {
	if config == nil {
		return nil
	}
	c := *config
	c.KeyFingerprint = subscriptionRateKeyFingerprint(account)
	if c.Enabled && (c.KeyFingerprint == "" || !validSubscriptionRateConfig(c)) {
		return infraerrors.BadRequest("INVALID_SUBSCRIPTION_RATE", "请为鱼鱼 API Key 填写有效的订阅 ID、套餐价格、总额度和分组倍率")
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[upstreamSubscriptionRateKey] = c
	return nil
}

// SubscriptionRateMultiplier 仅接受显式开启、凭据匹配且近期确认仍有额度的订阅。
func (a *Account) SubscriptionRateMultiplier(now time.Time) (float64, bool) {
	c := subscriptionRateConfig(a)
	if c == nil || !upstreamBillingProbeEnabled(a) {
		return 0, false
	}
	snapshot := decodeUpstreamBillingProbeSnapshot(a.Extra)
	if snapshot == nil || snapshot.Subscription == nil {
		return 0, false
	}
	o := snapshot.Subscription
	if o.Error != "" || o.KeyFingerprint != c.KeyFingerprint || o.SubscriptionID != c.SubscriptionID || o.ConfigFingerprint != subscriptionRateConfigFingerprint(c) || o.RemainingUSD == nil || math.IsNaN(*o.RemainingUSD) || math.IsInf(*o.RemainingUSD, 0) || *o.RemainingUSD <= 0 || now.Before(o.ObservedAt) || !now.Before(o.FreshUntil) || !now.Before(o.ExpiresAt) {
		return 0, false
	}
	return c.Price / c.QuotaUSD * c.GroupMultiplier, true
}

func (s *UpstreamBillingProbeService) observeSubscriptionRate(ctx context.Context, account *Account, snapshot *UpstreamBillingProbeSnapshot) {
	c := subscriptionRateConfig(account)
	if c == nil {
		return
	}
	if s.accountTestService == nil {
		return
	}
	now := snapshot.LastAttemptAt
	o, reason := s.fetchBalanceCenterSubscription(ctx, account, c.SubscriptionID, now)
	snapshot.Subscription = &UpstreamSubscriptionRateObservation{
		KeyFingerprint: c.KeyFingerprint, SubscriptionID: c.SubscriptionID,
		ConfigFingerprint: subscriptionRateConfigFingerprint(c), Rate: c.Price / c.QuotaUSD * c.GroupMultiplier,
		RemainingUSD: o.RemainingUSD, ExpiresAt: o.ExpiresAt, ObservedAt: now, FreshUntil: now.Add(2 * time.Minute), Error: reason,
	}
	// 订阅额度可能先于现金余额耗尽，单独缩短已配置账号的探测间隔。
	snapshot.NextProbeAt = now.Add(time.Minute)
}
