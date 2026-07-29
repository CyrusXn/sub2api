package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountUpstreamSiteRoutesUseStaticPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}}}
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })

	registerAccountRoutes(router.Group("/api/v1/admin"), handlers, stepUp)

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	require.True(t, registered[http.MethodGet+" /api/v1/admin/accounts/upstream-sites"])
	require.True(t, registered[http.MethodPut+" /api/v1/admin/accounts/upstream-sites/credentials"])
	require.True(t, registered[http.MethodDelete+" /api/v1/admin/accounts/upstream-sites/:host/credentials"])
}
