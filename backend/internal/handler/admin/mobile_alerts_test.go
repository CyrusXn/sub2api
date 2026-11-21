package admin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mobileReaderStub struct {
	balanceCenterAdminService
	cursor int64
}

func (s *mobileReaderStub) ListMobileAlerts(_ context.Context, cursor int64) ([]service.MobileAlert, error) {
	s.cursor = cursor
	return []service.MobileAlert{{ID: 43, Kind: "low_balance", Title: "余额不足"}}, nil
}

func TestMobileAlertRouteRequiresDedicatedTokenAndValidCursor(t *testing.T) {
	const token = "test-only-0123456789012345678901234567890123456789"
	t.Setenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256", fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
	reader := &mobileReaderStub{}
	h := &BalanceCenterHandler{service: reader}
	r := gin.New()
	r.GET("/mobile-alerts", h.MobileAlerts)
	for _, tc := range []struct {
		method, query, authorization string
		status                       int
	}{
		{"GET", "?after=42", "", http.StatusUnauthorized},
		{"GET", "?after=-1", "Bearer " + token, http.StatusBadRequest},
		{"POST", "", "Bearer " + token, http.StatusNotFound},
		{"GET", "?after=42", "Bearer " + token, http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, "/mobile-alerts"+tc.query, nil)
		req.Header.Set("Authorization", tc.authorization)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		require.Equal(t, tc.status, res.Code)
		if tc.status == http.StatusOK {
			require.Equal(t, "no-store", res.Header().Get("Cache-Control"))
			require.Contains(t, res.Body.String(), `"id":43`)
			require.Equal(t, int64(42), reader.cursor)
		} else {
			require.Zero(t, reader.cursor)
		}
	}
}
