//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamTimeoutGlobalPenaltySkipsOnlyOpenAI(t *testing.T) {
	for _, action := range []string{"error", "temp_unsched"} {
		for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
			t.Run(action+"/"+platform, func(t *testing.T) {
				repo := &errorPolicyRepoStub{}
				settings := NewSettingService(&settingRepoStub{values: map[string]string{
					SettingKeyStreamTimeoutSettings: fmt.Sprintf(`{"enabled":true,"action":%q,"temp_unsched_minutes":1,"threshold_count":1,"threshold_window_minutes":1}`, action),
				}}, nil)
				svc := &RateLimitService{accountRepo: repo, settingService: settings}
				stopped := svc.HandleStreamTimeout(context.Background(), &Account{ID: 96123, Platform: platform}, "upstream-a")
				if platform == PlatformOpenAI {
					require.False(t, stopped, "全局流超时策略不能整号处罚 OpenAI")
					require.Zero(t, repo.setErrCalls)
					require.Zero(t, repo.tempCalls)
				} else {
					require.True(t, stopped, "其他平台保留原有超时策略")
					require.Equal(t, 1, repo.setErrCalls+repo.tempCalls)
				}
			})
		}
	}
}

func TestOpenAIFirstOutputTimeoutCoolsCanonicalModelWithoutAccountPenalty(t *testing.T) {
	repo := &consecutiveFailureRepo{}
	settings := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyStreamTimeoutSettings: `{"enabled":true,"action":"error","threshold_count":1,"threshold_window_minutes":1}`,
	}}, nil)
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo, settingService: settings}}
	account := &Account{ID: 96124, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": "upstream-a", "upstream-a": "upstream-b"}}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	err := svc.newOpenAIFirstOutputTimeoutError(c.Request.Context(), c, account, nil, "", time.Now(), "public-alias", "", time.Second, "semantic_output", nil)
	require.Equal(t, http.StatusGatewayTimeout, err.StatusCode)
	svc.ReportOpenAIAccountScheduleResult(account, "upstream-a", false, nil, err)
	require.Empty(t, repo.errors, "首字超时不能经全局设置永久停号")
	require.Equal(t, 1, repo.modelCooldown.calls)
	require.Equal(t, "upstream-a", repo.modelCooldown.model)
	require.True(t, svc.getOpenAIAccountModelTransientState().isBlocked(account.ID, "upstream-a", time.Now()))
	require.False(t, svc.getOpenAIAccountModelTransientState().isBlocked(account.ID, "upstream-b", time.Now()))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIResponsesIdleTimeoutCoolsOnlyCanonicalModel(t *testing.T) {
	repo := &consecutiveFailureRepo{}
	settings := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyStreamTimeoutSettings: `{"enabled":true,"action":"error","threshold_count":1,"threshold_window_minutes":1}`,
	}}, nil)
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo, settingService: settings},
		cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
	account := &Account{ID: 96125, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": "upstream-a", "upstream-a": "upstream-b"}}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() {
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"delivered-text\"}\n\n")
	}()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "public-alias", "upstream-a")
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.NotErrorAs(t, err, &failover, "正文后不能重放请求")
	svc.ReportOpenAIAccountScheduleResult(account, "upstream-a", false, nil, err)
	require.Empty(t, repo.errors)
	require.Equal(t, 1, repo.modelCooldown.calls)
	require.Equal(t, "upstream-a", repo.modelCooldown.model)
	require.Contains(t, rec.Body.String(), "delivered-text")
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
	require.False(t, svc.getOpenAIAccountModelTransientState().isBlocked(account.ID, "upstream-b", time.Now()))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}
