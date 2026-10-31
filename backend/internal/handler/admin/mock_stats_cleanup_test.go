package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUnimplementedStatsDoNotReturnSyntheticSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"兑换码", (&RedeemHandler{}).GetStats},
		{"分组", (&GroupHandler{}).GetStats},
		{"代理", (&ProxyHandler{}).GetStats},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/stats", nil)
			ctx.Params = gin.Params{{Key: "id", Value: "1"}}
			tc.handler(ctx)
			require.Equal(t, http.StatusNotImplemented, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "success_rate")
			require.NotContains(t, recorder.Body.String(), "total_requests")
		})
	}
}
