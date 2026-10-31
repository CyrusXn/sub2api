//go:build unit

package service

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEstimateInflightReservationCost(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	pricing, err := billing.GetModelPricing("claude-sonnet-4-5")
	require.NoError(t, err)

	cfg := config.InflightReservationConfig{DefaultMaxTokens: 8192, MaxOutputTokens: 64000, MaxInputTokens: 1000}

	// explicit max_tokens; body 400 bytes → 100 input tokens
	cost, ok := EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 400, 1000, 1)
	require.True(t, ok)
	require.InDelta(t, 100*pricing.InputPricePerToken+1000*pricing.OutputPricePerToken, cost, 1e-12)

	// missing max_tokens → default; rate multiplier applied
	cost, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 0, 0, 2)
	require.True(t, ok)
	require.InDelta(t, 2*8192*pricing.OutputPricePerToken, cost, 1e-12)

	// caps: input and output clamped
	cost, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 1_000_000, 1_000_000, 1)
	require.True(t, ok)
	require.InDelta(t, 1000*pricing.InputPricePerToken+64000*pricing.OutputPricePerToken, cost, 1e-12)

	// free group / missing billing → fail open
	_, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 10, 10, 0)
	require.False(t, ok)
	_, ok = EstimateInflightReservationCost(nil, cfg, "claude-sonnet-4-5", 10, 10, 1)
	require.False(t, ok)
	_, ok = EstimateInflightReservationCost(billing, cfg, "", 10, 10, 1)
	require.False(t, ok)
}

func TestReserveInflightBalance_NoReservationCacheFailsOpen(t *testing.T) {
	cfg := &config.Config{}
	cfg.Billing.InflightReservation.Enabled = true
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	release, err := svc.ReserveInflightBalance(context.Background(), &User{ID: 1}, nil, nil, 100)
	require.NoError(t, err)
	release()
}

func TestReserveInflightBalance_SimpleModeDisabled(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Billing.InflightReservation.Enabled = true
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	require.False(t, svc.InflightReservationEnabled())
}

// ---------------------------------------------------------------------------
// estimate: same model / resolver as billing
// ---------------------------------------------------------------------------

func newInflightEstimateGateway(t *testing.T, channelService *ChannelService) *GatewayService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, DefaultMaxTokens: 1000, MaxInputTokens: 200000, MaxOutputTokens: 128000}
	billing := NewBillingService(cfg, nil)
	svc := &GatewayService{
		cfg:            cfg,
		billingService: billing,
		resolver:       NewModelPricingResolver(channelService, billing),
		channelService: channelService,
	}
	// 与正式构造器一样复用费率解析器，避免每次估算都创建缓存清理协程。
	svc.userGroupRateResolver = newUserGroupRateResolver(nil, nil, resolveUserGroupRateCacheTTL(cfg), &svc.userGroupRateSF, "service.gateway")
	svc.userGroupRateCache = svc.userGroupRateResolver.cache
	return svc
}

func TestInflightEstimate_ChannelAliasUsesMappedModel(t *testing.T) {
	groupID := int64(10)
	ch := Channel{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{groupID},
		ModelMapping: map[string]map[string]string{
			"anthropic": {"my-alias": "claude-sonnet-4-5"},
		},
	}
	cs := newTestChannelService(makeStandardRepo(ch, map[int64]string{groupID: "anthropic"}))
	svc := newInflightEstimateGateway(t, cs)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}

	// Old estimate (client model, base pricing only) → 0 → reservation bypassed.
	_, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "my-alias", 4000, 1000, 1)
	require.False(t, ok, "precondition: alias has no base pricing")

	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "my-alias", BodyBytes: 4000, MaxTokens: 1000})
	require.True(t, priced)
	require.Greater(t, est, 0.0)

	direct, _ := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "claude-sonnet-4-5", BodyBytes: 4000, MaxTokens: 1000})
	require.InDelta(t, direct, est, 1e-12, "alias must be estimated as the channel-mapped billing model")
}

