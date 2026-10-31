//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIUpstreamBalanceStopsAccountAndAllowsFailover(t *testing.T) {
	for _, status := range []int{400, 402, 403, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, rateLimitService: rl}
			rl.SetAccountRuntimeBlocker(svc)
			account := openAIModelNotFoundTempAccount()
			account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
				"error_code": float64(status), "keywords": []any{"balance"}, "duration_minutes": float64(10), "scope": "account",
			}}
			body := []byte(`{"error":{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}}`)
			require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "Insufficient account balance", body))
			require.True(t, shouldFailoverOpenAIPassthroughResponse(account, status, body))
			require.NotEqual(t, ErrorPolicyTempUnscheduled, rl.CheckErrorPolicy(context.Background(), account, status, body, "gpt-6"))
			require.True(t, rl.HandleUpstreamError(context.Background(), account, status, nil, body, "gpt-6"))
			require.True(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, body, "gpt-6"))
			require.Positive(t, repo.setErrCalls)
			require.Zero(t, repo.tempCalls)
			require.Empty(t, repo.modelRateLimitCalls)
			require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIModelUnsupported404NeverDisablesWholeAccount(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
	rl.SetAccountRuntimeBlocker(svc)
	account := openAIModelNotFoundTempAccount()
	account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
		"error_code": float64(404), "keywords": []any{"not supported"}, "duration_minutes": float64(10), "scope": "account",
	}}
	body := []byte(`{"error":{"message":"Model 'gpt-6-luna' is not supported"}}`)
	require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, 404, "", body))
	require.True(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, 404, nil, body, "gpt-6-luna"))
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-6-luna", repo.modelRateLimitCalls[0].scope)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, account.IsSchedulableForModelWithContext(context.Background(), "gpt-6-sol"))
}

func TestOpenAI404CustomErrorPolicyDoesNotDisableAccount(t *testing.T) {
	for _, model := range []string{"", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := openAIModelNotFoundTempAccount()
			account.Credentials["custom_error_codes_enabled"] = true
			account.Credentials["custom_error_codes"] = []any{float64(404)}
			body := []byte(`{"error":{"message":"upstream endpoint missing"}}`)
			rl.HandleUpstreamError(context.Background(), account, 404, nil, body, model)
			require.Zero(t, repo.setErrCalls)
			require.Zero(t, repo.tempCalls)
		})
	}
}

