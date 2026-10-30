package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesAPIKeyCompletionGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const delta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\" \\n\\t\"}\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name  string
			tail  string
			cause error
		}{
			{name: "clean EOF"},
			{name: "read failure", cause: io.ErrUnexpectedEOF},
			{name: "late failed", tail: "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"temporary failure\"}}}\n\n"},
			{name: "unknown incomplete", tail: "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n"},
			{name: "cancelled upstream", tail: "data: {\"type\":\"response.cancelled\",\"response\":{\"status\":\"cancelled\"}}\n\n"},
			{name: "done without completed", tail: "data: [DONE]\n\n"},
			{name: "failed status disguised as completed", tail: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"temporary failure\"}}}\n\n"},
			{name: "unfinished status disguised as completed", tail: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"in_progress\"}}\n\n"},
			{name: "completed carrying error", tail: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":{\"code\":\"server_error\"}}}\n\n"},
			{name: "whitespace only completed with usage", tail: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\" \"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"},
		} {
			t.Run(tc.name+map[bool]string{false: "/native", true: "/passthrough"}[passthrough], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: &openAIStreamReadThenErrorCloser{reader: strings.NewReader(delta + tc.tail), err: tc.cause}}
				if tc.cause == nil {
					resp.Body = io.NopCloser(strings.NewReader(delta + tc.tail))
				}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
				} else {
					_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
				}
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, http.StatusBadGateway, failover.StatusCode)
				require.False(t, failover.RetryableOnSameAccount, "坏流应直接切换账号")
				require.Empty(t, rec.Body.String(), "失败账号的首字不能污染下一个账号的响应")
				require.False(t, c.Writer.Written())
				// 未开启池模式的普通 API Key 也必须受保护。
				delete(account.Credentials, "pool_mode")
				resp.Body = io.NopCloser(strings.NewReader(delta))
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
				} else {
					_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
				}
				require.ErrorAs(t, err, &failover)
				require.Empty(t, rec.Body.String())
			})
		}
	}
}

func TestOpenAIResponsesStreamsRealOutputBeforeCompletion(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		rec := newOpenAIResponseFlushRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		svc := &OpenAIGatewayService{}
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		resp := &http.Response{Header: http.Header{}, Body: reader}
		account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		done := make(chan error, 1)
		go func() {
			if passthrough {
				_, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
				done <- err
			} else {
				_, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
				done <- err
			}
		}()
		_, err := io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_ok\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\n")
		require.NoError(t, err)
		body, _ := rec.snapshot()
		require.Empty(t, body, "空白不能锁定账号")
		_, err = io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"healthy-output\"}\n\n")
		require.NoError(t, err)
		select {
		case <-rec.flushEvents:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("真实首字必须在终态之前立即交付")
		}
		body, _ = rec.snapshot()
		require.Contains(t, body, "healthy-output")
		require.NotContains(t, body, `"type":"response.completed"`)
		_, err = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n")
		require.NoError(t, err)
		writer.Close()
		require.NoError(t, <-done)
		body, _ = rec.snapshot()
		require.Equal(t, 1, strings.Count(body, `"type":"response.completed"`))
	}
}

func TestOpenAIResponsesUnsuccessfulTerminalIsNotSuccess(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		svc := &OpenAIGatewayService{}
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"))}
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		var err error
		if passthrough {
			_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "", "")
		} else {
			_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "", "")
		}
		require.Error(t, err)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover), "达到输出上限不应换号重复生成")
		require.Contains(t, rec.Body.String(), "max_output_tokens")
	}
}

func TestOpenAIResponsesCompletionGuardAcceptsLargeSuccessfulStream(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		// 完整响应可能包含多张图片，不能沿用仅用于首字前导事件的 8 MiB 上限。
		chunk := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Repeat("a", 64*1024) + "\"}\n\n"
		body := strings.Repeat(chunk, 129) + "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		svc := &OpenAIGatewayService{}
		var err error
		if passthrough {
			_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "", "")
		} else {
			_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "", "")
		}
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.completed"`))
	}
}