func TestInflightEstimate_GroupPerRequestPricing(t *testing.T) {
	groupID := int64(20)
	price := 0.5
	group := &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{
		{Models: []string{"custom-per-request"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price},
	}}
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: group}

	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "custom-per-request", BodyBytes: 100})
	require.True(t, priced)
	require.InDelta(t, 1.0, est, 1e-12, "per_request 0.5 × group rate 2")

	// Wildcard group token pricing is honored as well.
	in, out := 1e-6, 2e-6
	group.ModelPricing = []ChannelModelPricing{{Models: []string{"wild-*"}, BillingMode: BillingModeToken, InputPrice: &in, OutputPrice: &out}}
	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "wild-x", BodyBytes: 4000, MaxTokens: 500})
	require.True(t, priced)
	require.InDelta(t, (1000*in+500*out)*2, est, 1e-12)
}

func TestInflightEstimate_UnpricedIsReportedAndNonMeteredIsNot(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}}
	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "totally-unknown-model-xyz"})
	require.False(t, priced)
	require.Zero(t, est)

	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{})
	require.True(t, priced, "non-metered requests (e.g. media status lookups) are not 'unpriced'")
	require.Zero(t, est)
}

func TestInflightEstimate_MediaKinds(t *testing.T) {
	groupID := int64(30)
	p4k := 0.3
	search := 1000.0
	group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1, ImagePrice4K: &p4k, SearchPricePer1k: &search}
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: group}

	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "gpt-image-1", Kind: InflightEstimateImage, Units: 2})
	require.True(t, priced)
	require.GreaterOrEqual(t, est, 0.6, "image estimate uses the highest size tier × n")

	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "grok-web-search", Kind: InflightEstimatePerRequest, SearchCalls: 1})
	require.True(t, priced)
	require.InDelta(t, 1.0, est, 1e-12)

	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "realtime", Kind: InflightEstimateAudio, AudioMode: "realtime", AudioUnits: 1})
	require.True(t, priced)
	require.Greater(t, est, 0.0)
}

func TestInflightEstimate_AdminSettlementMultiplier(t *testing.T) {
	for _, gateway := range []string{"anthropic", "openai"} {
		for _, tc := range []struct {
			name       string
			user       *float64
			group      float64
			configured bool
			want       float64
		}{
			{name: "旧分组未加载倍率", want: 1},
			{name: "分组免费", configured: true, want: 0},
			{name: "分组半价", group: 0.5, configured: true, want: 0.5},
			{name: "分组双倍", group: 2, configured: true, want: 2},
			{name: "用户免费优先分组", user: inflightMultiplier(0), group: 2, configured: true, want: 0},
			{name: "用户半价优先分组", user: inflightMultiplier(0.5), group: 2, configured: true, want: 0.5},
			{name: "用户双倍优先免费分组", user: inflightMultiplier(2), configured: true, want: 2},
		} {
			t.Run(gateway+"/"+tc.name, func(t *testing.T) {
				svc := newInflightEstimateGateway(t, nil)
				groupID := int64(50)
				apiKey := &APIKey{User: &User{ID: 1, AdminUsageMultiplier: tc.user}, GroupID: &groupID, Group: &Group{
					ID: groupID, Platform: gateway, RateMultiplier: 3, AdminUsageMultiplier: tc.group, AdminUsageMultiplierConfigured: tc.configured,
				}}
				req := InflightEstimateRequest{Model: "claude-sonnet-4-5", BodyBytes: 400, MaxTokens: 100}
				base, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, req.Model, req.BodyBytes, req.MaxTokens, 3)
				require.True(t, ok)
				var cost float64
				if gateway == "openai" {
					openai := &OpenAIGatewayService{cfg: svc.cfg, billingService: svc.billingService, resolver: svc.resolver}
					cost, ok = openai.EstimateInflightReservation(context.Background(), apiKey, req)
				} else {
					cost, ok = svc.EstimateInflightReservation(context.Background(), apiKey, req)
				}
				require.True(t, ok)
				require.InDelta(t, base*tc.want, cost, 1e-12)
			})
		}
	}
}

func inflightMultiplier(value float64) *float64 { return &value }

