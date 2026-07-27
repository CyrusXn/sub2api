package admin

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupAdminUsageMultiplierRouter(t *testing.T) (*gin.Engine, *stubAdminService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	userHandler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil)
	groupHandler := NewGroupHandler(adminSvc, nil, nil)
	router.POST("/api/v1/admin/users", userHandler.Create)
	router.PUT("/api/v1/admin/users/:id", userHandler.Update)
	router.POST("/api/v1/admin/groups", groupHandler.Create)
	router.PUT("/api/v1/admin/groups/:id", groupHandler.Update)
	return router, adminSvc
}

func TestAdminUsageMultiplierHandlersPassValuesToService(t *testing.T) {
	router, adminSvc := setupAdminUsageMultiplierRouter(t)
	usageStatsCache = newSnapshotCache(time.Minute)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/groups", map[string]any{
		"name": "premium", "admin_usage_multiplier": 1.25,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, adminSvc.lastCreateGroupInput)
	require.NotNil(t, adminSvc.lastCreateGroupInput.AdminUsageMultiplier)
	require.InDelta(t, 1.25, *adminSvc.lastCreateGroupInput.AdminUsageMultiplier, 1e-12)

	usageStatsCache.Set("group-update", map[string]any{"cached": true})
	rec = doJSON(t, router, http.MethodPut, "/api/v1/admin/groups/2", map[string]any{
		"admin_usage_multiplier": 0.8,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, adminSvc.lastUpdateGroupInput)
	require.NotNil(t, adminSvc.lastUpdateGroupInput.AdminUsageMultiplier)
	require.InDelta(t, 0.8, *adminSvc.lastUpdateGroupInput.AdminUsageMultiplier, 1e-12)
	_, cached := usageStatsCache.Get("group-update")
	require.False(t, cached)

	rec = doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-user@example.com", "password": "pass123", "role": "user", "admin_usage_multiplier": 1.4,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, adminSvc.lastCreateUserInput)
	require.NotNil(t, adminSvc.lastCreateUserInput.AdminUsageMultiplier)
	require.InDelta(t, 1.4, *adminSvc.lastCreateUserInput.AdminUsageMultiplier, 1e-12)

	rec = doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{
		"admin_usage_multiplier": 0.6,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, adminSvc.lastUpdateUserInput)
	require.NotNil(t, adminSvc.lastUpdateUserInput.AdminUsageMultiplier)
	require.InDelta(t, 0.6, *adminSvc.lastUpdateUserInput.AdminUsageMultiplier, 1e-12)

	usageStatsCache.Set("user-clear", map[string]any{"cached": true})
	rec = doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{
		"clear_admin_usage_multiplier": true,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, adminSvc.lastUpdateUserInput.ClearAdminUsageMultiplier)
	_, cached = usageStatsCache.Get("user-clear")
	require.False(t, cached)
}
