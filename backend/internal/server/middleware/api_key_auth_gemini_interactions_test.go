package middleware

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoogleAuthInteractionsResourceDoesNotRequireNewSpendEligibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gid := int64(3)
	key := &service.APIKey{ID: 1, Key: "local-test", UserID: 2, Status: service.StatusAPIKeyQuotaExhausted, GroupID: &gid, Group: &service.Group{ID: gid, Platform: service.PlatformGemini, Status: service.StatusActive}, User: &service.User{ID: 2, Status: service.StatusActive, Balance: 0, Concurrency: 2}}
	repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
	cfg := &config.Config{}
	svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/v1beta/interactions/owned", 200}, {"POST", "/v1beta/interactions/owned/cancel", 200}, {"DELETE", "/v1beta/interactions/owned", 200}, {"POST", "/v1beta/interactions", 429}, {"GET", "/v1beta/models", 429}, {"POST", "/v1beta/models/gemini:generateContent", 429}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := gin.New()
			r.Use(APIKeyAuthGoogle(svc, cfg))
			r.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("x-goog-api-key", "local-test")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
	key.Status = "disabled"
	r := gin.New()
	r.Use(APIKeyAuthGoogle(svc, cfg))
	r.GET("/v1beta/interactions/owned", func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest(http.MethodGet, "/v1beta/interactions/owned", nil)
	req.Header.Set("x-goog-api-key", "local-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 401, w.Code)
	key.Status = service.StatusAPIKeyExpired
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
}
