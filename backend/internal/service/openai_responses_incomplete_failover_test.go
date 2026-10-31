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

const incompleteOpaqueReasoning = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bad\",\"status\":\"in_progress\"}}\n\n" +
	"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"rs_bad\",\"type\":\"reasoning\",\"summary\":[]}}\n\n" +
	"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_bad\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"opaque-reasoning\"}}\n\n"

func TestOpenAIResponsesIncompleteFailureRecovery(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, prelude, terminal string
			wantFailover, wantCool  bool
		}{
			{name: "opaque_incomplete", prelude: incompleteOpaqueReasoning, terminal: `{"type":"response.incomplete","response":{"id":"resp_bad","status":"incomplete"}}`, wantFailover: true},
			{name: "opaque_failed", prelude: incompleteOpaqueReasoning, terminal: `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"upstream_error","message":"temporary failure"}}}`, wantFailover: true},
			{name: "text_incomplete", prelude: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"delivered-text\"}\n\n", terminal: `{"type":"response.incomplete","response":{"id":"resp_bad","status":"incomplete"}}`, wantCool: true},
			{name: "text_failed", prelude: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"delivered-text\"}\n\n", terminal: `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"upstream_error","message":"temporary failure"}}}`, wantCool: true},
			{name: "tool_incomplete", prelude: "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_test\",\"call_id\":\"call_test\",\"name\":\"lookup\",\"arguments\":\"{}\"}}\n\n", terminal: `{"type":"response.incomplete","response":{"id":"resp_bad","status":"incomplete"}}`, wantCool: true},
			{name: "output_limit", terminal: `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`},
			{name: "content_filter", terminal: `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"}}}`},
		} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tc.name), func(t *testing.T) {
				repo := &consecutiveFailureRepo{}
				svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
				account := &Account{ID: 96120, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.prelude + "data: " + tc.terminal + "\n\n"))}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.2", "")
				} else {
					_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.2", "gpt-5.2")
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.Equal(t, tc.wantFailover, errors.As(err, &failover), "隐藏思考数据不能阻止首字前切号")
				if tc.wantFailover {
					require.Equal(t, 502, failover.StatusCode)
					require.False(t, failover.RetryableOnSameAccount)
					require.Empty(t, rec.Body.String(), "失败账号的隐藏数据不能泄漏到下一账号的流")
				}
				svc.ReportOpenAIAccountScheduleResult(account, "gpt-5.2", false, nil, err)
				if tc.wantCool || tc.wantFailover {
					require.Equal(t, 1, repo.modelCooldown.calls, "一次上游故障只累计一次模型冷却")
					require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.2"))
				} else {
					require.Zero(t, repo.modelCooldown.calls, "请求级截断不能处罚账号")
				}
				if tc.wantCool {
					if tc.name == "tool_incomplete" {
						require.Contains(t, rec.Body.String(), "call_test")
					} else {
						require.Contains(t, rec.Body.String(), "delivered-text")
					}
				}
				require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-luna"))
				require.Empty(t, repo.errors, "瞬时故障不能永久停号")
			})
		}
	}
}

func TestOpenAIResponsesOpaqueReasoningSurvivesSuccessfulStream(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 96121, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			body := incompleteOpaqueReasoning + "data: {\"type\":\"response.output_text.delta\",\"delta\":\"healthy-output\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bad\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.2", "gpt-5.2")
			} else {
				_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.2", "gpt-5.2")
			}
			require.NoError(t, err)
			require.Contains(t, rec.Body.String(), "opaque-reasoning", "成功响应必须保留原始思考项")
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.output_item.done"`), "暂存事件只能交付一次，终态快照仍可包含相同思考项")
			require.Less(t, strings.Index(rec.Body.String(), "opaque-reasoning"), strings.Index(rec.Body.String(), "healthy-output"))
		})
	}
}

func TestOpenAIResponsesReadFailureCoolsOnlyCanonicalModel(t *testing.T) {
	const delivered = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"delivered-text\"}\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, tail string
			cause      error
			cancel     bool
		}{
			{name: "clean_eof"},
			{name: "done_without_terminal", tail: "data: [DONE]\n\n"},
			{name: "read_error", cause: io.ErrUnexpectedEOF},
			{name: "upstream_timeout", cause: context.DeadlineExceeded},
			{name: "client_cancel", cause: context.Canceled, cancel: true},
		} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tc.name), func(t *testing.T) {
				repo := &consecutiveFailureRepo{}
				svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
				account := &Account{ID: 96122, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": "upstream-a", "upstream-a": "upstream-b"}}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				if tc.cancel {
					cancel()
				}
				body := io.NopCloser(strings.NewReader(delivered + tc.tail))
				if tc.cause != nil {
					body = &openAIStreamReadThenErrorCloser{reader: strings.NewReader(delivered + tc.tail), err: tc.cause}
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "public-alias", "")
				} else {
					_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "public-alias", "upstream-a")
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "已输出或客户端已取消的请求不能重放")
				svc.ReportOpenAIAccountScheduleResult(account, "upstream-a", false, nil, err)
				if tc.cancel {
					require.Zero(t, repo.modelCooldown.calls, "客户端取消不能处罚账号")
				} else {
					require.Equal(t, 1, repo.modelCooldown.calls, "正文后断流必须只记录一次模型冷却")
					require.Equal(t, "upstream-a", repo.modelCooldown.model, "不能把已映射的模型再映射一次")
					require.True(t, svc.getOpenAIAccountModelTransientState().isBlocked(account.ID, "upstream-a", time.Now()))
					require.Contains(t, rec.Body.String(), "delivered-text")
					require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
				}
				require.False(t, svc.getOpenAIAccountModelTransientState().isBlocked(account.ID, "upstream-b", time.Now()))
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
				require.Empty(t, repo.errors)
			})
		}
	}
}
