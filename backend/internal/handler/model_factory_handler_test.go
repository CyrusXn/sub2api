package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type publicFactoryGroups struct{ service.GroupRepository }

func (*publicFactoryGroups) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{
		{ID: 1, Name: "公开", Platform: service.PlatformOpenAI, RateMultiplier: 0.15},
		{ID: 2, Name: "私有", Platform: service.PlatformOpenAI, IsExclusive: true},
	}, nil
}

type publicFactoryAccounts struct{ service.AccountRepository }

func (*publicFactoryAccounts) ListActive(context.Context) ([]service.Account, error) {
	return []service.Account{{
		Name: "private-account-name", Platform: service.PlatformOpenAI, GroupIDs: []int64{1, 2},
		Credentials: map[string]any{
			"api_key":       "private-test-key",
			"model_mapping": map[string]any{"gpt-4o": "private-upstream-model"},
		},
	}}, nil
}

func TestModelFactoryAnonymousPublicCatalog(t *testing.T) {
	groups := &publicFactoryGroups{}
	billing := service.NewBillingService(&config.Config{}, nil)
	plaza := service.NewModelPlazaService(nil, groups, nil, billing, service.NewModelPricingResolver(nil, billing))
	h := NewModelFactoryHandler(service.NewModelFactoryService(&publicFactoryAccounts{}, groups, plaza))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/model-factory", h.Get)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/model-factory", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data modelPlazaResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data.Groups, 1)
	require.Equal(t, "公开", body.Data.Groups[0].Name)
	require.Equal(t, 0.15, body.Data.Groups[0].RateMultiplier)
	require.Len(t, body.Data.Groups[0].Models, 1)
	require.Equal(t, "gpt-4o", body.Data.Groups[0].Models[0].Name)
	for _, private := range []string{"私有", "private-account-name", "private-test-key", "private-upstream-model", "credentials", "api_key"} {
		require.NotContains(t, w.Body.String(), private)
	}
}