func TestInflightEstimate_CandidateAccountSettlementMultiplier(t *testing.T) {
	for _, gateway := range []string{PlatformAnthropic, PlatformOpenAI} {
		for _, tc := range []struct {
			name  string
			rates []*float64
			want  float64
		}{
			{name: "旧账号默认一", rates: []*float64{nil}, want: 1},
			{name: "全部账号免费", rates: []*float64{inflightMultiplier(0), inflightMultiplier(0)}, want: 0},
			{name: "全部账号半价", rates: []*float64{inflightMultiplier(0.5)}, want: 0.5},
			{name: "账号双倍", rates: []*float64{inflightMultiplier(2)}, want: 2},
			{name: "混合取最大值", rates: []*float64{inflightMultiplier(0), nil, inflightMultiplier(0.5), inflightMultiplier(2)}, want: 2},
			{name: "非法负数沿账号默认", rates: []*float64{inflightMultiplier(-1)}, want: 1},
		} {
			t.Run(gateway+"/"+tc.name, func(t *testing.T) {
				svc := newInflightEstimateGateway(t, nil)
				groupID := int64(51)
				accounts := make([]Account, len(tc.rates))
				for i, rate := range tc.rates {
					accounts[i] = Account{ID: int64(i + 1), Platform: gateway, AdminUsageMultiplier: rate}
				}
				snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, gateway): accounts}}
				attachInflightSnapshot(svc, snap)
				apiKey := &APIKey{User: &User{ID: 1, AdminUsageMultiplier: inflightMultiplier(0.5)}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: gateway, RateMultiplier: 3}}
				req := InflightEstimateRequest{Model: "claude-sonnet-4-5", BodyBytes: 400, MaxTokens: 100}
				base, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, req.Model, req.BodyBytes, req.MaxTokens, 3)
				require.True(t, ok)
				var cost float64
				if gateway == PlatformOpenAI {
					openai := &OpenAIGatewayService{cfg: svc.cfg, billingService: svc.billingService, resolver: svc.resolver, schedulerSnapshot: svc.schedulerSnapshot}
					cost, ok = openai.EstimateInflightReservation(context.Background(), apiKey, req)
				} else {
					cost, ok = svc.EstimateInflightReservation(context.Background(), apiKey, req)
				}
				require.True(t, ok)
				require.InDelta(t, base*0.5*tc.want, cost, 1e-12)
				require.Equal(t, int64(1), snap.reads.Load(), "定价及附加倍率共用一次账号快照")
			})
		}
	}
}

func TestInflightEstimate_UnknownAccountMultiplierDoesNotMeanFree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accounts []Account
		cancel   bool
	}{
		{name: "空候选"},
		{name: "免费账号不支持当前模型", accounts: []Account{{Platform: PlatformAnthropic, AdminUsageMultiplier: inflightMultiplier(0), Credentials: map[string]any{"model_mapping": map[string]any{"other-model": "claude-sonnet-4-5"}}}}},
		{name: "快照失败", cancel: true},
		{name: "非有限账号倍率", accounts: []Account{{Platform: PlatformAnthropic, AdminUsageMultiplier: inflightMultiplier(math.NaN())}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newInflightEstimateGateway(t, nil)
			groupID := int64(52)
			snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformAnthropic): tc.accounts}}
			attachInflightSnapshot(svc, snap)
			apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			cost, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "claude-sonnet-4-5", BodyBytes: 400, MaxTokens: 100})
			require.False(t, priced, "未知候选倍率遵循无法估算契约，而不是声明免费")
			require.Zero(t, cost)
		})
	}
}

func TestInflightEstimate_AccountMappingAndMultiplierShareSnapshot(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	groupID := int64(53)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformAnthropic): {
		{ID: 1, Platform: PlatformAnthropic, AdminUsageMultiplier: inflightMultiplier(0.5), Credentials: map[string]any{"model_mapping": map[string]any{"billing-alias": "claude-opus-4-1"}}},
		{ID: 2, Platform: PlatformAnthropic, AdminUsageMultiplier: inflightMultiplier(3), Credentials: map[string]any{"model_mapping": map[string]any{"billing-alias": "claude-sonnet-4-5"}}},
		{ID: 3, Platform: PlatformAnthropic, AdminUsageMultiplier: inflightMultiplier(100), Credentials: map[string]any{"model_mapping": map[string]any{"other-alias": "claude-opus-4-1"}}},
	}}}
	attachInflightSnapshot(svc, snap)
	apiKey := &APIKey{User: &User{ID: 1, AdminUsageMultiplier: inflightMultiplier(0.5)}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 3}}
	req := InflightEstimateRequest{Model: "billing-alias", BodyBytes: 400, MaxTokens: 100}
	cost, ok := svc.EstimateInflightReservation(context.Background(), apiKey, req)
	require.True(t, ok)
	opus, priced := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "claude-opus-4-1", req.BodyBytes, req.MaxTokens, 3)
	require.True(t, priced)
	require.InDelta(t, opus*0.5*3, cost, 1e-12, "只按支持模型的账号取最高倍率与最高模型价")
	require.Equal(t, int64(1), snap.reads.Load(), "倍率和映射不能重复读快照")
}

