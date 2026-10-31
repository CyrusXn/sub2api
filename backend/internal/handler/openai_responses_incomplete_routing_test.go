//go:build unit

package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type incompleteRoutingUpstream struct {
	service.HTTPUpstream
	calls   []int64
	visible bool
	allFail bool
}

func (u *incompleteRoutingUpstream) Do(_ *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, id)
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_good\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"healthy-account-output\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_good\",\"model\":\"gpt-5.2\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"healthy-account-output\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	if id == 9910 || u.allFail {
		body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bad\",\"status\":\"in_progress\"}}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_bad\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"failed-account-hidden-data\"}}\n\n"
		if u.visible {
			body += "data: {\"type\":\"response.output_text.delta\",\"delta\":\"already-delivered\"}\n\n"
		}
		body += "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_bad\",\"status\":\"incomplete\"}}\n\n"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *incompleteRoutingUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestOpenAIResponsesIncompleteRoutesSameGroupHTTP(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, scenario := range []string{"hidden_then_next", "hidden_all_fail", "visible_then_next_request"} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, scenario), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				groupID := int64(4204)
				repo := &requestErrorRoutingRepo{}
				for i := range 3 {
					repo.accounts = append(repo.accounts, service.Account{
						ID: int64(9910 + i), Name: "隔离流式测试账号", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Priority: i + 1,
						Credentials: map[string]any{"api_key": "test-only", "base_url": "https://provider.example/v1"},
						Extra: map[string]any{"openai_passthrough": passthrough, "upstream_model_catalog": map[string]any{
							"models": []string{"gpt-5.2", "gpt-6-luna"},
						}},
					})
				}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Gateway.MaxAccountSwitches = 1
				upstream := &incompleteRoutingUpstream{visible: scenario == "visible_then_next_request", allFail: scenario == "hidden_all_fail"}
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				rl := service.NewRateLimitService(repo, nil, cfg, nil, nil)
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), rl, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
				router := gin.New()
				router.POST("/v1/responses", func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1804, GroupID: &groupID, User: &service.User{ID: 1704, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}})
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1704})
					h.Responses(c)
				})
				site := httptest.NewServer(router)
				t.Cleanup(site.Close)
				request := func(model string) string {
					resp, err := site.Client().Post(site.URL+"/v1/responses", "application/json", strings.NewReader(fmt.Sprintf(`{"model":%q,"input":"hello","stream":true}`, model)))
					require.NoError(t, err)
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					return string(body)
				}
				body := request("gpt-5.2")
				switch scenario {
				case "hidden_then_next":
					require.Equal(t, []int64{9910, 9911}, upstream.calls, "同一用户请求必须从失败 A 切到同组 B")
					require.Contains(t, body, "healthy-account-output")
					require.NotContains(t, body, "failed-account-hidden-data")
					require.NotContains(t, body, "response.incomplete")
					require.Equal(t, []int64{9910}, repo.modelCooldowns)
				case "hidden_all_fail":
					require.Equal(t, []int64{9910, 9911, 9912}, upstream.calls, "全部失败也必须去重遍历，不能提前结束或回环")
					require.NotContains(t, body, "failed-account-hidden-data")
					require.Contains(t, body, "error")
					require.ElementsMatch(t, []int64{9910, 9911, 9912}, repo.modelCooldowns)
				case "visible_then_next_request":
					require.Equal(t, []int64{9910}, upstream.calls, "已交付正文不能重复执行当前请求")
					require.Contains(t, body, "already-delivered")
					require.Contains(t, body, "response.incomplete")
					require.Equal(t, []int64{9910}, repo.modelCooldowns)
					upstream.calls = nil
					body = request("gpt-5.2")
					require.Equal(t, []int64{9911}, upstream.calls, "同用户下一笔请求必须避开仍在冷却的 A")
					require.Contains(t, body, "healthy-account-output")
				}
				require.Empty(t, repo.accountErrors, "瞬时故障只冷却当前模型，不停用整账号")
			})
		}
	}
}
