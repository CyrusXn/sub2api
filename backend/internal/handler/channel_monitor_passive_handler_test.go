package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passiveHandlerRepoStub struct {
	service.ChannelMonitorV2Repository
	filter service.ChannelMonitorV2Filter
	calls  int
}

func (r *passiveHandlerRepoStub) GetPassiveCards(_ context.Context, filter service.ChannelMonitorV2Filter) ([]service.ChannelMonitorPassiveCard, error) {
	r.calls++
	r.filter = filter
	return []service.ChannelMonitorPassiveCard{}, nil
}

type passiveHandlerSettings struct{ enabled bool }

func (s passiveHandlerSettings) GetChannelMonitorRuntime(context.Context) service.ChannelMonitorRuntime {
	return service.ChannelMonitorRuntime{Enabled: s.enabled, Mode: service.ChannelMonitorModeV1}
}

func TestChannelMonitorPassiveHandlerScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, admin := range []bool{false, true} {
		repo := &passiveHandlerRepoStub{}
		svc := service.NewChannelMonitorV2Service(repo)
		svc.SetRuntimeReader(passiveHandlerSettings{enabled: true})
		authorizer := &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 7}}}
		handler := &ChannelMonitorV2Handler{service: svc, apiKeyService: authorizer}
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodGet, "/channel-monitor-passive?range=15d&platform=openai,grok", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		if admin {
			c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		}
		handler.PassiveCards(c)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, 1, repo.calls)
		require.Equal(t, !admin, repo.filter.RestrictGroups)
		if !admin {
			require.Equal(t, []int64{7}, repo.filter.AllowedGroupIDs)
		}
		require.Equal(t, []string{"openai", "grok"}, repo.filter.Platforms)
		require.Equal(t, 15*24*time.Hour, repo.filter.End.Sub(repo.filter.Start))
		require.Contains(t, response.Body.String(), `"items":[]`)
	}
}

func TestChannelMonitorPassiveHandlerRejectsInvalidRange(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/channel-monitor-passive?range=invalid", nil)
	NewChannelMonitorV2Handler(nil, nil).PassiveCards(c)
	require.Equal(t, http.StatusBadRequest, response.Code)
}
