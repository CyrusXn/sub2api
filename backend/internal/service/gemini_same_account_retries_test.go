//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	geminiRetryTest503Body       = `{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`
	geminiRetryTestSignatureBody = `{"error":{"code":400,"message":"Function call is missing a thought_signature in functionCall parts.","status":"INVALID_ARGUMENT"}}`
	geminiRetryTestOKBody        = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`
)

func geminiSameAccountRetryTestAccount() *Account {
	return &Account{
		ID:          840,
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key"},
	}
}

func TestGeminiForward_SameAccountRetriesFollowConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, retries := range []int{0, 1} {
		for _, fw := range geminiSingleCheckForwarders {
			t.Run(fmt.Sprintf("%s/retries=%d", fw.name, retries), func(t *testing.T) {
				svc, upstream, _ := newGeminiSingleCheckService(geminiSequenceUpstreamResponse{status: http.StatusTooManyRequests, body: `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`})
				svc.cfg.Gateway.MaxSameAccountRetriesGemini = retries

				err, _ := fw.forward(t, svc, geminiSameAccountRetryTestAccount())

				var failoverErr *UpstreamFailoverError
				require.True(t, errors.As(err, &failoverErr), "同号重试耗尽应换号，got %T: %v", err, err)
				require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
				require.Equal(t, retries+1, upstream.calls)
			})
		}
	}
}

func TestGeminiForward_ServerErrorsFailoverWithoutSameAccountRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		for _, fw := range geminiSingleCheckForwarders {
			t.Run(fmt.Sprintf("%s/status=%d", fw.name, status), func(t *testing.T) {
				svc, upstream, repo := newGeminiSingleCheckService(geminiSequenceUpstreamResponse{
					status: status,
					body:   `{"error":{"code":500,"message":"upstream unavailable","status":"INTERNAL"}}`,
				})
				svc.cfg.Gateway.MaxSameAccountRetriesGemini = 1

				err, _ := fw.forward(t, svc, geminiSameAccountRetryTestAccount())

				var failoverErr *UpstreamFailoverError
				require.True(t, errors.As(err, &failoverErr), "上游服务端错误应立即换号，got %T: %v", err, err)
				require.Equal(t, status, failoverErr.StatusCode)
				require.False(t, failoverErr.RetryableOnSameAccount)
				require.Equal(t, 1, upstream.calls, "每次上游服务端错误后应立即换号")
				require.Equal(t, 1, repo.tempCalls, "上游服务端错误后应冷却失败账号")
				require.WithinDuration(t, time.Now().Add(geminiUpstreamServerErrorCooldown), repo.tempUntil, time.Second)
			})
		}
	}
}

func TestGeminiPoolModeServerErrorCoolsAccountAndFailsOverImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fw := range geminiSingleCheckForwarders {
		t.Run(fw.name, func(t *testing.T) {
			svc, upstream, repo := newGeminiSingleCheckService(geminiSequenceUpstreamResponse{
				status: http.StatusServiceUnavailable,
				body:   geminiRetryTest503Body,
			})
			account := geminiSameAccountRetryTestAccount()
			account.Credentials["pool_mode"] = true
			account.Credentials["pool_mode_retry_status_codes"] = []any{float64(http.StatusServiceUnavailable)}
			svc.cfg.Gateway.MaxSameAccountRetriesGemini = 3

			err, _ := fw.forward(t, svc, account)

			var failoverErr *UpstreamFailoverError
			require.True(t, errors.As(err, &failoverErr), "池模式上游 503 应立即换号，got %T: %v", err, err)
			require.False(t, failoverErr.RetryableOnSameAccount, "5xx 不因池模式重试配置而留在原账号")
			require.Equal(t, 1, upstream.calls)
			require.Equal(t, 1, repo.tempCalls)
		})
	}
}

func TestGeminiForward_Upstream400DoesNotFailoverOrCoolAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fw := range geminiSingleCheckForwarders {
		t.Run(fw.name, func(t *testing.T) {
			svc, upstream, repo := newGeminiSingleCheckService(geminiSequenceUpstreamResponse{
				status: http.StatusBadRequest,
				body:   `{"error":{"code":400,"message":"invalid request","status":"INVALID_ARGUMENT"}}`,
			})
			err, _ := fw.forward(t, svc, geminiSameAccountRetryTestAccount())
			var failoverErr *UpstreamFailoverError
			require.Error(t, err)
			require.False(t, errors.As(err, &failoverErr))
			require.Equal(t, 1, upstream.calls)
			require.Zero(t, repo.tempCalls)
		})
	}
}

func TestGeminiForward_LocalRequestErrorDoesNotSelectAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fw := range geminiSingleCheckForwarders {
		t.Run(fw.name, func(t *testing.T) {
			svc, upstream, repo := newGeminiSingleCheckService()
			account := geminiSameAccountRetryTestAccount()
			var err error
			switch fw.name {
			case "native":
				c, _ := newGeminiNativeTestContext(t)
				// 原生接口透传请求体；以本地缺失凭据测试构造失败，不假设本层解析 JSON。
				account.Credentials["api_key"] = ""
				_, err = svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[]}`))
			case "claude_compat":
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				body := []byte("{")
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				_, err = svc.Forward(context.Background(), c, account, body)
			case "chat_completions_compat":
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				body := []byte("{")
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body)
			}

			var failoverErr *UpstreamFailoverError
			require.Error(t, err, "本地请求解析或构造失败应返回错误")
			require.False(t, errors.As(err, &failoverErr), "本地错误不应进入切号")
			require.Zero(t, upstream.calls, "本地失败不应访问上游")
			require.Zero(t, repo.tempCalls, "本地失败不应冷却账号")
		})
	}
}

func TestGeminiClaudeCompat_SignatureDowngradeDoesNotConsumeSameAccountRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	forward := func(svc *GeminiMessagesCompatService) (*ForwardResult, error) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		body := []byte(`{"model":"gemini-2.5-flash","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
		return svc.Forward(context.Background(), c, geminiSameAccountRetryTestAccount(), body)
	}

	t.Run("both downgrade stages run without same-account retries", func(t *testing.T) {
		svc, upstream, _ := newGeminiSingleCheckService(
			geminiSequenceUpstreamResponse{status: http.StatusBadRequest, body: geminiRetryTestSignatureBody},
			geminiSequenceUpstreamResponse{status: http.StatusBadRequest, body: geminiRetryTestSignatureBody},
			geminiSequenceUpstreamResponse{status: http.StatusOK, body: geminiRetryTestOKBody},
		)
		svc.cfg.Gateway.MaxSameAccountRetriesGemini = 0

		result, err := forward(svc)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, 3, upstream.calls)
	})

	t.Run("same-account retries are counted apart from downgrades", func(t *testing.T) {
		svc, upstream, _ := newGeminiSingleCheckService(
			geminiSequenceUpstreamResponse{status: http.StatusTooManyRequests, body: `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`},
			geminiSequenceUpstreamResponse{status: http.StatusBadRequest, body: geminiRetryTestSignatureBody},
			geminiSequenceUpstreamResponse{status: http.StatusBadRequest, body: geminiRetryTestSignatureBody},
			geminiSequenceUpstreamResponse{status: http.StatusTooManyRequests, body: `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`},
		)
		svc.cfg.Gateway.MaxSameAccountRetriesGemini = 1

		_, err := forward(svc)

		var failoverErr *UpstreamFailoverError
		require.True(t, errors.As(err, &failoverErr), "同号重试耗尽应换号，got %T: %v", err, err)
		require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
		require.Equal(t, 4, upstream.calls)
	})
}
