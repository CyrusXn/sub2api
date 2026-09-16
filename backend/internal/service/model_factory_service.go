package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ModelFactoryService 按公开分组聚合账号声明的模型，定价复用实际计费解析链。
type ModelFactoryService struct {
	accountRepo AccountRepository
	groupRepo   GroupRepository
	plaza       *ModelPlazaService
}

func NewModelFactoryService(accountRepo AccountRepository, groupRepo GroupRepository, plaza *ModelPlazaService) *ModelFactoryService {
	return &ModelFactoryService{accountRepo: accountRepo, groupRepo: groupRepo, plaza: plaza}
}

func (s *ModelFactoryService) ListGroups(ctx context.Context) ([]PlazaGroup, error) {
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list public groups: %w", err)
	}
	// 批量读取，避免每个分组重复查询账号和绑定关系。
	accounts, err := s.accountRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model accounts: %w", err)
	}
	byGroup := make(map[int64][]*Account)
	for i := range accounts {
		for _, gid := range accounts[i].GroupIDs {
			byGroup[gid] = append(byGroup[gid], &accounts[i])
		}
	}
	out := make([]PlazaGroup, 0, len(groups))
	officialMemo := make(map[string]*PlazaOfficialPricing)
	for i := range groups {
		g := &groups[i]
		if g.IsExclusive {
			continue
		}
		pg := PlazaGroup{
			ID: g.ID, Name: g.Name, Description: g.Description, Platform: g.Platform,
			SubscriptionType: g.SubscriptionType, RateMultiplier: g.RateMultiplier,
			PeakRateEnabled: g.PeakRateEnabled, PeakStart: g.PeakStart, PeakEnd: g.PeakEnd, PeakRateMultiplier: g.PeakRateMultiplier,
			ImageRateIndependent: g.ImageRateIndependent, ImageRateMultiplier: g.ImageRateMultiplier,
			LongContextPricingEnabled: g.LongContextPricingEnabled,
			Models:                    make([]PlazaModel, 0),
		}
		seen := make(map[string]struct{})
		for _, account := range byGroup[g.ID] {
			platform := g.Platform
			if platform == PlatformComposite {
				platform = account.Platform
			}
			for _, name := range g.ModelAllowlist.FilterForListing(factoryAccountModelNames(account)) {
				// 通配符只表示路由规则，不冒充具体模型；同一平台、分组内去重。
				if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "*?") {
					continue
				}
				key := platform + "\x00" + name
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				m := PlazaModel{Name: name, Platform: platform}
				var resolved *ResolvedPricing
				if s.plaza.resolver != nil {
					resolved = s.plaza.resolver.Resolve(ctx, PricingInput{Model: name, GroupID: &g.ID, Group: g})
					if resolved != nil {
						m.Pricing = resolved.channelPricing
					}
				}
				s.plaza.fillDisplayPricing(ctx, &m, g)
				if s.plaza.billingService != nil && (isOpenAIImageGenerationModel(name) || isImageGenerationModel(name)) {
					m.Pricing = s.imagePricing(ctx, name, g, resolved)
					m.LongContextBasis, m.TimePricing = "", nil
				}
				m.OfficialPricing = s.plaza.lookupOfficialPricing(ctx, name, officialMemo)
				pg.Models = append(pg.Models, m)
			}
		}
		sort.Slice(pg.Models, func(i, j int) bool {
			if pg.Models[i].Name != pg.Models[j].Name {
				return pg.Models[i].Name < pg.Models[j].Name
			}
			return pg.Models[i].Platform < pg.Models[j].Platform
		})
		// 没有明确模型的公开分组仍保留，页面据实显示空状态。
		out = append(out, pg)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		if out[i].RateMultiplier != out[j].RateMultiplier {
			return out[i].RateMultiplier < out[j].RateMultiplier
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// 生图按尺寸计费，不能把目录中的 token 参考价当作图片实付价。
func (s *ModelFactoryService) imagePricing(ctx context.Context, model string, group *Group, resolved *ResolvedPricing) *ChannelModelPricing {
	pricing := &ChannelModelPricing{BillingMode: BillingModeImage}
	key := &APIKey{Group: group}
	for _, tier := range []string{"1K", "2K", "4K"} {
		cost := s.plaza.billingService.CalculateImageCost(model, tier, 1, imagePriceConfigFromAPIKey(key), 1)
		if resolved != nil && (resolved.Mode == BillingModeImage || resolved.Mode == BillingModePerRequest) &&
			(resolved.Source == PricingSourceGroup || !apiKeyHasConfiguredImagePrice(key, tier)) {
			if configured, err := s.plaza.billingService.CalculateCostUnified(CostInput{
				Ctx: ctx, Model: model, GroupID: &group.ID, Group: group, RequestCount: 1,
				SizeTier: tier, RateMultiplier: 1, Resolver: s.plaza.resolver, Resolved: resolved,
			}); err == nil {
				cost = configured
			}
		}
		price := cost.TotalCost
		pricing.Intervals = append(pricing.Intervals, PricingInterval{TierLabel: tier, PerRequestPrice: &price})
	}
	return pricing
}

func factoryAccountModelNames(account *Account) []string {
	mapping := account.GetModelMapping()
	names := make([]string, 0, len(mapping))
	for name := range mapping {
		names = append(names, name)
	}
	// 未配置映射时沿用网关的默认模型目录，不把任意模型都宣称为可用。
	if len(mapping) == 0 {
		for _, name := range defaultModelsListCandidateIDs(account.Platform) {
			if account.IsModelSupported(name) {
				names = append(names, name)
			}
		}
	}
	return names
}
