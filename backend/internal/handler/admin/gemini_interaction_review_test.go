package admin

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

type interactionAdminReviewRepo struct {
	service.GeminiInteractionRepository
	calls int
}

func (r *interactionAdminReviewRepo) ListReview(context.Context, int, int) ([]*service.GeminiInteraction, int, error) {
	return []*service.GeminiInteraction{{ID: 1, PublicID: "local", UpstreamID: "private-upstream", PayloadHash: "private-hash", ReviewReason: "terminal_usage_missing", BalanceHold: 1.5}}, 1, nil
}
func (r *interactionAdminReviewRepo) ResolveReview(context.Context, int64, int64, service.GeminiInteractionResolution) (*service.GeminiInteraction, error) {
	r.calls++
	return &service.GeminiInteraction{Settled: true}, nil
}

func TestGeminiInteractionAdminReviewListDoesNotExposePrivateMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &interactionAdminReviewRepo{}
	h := &BalanceCenterHandler{interactions: service.NewGeminiInteractionService(r, nil, nil, nil, nil)}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?page=1&page_size=20", nil)
	h.InteractionReviews(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"public_id":"local"`)
	require.NotContains(t, w.Body.String(), "private-")
}

func TestGeminiInteractionAdminReviewRequiresIdentityAndValidInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body, id string
		auth           bool
		status         int
	}{
		{"unauthenticated", `{"action":"release","reason":"已核实上游无费用"}`, "1", false, 401},
		{"invalid id", `{}`, "-1", true, 400},
		{"fractional tokens", `{"action":"settle","reason":"已核实上游用量","usage":{"input_tokens":1.5}}`, "1", true, 400},
		{"empty usage", `{"action":"settle","reason":"已核实上游用量","usage":{}}`, "1", true, 400},
		{"release", `{"action":"release","reason":"已核实上游无费用"}`, "1", true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &interactionAdminReviewRepo{}
			h := &BalanceCenterHandler{interactions: service.NewGeminiInteractionService(r, nil, nil, nil, nil)}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = gin.Params{{Key: "id", Value: tc.id}}
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.auth {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
			}
			h.ResolveInteractionReview(c)
			require.Equal(t, tc.status, w.Code)
			if tc.status != 200 {
				require.Zero(t, r.calls)
			} else {
				require.Contains(t, w.Body.String(), `"settled":true`)
			}
		})
	}
}