func TestOpenAIStreamEmptyDeltaDoesNotCommitOutput(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.output_text.delta","delta":""}`,
		`{"type":"response.output_text.delta","delta":" \n\t"}`,
		`{"type":"response.output_text.done","text":" "}`,
		`{"type":"response.content_part.added","part":{"type":"output_text","text":" "}}`,
	} {
		eventType := gjson.Get(payload, "type").String()
		require.False(t, openAIStreamDataStartsClientOutput(payload, eventType), payload)
		require.False(t, openAIStreamDataStartsVisibleOutput(payload, eventType), payload)
		require.False(t, openAIStreamDataStartsSemanticTTFT(payload, eventType), payload)
	}
}

func TestOpenAIResponsesEmptyStructuresPreserveFailover(t *testing.T) {
	for _, placeholder := range []string{
		`{"type":"response.output_item.added","item":{"type":"message","content":[]}}`,
		`{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`,
		`{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.done","text":" "}`,
	} {
		for _, tail := range []string{
			`{"type":"response.failed","response":{"error":{"code":"server_error","message":"temporary failure"}}}`,
			`{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1}}}`,
		} {
			for _, passthrough := range []bool{false, true} {
				c, rec := newOpenAIUpstreamErrorTestContext(t)
				svc := &OpenAIGatewayService{}
				resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + placeholder + "\n\ndata: " + tail + "\n\n"))}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
				} else {
					_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
				}
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover, placeholder)
				require.True(t, failover.ExhaustBeforeFirstOutput, placeholder)
				require.Empty(t, rec.Body.String())
			}
		}
	}
}

func TestOpenAIResponsesCompletionGuardLimitAndCancellation(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, cancelRequest := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			var body io.ReadCloser = io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Repeat(" ", openAIFirstOutputStageMaxBytes+1) + "\"}\n\n"))
			if cancelRequest {
				cancel()
				body = &openAIStreamReadThenErrorCloser{reader: strings.NewReader(""), err: context.Canceled}
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{UpstreamResponseReadMaxBytes: 128}}}
			resp := &http.Response{Header: http.Header{}, Body: body}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "", "")
			} else {
				_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "", "")
			}
			require.Error(t, err)
			// 缓冲慢流时允许已发出的注释占位，但不能泄露模型数据。
			output := rec.Body.String()
			require.NotContains(t, output, `data:`)
			require.NotContains(t, output, `event:`)
			var failover *UpstreamFailoverError
			if cancelRequest {
				require.ErrorIs(t, err, context.Canceled)
				require.False(t, errors.As(err, &failover), "客户端取消不应当作账号故障")
			} else {
				require.ErrorAs(t, err, &failover)
			}
		}
	}
}

func TestOpenAIStreamReadFailureCountsForAccountHealth(t *testing.T) {
	status, _, eligible := classifyOpenAIAPIKeyHealthFailure(newOpenAIUpstreamStreamReadError(io.ErrUnexpectedEOF))
	require.True(t, eligible)
	require.Equal(t, http.StatusBadGateway, status)
}

func TestOpenAIResponsesRealOutputFailureIsNotReplayed(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tail := range []string{"", "data: [DONE]\n\n", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"temporary failure\"}}}\n\n"} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			svc := &OpenAIGatewayService{}
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\"}\n\n" + tail))}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
			} else {
				_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
			}
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.Contains(t, rec.Body.String(), `"delta":"Hi"`)
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
		}
	}
}

func TestOpenAIResponsesPlaceholderKeepalivePreservesFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{true: "passthrough", false: "native"}[passthrough], func(t *testing.T) {
			rec := newOpenAIResponseFlushRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			done := make(chan error, 1)
			go func() {
				svc := &OpenAIGatewayService{}
				resp := &http.Response{Header: http.Header{}, Body: reader}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				if passthrough {
					_, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
					done <- err
				} else {
					_, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
					done <- err
				}
			}()
			_, err := io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\n")
			require.NoError(t, err)
			select {
			case <-rec.flushEvents:
			case <-time.After(TextStreamPlaceholderDelay + time.Second):
				writer.Close()
				<-done
				t.Fatal("占位等待结束后应先发送保活，不改变后续切号资格")
			}
			body, _ := rec.snapshot()
			require.True(t, strings.HasPrefix(body, ":"), body)
			require.NotContains(t, body, "data:")
			writer.Close()
			err = <-done
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover, "保活之后仅空白断流仍可换号")
		})
	}
}
