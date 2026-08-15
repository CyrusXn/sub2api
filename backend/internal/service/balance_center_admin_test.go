package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceCenterAdminRepositoryStub struct {
	BalanceCenterRepository
	lastFilter   BalanceCenterListFilter
	createdEvent *BalanceCenterRechargeEvent
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
func (r *balanceCenterAdminRepositoryStub) GetBalanceCenterRechargeSummary(_ context.Context, filter BalanceCenterListFilter) (*BalanceCenterRechargeSummary, error) {
	r.lastFilter = filter
	return &BalanceCenterRechargeSummary{}, nil
}
func (r *balanceCenterAdminRepositoryStub) CreateBalanceCenterRechargeEvent(_ context.Context, item *BalanceCenterRechargeEvent) (*BalanceCenterRechargeEvent, error) {
	copy := *item
	r.createdEvent = &copy
	return &copy, nil
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

func TestBalanceCenterRechargeSummaryNormalizesPaginationAndForwardsFilters(t *testing.T) {
	repo := &balanceCenterAdminRepositoryStub{}
	svc := NewBalanceCenterService(repo, nil)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	_, err := svc.GetRechargeSummary(context.Background(), BalanceCenterListFilter{
		Page: -1, PageSize: 1000, SiteID: 9, StartTime: &start, EndTime: &end,
	})

	require.NoError(t, err)
	require.Equal(t, 1, repo.lastFilter.Page)
	require.Equal(t, 100, repo.lastFilter.PageSize)
	require.Equal(t, int64(9), repo.lastFilter.SiteID)
	require.Equal(t, &start, repo.lastFilter.StartTime)
	require.Equal(t, &end, repo.lastFilter.EndTime)
}

func TestBalanceCenterCreateRechargeEventDefaultsToCNY(t *testing.T) {
	repo := &balanceCenterAdminRepositoryStub{}
	svc := NewBalanceCenterService(repo, nil)

	_, err := svc.CreateRechargeEvent(context.Background(), &BalanceCenterRechargeEvent{
		SourceKey: "tx-1", Amount: 10, OccurredAt: time.Now().UTC(),
	})

	require.NoError(t, err)
	require.Equal(t, "CNY", repo.createdEvent.Currency)
}

func TestBalanceCenterCreateRechargeEventRejectsNonCNY(t *testing.T) {
	repo := &balanceCenterAdminRepositoryStub{}
	svc := NewBalanceCenterService(repo, nil)

	_, err := svc.CreateRechargeEvent(context.Background(), &BalanceCenterRechargeEvent{
		SourceKey: "tx-1", Amount: 10, Currency: "USD", OccurredAt: time.Now().UTC(),
	})

	require.EqualError(t, err, "充值记录币种仅支持 CNY")
	require.Nil(t, repo.createdEvent)
}