func TestInflightEstimate_ResolvedPlatformUsesMatchingAccountPool(t *testing.T) {
	for _, tc := range []struct {
		name     string
		platform string
		openai   bool
	}{
		{name: "TypeSafe分组保留平台", platform: PlatformTypeSafe},
		{name: "综合分组使用路由平台", platform: PlatformDeepseek, openai: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newInflightEstimateGateway(t, nil)
			groupID := int64(54)
			snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, tc.platform): {
				{ID: 1, Platform: tc.platform, AdminUsageMultiplier: inflightMultiplier(0.5), Credentials: map[string]any{"model_mapping": map[string]any{"route-alias": "route-alias"}}},
			}}}
			attachInflightSnapshot(svc, snap)
			price := 2.0
			apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: tc.platform, RateMultiplier: 1,
				ModelPricing: []ChannelModelPricing{{Models: []string{"route-alias"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}},
			}}
			ctx := context.Background()
			var cost float64
			var ok bool
			if tc.openai {
				apiKey.Group.Platform = PlatformComposite
				ctx = WithResolvedTargetPlatform(ctx, tc.platform)
				openai := &OpenAIGatewayService{cfg: svc.cfg, billingService: svc.billingService, resolver: svc.resolver, schedulerSnapshot: svc.schedulerSnapshot}
				cost, ok = openai.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "route-alias"})
			} else {
				cost, ok = svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "route-alias"})
			}
			require.True(t, ok)
			require.InDelta(t, 1.0, cost, 1e-12)
			require.Equal(t, int64(1), snap.reads.Load())
		})
	}
}

func TestInflightEstimate_AnthropicSelectionUsesRequestedModelBeforeChannelMapping(t *testing.T) {
	groupID := int64(55)
	cs := newTestChannelService(makeStandardRepo(Channel{
		ID: 1, Status: StatusActive, GroupIDs: []int64{groupID},
		ModelMapping: map[string]map[string]string{PlatformAnthropic: {"public-alias": "claude-sonnet-4-5"}},
	}, map[int64]string{groupID: PlatformAnthropic}))
	svc := newInflightEstimateGateway(t, cs)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformAnthropic): {
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(0), Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}}},
		{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(2), Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": "claude-sonnet-4-5"}}},
	}}}
	attachInflightSnapshot(svc, snap)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}
	req := InflightEstimateRequest{Model: "public-alias", BodyBytes: 400, MaxTokens: 100}
	cost, priced := svc.EstimateInflightReservation(context.Background(), apiKey, req)
	require.True(t, priced)
	base, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "claude-sonnet-4-5", req.BodyBytes, req.MaxTokens, 1)
	require.True(t, ok)
	require.InDelta(t, base*2, cost, 1e-12, "Anthropic选号先按public model，不能只看到channel mapped免费账号")
	require.Equal(t, int64(1), snap.reads.Load())
}

func TestInflightEstimate_CompositeWSUsesResolvedUpstreamSelectionModel(t *testing.T) {
	groupID := int64(56)
	svc := newInflightEstimateGateway(t, nil)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformOpenAI): {
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(0), Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": "gpt-5"}}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(2), Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5": "gpt-5"}}},
	}}}
	attachInflightSnapshot(svc, snap)
	openai := &OpenAIGatewayService{cfg: svc.cfg, billingService: svc.billingService, resolver: svc.resolver, schedulerSnapshot: svc.schedulerSnapshot}
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1}}
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, Source: CompositeRouteSourceExplicit, PublicModel: "public-alias", TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5",
	})
	req := InflightEstimateRequest{Model: "public-alias", BodyBytes: 400, MaxTokens: 100}
	cost, priced := openai.EstimateInflightReservation(ctx, apiKey, req)
	require.True(t, priced)
	base, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "gpt-5", req.BodyBytes, req.MaxTokens, 1)
	require.True(t, ok)
	require.InDelta(t, base*2, cost, 1e-12, "显式route的公共alias不参与真实WS选号，不能据其免费账号跳过预留")
	require.Equal(t, int64(1), snap.reads.Load())
}

