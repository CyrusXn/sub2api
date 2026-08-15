//go:build unit

package admin

import (
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

type userConcurrencyListCacheStub struct {
	service.ConcurrencyCache
	loads map[int64]*service.UserLoadInfo
}

func (s *userConcurrencyListCacheStub) GetUsersLoadBatch(_ context.Context, _ []service.UserWithConcurrency) (map[int64]*service.UserLoadInfo, error) {
	return s.loads, nil
}

func TestUserHandlerListIncludesActivityFieldsAndSortParams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	lastLoginAt := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	lastActiveAt := lastLoginAt.Add(30 * time.Minute)
	lastUsedAt := lastLoginAt.Add(90 * time.Minute)

	adminSvc := newStubAdminService()
	adminSvc.users = []service.User{
		{
			ID:           7,
			Email:        "activity@example.com",
			Username:     "activity-user",
			Role:         service.RoleUser,
			Status:       service.StatusActive,
			LastActiveAt: &lastActiveAt,
			LastUsedAt:   &lastUsedAt,
			CreatedAt:    lastLoginAt.Add(-24 * time.Hour),
			UpdatedAt:    lastLoginAt,
		},
	}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/users?sort_by=last_used_at&sort_order=asc&search=activity",
		nil,
	)

	handler.List(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "last_used_at", adminSvc.lastListUsers.sortBy)
	require.Equal(t, "asc", adminSvc.lastListUsers.sortOrder)
	require.Equal(t, "activity", adminSvc.lastListUsers.filters.Search)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				LastActiveAt *time.Time `json:"last_active_at"`
				LastUsedAt   *time.Time `json:"last_used_at"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Items, 1)
	require.WithinDuration(t, lastActiveAt, *resp.Data.Items[0].LastActiveAt, time.Second)
	require.WithinDuration(t, lastUsedAt, *resp.Data.Items[0].LastUsedAt, time.Second)
}

func TestUserHandlerListSortsByAvailableConcurrencyDescending(t *testing.T) {
	gin.SetMode(gin.TestMode)

	adminSvc := newStubAdminService()
	adminSvc.users = []service.User{
		{ID: 1, Email: "nearly-full@example.com", Concurrency: 10},
		{ID: 2, Email: "available@example.com", Concurrency: 5},
	}
	concurrencySvc := service.NewConcurrencyService(&userConcurrencyListCacheStub{
		loads: map[int64]*service.UserLoadInfo{
			1: {UserID: 1, CurrentConcurrency: 9},
			2: {UserID: 2, CurrentConcurrency: 1},
		},
	})
	handler := NewUserHandler(adminSvc, concurrencySvc, nil, nil, nil, nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/users?sort_by=concurrency&concurrency_metric=available&sort_order=desc",
		nil,
	)

	handler.List(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Data struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, []int64{2, 1}, []int64{resp.Data.Items[0].ID, resp.Data.Items[1].ID})
}

func TestUserHandlerGetByIDIncludesActivityFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	lastLoginAt := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	lastActiveAt := lastLoginAt.Add(30 * time.Minute)
	lastUsedAt := lastLoginAt.Add(90 * time.Minute)

	adminSvc := newStubAdminService()
	adminSvc.users = []service.User{
		{
			ID:           8,
			Email:        "detail@example.com",
			Username:     "detail-user",
			Role:         service.RoleUser,
			Status:       service.StatusActive,
			LastActiveAt: &lastActiveAt,
			LastUsedAt:   &lastUsedAt,
			CreatedAt:    lastLoginAt.Add(-24 * time.Hour),
			UpdatedAt:    lastLoginAt,
		},
	}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "8"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/8", nil)

	handler.GetByID(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			LastActiveAt *time.Time `json:"last_active_at"`
			LastUsedAt   *time.Time `json:"last_used_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.WithinDuration(t, lastActiveAt, *resp.Data.LastActiveAt, time.Second)
	require.WithinDuration(t, lastUsedAt, *resp.Data.LastUsedAt, time.Second)
}