func TestOpenAIStreamBalanceAndModelErrorRouting(t *testing.T) {
	for _, envelope := range []string{
		`{"type":"error","error":{"code":"insufficient_balance","message":"Insufficient account balance"}}`,
		`{"type":"response.failed","response":{"error":{"code":"insufficient_balance","message":"Insufficient account balance"}}}`,
	} {
		payload := []byte(envelope)
		require.True(t, openAIStreamFailedEventShouldFailover(payload, "Insufficient account balance"))
		require.True(t, openAIStreamErrorEventShouldFailover(payload, "Insufficient account balance"))
		require.Equal(t, 403, openAIStreamFailureStatus(payload, "Insufficient account balance"))
	}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"model_not_found","message":"Model gpt-6-luna is not supported"}}}`)
	require.Equal(t, 404, openAIStreamFailureStatus(payload, "Model gpt-6-luna is not supported"))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, "Model gpt-6-luna is not supported"))
}

func TestOpenAIStreamExplicitRequestErrorsDoNotSwitchAccounts(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 405, 413, 422} {
		for _, shape := range []string{"error", "response.error"} {
			t.Run(fmt.Sprintf("%s/%d", shape, status), func(t *testing.T) {
				payload := []byte(fmt.Sprintf(`{"type":"error","error":{"status_code":%d,"type":"invalid_request_error","message":"request rejected"}}`, status))
				if shape == "response.error" {
					payload = []byte(fmt.Sprintf(`{"type":"response.failed","response":{"error":{"status_code":%d,"message":"request rejected"}}}`, status))
				}
				require.Equal(t, status, openAIStreamFailureStatus(payload, "request rejected"))
				require.False(t, openAIStreamFailedEventShouldFailover(payload, "request rejected"))
				require.False(t, openAIStreamErrorEventShouldFailover(payload, "request rejected"))
			})
		}
	}
}

func TestOpenAIStreamModel404CoolsOnlyRequestedModel(t *testing.T) {
	for _, transport := range []string{"sse", "ws"} {
		t.Run(transport, func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
			rl.SetAccountRuntimeBlocker(svc)
			account := openAIModelNotFoundTempAccount()
			payload := []byte(`{"type":"response.failed","response":{"error":{"code":"model_not_found","message":"Model gpt-6-luna is not supported"}}}`)
			if transport == "ws" {
				require.True(t, svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-6-luna", nil, payload))
			} else {
				err := svc.newOpenAIStreamFailoverErrorWithModel(nil, account, false, "", payload, "Model gpt-6-luna is not supported", "gpt-6-luna")
				require.Equal(t, 404, err.StatusCode)
				require.True(t, isUpstreamModelNotFoundError(err.StatusCode, err.ResponseBody))
			}
			require.Len(t, repo.modelRateLimitCalls, 1)
			require.Equal(t, "gpt-6-luna", repo.modelRateLimitCalls[0].scope)
			require.Zero(t, repo.tempCalls)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			require.True(t, account.IsSchedulableForModelWithContext(context.Background(), "gpt-6-sol"))
		})
	}
}

func TestOpenAIModelCatalogPreservedOnAccountEdit(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprint(stored), func(t *testing.T) {
			extra := map[string]any{}
			catalog := UpstreamModelCatalogSnapshot{Models: []string{"gpt-5.2"}}
			if stored {
				extra[UpstreamModelCatalogExtraKey] = catalog
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
				Credentials: map[string]any{"api_key": "test-only"}, Extra: extra,
			}}}
			got, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{UpstreamModelCatalogExtraKey: map[string]any{"models": []string{"injected"}}}})
			require.NoError(t, err)
			if stored {
				require.Equal(t, catalog, got.Extra[UpstreamModelCatalogExtraKey])
			} else {
				require.NotContains(t, got.Extra, UpstreamModelCatalogExtraKey)
			}
		})
	}
}

func TestOpenAITextCatalogDoesNotBlockOAuthImageTool(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		UpstreamModelCatalogExtraKey: UpstreamModelCatalogSnapshot{Models: []string{"gpt-6-sol"}},
	}}
	require.True(t, account.IsModelSupported("gpt-image-2"))
}

func TestOpenAIErrorRoutingDoesNotReadEchoedUserContent(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: &modelNotFoundManagedAccountRepo{}}
	account := newOpenAIUpstreamErrorTestAccount()
	for _, status := range []int{429, 502, 503} {
		body := []byte(`{"error":{"message":"temporary upstream failure"},"input":"Insufficient account balance; model not found"}`)
		require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "temporary upstream failure", body))
	}
	require.False(t, isUpstreamModelNotFoundError(404, []byte(`{"error":{"message":"endpoint not found"},"input":"model not found"}`)))
	for _, body := range []string{
		`{"detail":{"message":"temporary failure","input":"insufficient balance; model not found"}}`,
		`{"error":{"message":{"input":"insufficient balance; model not found"}}}`,
	} {
		require.False(t, isUpstreamInsufficientBalanceError(502, []byte(body)))
		require.False(t, isUpstreamModelNotFoundError(404, []byte(body)))
	}
}

func TestOpenAICompatStreamUpstreamBalanceAllowsFailover(t *testing.T) {
	for _, path := range []string{"raw_chat", "chat_buffered", "chat_stream", "messages_buffered", "messages_stream"} {
		t.Run(path, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			payload := `{"type":"response.failed","response":{"id":"resp_test","object":"response","status":"failed","error":{"code":"insufficient_balance","message":"Insufficient account balance"},"output":[]}}`
			if path == "raw_chat" {
				payload = `{"error":{"code":"insufficient_balance","message":"Insufficient account balance"}}`
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + payload + "\n\n"))}
			svc := &OpenAIGatewayService{}
			account := rawChatCompletionsTestAccount()
			var err error
			switch path {
			case "raw_chat":
				_, err = svc.streamRawChatCompletions(c, resp, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", nil, nil, time.Now(), 0)
			case "chat_buffered":
				_, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now())
			case "chat_stream":
				_, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now(), 0)
			case "messages_buffered":
				_, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now())
			case "messages_stream":
				_, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now())
			}
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.True(t, errors.As(err, &failover))
			require.Equal(t, NextAccountRetry, failover.NextAccountAction)
			require.Equal(t, GatewayFailureScopeAccount, failover.Scope)
			require.Empty(t, rec.Body.String(), "切号前不能把上游欠费伪装成用户余额不足写出")
		})
	}
}

func TestOpenAIRequestErrorsNeverPenalizeAccount(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"invalid_request_error","code":"invalid_parameter","message":"Invalid prompt value: Insufficient account balance"}}`,
		`{"error":{"code":"context_length_exceeded","message":"Request context is too long: Insufficient account balance"}}`,
		`{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"tools","message":"bad request parameter"}}`,
		`{"error":{"message":"The request exceeds the context limit.","param":"input"}}`,
		`{"type":"response.failed","response":{"error":{"type":"invalid_request_error","message":"The request exceeds the context limit.","param":"input"}}}`,
	} {
		for _, status := range []int{400, 403, 429, 502, 503} {
			t.Run(fmt.Sprintf("%d/%s", status, body), func(t *testing.T) {
				repo := &errorPolicyRepoStub{}
				rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
				svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
				rl.SetAccountRuntimeBlocker(svc)
				account := openAIModelNotFoundTempAccount()
				account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
					"error_code": float64(status), "keywords": []any{"request"}, "duration_minutes": float64(10), "scope": "account",
				}}
				payload := []byte(body)
				require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "", payload))
				require.False(t, shouldFailoverOpenAIPassthroughResponse(account, status, payload))
				require.Equal(t, ErrorPolicySkipped, rl.CheckErrorPolicy(context.Background(), account, status, payload, "gpt-test"))
				require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, payload, "gpt-test"))
				require.False(t, rl.HandleUpstreamError(context.Background(), account, status, nil, payload, "gpt-test"))
				err := newOpenAIUpstreamFailoverError(status, nil, payload, "", true)
				svc.ReportOpenAIAccountScheduleResult(account, "gpt-test", false, nil, err)
				require.Zero(t, repo.tempCalls)
				require.Zero(t, repo.setErrCalls)
				require.Empty(t, repo.modelRateLimitCalls)
				require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"))
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			})
		}
	}
}

