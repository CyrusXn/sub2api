package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type balanceCenterAdminRepositoryStub struct {
	BalanceCenterRepository
	lastFilter BalanceCenterListFilter
}

func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterOverview(context.Context) ([]BalanceCenterOverviewItem, error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterSites(context.Context) ([]BalanceCenterSite, error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterSnapshots(_ context.Context, filter BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterSnapshot], error) {
	r.lastFilter = filter
	return &BalanceCenterPage[BalanceCenterSnapshot]{}, nil
}
func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterManualRows(context.Context) ([]BalanceCenterManualRow, error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) ReplaceBalanceCenterManualRows(context.Context, []BalanceCenterManualRow) error {
	return nil
}
func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterRechargeEvents(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterRechargeEvent], error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) CreateBalanceCenterRechargeEvent(context.Context, *BalanceCenterRechargeEvent) (*BalanceCenterRechargeEvent, error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) DeleteBalanceCenterRechargeEvent(context.Context, int64) error {
	return nil
}
func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterReconciliations(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterReconciliation], error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) CreateBalanceCenterReconciliation(context.Context, *BalanceCenterReconciliation) (*BalanceCenterReconciliation, error) {
	return nil, nil
}
func (r *balanceCenterAdminRepositoryStub) ListBalanceCenterAlerts(context.Context, BalanceCenterListFilter) (*BalanceCenterPage[BalanceCenterAlertDelivery], error) {
	return nil, nil
}

func TestBalanceCenterAdminNormalizesPagination(t *testing.T) {
	repo := &balanceCenterAdminRepositoryStub{}
	svc := NewBalanceCenterService(repo, nil)
	_, err := svc.ListSnapshots(context.Background(), BalanceCenterListFilter{Page: -1, PageSize: 1000})
	require.NoError(t, err)
	require.Equal(t, 1, repo.lastFilter.Page)
	require.Equal(t, 100, repo.lastFilter.PageSize)
}

func TestBalanceCenterAdminRejectsInvalidManualRows(t *testing.T) {
	repo := &balanceCenterAdminRepositoryStub{}
	svc := NewBalanceCenterService(repo, nil)
	err := svc.ReplaceManualRows(context.Background(), []BalanceCenterManualRow{{Label: "", Expression: "10", Amount: 10}})
	require.EqualError(t, err, "手工基线行内容无效")
}
