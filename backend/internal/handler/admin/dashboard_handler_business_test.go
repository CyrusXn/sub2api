package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type businessSummaryRangeCapture struct {
	service.DashboardAggregationRepository
	service.DashboardMetricsRepository
	start, end time.Time
}

func (r *businessSummaryRangeCapture) GetDashboardBusinessSummary(_ context.Context, start, end time.Time) (*service.DashboardBusinessSummary, error) {
	r.start, r.end = start, end
	return &service.DashboardBusinessSummary{}, nil
}

func TestGetBusinessSummaryUsesBusinessDayTimezone(t *testing.T) {
	previousTZ := timezone.Name()
	previousLocal := time.Local
	require.NoError(t, timezone.Init("Asia/Shanghai"))
	t.Cleanup(func() {
		time.Local = previousLocal
		require.NoError(t, timezone.Init(previousTZ))
	})
	gin.SetMode(gin.TestMode)

	for _, browserTZ := range []string{"America/Los_Angeles", "UTC", "Asia/Shanghai"} {
		t.Run(browserTZ, func(t *testing.T) {
			repo := &businessSummaryRangeCapture{}
			h := NewDashboardHandler(service.NewDashboardService(nil, repo, nil, nil), nil)
			router := gin.New()
			router.GET("/business", h.GetBusinessSummary)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
				"/business?start_date=2026-10-20&end_date=2026-10-20&timezone="+browserTZ, nil))

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "2026-10-20T00:00:00+08:00", repo.start.Format(time.RFC3339))
			require.Equal(t, "2026-10-21T00:00:00+08:00", repo.end.Format(time.RFC3339))
		})
	}
}