func TestInflightEstimate_OpenAISelectionCandidatesFreeAndUnmatched(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mappings []string
		priced   bool
	}{
		{name: "全部可能选号模型免费", mappings: []string{"public-alias", "gpt-5"}, priced: true},
		{name: "没有匹配不能当免费", mappings: []string{"other-model"}, priced: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newInflightEstimateGateway(t, nil)
			groupID := int64(57)
			accounts := make([]Account, len(tc.mappings))
			for i, model := range tc.mappings {
				accounts[i] = Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(0), Credentials: map[string]any{"model_mapping": map[string]any{model: "gpt-5"}}}
			}
			snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformOpenAI): accounts}}
			attachInflightSnapshot(svc, snap)
			openai := &OpenAIGatewayService{cfg: svc.cfg, billingService: svc.billingService, resolver: svc.resolver, schedulerSnapshot: svc.schedulerSnapshot}
			apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 1}}
			ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{Matched: true, Source: CompositeRouteSourceExplicit, PublicModel: "public-alias", TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5"})
			cost, priced := openai.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "public-alias", BodyBytes: 400, MaxTokens: 100})
			require.Equal(t, tc.priced, priced)
			require.Zero(t, cost)
			require.Equal(t, int64(1), snap.reads.Load())
		})
	}
}

func TestInflightEstimate_CapabilitySelectionDoesNotUseBillingModelFilter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		openai bool
		req    InflightEstimateRequest
	}{
		{name: "实时语音能力型全池", openai: true, req: InflightEstimateRequest{Model: "grok-voice-latest", Kind: InflightEstimateAudio, AudioMode: "realtime", AudioUnits: 1}},
		{name: "TTS文本模型选号", openai: true, req: InflightEstimateRequest{Model: "tts", Kind: InflightEstimateAudio, AudioMode: "tts", AudioUnits: 1}},
		{name: "STT文本模型选号", openai: true, req: InflightEstimateRequest{Model: "stt", Kind: InflightEstimateAudio, AudioMode: "stt", AudioUnits: 1}},
		{name: "独立网页搜索运行时模型选号", req: InflightEstimateRequest{Model: "grok-web-search", Kind: InflightEstimatePerRequest, SearchCalls: 1}},
		{name: "独立X搜索运行时模型选号", req: InflightEstimateRequest{Model: "grok-x-search", Kind: InflightEstimatePerRequest, SearchCalls: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newInflightEstimateGateway(t, nil)
			groupID := int64(58)
			apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformGrok, RateMultiplier: 1}}
			base, priced := svc.EstimateInflightReservation(context.Background(), apiKey, tc.req)
			require.True(t, priced)
			require.Greater(t, base, 0.0)
			snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformGrok): {
				{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(0), Credentials: map[string]any{"model_mapping": map[string]any{tc.req.Model: tc.req.Model}}},
				{ID: 2, Platform: PlatformGrok, Type: AccountTypeAPIKey, AdminUsageMultiplier: inflightMultiplier(2), Credentials: map[string]any{"model_mapping": map[string]any{"grok-4.5": "grok-4.5"}}},
			}}}
			attachInflightSnapshot(svc, snap)
			var cost float64
			if tc.openai {
				openai := &OpenAIGatewayService{cfg: svc.cfg, billingService: svc.billingService, resolver: svc.resolver, schedulerSnapshot: svc.schedulerSnapshot}
				cost, priced = openai.EstimateInflightReservation(context.Background(), apiKey, tc.req)
			} else {
				cost, priced = svc.EstimateInflightReservation(context.Background(), apiKey, tc.req)
			}
			require.True(t, priced)
			require.InDelta(t, base*2, cost, 1e-12, "计费标识不等于能力型入口的真实选号模型，须覆盖全池倍率")
			require.Equal(t, int64(1), snap.reads.Load())
		})
	}
}

