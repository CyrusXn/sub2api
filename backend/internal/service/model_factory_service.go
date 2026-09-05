package service

import (
	"context"
	"fmt"
	"sort"
)

// ModelFactoryGroup 是用户端模型工厂展示的最小数据契约。
type ModelFactoryGroup struct {
	ID             int64
	Name           string
	Platform       string
	RateMultiplier float64
	Models         []string
}

// ModelFactoryService 按可用分组聚合账号实际声明的模型。
type ModelFactoryService struct {
	accountRepo AccountRepository
}

func NewModelFactoryService(accountRepo AccountRepository) *ModelFactoryService {
	return &ModelFactoryService{accountRepo: accountRepo}
}

// ListGroups 每次请求均从账号仓储读取，避免缓存导致模型工厂展示过期。
func (s *ModelFactoryService) ListGroups(ctx context.Context, groups []Group) ([]ModelFactoryGroup, error) {
	if s.accountRepo == nil {
		return nil, fmt.Errorf("account repository is unavailable")
	}
	out := make([]ModelFactoryGroup, 0, len(groups))
	for _, group := range groups {
		accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, group.ID)
		if err != nil {
			return nil, fmt.Errorf("list accounts for group %d: %w", group.ID, err)
		}
		set := make(map[string]struct{})
		for i := range accounts {
			// GetModelMapping 已统一处理各平台默认映射及别名。
			for model := range accounts[i].GetModelMapping() {
				// 通配映射代表兜底路由，不是可展示的具体模型名称。
				if model != "" && model != "*" {
					set[model] = struct{}{}
				}
			}
		}
		models := make([]string, 0, len(set))
		for model := range set {
			models = append(models, model)
		}
		sort.Strings(models)
		if len(models) == 0 {
			continue
		}
		out = append(out, ModelFactoryGroup{ID: group.ID, Name: group.Name, Platform: group.Platform, RateMultiplier: group.RateMultiplier, Models: models})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].RateMultiplier < out[j].RateMultiplier || (out[i].RateMultiplier == out[j].RateMultiplier && out[i].Name < out[j].Name)
	})
	return out, nil
}
