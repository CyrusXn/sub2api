//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/tidwall/gjson"
)

type requestErrorRoutingRepo struct {
	openAIWSFailoverHandlerAccountRepoStub
	accountErrors  []int64
	modelCooldowns []int64
}

func (r *requestErrorRoutingRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return r.accounts, nil
}

func (r *requestErrorRoutingRepo) SetError(_ context.Context, id int64, _ string) error {
	r.accountErrors = append(r.accountErrors, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Status = service.StatusError
			r.accounts[i].Schedulable = false
		}
	}
	return nil
}

func (r *requestErrorRoutingRepo) SetModelRateLimit(_ context.Context, id int64, model string, until time.Time, _ ...string) error {
	r.modelCooldowns = append(r.modelCooldowns, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			if r.accounts[i].Extra == nil {
				r.accounts[i].Extra = map[string]any{}
			}
			r.accounts[i].Extra["model_rate_limits"] = map[string]any{model: map[string]any{"rate_limit_reset_at": until.Format(time.RFC3339)}}
		}
	}
	return nil
}

type requestErrorRoutingUpstream struct {
	service.HTTPUpstream
	calls        []int64
	status       int
	body         string
	allAccounts  bool
	failBeforeID int64
	sse          bool
}

func (u *requestErrorRoutingUpstream) Do(_ *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, id)
	status, body := http.StatusOK, `{"id":"resp_ok","object":"response","model":"gpt-5.2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	if (id == 9910 || u.allAccounts || id < u.failBeforeID) && u.status != 0 {
		status, body = u.status, u.body
	}
	contentType := "application/json"
	if u.sse && (id == 9910 || u.allAccounts) {
		status, contentType = 200, "text/event-stream"
		body = "event: response.failed\ndata: " + u.body + "\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *requestErrorRoutingUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestOpenAIRequestErrorRoutingSiteHTTP(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, scenario := range []string{"six_last_ok", "six_all_fail", "upstream_auth", "unsupported_then_valid", "only_second_supports", "balance", "balance_400", "sse_balance", "context_limit", "sse_context_limit", "request_502", "sse_request_502", "upstream_403", "all_502", "all_503", "404_then_valid", "all_404", "sse_404_then_valid", "sse_all_404", "401", "402", "403", "405", "413", "422", "429", "502", "503", "sse_402", "sse_405", "sse_413", "sse_422"} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, scenario), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				groupID := int64(4203)
				repo := &requestErrorRoutingRepo{}
				accountCount := 2
				if strings.HasPrefix(scenario, "six_") {
					accountCount = 6
				}
				for i := 0; i < accountCount; i++ {
					models := []string{"gpt-5.2", "gpt-6-luna"}
					if scenario == "only_second_supports" && i == 0 {
						models = models[:1]
					}
					repo.accounts = append(repo.accounts, service.Account{
						ID: int64(9910 + i), Name: "隔离测试账号", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Priority: i + 1,
						Credentials: map[string]any{"api_key": "test-only", "base_url": "https://provider.example/v1"},
						Extra:       map[string]any{"openai_passthrough": passthrough, "upstream_model_catalog": map[string]any{"models": models}},
					})
				}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Gateway.MaxAccountSwitches = 3
				if accountCount == 6 {
					cfg.Gateway.MaxAccountSwitches = 1
				}
				upstream := &requestErrorRoutingUpstream{}
				switch scenario {
				case "six_last_ok", "six_all_fail":
					upstream.status, upstream.body, upstream.failBeforeID = 502, `{"error":{"type":"upstream_error","message":"temporary upstream failure"}}`, 9915
					upstream.allAccounts = scenario == "six_all_fail"
				case "upstream_auth":
					upstream.status, upstream.body = 401, `{"error":{"type":"authentication_error","code":"invalid_api_key","message":"credential rejected"}}`
				case "balance":
					upstream.status, upstream.body = 403, `{"error":{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}}`
				case "balance_400":
					upstream.status, upstream.body = 400, `{"error":{"type":"invalid_request_error","code":"insufficient_balance","message":"Insufficient account balance"}}`
				case "upstream_403":
					upstream.status, upstream.body = 403, `{"error":{"type":"upstream_error","message":"upstream service unavailable"}}`
				case "all_502", "all_503":
					_, _ = fmt.Sscan(strings.TrimPrefix(scenario, "all_"), &upstream.status)
					upstream.allAccounts = true
					upstream.body = `{"error":{"type":"upstream_error","message":"temporary upstream failure"}}`
				case "context_limit", "sse_context_limit":
					upstream.status, upstream.body = 400, `{"error":{"type":"invalid_request_error","param":"input","message":"The request exceeds the context limit."}}`
				case "request_502", "sse_request_502":
					upstream.status, upstream.body = 502, `{"error":{"type":"invalid_request_error","param":"tools","message":"bad request parameter"}}`
				case "sse_balance":
					upstream.sse = true
					upstream.body = `{"type":"response.failed","response":{"error":{"code":"insufficient_balance","message":"Insufficient account balance"}}}`
				case "sse_402", "sse_405", "sse_413", "sse_422":
					var status int
					_, _ = fmt.Sscan(strings.TrimPrefix(scenario, "sse_"), &status)
					upstream.sse = true
					upstream.body = fmt.Sprintf(`{"type":"response.failed","response":{"error":{"status_code":%d,"message":"request rejected"}}}`, status)
				case "all_404", "sse_all_404":
					upstream.status, upstream.body, upstream.allAccounts = 404, `{"error":{"message":"Model gpt-6-luna is not supported"}}`, true
				case "404_then_valid", "sse_404_then_valid":
					upstream.status, upstream.body = 404, `{"error":{"message":"Model gpt-6-luna is not supported"}}`
				case "401", "402", "403", "405", "413", "422", "429", "502", "503":
					_, _ = fmt.Sscan(scenario, &upstream.status)
					upstream.body = `{"error":{"message":"temporary upstream failure"}}`
				}
				if scenario == "sse_all_404" || scenario == "sse_404_then_valid" {
					upstream.sse = true
					upstream.body = `{"type":"response.failed","response":{"error":{"code":"model_not_found","message":"Model gpt-6-luna is not supported"}}}`
				}
				if scenario == "sse_context_limit" || scenario == "sse_request_502" {
					upstream.sse = true
					upstream.body = fmt.Sprintf(`{"type":"response.failed","response":{"error":{"status_code":%d,"type":"invalid_request_error","message":%q}}}`, upstream.status, gjson.Get(upstream.body, "error.message").String())
				}
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				rl := service.NewRateLimitService(repo, nil, cfg, nil, nil)
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), rl, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
				router := gin.New()
				router.POST("/v1/responses", func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1803, GroupID: &groupID, User: &service.User{ID: 1703, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}})
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1703})
					h.Responses(c)
				})
				site := httptest.NewServer(router)
				t.Cleanup(site.Close)
				request := func(model string) *httptest.ResponseRecorder {
					payload, _ := json.Marshal(map[string]any{"model": model, "input": "hello", "stream": false})
					req, err := http.NewRequest(http.MethodPost, site.URL+"/v1/responses", strings.NewReader(string(payload)))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					resp, err := site.Client().Do(req)
					require.NoError(t, err)
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					rec.Code = resp.StatusCode
					_, _ = rec.Body.Write(body)
					return rec
				}
				switch scenario {
				case "six_last_ok", "six_all_fail":
					rec := request("gpt-5.2")
					if scenario == "six_last_ok" {
						require.Equal(t, 200, rec.Code, rec.Body.String())
					} else {
						require.GreaterOrEqual(t, rec.Code, 500, rec.Body.String())
					}
					require.Equal(t, []int64{9910, 9911, 9912, 9913, 9914, 9915}, upstream.calls, "配置切换上限不能提前终止同组去重遍历")
					require.Empty(t, repo.accountErrors)
				case "upstream_auth":
					rec := request("gpt-5.2")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910, 9911}, upstream.calls, "供应侧明确认证失败要切下一账号")
				case "unsupported_then_valid":
					for _, model := range []string{"claude-sonnet-4-6", "gpt-definitely-missing"} {
						rec := request(model)
						require.Equal(t, 404, rec.Code, rec.Body.String())
						require.Equal(t, "model_not_found", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
					}
					require.Empty(t, upstream.calls, "模型不匹配不能调用上游")
					rec := request("gpt-5.2")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910}, upstream.calls)
				case "only_second_supports":
					rec := request("gpt-6-luna")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9911}, upstream.calls)
				case "balance", "balance_400", "sse_balance":
					rec := request("gpt-5.2")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910, 9911}, upstream.calls)
					require.Equal(t, []int64{9910}, repo.accountErrors)
					require.Empty(t, repo.modelCooldowns, "上游欠费为持久停用，不混入模型瞬时故障")
					upstream.status, upstream.sse, upstream.calls = 0, false, nil
					rec = request("gpt-6-luna")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9911}, upstream.calls, "未人工恢复的欠费账号不能因换模型再次进入调度")
				case "context_limit", "sse_context_limit", "request_502", "sse_request_502":
					rec := request("gpt-5.2")
					require.Equal(t, 400, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910}, upstream.calls)
					require.Empty(t, repo.accountErrors)
					require.Empty(t, repo.modelCooldowns)
					upstream.status, upstream.sse, upstream.calls = 0, false, nil
					rec = request("gpt-5.2")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910}, upstream.calls, "用户请求错误不能影响账号下一次正常请求")
				case "all_404", "sse_all_404":
					rec := request("gpt-6-luna")
					require.Equal(t, 404, rec.Code, rec.Body.String())
					require.Equal(t, "model_not_found", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
					require.Equal(t, []int64{9910, 9911}, upstream.calls, "每个账号只尝试一次，不能循环")
				case "all_502", "all_503":
					rec := request("gpt-5.2")
					require.GreaterOrEqual(t, rec.Code, 500, rec.Body.String())
					require.Equal(t, []int64{9910, 9911}, upstream.calls, "同分组故障全部遍历后才结束，不能回到 A")
					require.Empty(t, repo.accountErrors, "瞬时故障耗尽也不能永久关号")
					require.ElementsMatch(t, []int64{9910, 9911}, repo.modelCooldowns)
				case "404_then_valid", "sse_404_then_valid":
					rec := request("gpt-6-luna")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910, 9911}, upstream.calls)
					upstream.status, upstream.sse = 0, false
					upstream.calls = nil
					rec = request("gpt-5.2")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910}, upstream.calls, "404 后其他模型仍可使用原账号")
				case "sse_402", "sse_405", "sse_413", "sse_422":
					rec := request("gpt-5.2")
					var status int
					_, _ = fmt.Sscan(strings.TrimPrefix(scenario, "sse_"), &status)
					require.Equal(t, status, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910}, upstream.calls)
				case "401", "402", "403", "405", "413", "422":
					rec := request("gpt-5.2")
					require.GreaterOrEqual(t, rec.Code, 400, rec.Body.String())
					require.Equal(t, []int64{9910}, upstream.calls, "普通请求或认证错误不能换号重试")
				default:
					rec := request("gpt-5.2")
					require.Equal(t, 200, rec.Code, rec.Body.String())
					require.Equal(t, []int64{9910, 9911}, upstream.calls)
				}
				if scenario != "401" && scenario != "402" && scenario != "403" && scenario != "upstream_auth" && scenario != "upstream_403" && scenario != "balance" && scenario != "balance_400" && scenario != "sse_balance" {
					require.Empty(t, repo.accountErrors)
				}
			})
		}
	}
}