func TestInflightReservation_DisabledAndSubscriptionRemainUnreserved(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 60)
	user := &User{ID: 1, AdminUsageMultiplier: inflightMultiplier(2)}
	svc.cfg.Billing.InflightReservation.Enabled = false
	reservation, err := svc.ReserveInflight(context.Background(), user, nil, nil, 100)
	require.NoError(t, err)
	require.Nil(t, reservation)
	svc.cfg.Billing.InflightReservation.Enabled = true
	reservation, err = svc.ReserveInflight(context.Background(), user, &Group{SubscriptionType: SubscriptionTypeSubscription}, &UserSubscription{}, 100)
	require.NoError(t, err)
	require.Nil(t, reservation)
	require.Zero(t, cache.count())
}

// ---------------------------------------------------------------------------
// reservation handle: hand-off to billing task, renewal
// ---------------------------------------------------------------------------

type memInflightCache struct {
	BillingCache
	mu        sync.Mutex
	balance   float64
	res       map[string]float64
	exp       map[string]time.Time
	renews    atomic.Int32
	deducts   atomic.Int32
	releaseCt atomic.Int32
}

func newMemInflightCache(balance float64) *memInflightCache {
	return &memInflightCache{balance: balance, res: map[string]float64{}, exp: map[string]time.Time{}}
}

func (m *memInflightCache) GetUserBalance(context.Context, int64) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balance, nil
}

func (m *memInflightCache) DeductUserBalance(_ context.Context, _ int64, amount float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balance -= amount
	m.deducts.Add(1)
	return nil
}

func (m *memInflightCache) gc() {
	now := time.Now()
	for id, e := range m.exp {
		if !e.After(now) {
			delete(m.exp, id)
			delete(m.res, id)
		}
	}
}

func (m *memInflightCache) ReserveInflightBalance(_ context.Context, _ int64, id string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gc()
	sum := 0.0
	for _, v := range m.res {
		sum += v
	}
	if len(m.res) > 0 && balance-sum < amount {
		return false, sum, nil
	}
	m.res[id] = amount
	m.exp[id] = time.Now().Add(ttl)
	return true, sum, nil
}

func (m *memInflightCache) ReleaseInflightBalance(_ context.Context, _ int64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.res, id)
	delete(m.exp, id)
	m.releaseCt.Add(1)
	return nil
}

func (m *memInflightCache) RenewInflightBalance(_ context.Context, _ int64, id string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renews.Add(1)
	if _, ok := m.res[id]; !ok {
		return false, nil
	}
	m.exp[id] = time.Now().Add(ttl)
	return true, nil
}

func (m *memInflightCache) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gc()
	return len(m.res)
}

func newInflightSvc(t *testing.T, cache BillingCache, ttlSeconds int) *BillingCacheService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: ttlSeconds}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	return svc
}

func TestInflightReservation_HeldUntilBillingTaskDone(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 60)
	user := &User{ID: 1}

	res, err := svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.NoError(t, err)
	require.NotNil(t, res)
	taskDone := res.Acquire() // billing task submitted
	res.HandlerDone()         // handler returned
	require.Equal(t, 1, cache.count(), "reservation must survive handler return while billing is pending")

	_, err = svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.ErrorIs(t, err, ErrInsufficientBalance)

	taskDone()
	taskDone() // idempotent
	require.Equal(t, 0, cache.count())
	require.Equal(t, int32(1), cache.releaseCt.Load(), "released exactly once")

	// No billing task → HandlerDone releases immediately; Acquire after release is a no-op.
	res2, err := svc.ReserveInflight(context.Background(), user, nil, nil, 0.1)
	require.NoError(t, err)
	res2.HandlerDone()
	res2.HandlerDone()
	require.Equal(t, 0, cache.count())
	res2.Acquire()()
	require.Equal(t, int32(2), cache.releaseCt.Load())

	// Nil handle is safe.
	var nilRes *InflightReservation
	nilRes.Acquire()()
	nilRes.HandlerDone()
	nilRes.Release()
}

func TestInflightReservation_RenewedWhileHandlerActive(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 1)
	user := &User{ID: 2}

	res, err := svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.NoError(t, err)
	time.Sleep(2500 * time.Millisecond) // > 2 × TTL
	require.Equal(t, 1, cache.count(), "renewal keeps the reservation alive past its TTL")
	require.Greater(t, cache.renews.Load(), int32(3))
	_, err = svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.ErrorIs(t, err, ErrInsufficientBalance)

	done := res.Acquire()
	res.HandlerDone()
	renewsAtStop := cache.renews.Load()
	time.Sleep(1200 * time.Millisecond)
	require.Equal(t, renewsAtStop, cache.renews.Load(), "no renewal after handler end")
	require.Equal(t, 0, cache.count(), "post-handler hold is bounded by TTL")
	done()
}

