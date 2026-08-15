package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type balanceCenterHandlerServiceStub struct {
	balanceCenterAdminService
	filter         service.BalanceCenterListFilter
	liandongCurl   string
	automaticCount int
}

func (s *balanceCenterHandlerServiceStub) GetRechargeSummary(_ context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterRechargeSummary, error) {
	s.filter = filter
	return &service.BalanceCenterRechargeSummary{Items: []service.BalanceCenterRechargeEvent{}, Sites: []service.BalanceCenterRechargeSiteSummary{}}, nil
}

func (s *balanceCenterHandlerServiceStub) ListSnapshots(_ context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterSnapshot], error) {
	s.filter = filter
	return &service.BalanceCenterPage[service.BalanceCenterSnapshot]{Items: []service.BalanceCenterSnapshot{}, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *balanceCenterHandlerServiceStub) SaveLiandongSession(_ context.Context, rawCurl string) (*service.BalanceCenterLiandongSessionResult, error) {
	s.liandongCurl = rawCurl
	return &service.BalanceCenterLiandongSessionResult{Keywords: "13800000000"}, nil
}

func (s *balanceCenterHandlerServiceStub) SyncLiandong(context.Context) (*service.BalanceCenterLiandongSyncResult, error) {
	return &service.BalanceCenterLiandongSyncResult{Synced: 2}, nil
}

func (s *balanceCenterHandlerServiceStub) SyncAutomaticRecords(context.Context) (*service.BalanceCenterLiandongSyncResult, error) {
	return &service.BalanceCenterLiandongSyncResult{Synced: s.automaticCount}, nil
}

func TestBalanceCenterHandlerRejectsInvalidSnapshotTime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_time=invalid", nil)
	h := &BalanceCenterHandler{service: &balanceCenterHandlerServiceStub{}}

	h.Snapshots(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestBalanceCenterHandlerParsesSnapshotFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?page=2&page_size=50&site_id=3&account_id=7&status=ok", nil)
	stub := &balanceCenterHandlerServiceStub{}
	h := &BalanceCenterHandler{service: stub}

	h.Snapshots(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(3), stub.filter.SiteID)
	require.Equal(t, int64(7), stub.filter.AccountID)
	require.Equal(t, "ok", stub.filter.Status)
}

func TestBalanceCenterHandlerParsesRechargeSummaryTimeFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?page=2&page_size=50&site_id=3&start_time=2026-08-01T00:00:00Z&end_time=2026-08-31T23:59:59Z", nil)
	stub := &balanceCenterHandlerServiceStub{}
	h := &BalanceCenterHandler{service: stub}

	h.RechargeSummary(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 2, stub.filter.Page)
	require.Equal(t, 50, stub.filter.PageSize)
	require.Equal(t, int64(3), stub.filter.SiteID)
	require.Equal(t, "2026-08-01T00:00:00Z", stub.filter.StartTime.Format(time.RFC3339))
	require.Equal(t, "2026-08-31T23:59:59Z", stub.filter.EndTime.Format(time.RFC3339))
	require.Contains(t, recorder.Body.String(), `"total_amount":0`)
	require.Contains(t, recorder.Body.String(), `"sites":[]`)
}

func TestBalanceCenterHandlerRejectsInvalidRechargeSummaryTime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_time=invalid", nil)
	h := &BalanceCenterHandler{service: &balanceCenterHandlerServiceStub{}}

	h.RechargeSummary(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestBalanceCenterHandlerRejectsReversedRechargeSummaryTimeRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_time=2026-08-02T00:00:00Z&end_time=2026-08-01T00:00:00Z", nil)
	h := &BalanceCenterHandler{service: &balanceCenterHandlerServiceStub{}}

	h.RechargeSummary(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "开始时间不能晚于结束时间")
}

func TestBalanceCenterHandlerSavesLiandongSessionWithoutReturningCurl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	payload, err := json.Marshal(map[string]string{"curl": "curl-sensitive"})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	stub := &balanceCenterHandlerServiceStub{}
	h := &BalanceCenterHandler{service: stub}

	h.SaveLiandongSession(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "curl-sensitive", stub.liandongCurl)
	require.NotContains(t, recorder.Body.String(), "curl-sensitive")
	require.Contains(t, recorder.Body.String(), "13800000000")
}

func TestBalanceCenterHandlerSyncsAutomaticRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	h := &BalanceCenterHandler{service: &balanceCenterHandlerServiceStub{automaticCount: 5}}

	h.SyncAutomaticRecords(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"synced":5`)
}
