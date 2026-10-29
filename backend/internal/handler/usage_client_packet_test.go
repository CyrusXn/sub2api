package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type clientPacketRepo struct {
	service.UsageLogRepository
	userID, keyID int64
	id            string
	ms            int
	calls         int
}

func (r *clientPacketRepo) ReportClientFirstPacket(_ context.Context, userID, keyID int64, id string, ms int) error {
	r.userID, r.keyID, r.id, r.ms = userID, keyID, id, ms
	r.calls++
	return nil
}

func TestReportClientPacketUsesAuthenticatedOwner(t *testing.T) {
	repo := &clientPacketRepo{}
	h := &UsageHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Request = httptest.NewRequest(http.MethodPost, "/usage/client-first-packet", strings.NewReader(`{"user_id":999,"api_key_id":2,"request_id":"server-issued-id","milliseconds":1560}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.ReportClientFirstPacket(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.userID)
	require.Equal(t, int64(2), repo.keyID)
	require.Equal(t, "server-issued-id", repo.id)
	require.Equal(t, 1560, repo.ms)
	require.Equal(t, 1, repo.calls)
}

func TestReportClientPacketRejectsMissingTiming(t *testing.T) {
	repo := &clientPacketRepo{}
	h := &UsageHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Request = httptest.NewRequest(http.MethodPost, "/usage/client-first-packet", strings.NewReader(`{"api_key_id":2,"request_id":"server-issued-id"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.ReportClientFirstPacket(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, repo.calls)
}
