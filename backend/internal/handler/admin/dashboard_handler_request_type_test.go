package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dashboardUsageRepoCapture struct {
	service.UsageLogRepository
	trendRequestType          *int16
	trendStream               *bool
	modelRequestType          *int16
	modelStream               *bool
	trendMismatch             *bool
	modelMismatch             *bool
	groupMismatch             *bool
	trendUpstreamSiteAccounts []int64
	modelUpstreamSiteAccounts []int64
	groupUpstreamSiteAccounts []int64
	trendAccounts             []int64
	modelAccounts             []int64
	groupAccounts             []int64
	trendUsers                []int64
	trendAPIKeys              []int64
	trendGroups               []int64
	trendModels               []string
	trendRequestTypes         []int16
	trendBillingTypes         []int8
	trendBillingModes         []string
	trendMismatches           []bool
	rankingLimit              int
	ranking                   []usagestats.UserSpendingRankingItem
	rankingTotal              float64
}

func (s *dashboardUsageRepoCapture) GetUsageTrendWithUsageFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	filters usagestats.UsageLogFilters,
) ([]usagestats.TrendDataPoint, error) {
	s.trendRequestType = filters.RequestType
	s.trendStream = filters.Stream
	s.trendMismatch = filters.UpstreamModelMismatch
	s.trendUpstreamSiteAccounts = append([]int64(nil), filters.UpstreamSiteAccountIDs...)
	s.trendAccounts = append([]int64(nil), filters.AccountIDs...)
	s.trendUsers = append([]int64(nil), filters.UserIDs...)
	s.trendAPIKeys = append([]int64(nil), filters.APIKeyIDs...)
	s.trendGroups = append([]int64(nil), filters.GroupIDs...)
	s.trendModels = append([]string(nil), filters.Models...)
	s.trendRequestTypes = append([]int16(nil), filters.RequestTypes...)
	s.trendBillingTypes = append([]int8(nil), filters.BillingTypes...)
	s.trendBillingModes = append([]string(nil), filters.BillingModes...)
	s.trendMismatches = append([]bool(nil), filters.UpstreamModelMismatches...)
	return []usagestats.TrendDataPoint{}, nil
}

func (s *dashboardUsageRepoCapture) GetUsageTrendWithFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	userID, apiKeyID, accountID, groupID int64,
	model string,
	requestType *int16,
	stream *bool,
	billingType *int8,
) ([]usagestats.TrendDataPoint, error) {
	s.trendRequestType = requestType
	s.trendStream = stream
	return []usagestats.TrendDataPoint{}, nil
}

func (s *dashboardUsageRepoCapture) GetModelStatsWithUsageFiltersBySource(
	ctx context.Context,
	startTime, endTime time.Time,
	filters usagestats.UsageLogFilters,
	source string,
) ([]usagestats.ModelStat, error) {
	s.modelRequestType = filters.RequestType
	s.modelStream = filters.Stream
	s.modelMismatch = filters.UpstreamModelMismatch
	s.modelUpstreamSiteAccounts = append([]int64(nil), filters.UpstreamSiteAccountIDs...)
	s.modelAccounts = append([]int64(nil), filters.AccountIDs...)
	return []usagestats.ModelStat{}, nil
}

func (s *dashboardUsageRepoCapture) GetGroupStatsWithUsageFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	filters usagestats.UsageLogFilters,
) ([]usagestats.GroupStat, error) {
	s.groupMismatch = filters.UpstreamModelMismatch
	s.groupUpstreamSiteAccounts = append([]int64(nil), filters.UpstreamSiteAccountIDs...)
	s.groupAccounts = append([]int64(nil), filters.AccountIDs...)
	return []usagestats.GroupStat{}, nil
}

func (s *dashboardUsageRepoCapture) GetModelStatsWithFilters(
	ctx context.Context,
	startTime, endTime time.Time,
	userID, apiKeyID, accountID, groupID int64,
	requestType *int16,
	stream *bool,
	billingType *int8,
) ([]usagestats.ModelStat, error) {
	s.modelRequestType = requestType
	s.modelStream = stream
	return []usagestats.ModelStat{}, nil
}

func (s *dashboardUsageRepoCapture) GetUserSpendingRanking(
	ctx context.Context,
	startTime, endTime time.Time,
	limit int,
) (*usagestats.UserSpendingRankingResponse, error) {
	s.rankingLimit = limit
	return &usagestats.UserSpendingRankingResponse{
		Ranking:         s.ranking,
		TotalActualCost: s.rankingTotal,
		TotalRequests:   44,
		TotalTokens:     1234,
	}, nil
}

func newDashboardRequestTypeTestRouter(repo *dashboardUsageRepoCapture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	dashboardSvc := service.NewDashboardService(repo, nil, nil, nil)
	handler := NewDashboardHandler(dashboardSvc, nil)
	router := gin.New()
	router.GET("/admin/dashboard/trend", handler.GetUsageTrend)
	router.GET("/admin/dashboard/models", handler.GetModelStats)
	router.GET("/admin/dashboard/groups", handler.GetGroupStats)
	router.GET("/admin/dashboard/users-ranking", handler.GetUserSpendingRanking)
	return router
}

func TestDashboardTrendRequestTypePriority(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/trend?request_type=ws_v2&stream=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, repo.trendRequestType)
	require.Equal(t, int16(service.RequestTypeWSV2), *repo.trendRequestType)
	require.Nil(t, repo.trendStream)
}