func TestOpenAIExplicitUpstreamTypeAllowsFailoverDespite403(t *testing.T) {
	account := newOpenAIUpstreamErrorTestAccount()
	svc := &OpenAIGatewayService{accountRepo: &modelNotFoundManagedAccountRepo{}}
	body := []byte(`{"error":{"type":"upstream_error","message":"upstream service unavailable"}}`)
	require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, 403, "", body))
	require.True(t, shouldFailoverOpenAIPassthroughResponse(account, 403, body))
}

func TestOpenAIExplicitUpstream4xxOnlyCoolsRequestedModel(t *testing.T) {
	for _, status := range []int{400, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
			rl.SetAccountRuntimeBlocker(svc)
			account := openAIModelNotFoundTempAccount()
			body := []byte(`{"error":{"type":"upstream_error","message":"Upstream unavailable"}}`)
			require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "", body))
			require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, body, "gpt-test"))
			require.Zero(t, repo.setErrCalls)
			require.Zero(t, repo.tempCalls)
			require.Len(t, repo.modelRateLimitCalls, 1)
			require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"))
			require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-other"))
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIWS403UpstreamTerminalCoolsRequestedModel(t *testing.T) {
	for _, payload := range []string{
		`{"type":"error","error":{"type":"upstream_error","status_code":403,"message":"temporary upstream failure"}}`,
		`{"type":"response.failed","response":{"error":{"type":"upstream_error","status_code":403,"message":"temporary upstream failure"}}}`,
	} {
		repo := &errorPolicyRepoStub{}
		svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
		account := openAIModelNotFoundTempAccount()
		require.True(t, svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-test", nil, []byte(payload)))
		require.Len(t, repo.modelRateLimitCalls, 1)
		require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"))
		require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-other"))
		require.Zero(t, repo.setErrCalls)
		require.Zero(t, repo.tempCalls)
	}
}

func TestOpenAIWSUpstreamBalanceStopsAccount(t *testing.T) {
	for _, shape := range []string{"error", "response.failed", "dial400", "dial403"} {
		t.Run(shape, func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
			rl.SetAccountRuntimeBlocker(svc)
			account := openAIModelNotFoundTempAccount()
			payload := []byte(`{"type":"error","error":{"code":"insufficient_balance","message":"Insufficient account balance"}}`)
			if shape == "response.failed" {
				payload = []byte(`{"type":"response.failed","response":{"error":{"code":"insufficient_balance","message":"Insufficient account balance"}}}`)
			}
			if strings.HasPrefix(shape, "dial") {
				status := 400
				if shape == "dial403" {
					status = 403
				}
				svc.handleOpenAIWSDialTransientFailure(context.Background(), account, "gpt-test", &openAIWSDialError{StatusCode: status, ResponseBody: payload, Err: errors.New("handshake rejected")})
			} else {
				require.True(t, svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-test", nil, payload))
			}
			require.Equal(t, 1, repo.setErrCalls)
			require.Empty(t, repo.modelRateLimitCalls)
			require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIWSRequestErrorsNeverPenalizeAccount(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
	account := openAIModelNotFoundTempAccount()
	payload := []byte(`{"type":"error","error":{"status_code":502,"type":"invalid_request_error","param":"input","message":"The request exceeds the context limit."}}`)
	require.Zero(t, openAIWSPayloadTransientStatus(payload))
	require.False(t, svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-test", nil, payload))
	svc.handleOpenAIWSDialTransientFailure(context.Background(), account, "gpt-test", &openAIWSDialError{StatusCode: 502, ResponseBody: payload, Err: errors.New("request rejected")})
	require.Empty(t, repo.modelRateLimitCalls)
	require.Zero(t, repo.setErrCalls)
}

func TestOpenAIWSErrorRateLimitRecordsOnce(t *testing.T) {
	for _, status := range []int{429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			repo := &oauth429RateLimitRepo{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rl}
			account := &Account{ID: 5204, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			payload := []byte(fmt.Sprintf(`{"type":"error","error":{"status":%d,"code":"rate_limit_exceeded","type":"rate_limit_error"}}`, status))
			svc.handleOpenAIWSErrorEventTransientFailure(context.Background(), account, "gpt-test", nil, payload)
			svc.persistOpenAIWSRateLimitSignal(context.Background(), account, nil, payload, "rate_limit_exceeded", "rate_limit_error", "", "gpt-test")
			require.Equal(t, 1, repo.setModelRateLimitCalls)
		})
	}
}

func TestOpenAIWSV2PreOutputErrorsReturnRoutingDecision(t *testing.T) {
	for _, event := range []string{"error", "response.failed"} {
		for _, kind := range []string{"upstream", "balance", "request"} {
			t.Run(event+"/"+kind, func(t *testing.T) {
				cfg := newOpenAIWSV2TestConfig()
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				errJSON := `{"type":"upstream_error","status":502,"message":"Upstream unavailable"}`
				if kind == "balance" {
					errJSON = `{"type":"invalid_request_error","code":"insufficient_balance","message":"Insufficient account balance"}`
				} else if kind == "request" {
					errJSON = `{"type":"invalid_request_error","code":"context_length_exceeded","message":"The request exceeds the context limit."}`
				}
				payload := fmt.Sprintf(`{"type":"error","error":%s}`, errJSON)
				if event == "response.failed" {
					payload = fmt.Sprintf(`{"type":"response.failed","response":{"id":"test_failure","status":"failed","error":%s}}`, errJSON)
				}
				pool := newOpenAIWSConnPool(cfg)
				t.Cleanup(pool.Close)
				pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: [][]byte{[]byte(payload)}}})
				repo := &errorPolicyRepoStub{}
				svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo,
					rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil),
					httpUpstream:     &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
					openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
				account := &Account{ID: 5205, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test-only"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
				c, rec := newOpenAIUpstreamErrorTestContext(t)
				_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.5","stream":false,"input":"hello"}`))
				require.Error(t, err)
				var failover *UpstreamFailoverError
				if kind == "request" {
					require.False(t, errors.As(err, &failover))
					require.Equal(t, 400, rec.Code)
					require.Zero(t, repo.setErrCalls)
					require.Empty(t, repo.modelRateLimitCalls)
				} else {
					require.ErrorAs(t, err, &failover)
					require.True(t, failover.ShouldRetryNextAccount())
					require.Empty(t, rec.Body.String(), "正文写出前把失败交回同组切号循环")
					if kind == "balance" {
						require.Equal(t, 1, repo.setErrCalls)
					} else {
						svc.ReportOpenAIAccountScheduleResult(account, "gpt-5.5", false, nil, err)
						require.Zero(t, repo.setErrCalls)
						require.Len(t, repo.modelRateLimitCalls, 1)
					}
				}
			})
		}
	}
}

func TestOpenAIWSIngressPreOutputFailuresReturnFailover(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, event := range []struct{ name, payload string }{
			{"response_failed", `{"type":"response.failed","response":{"id":"isolated_rate_limit","status":"failed","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","status_code":429,"message":"Rate limit exceeded"}}}`},
			{"numeric_error", `{"type":"error","error":{"type":"upstream_error","status_code":429,"message":"busy"}}`},
			{"upstream_balance", `{"type":"error","error":{"type":"rate_limit_error","code":"insufficient_quota","message":"Insufficient account balance"}}`},
		} {
			t.Run(mode+"/"+event.name, func(t *testing.T) {
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.IngressModeDefault = mode
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				upstream := newStagedPassthroughConn()
				t.Cleanup(func() { _ = upstream.Close() })
				upstream.Send(event.payload)
				svc := newPassthroughLifecycleService(cfg, upstream)
				pool := newOpenAIWSConnPool(cfg)
				t.Cleanup(pool.Close)
				pool.setClientDialerForTest(&stagedPassthroughDialer{conn: upstream})
				svc.openaiWSPool = pool
				repo := &errorPolicyRepoStub{}
				svc.accountRepo = repo
				svc.rateLimitService = NewRateLimitService(repo, nil, cfg, nil, nil)
				account := passthroughLifecycleAccount()
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				site, result := startPassthroughLifecycleServer(t, ctx, svc, account)
				t.Cleanup(site.Close)
				conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(site.URL, "http"), nil)
				require.NoError(t, err)
				defer conn.CloseNow()
				require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)))
				_, frame, readErr := conn.Read(ctx)
				_ = conn.CloseNow()
				var routeErr error
				select {
				case routeErr = <-result:
				case <-ctx.Done():
					t.Fatal("隔离 WebSocket 请求未按时结束")
				}
				var failover *UpstreamFailoverError
				require.ErrorAs(t, routeErr, &failover, "上游故障必须在写出前交回同组切号循环")
				require.True(t, failover.ShouldRetryNextAccount())
				require.Error(t, readErr)
				require.Empty(t, frame, "切号前不能把上游错误发给客户端")
				svc.ReportOpenAIAccountScheduleResult(account, "gpt-5.5", false, nil, routeErr)
				if event.name == "upstream_balance" {
					require.Equal(t, 1, repo.setErrCalls, "上游余额不足必须停号，不能当作普通限流")
					require.Empty(t, repo.modelRateLimitCalls)
					require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
				} else {
					require.Len(t, repo.modelRateLimitCalls, 1, "限流副作用只能记录一次")
					require.Zero(t, repo.setErrCalls)
				}
				require.Zero(t, repo.tempCalls)
			})
		}
	}
}

