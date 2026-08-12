package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type balanceCenterHandlerServiceStub struct {
	balanceCenterAdminService
	filter service.BalanceCenterListFilter
}

func (s *balanceCenterHandlerServiceStub) ListSnapshots(_ context.Context, filter service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterSnapshot], error) {
	s.filter = filter
	return &service.BalanceCenterPage[service.BalanceCenterSnapshot]{Items: []service.BalanceCenterSnapshot{}, Page: filter.Page, PageSize: filter.PageSize}, nil
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