func TestDashboardTrendInvalidRequestType(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/trend?request_type=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardTrendInvalidStream(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/trend?stream=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardModelStatsRequestTypePriority(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/models?request_type=sync&stream=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, repo.modelRequestType)
	require.Equal(t, int16(service.RequestTypeSync), *repo.modelRequestType)
	require.Nil(t, repo.modelStream)
}

func TestDashboardModelStatsInvalidRequestType(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/models?request_type=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardModelStatsInvalidStream(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/models?stream=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardModelStatsInvalidModelSource(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/models?model_source=invalid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDashboardModelStatsValidModelSource(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/models?model_source=upstream", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestDashboardModelAuditFilterPropagatesToTrendModelAndGroupQueries(t *testing.T) {
	resetDashboardReadCachesForTest()
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	for _, path := range []string{
		"/admin/dashboard/trend?upstream_model_mismatch=true",
		"/admin/dashboard/models?upstream_model_mismatch=true",
		"/admin/dashboard/groups?upstream_model_mismatch=true",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
	}

	require.NotNil(t, repo.trendMismatch)
	require.True(t, *repo.trendMismatch)
	require.NotNil(t, repo.modelMismatch)
	require.True(t, *repo.modelMismatch)
	require.NotNil(t, repo.groupMismatch)
	require.True(t, *repo.groupMismatch)
}

func TestDashboardModelAuditFilterRejectsInvalidBoolean(t *testing.T) {
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	for _, path := range []string{
		"/admin/dashboard/trend?upstream_model_mismatch=invalid",
		"/admin/dashboard/models?upstream_model_mismatch=invalid",
		"/admin/dashboard/groups?upstream_model_mismatch=invalid",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}

func TestDashboardUpstreamSiteFilterPropagatesToTrendModelAndGroupQueries(t *testing.T) {
	resetDashboardReadCachesForTest()
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)

	for _, path := range []string{
		"/admin/dashboard/trend?upstream_site_account_ids=11,12",
		"/admin/dashboard/models?upstream_site_account_ids=11,12",
		"/admin/dashboard/groups?upstream_site_account_ids=11,12",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
	}

	require.Equal(t, []int64{11, 12}, repo.trendUpstreamSiteAccounts)
	require.Equal(t, []int64{11, 12}, repo.modelUpstreamSiteAccounts)
	require.Equal(t, []int64{11, 12}, repo.groupUpstreamSiteAccounts)
}

func TestDashboardAccountMultiFilterPropagatesToTrendModelAndGroupQueries(t *testing.T) {
	resetDashboardReadCachesForTest()
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)
	for _, path := range []string{
		"/admin/dashboard/trend?account_ids=11,12",
		"/admin/dashboard/models?account_ids=11,12",
		"/admin/dashboard/groups?account_ids=11,12",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, path)
	}
	require.Equal(t, []int64{11, 12}, repo.trendAccounts)
	require.Equal(t, []int64{11, 12}, repo.modelAccounts)
	require.Equal(t, []int64{11, 12}, repo.groupAccounts)
}

func TestDashboardAllMultiFiltersPropagateToTrendQuery(t *testing.T) {
	resetDashboardReadCachesForTest()
	repo := &dashboardUsageRepoCapture{}
	router := newDashboardRequestTypeTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/trend?user_ids=1,2&api_key_ids=3,4&account_ids=5,6&group_ids=7,8&models=claude,gpt&request_types=sync,stream&billing_types=1,2&billing_modes=token,image&upstream_model_mismatches=true,false&upstream_site_account_ids=9,10", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []int64{1, 2}, repo.trendUsers)
	require.Equal(t, []int64{3, 4}, repo.trendAPIKeys)
	require.Equal(t, []int64{5, 6}, repo.trendAccounts)
	require.Equal(t, []int64{7, 8}, repo.trendGroups)
	require.Equal(t, []string{"claude", "gpt"}, repo.trendModels)
	require.Equal(t, []int16{int16(service.RequestTypeSync), int16(service.RequestTypeStream)}, repo.trendRequestTypes)
	require.Equal(t, []int8{1, 2}, repo.trendBillingTypes)
	require.Equal(t, []string{"token", "image"}, repo.trendBillingModes)
	require.Equal(t, []bool{true, false}, repo.trendMismatches)
	require.Equal(t, []int64{9, 10}, repo.trendUpstreamSiteAccounts)
}

func TestDashboardUsersRankingLimitAndCache(t *testing.T) {
	dashboardUsersRankingCache = newSnapshotCache(5 * time.Minute)
	repo := &dashboardUsageRepoCapture{
		ranking: []usagestats.UserSpendingRankingItem{
			{UserID: 7, Email: "rank@example.com", ActualCost: 10.5, Requests: 3, Tokens: 300},
		},
		rankingTotal: 88.8,
	}
	router := newDashboardRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard/users-ranking?limit=100&start_date=2025-01-01&end_date=2025-01-02", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 50, repo.rankingLimit)
	require.Contains(t, rec.Body.String(), "\"total_actual_cost\":88.8")
	require.Contains(t, rec.Body.String(), "\"total_requests\":44")
	require.Contains(t, rec.Body.String(), "\"total_tokens\":1234")
	require.Equal(t, "miss", rec.Header().Get("X-Snapshot-Cache"))

	req2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard/users-ranking?limit=100&start_date=2025-01-01&end_date=2025-01-02", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)
	require.Equal(t, "hit", rec2.Header().Get("X-Snapshot-Cache"))
}