func TestOpenAIWSIngressFailureEventPairCoolsOncePerTurn(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.IngressModeDefault = mode
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			upstream := newStagedPassthroughConn()
			t.Cleanup(func() { _ = upstream.Close() })
			svc := newPassthroughLifecycleService(cfg, upstream)
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: upstream})
			svc.openaiWSPool = pool
			repo := &errorPolicyRepoStub{}
			svc.accountRepo = repo
			svc.rateLimitService = NewRateLimitService(repo, nil, cfg, nil, nil)
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			turnDone := make(chan int, 2)
			site, result := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(turn int, _ *OpenAIForwardResult, _ error) { turnDone <- turn }}
			})
			t.Cleanup(site.Close)
			conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(site.URL, "http"), nil)
			require.NoError(t, err)
			defer conn.CloseNow()
			for turn := 1; turn <= 2; turn++ {
				require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)))
				upstream.Send(fmt.Sprintf(`{"type":"response.created","response":{"id":"pair_%d"}}`, turn))
				upstream.Send(`{"type":"response.output_text.delta","delta":"already delivered"}`)
				upstream.Send(`{"type":"error","error":{"type":"upstream_error","status_code":502,"message":"temporary upstream failure"}}`)
				upstream.Send(fmt.Sprintf(`{"type":"response.failed","response":{"id":"pair_%d","status":"failed","error":{"type":"upstream_error","status_code":502,"message":"temporary upstream failure"}}}`, turn))
				for frame := 0; frame < 4; frame++ {
					_, _, err := conn.Read(ctx)
					require.NoError(t, err, "已经输出的轮次不能切号重放")
				}
				select {
				case got := <-turnDone:
					require.Equal(t, turn, got)
				case <-ctx.Done():
					t.Fatal("隔离 WebSocket 故障轮次未结束")
				}
				require.Len(t, repo.modelRateLimitCalls, turn, "同一轮 error 和 response.failed 只能冷却一次，下一轮仍须记录")
				require.Equal(t, "gpt-5.5", repo.modelRateLimitCalls[turn-1].scope)
				require.Zero(t, repo.setErrCalls)
			}
			_ = conn.CloseNow()
			select {
			case <-result:
			case <-ctx.Done():
				t.Fatal("隔离 WebSocket 会话未结束")
			}
		})
	}
}