func TestSyncBalanceCacheAfterDeduction_SynchronousWhenInflightEnabled(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 60)
	p := &postUsageBillingParams{Cost: &CostBreakdown{ActualCost: 0.25}, User: &User{ID: 3}}
	syncBalanceCacheAfterDeduction(context.Background(), p, &billingDeps{billingCacheService: svc}, nil)
	require.Equal(t, int32(1), cache.deducts.Load(), "cache deduction must land before the billing task returns")
	bal, _ := cache.GetUserBalance(context.Background(), 3)
	require.InDelta(t, 0.75, bal, 1e-12)
}

// 计费侧：requested 来源下别名本身无价 → billableModelWithFallback 回退到映射模型。
// 准入估算必须同口径地 > 0，且等于按回退模型的估算。
func TestInflightEstimate_RequestedSourceUnpricedAliasFallsBackLikeBilling(t *testing.T) {
	groupID := int64(30)
	ch := Channel{
		ID:                 3,
		Status:             StatusActive,
		GroupIDs:           []int64{groupID},
		BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{
			"anthropic": {"req-alias": "claude-sonnet-4-5"},
		},
	}
	cs := newTestChannelService(makeStandardRepo(ch, map[int64]string{groupID: "anthropic"}))
	svc := newInflightEstimateGateway(t, cs)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}
	ctx := context.Background()

	billed := svc.billableModelWithFallback(ctx, apiKey, "req-alias", "claude-sonnet-4-5", "req-alias")
	require.Equal(t, "claude-sonnet-4-5", billed, "precondition: billing falls back to the mapped model")

	est, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "req-alias", BodyBytes: 4000, MaxTokens: 1000})
	require.True(t, priced)
	require.Greater(t, est, 0.0)
	direct, _ := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: billed, BodyBytes: 4000, MaxTokens: 1000})
	require.InDelta(t, direct, est, 1e-12)
}

// 计费侧：别名仅在账号级映射（无渠道映射）→ UpstreamModel 为账号映射模型，计费回退到它。
// 准入时账号未选定：按分组内候选账号映射模型的最高估算。
func TestInflightEstimate_AccountLevelMappingFallsBackLikeBilling(t *testing.T) {
	groupID := int64(31)
	svc := newInflightEstimateGateway(t, nil)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformAnthropic): {
		{ID: 1, Platform: PlatformAnthropic, Credentials: map[string]any{"model_mapping": map[string]any{"acct-alias-31": "claude-sonnet-4-5"}}},
		{ID: 2, Platform: PlatformAnthropic, Credentials: map[string]any{"model_mapping": map[string]any{"acct-alias-31": "claude-opus-4-1"}}},
	}}}
	attachInflightSnapshot(svc, snap)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}
	ctx := context.Background()

	_, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "acct-alias-31", 4000, 1000, 1)
	require.False(t, ok, "precondition: alias has no pricing")

	est, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "acct-alias-31", BodyBytes: 4000, MaxTokens: 1000})
	require.True(t, priced)
	req := InflightEstimateRequest{BodyBytes: 4000, MaxTokens: 1000}
	best := 0.0
	for _, upstream := range []string{"claude-sonnet-4-5", "claude-opus-4-1"} {
		billed := svc.billableModelWithFallback(ctx, apiKey, "acct-alias-31", upstream, "acct-alias-31")
		require.Equal(t, upstream, billed, "precondition: billing charges the account-mapped model")
		req.Model = billed
		c, _ := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, req.Model, req.BodyBytes, req.MaxTokens, 1)
		require.Greater(t, c, 0.0)
		best = math.Max(best, c)
	}
	require.InDelta(t, best, est, 1e-12, "estimate = max over candidate account-mapped billing models")
}

type inflightSnapshotCacheStub struct {
	SchedulerCache
	mu       sync.Mutex
	byBucket map[string][]Account
	reads    atomic.Int64
}

func inflightBucketKey(groupID int64, platform string) string {
	return fmt.Sprintf("%d|%s", groupID, platform)
}

func (c *inflightSnapshotCacheStub) set(groupID int64, platform string, accounts []Account) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byBucket[inflightBucketKey(groupID, platform)] = accounts
}

