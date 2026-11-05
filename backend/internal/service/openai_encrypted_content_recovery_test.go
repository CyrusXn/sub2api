package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIEncryptedHistoryHTTPRecovery(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, failure string
			calls         int
			noCipher      bool
			stillFails    bool
		}{
			{"official", `{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"bad encrypted content"}}`, 2, false, false},
			{"signature", `{"error":{"code":"thinking_signature_invalid","message":"The encrypted content could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}`, 2, false, false},
			{"function_output", `{"error":{"type":"invalid_request_error","message":"Encrypted function output content could not be decrypted or decoded. The conversation history carries encrypted reasoning from another line; start a new conversation and retry."}}`, 2, false, false},
			{"bounded_retry", `{"error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}`, 2, false, true},
			{"no_cipher", `{"error":{"code":"invalid_encrypted_content","message":"bad encrypted content"}}`, 1, true, false},
			{"unrelated", `{"error":{"type":"invalid_request_error","message":"bad parameter"}}`, 1, false, false},
			{"healthy", "", 1, false, false},
		} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tc.name), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				response := func(status int, body string) *http.Response {
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
				}
				const success = `{"id":"resp_recovered","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				upstream := &httpUpstreamRecorder{}
				if tc.failure != "" {
					upstream.responses = append(upstream.responses, response(400, tc.failure))
				}
				if tc.stillFails {
					upstream.responses = append(upstream.responses, response(400, tc.failure))
				} else {
					upstream.responses = append(upstream.responses, response(200, success))
				}
				cfg := &config.Config{}
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
				account := &Account{ID: 10, Name: "隔离密文测试", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test-only", "base_url": "https://example.com"},
					Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}}
				newContext := func() *gin.Context {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
					c.Set(openAIWSIngressSessionHashContextKey, "ingress-encrypted-recovery")
					return c
				}
				input := `{"model":"gpt-5","stream":false,"prompt_cache_key":"encrypted-recovery","input":[{"type":"reasoning","encrypted_content":"foreign-cipher","summary":[{"type":"summary_text","text":"keep-summary"}]},{"type":"message","role":"user","content":"keep-message"},{"type":"function_call_output","call_id":"call_1","output":"keep-tool-result"}]}`
				if tc.noCipher {
					input = strings.ReplaceAll(input, `"encrypted_content":"foreign-cipher",`, "")
				}
				result, err := svc.Forward(context.Background(), newContext(), account, []byte(input))
				require.Len(t, upstream.bodies, tc.calls)
				if tc.calls == 1 && tc.failure != "" || tc.stillFails {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "foreign-cipher", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String(), "健康密文不能预先清除")
				if tc.calls == 1 {
					return
				}
				retry := upstream.bodies[1]
				require.False(t, gjson.GetBytes(retry, "input.0.encrypted_content").Exists())
				require.Equal(t, "keep-summary", gjson.GetBytes(retry, "input.0.summary.0.text").String())
				require.Equal(t, "keep-message", gjson.GetBytes(retry, "input.1.content").String())
				require.Equal(t, "keep-tool-result", gjson.GetBytes(retry, "input.2.output").String())
				require.NotEmpty(t, svc.sessionInvalidEncryptedContentDigests(0, "ingress-encrypted-recovery"), "HTTP bridge 必须按 ingress 会话键记录和读取")
				upstream.responses = append(upstream.responses, response(200, success))
				_, err = svc.Forward(context.Background(), newContext(), account, []byte(input))
				require.NoError(t, err)
				require.Len(t, upstream.bodies, 3)
				require.False(t, gjson.GetBytes(upstream.bodies[2], "input.0.encrypted_content").Exists(), "后续轮次不应再次发送已拒绝密文")
			})
		}
	}
}

func TestOpenAIEncryptedHistoryWSClassification(t *testing.T) {
	for _, code := range []string{"invalid_encrypted_content", "thinking_signature_invalid", ""} {
		reason, retry := classifyOpenAIWSErrorEventFromRaw(code, "invalid_request_error", "Encrypted function output content could not be decrypted or decoded.")
		require.Equal(t, "invalid_encrypted_content", reason)
		require.True(t, retry)
	}
}

func TestOpenAIEncryptedHistoryNestedSSEFailureCode(t *testing.T) {
	const payload = `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"The encrypted content could not be verified."}}}`
	err := newOpenAIEncryptedContentRecoverySignal([]byte(payload), extractOpenAISSEErrorMessage([]byte(payload)))
	require.Error(t, err, "response.failed 的嵌套错误码应进入密文恢复")
	var signal *openAIEncryptedContentRecoverySignal
	require.ErrorAs(t, err, &signal)
}

func TestOpenAIEncryptedHistorySSERecovery(t *testing.T) {
	for _, failure := range []struct{ name, body string }{
		{"function_output", `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"Encrypted function output content could not be decrypted or decoded."}}}`},
		{"nested_code", `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"The encrypted content could not be verified."}}}`},
	} {
		for _, passthrough := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				for _, outputStarted := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/passthrough=%t/stream=%t/output=%t", failure.name, passthrough, stream, outputStarted), func(t *testing.T) {
						prelude := ""
						if outputStarted {
							prelude = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"already-delivered\"}\n\n"
						}
						response := func(body string) *http.Response {
							return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
						}
						upstream := &httpUpstreamRecorder{responses: []*http.Response{
							response(prelude + "data: " + failure.body + "\n\n"),
							response("data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered-output\"}\n\n" +
								"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered-output\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"),
						}}
						cfg := &config.Config{}
						svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
						account := &Account{ID: 10, Name: "隔离流式密文测试", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
							Credentials: map[string]any{"api_key": "test-only", "base_url": "https://example.com"},
							Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}}
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
						SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
						input := []byte(fmt.Sprintf(`{"model":"gpt-5","stream":%t,"input":[{"type":"reasoning","encrypted_content":"foreign-cipher","summary":[]},{"type":"message","role":"user","content":"hello"},{"type":"custom_tool_call_output","call_id":"call_custom","output":[{"type":"encrypted_content","encrypted_content":"tool-cipher"}]}]}`, stream))
						_, err := svc.Forward(context.Background(), c, account, input)
						if stream && outputStarted {
							require.Error(t, err)
							require.Len(t, upstream.bodies, 1, "正文已交付不能重放")
							require.Contains(t, rec.Body.String(), "already-delivered")
							return
						}
						require.NoError(t, err)
						require.Len(t, upstream.bodies, 2)
						require.NotContains(t, string(upstream.bodies[1]), "foreign-cipher")
						require.NotContains(t, string(upstream.bodies[1]), "tool-cipher")
						require.Contains(t, string(upstream.bodies[1]), "结果不可用")
						require.Contains(t, string(upstream.bodies[1]), "call_custom")
						require.Contains(t, rec.Body.String(), "recovered-output")
						require.NotContains(t, rec.Body.String(), "resp_bad")
					})
				}
			}
		}
	}
}