type routingFailureWSDialer struct {
	status int
	body   []byte
	calls  int
}

func TestOpenAIWSIngressHandshakeErrorsReturnRoutingDecision(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, kind := range []string{"request_429", "request_502", "upstream_403", "upstream_502", "auth", "balance", "model"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				cfg := newOpenAIWSV2TestConfig()
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
				cfg.Gateway.OpenAIWS.IngressModeDefault = mode
				dialer := &routingFailureWSDialer{status: 403, body: []byte(`{"error":{"type":"upstream_error","message":"upstream unavailable"}}`)}
				switch kind {
				case "request_429", "request_502":
					dialer.status = 429
					if kind == "request_502" {
						dialer.status = 502
					}
					dialer.body = []byte(`{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"context limit exceeded"}}`)
				case "upstream_502":
					dialer.status = 502
				case "auth":
					dialer.status = 401
					dialer.body = []byte(`{"error":{"type":"authentication_error","code":"invalid_api_key"}}`)
				case "balance":
					dialer.body = []byte(`{"error":{"type":"upstream_error","code":"insufficient_balance","message":"Insufficient account balance"}}`)
				case "model":
					dialer.status = 404
					dialer.body = []byte(`{"error":{"code":"model_not_found","message":"Model gpt-5.5 is not supported"}}`)
				}
				pool := newOpenAIWSConnPool(cfg)
				t.Cleanup(pool.Close)
				pool.setClientDialerForTest(dialer)
				repo := &errorPolicyRepoStub{}
				svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo, rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil),
					openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(),
					openaiWSPool: pool, openaiWSPassthroughDialer: dialer}
				account := &Account{ID: 5207, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test-only"}, Extra: map[string]any{"responses_websockets_v2_enabled": true, "openai_apikey_responses_websockets_v2_mode": mode}}
				result := make(chan error, 1)
				site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						result <- err
						return
					}
					defer conn.CloseNow()
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = r.Clone(r.Context())
					result <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "test-only", []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`), nil)
				}))
				t.Cleanup(site.Close)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(site.URL, "http"), nil)
				require.NoError(t, err)
				defer conn.CloseNow()
				select {
				case err = <-result:
				case <-ctx.Done():
					t.Fatal("隔离 WebSocket 握手未按时结束")
				}
				require.Error(t, err)
				require.Equal(t, 1, dialer.calls, "明确握手错误不能重试当前账号")
				var failover *UpstreamFailoverError
				if strings.HasPrefix(kind, "request_") {
					require.False(t, errors.As(err, &failover), "请求类型优先于 429/502 包装状态")
					require.Zero(t, repo.setErrCalls)
					require.Empty(t, repo.modelRateLimitCalls)
				} else {
					require.ErrorAs(t, err, &failover, "上游错误必须交给 handler 切换同组账号")
					require.True(t, failover.ShouldRetryNextAccount())
					if kind == "balance" {
						require.Equal(t, 1, repo.setErrCalls)
					}
				}
			})
		}
	}
}

func (d *routingFailureWSDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	d.calls++
	return nil, d.status, nil, &openAIWSHandshakeError{Body: d.body, Err: errors.New("isolated upstream rejection")}
}

func TestOpenAIWSV2HandshakeErrorsReturnRoutingDecision(t *testing.T) {
	for _, kind := range []string{"balance", "upstream", "request"} {
		t.Run(kind, func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			dialer := &routingFailureWSDialer{status: 403, body: []byte(`{"error":{"type":"upstream_error","message":"upstream unavailable"}}`)}
			if kind == "balance" {
				dialer.body = []byte(`{"error":{"type":"invalid_request_error","code":"insufficient_balance","message":"Insufficient account balance"}}`)
			} else if kind == "request" {
				dialer.status = 502
				dialer.body = []byte(`{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"context limit exceeded"}}`)
			}
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			pool.setClientDialerForTest(dialer)
			repo := &errorPolicyRepoStub{}
			upstream := &httpUpstreamRecorder{resp: newOpenAIUpstreamErrorResponse(200, `{"id":"must_not_retry_same_account"}`)}
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo,
				rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil), httpUpstream: upstream,
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 5206, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test-only"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			c, rec := newOpenAIUpstreamErrorTestContext(t)
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.5","input":"hello"}`))
			require.Error(t, err)
			require.Nil(t, upstream.lastReq, "明确业务错误不能回退同账号 HTTP")
			require.Equal(t, 1, dialer.calls)
			var failover *UpstreamFailoverError
			if kind == "request" {
				require.False(t, errors.As(err, &failover))
				require.Equal(t, 400, rec.Code)
				require.Zero(t, repo.setErrCalls)
				require.Empty(t, repo.modelRateLimitCalls)
			} else {
				require.ErrorAs(t, err, &failover)
				require.True(t, failover.ShouldRetryNextAccount())
				require.Empty(t, rec.Body.String())
				svc.ReportOpenAIAccountScheduleResult(account, "gpt-5.5", false, nil, err)
				if kind == "balance" {
					require.Equal(t, 1, repo.setErrCalls)
					require.Empty(t, repo.modelRateLimitCalls)
				} else {
					require.Zero(t, repo.setErrCalls)
					require.Len(t, repo.modelRateLimitCalls, 1)
				}
			}
		})
	}
}