func (c *inflightSnapshotCacheStub) GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	c.reads.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.byBucket[inflightBucketKey(bucket.GroupID, bucket.Platform)]
	out := make([]*Account, 0, len(src))
	for i := range src {
		a := src[i]
		out = append(out, &a)
	}
	return out, true, nil
}

// inflightCountingAccountRepo 统计请求路径上的任何直接查库。
type inflightCountingAccountRepo struct {
	AccountRepository
	dbCalls atomic.Int64
}

func (r *inflightCountingAccountRepo) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	r.dbCalls.Add(1)
	return nil, nil
}

func attachInflightSnapshot(svc *GatewayService, snap *inflightSnapshotCacheStub) *inflightCountingAccountRepo {
	repo := &inflightCountingAccountRepo{}
	svc.accountRepo = repo
	svc.schedulerSnapshot = NewSchedulerSnapshotService(snap, nil, repo, nil, svc.cfg)
	return repo
}

// 定价与附加倍率共用一次账号快照；随机模型名不直接查库、不产生按模型名的缓存（内存有界）。
func TestInflightEstimate_AccountMappingNoDBAndBoundedMemory(t *testing.T) {
	groupID := int64(40)
	svc := newInflightEstimateGateway(t, nil)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(groupID, PlatformAnthropic): {
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"acct-alias-40": "claude-sonnet-4-5", "claude-sonnet-4-5": "claude-sonnet-4-5"}}},
	}}}
	repo := attachInflightSnapshot(svc, snap)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		_, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "claude-sonnet-4-5", BodyBytes: 4000, MaxTokens: 1000})
		require.True(t, priced)
	}
	require.Equal(t, int64(100), snap.reads.Load(), "已定价模型也需单次快照读取账号倍率")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	const n = 20000
	for i := 0; i < n; i++ {
		_, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: fmt.Sprintf("rand-%d-%d", i, time.Now().UnixNano()), BodyBytes: 4000, MaxTokens: 1000})
		require.False(t, priced)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	require.Zero(t, repo.dbCalls.Load(), "no direct DB query on the request path")
	require.Equal(t, int64(n+100), snap.reads.Load(), "每次估算只读取一次调度器快照")
	require.Less(t, int64(after.HeapAlloc)-int64(before.HeapAlloc), int64(8<<20), "no per-model cache growth")
}

// 无分组 API Key：使用调度器的未分组账号池，估算与计费回退的账号映射模型同口径。
func TestInflightEstimate_NoGroupKeyUsesUngroupedAccountMapping(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{inflightBucketKey(0, PlatformAnthropic): {
		{ID: 1, Platform: PlatformAnthropic, Credentials: map[string]any{"model_mapping": map[string]any{"acct-alias-nog": "claude-opus-4-1"}}},
	}}}
	attachInflightSnapshot(svc, snap)
	apiKey := &APIKey{User: &User{ID: 1}}
	ctx := context.Background()

	est, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{Model: "acct-alias-nog", BodyBytes: 4000, MaxTokens: 1000})
	require.True(t, priced)
	direct, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "claude-opus-4-1", 4000, 1000, 1)
	require.True(t, ok)
	require.InDelta(t, direct, est, 1e-12)
}

// 管理员新增账号映射后立即生效（不缓存负结果）。
func TestInflightEstimate_AccountMappingNoNegativeCaching(t *testing.T) {
	groupID := int64(41)
	svc := newInflightEstimateGateway(t, nil)
	snap := &inflightSnapshotCacheStub{byBucket: map[string][]Account{}}
	attachInflightSnapshot(svc, snap)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}
	ctx := context.Background()
	req := InflightEstimateRequest{Model: "acct-alias-41", BodyBytes: 4000, MaxTokens: 1000}

	_, priced := svc.EstimateInflightReservation(ctx, apiKey, req)
	require.False(t, priced)
	snap.set(groupID, PlatformAnthropic, []Account{{ID: 9, Platform: PlatformAnthropic, Credentials: map[string]any{"model_mapping": map[string]any{"acct-*": "claude-sonnet-4-5"}}}})
	est, priced := svc.EstimateInflightReservation(ctx, apiKey, req)
	require.True(t, priced, "new mapping (incl. wildcard) visible on next request")
	require.Greater(t, est, 0.0)
}
