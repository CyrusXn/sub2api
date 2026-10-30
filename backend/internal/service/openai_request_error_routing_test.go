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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIBalanceErrorDoesNotFailoverOrChangeAccount(t *testing.T) {
	for _, status := range []int{400, 402, 403, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, rateLimitService: rl}
			rl.SetAccountRuntimeBlocker(svc)
			account := openAIModelNotFoundTempAccount()
			account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
				"error_code": float64(status), "keywords": []any{"balance"}, "duration_minutes": float64(10), "scope": "account",
			}}
			body := []byte(`{"error":{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}}`)
			require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "Insufficient account balance", body))
			require.False(t, shouldFailoverOpenAIPassthroughResponse(account, status, body))
			require.NotEqual(t, ErrorPolicyTempUnscheduled, rl.CheckErrorPolicy(context.Background(), account, status, body, "gpt-6"))
			require.False(t, rl.HandleUpstreamError(context.Background(), account, status, nil, body, "gpt-6"))
			require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, body, "gpt-6"))
			require.Zero(t, repo.tempCalls)
			require.Empty(t, repo.modelRateLimitCalls)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			require.True(t, account.IsSchedulable())
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
		require.False(t, openAIStreamFailedEventShouldFailover(payload, "Insufficient account balance"))
		require.False(t, openAIStreamErrorEventShouldFailover(payload, "Insufficient account balance"))
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
				payload := []byte(fmt.Sprintf(`{"type":"error","error":{"status_code":%d,"type":"server_error","message":"request rejected"}}`, status))
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

func TestOpenAICompatStreamBalanceDoesNotBecomeGatewayFailure(t *testing.T) {
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
			require.False(t, errors.As(err, &failover))
			require.Equal(t, 403, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "Insufficient account balance")
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
			switch path {
			case "responses":
				_, err := svc.handleErrorResponse(context.Background(), resp, c, account, nil, "gpt-6-sol")
				require.Error(t, err)
			case "compat":
				_, err := svc.handleCompatErrorResponse(resp, c, account, func(_ *gin.Context, status int, typ, message string) {
					c.JSON(status, gin.H{"error": gin.H{"type": typ, "message": message}})
				})
				require.Error(t, err)
			case "passthrough":
				require.Error(t, svc.handleErrorResponsePassthrough(context.Background(), resp, c, account, nil, []byte(body)))
			}
			require.Equal(t, 403, rec.Code)
			require.Contains(t, rec.Body.String(), "Insufficient account balance")
		})
	}
}