func TestOpenAICompatBufferedRequestErrorsPreserveStatus(t *testing.T) {
	for _, messages := range []bool{false, true} {
		for _, shape := range []string{"response_error", "bare_error", "root_status"} {
			for _, status := range []int{402, 405, 413, 422} {
				t.Run(fmt.Sprintf("messages_%t/%s/%d", messages, shape, status), func(t *testing.T) {
					payload := fmt.Sprintf(`{"type":"response.failed","response":{"status":"failed","error":{"status_code":%d,"message":"request rejected"}}}`, status)
					if shape == "bare_error" {
						payload = fmt.Sprintf(`{"type":"error","error":{"status_code":%d,"message":"request rejected"}}`, status)
					} else if shape == "root_status" {
						payload = fmt.Sprintf(`{"type":"response.failed","status_code":%d,"response":{"status":"failed","error":{"message":"request rejected"}}}`, status)
					}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + payload + "\n\n"))}
					svc := &OpenAIGatewayService{}
					var err error
					if messages {
						_, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, rawChatCompletionsTestAccount(), "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now())
					} else {
						_, err = svc.handleChatBufferedStreamingResponse(resp, c, rawChatCompletionsTestAccount(), "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now())
					}
					require.Error(t, err)
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover))
					require.Equal(t, status, rec.Code, rec.Body.String())
				})
			}
		}
	}
}

