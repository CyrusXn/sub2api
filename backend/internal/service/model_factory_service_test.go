package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type factoryGroupRepo struct {
	GroupRepository
	groups []Group
}

func (r *factoryGroupRepo) ListActive(context.Context) ([]Group, error) { return r.groups, nil }

type factoryAccountRepo struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r *factoryAccountRepo) ListActive(context.Context) ([]Account, error) { return r.accounts, r.err }

func TestModelFactoryPublicAccountModelsAndPrices(t *testing.T) {
	input, output, cache := 2e-6, 8e-6, 0.2e-6
	groups := &factoryGroupRepo{groups: []Group{
		{ID: 1, Name: "公开", Platform: PlatformOpenAI, RateMultiplier: 0.15, ModelPricing: []ChannelModelPricing{{Models: []string{"demo-model"}, BillingMode: BillingModeToken, InputPrice: &input, OutputPrice: &output, CacheReadPrice: &cache}}},
		{ID: 2, Name: "专属", Platform: PlatformOpenAI, IsExclusive: true},
		{ID: 3, Name: "空分组", Platform: PlatformGemini},
	}}
	accounts := &factoryAccountRepo{accounts: []Account{
		{Platform: PlatformOpenAI, GroupIDs: []int64{1, 2}, Credentials: map[string]any{"model_mapping": map[string]any{"demo-model": "upstream-model", "gpt-*": "*", "*": "*"}}},
		{Platform: PlatformOpenAI, GroupIDs: []int64{1}, Credentials: map[string]any{"model_mapping": map[string]any{"demo-model": "another-upstream-model"}}},
	}}
	billing := NewBillingService(&config.Config{}, nil)
	plaza := NewModelPlazaService(nil, groups, nil, billing, NewModelPricingResolver(nil, billing))
	svc := NewModelFactoryService(accounts, groups, plaza)
	result, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, int64(3), result[0].ID)
	require.Empty(t, result[0].Models)
	public := result[1]
	require.Equal(t, 0.15, public.RateMultiplier)
	require.Len(t, public.Models, 1)
	require.Equal(t, "demo-model", public.Models[0].Name)
	require.NotNil(t, public.Models[0].Pricing)
	require.Equal(t, input, *public.Models[0].Pricing.InputPrice)
	require.Equal(t, cache, *public.Models[0].Pricing.CacheReadPrice)
	accounts.err = errors.New("unavailable")
	_, err = svc.ListGroups(context.Background())
	require.Error(t, err)
}

func TestModelFactoryImagePricingUsesGroupSizePrices(t *testing.T) {
	small, medium, large := 0.06, 0.07, 0.08
	billing := NewBillingService(&config.Config{}, nil)
	svc := &ModelFactoryService{plaza: NewModelPlazaService(nil, nil, nil, billing, nil)}
	group := &Group{ImagePrice1K: &small, ImagePrice2K: &medium, ImagePrice4K: &large}
	pricing := svc.imagePricing(context.Background(), "gpt-image-2", group, nil)
	require.Equal(t, BillingModeImage, pricing.BillingMode)
	require.Len(t, pricing.Intervals, 3)
	require.Equal(t, small, *pricing.Intervals[0].PerRequestPrice)
	require.Equal(t, large, *pricing.Intervals[2].PerRequestPrice)
}
