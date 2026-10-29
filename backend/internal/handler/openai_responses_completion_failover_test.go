package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type completionFailoverUpstream struct {
	service.HTTPUpstream
	calls          []int64
	delayFailure   bool
	failAll        bool
	delayHeaders   bool
	emptyStructure bool
	directFailure  bool
}

func (u *completionFailoverUpstream) Do(_ *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, id)
	if id == 9910 && u.delayHeaders {
		time.Sleep(1200 * time.Millisecond)
	}
	if id == 9911 && u.emptyStructure {
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"server_error","message":"temporary upstream failure"}}`))}, nil
	}
	data := "data: {\"type\":\"response.output_text.delta\",\"delta\":\" \\n\\t\"}\n\n"
	if u.emptyStructure {
		data = "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"content\":[]}}\n\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"temporary failure\"}}}\n\n"
	}
	if u.directFailure {
		data = "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"temporary failure\"}}}\n\n"
	}
	if id == 9913 && !u.failAll {
		data = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"healthy-output\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	}
	if id == 9910 && u.delayFailure {
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			if _, err := io.WriteString(writer, data); err != nil {
				return
			}
			timer := time.NewTimer(1200 * time.Millisecond)
			defer timer.Stop()
			<-timer.C
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data))}, nil
}

func (u *completionFailoverUpstream) DoWithTLS(r *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, proxy, id, concurrency)
}

func TestOpenAIResponsesSwitchesAccountAfterFakeFirstOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []struct{ keepalive, failAll, delayHeaders, emptyStructure, directFailure bool }{{}, {keepalive: true}, {keepalive: true, failAll: true}, {keepalive: true, delayHeaders: true}, {emptyStructure: true}, {directFailure: true}} {
		keepalive := scenario.keepalive
		for _, passthrough := range []bool{false, true} {
			t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough]+map[bool]string{false: "", true: "/keepalive"}[keepalive]+map[bool]string{false: "", true: "/exhausted"}[scenario.failAll]+map[bool]string{false: "", true: "/slow-headers"}[scenario.delayHeaders]+map[bool]string{false: "", true: "/empty-structure"}[scenario.emptyStructure]+map[bool]string{false: "", true: "/direct-failure"}[scenario.directFailure], func(t *testing.T) {
				groupID := int64(4204)
				accounts := []service.Account{}
				for i, id := range []int64{9910, 9911, 9912, 9913} {
					accounts = append(accounts, service.Account{ID: id, Name: "completion-test", Platform: service.PlatformOpenAI,
						Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: i + 1,
						Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
						Extra:       map[string]any{"openai_passthrough": passthrough},
					})
				}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Gateway.MaxAccountSwitches = 1
				if keepalive {
					cfg.Gateway.StreamKeepaliveInterval = 1
				}
				repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
				upstream := &completionFailoverUpstream{delayFailure: keepalive, failAll: scenario.failAll, delayHeaders: scenario.delayHeaders, emptyStructure: scenario.emptyStructure, directFailure: scenario.directFailure}
				cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(cache.Stop)
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-6.1-sol","input":"hello","stream":true}`))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1804, GroupID: &groupID, User: &service.User{ID: 1704, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1704, Concurrency: 0})
				h.Responses(c)
				require.Equal(t, []int64{9910, 9911, 9912, 9913}, upstream.calls)
				require.Equal(t, http.StatusOK, rec.Code)
				require.NotContains(t, rec.Body.String(), "fake-first-output")
				if scenario.failAll {
					require.Contains(t, rec.Body.String(), `"type":"response.failed"`)
					require.Contains(t, rec.Body.String(), "upstream_error")
					require.NotContains(t, rec.Body.String(), "internal_error")
					return
				}
				require.Contains(t, rec.Body.String(), "healthy-output")
				require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.completed"`))
				if keepalive {
					require.True(t, strings.HasPrefix(rec.Body.String(), ":"), "已提交心跳仍应成功切号")
				}
			})
		}
	}
}

func TestOpenAIChatSwitchesAccountWithRequestScopedPlaceholder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(4204)
	accounts := []service.Account{}
	for i, id := range []int64{9910, 9911, 9912, 9913} {
		accounts = append(accounts, service.Account{ID: id, Name: "completion-test", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: i + 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
			Extra:       map[string]any{"openai_responses_supported": true},
		})
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	upstream := &completionFailoverUpstream{delayFailure: true}
	cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(cache.Stop)
	gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-6-luna","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1804, GroupID: &groupID, User: &service.User{ID: 1704, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1704, Concurrency: 0})
	h.ChatCompletions(c)
	require.Equal(t, []int64{9910, 9911, 9912, 9913}, upstream.calls, "占位之后仍应排除失败账号，遍历到健康账号")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, strings.Count(rec.Body.String(), ": \n\n"), "切号不能重置请求级占位")
	require.Contains(t, rec.Body.String(), "healthy-output")
	require.Contains(t, rec.Body.String(), `"model":"gpt-6-luna"`, "手动选择的模型在切号后保持不变")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "data: [DONE]"))
	require.NotNil(t, service.TextStreamFirstPacketMs(c))
}