func TestOpenAIModelListFiltersBeforeScheduling(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{"upstream_model_catalog": map[string]any{
			"models": []string{"gpt-6-sol", "gpt-6-luna"},
		}}}
	require.True(t, account.IsModelSupported("gpt-6-sol"))
	require.True(t, account.IsModelSupported("gpt-6-luna"))
	require.False(t, account.IsModelSupported("claude-sonnet-4-6"))
	require.False(t, account.IsModelSupported("gpt-nonexistent"))
	account.Credentials = map[string]any{"model_mapping": map[string]any{"public-alias": "gpt-6-sol", "missing-alias": "missing-upstream"}}
	require.True(t, account.IsModelSupported("public-alias"))
	require.True(t, account.IsModelSupported("missing-alias"), "管理员明确配置的模型优先于发现清单")
}

func TestOpenAIModelListDoesNotRestrictChangedUpstream(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://first.example/v1", "api_key": "test-only"}, Extra: map[string]any{}}
	account.Extra[UpstreamModelCatalogExtraKey] = UpstreamModelCatalogSnapshot{Models: []string{"gpt-5.2"}, Binding: upstreamModelCatalogBinding(account)}
	require.False(t, account.IsModelSupported("new-model"))
	account.Credentials["base_url"] = "https://second.example/v1"
	require.True(t, account.IsModelSupported("new-model"))
}

func TestOpenAIModelListIsSavedWhenCapabilitiesAreIncomplete(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"custom-live-model"}]}`))},
		{StatusCode: 502, Body: io.NopCloser(strings.NewReader(`{"error":"unavailable"}`))},
	}}
	repo := &upstreamModelMetadataRepoStub{}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key", "base_url": "https://provider.example/v1"}}
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Contains(t, repo.updates, "upstream_model_catalog")
	require.True(t, account.IsModelSupported("custom-live-model"))
	require.False(t, account.IsModelSupported("missing-model"))
}

func TestOpenAIBalanceErrorTerminalResponsePaths(t *testing.T) {
	for _, path := range []string{"responses", "compat", "passthrough"} {
		t.Run(path, func(t *testing.T) {
			c, rec := newOpenAIUpstreamErrorTestContext(t)
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := newOpenAIUpstreamErrorTestAccount()
			body := `{"error":{"code":"insufficient_balance","message":"Insufficient account balance"}}`
			resp := newOpenAIUpstreamErrorResponse(403, body)
			var err error
			switch path {
			case "responses":
				_, err = svc.handleErrorResponse(context.Background(), resp, c, account, nil, "gpt-6-sol")
				require.Error(t, err)
			case "compat":
				_, err = svc.handleCompatErrorResponse(resp, c, account, func(_ *gin.Context, status int, typ, message string) {
					c.JSON(status, gin.H{"error": gin.H{"type": typ, "message": message}})
				})
				require.Error(t, err)
			case "passthrough":
				err = svc.handleErrorResponsePassthrough(context.Background(), resp, c, account, nil, []byte(body))
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.ShouldRetryNextAccount())
			require.Equal(t, GatewayFailureScopeAccount, failover.Scope)
			require.Empty(t, rec.Body.String())
		})
	}
}
