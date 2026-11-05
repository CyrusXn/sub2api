package service

import (
	"context"
	"encoding/json"
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

func TestOpenAIEncryptedToolOutputLineage(t *testing.T) {
	for _, shape := range []struct{ typ, field string }{
		{"agent_message", "content"}, {"function_call_output", "output"}, {"custom_tool_call_output", "output"},
	} {
		t.Run(shape.typ, func(t *testing.T) {
			item := fmt.Sprintf(`{"type":%q,"id":"item_keep","call_id":"call_keep","author":"worker","recipient":"root",%q:[{"type":"input_text","text":"keep-result"},{"type":"encrypted_content","encrypted_content":"stale"},{"type":"encrypted_content","encrypted_content":"fresh"}],"opaque":9007199254740993}`, shape.typ, shape.field)
			body := []byte(`{"input":[` + item + `],"tools":[{"encrypted_content":"unrelated"}]}`)
			require.Equal(t, []string{openAIEncryptedContentDigest("stale"), openAIEncryptedContentDigest("fresh")}, collectOpenAIEncryptedContentDigestsRaw(body))
			invalid := map[string]struct{}{openAIEncryptedContentDigest("stale"): {}}
			clean, count, err := stripOpenAIInvalidEncryptedContentRaw(body, invalid)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			path := "input.0." + shape.field
			require.Equal(t, "keep-result", gjson.GetBytes(clean, path+".0.text").String())
			require.Equal(t, "input_text", gjson.GetBytes(clean, path+".1.type").String())
			require.Contains(t, gjson.GetBytes(clean, path+".1.text").String(), "结果不可用")
			require.Contains(t, gjson.GetBytes(clean, path+".1.text").String(), "重新核查")
			require.Equal(t, "fresh", gjson.GetBytes(clean, path+".2.encrypted_content").String())
			require.Equal(t, "call_keep", gjson.GetBytes(clean, "input.0.call_id").String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(clean, "input.0.opaque").Raw)
			require.Equal(t, "unrelated", gjson.GetBytes(clean, "tools.0.encrypted_content").String())
			require.Contains(t, string(body), `"encrypted_content":"stale"`, "原请求不可被改写")
			replay := []json.RawMessage{json.RawMessage(item)}
			next, count := stripOpenAIInvalidEncryptedContentFromReplayItems(replay, invalid)
			require.Equal(t, 1, count)
			require.JSONEq(t, gjson.GetBytes(clean, "input.0").Raw, string(next[0]))
			require.Equal(t, item, string(replay[0]))
			unchanged, count, err := stripOpenAIInvalidEncryptedContentRaw(clean, invalid)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, clean, unchanged)
			objectBody := []byte(`{"input":` + item + `}`)
			objectClean, count, err := stripOpenAIInvalidEncryptedContentRaw(objectBody, invalid)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			require.JSONEq(t, gjson.GetBytes(clean, "input.0").Raw, gjson.GetBytes(objectClean, "input").Raw)
			var decoded map[string]any
			require.NoError(t, decodeOpenAIJSONUseNumber(objectBody, &decoded))
			require.Equal(t, 1, replaceOpenAIEncryptedToolOutputContents(decoded, invalid))
			encoded, err := marshalOpenAIUpstreamJSON(decoded)
			require.NoError(t, err)
			require.JSONEq(t, string(objectClean), string(encoded))
		})
	}
}

func TestOpenAIEncryptedToolOutputForwardRecovery(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, sse := range []bool{false, true} {
			for _, failure := range []string{"encrypted", "unrelated", "repeated", "healthy", "started"} {
				t.Run(fmt.Sprintf("passthrough=%t/sse=%t/%s", passthrough, sse, failure), func(t *testing.T) {
					gin.SetMode(gin.TestMode)
					errorBody := `{"error":{"code":"invalid_encrypted_content","message":"Encrypted function output content could not be decrypted or decoded.","type":"invalid_request_error"}}`
					if failure == "unrelated" {
						errorBody = `{"error":{"code":"bad_parameter","message":"bad parameter"}}`
					}
					response := func(bad bool) *http.Response {
						status, ct, body := 200, "application/json", `{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered-output"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
						if bad {
							status, body = 400, errorBody
						}
						if sse {
							status, ct = 200, "text/event-stream"
							event := "response.completed"
							if bad {
								body = `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":` + gjson.Get(errorBody, "error").Raw + `}}`
								event = "response.failed"
							} else {
								body = `{"type":"response.completed","response":` + body + `}`
							}
							body = "event: " + event + "\ndata: " + body + "\n\n"
							if bad && failure == "started" {
								body = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"delivered\"}\n\n" + body
							}
						}
						return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body))}
					}
					upstream := &httpUpstreamRecorder{responses: []*http.Response{response(failure != "healthy"), response(failure == "repeated")}}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "test-only", "base_url": "https://example.com"}, Extra: map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}}
					newContext := func() *gin.Context {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
						SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
						c.Set(openAIWSIngressSessionHashContextKey, "tool-output-recovery")
						return c
					}
					body := []byte(fmt.Sprintf(`{"model":"gpt-5","stream":%t,"input":[{"type":"agent_message","author":"worker","recipient":"root","content":[{"type":"input_text","text":"keep-prefix"},{"type":"encrypted_content","encrypted_content":"agent-cipher"}]},{"type":"function_call_output","call_id":"call_keep","output":[{"type":"encrypted_content","encrypted_content":"tool-cipher"}]},{"type":"custom_tool_call_output","call_id":"custom_keep","output":[{"type":"encrypted_content","encrypted_content":"custom-cipher"}]},{"type":"message","role":"user","content":"continue"}]}`, sse))
					_, err := svc.Forward(context.Background(), newContext(), account, body)
					if failure == "unrelated" || (sse && failure == "started") {
						require.Error(t, err)
						require.Len(t, upstream.bodies, 1)
						return
					}
					if failure == "healthy" {
						require.NoError(t, err)
						require.Len(t, upstream.bodies, 1)
						require.Contains(t, string(upstream.bodies[0]), "agent-cipher")
						require.Contains(t, string(upstream.bodies[0]), "tool-cipher")
						require.Contains(t, string(upstream.bodies[0]), "custom-cipher")
						return
					}
					require.Len(t, upstream.bodies, 2, "仅在拒绝后恢复一次")
					if failure == "repeated" {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					retry := string(upstream.bodies[1])
					require.NotContains(t, retry, "agent-cipher")
					require.NotContains(t, retry, "tool-cipher")
					require.NotContains(t, retry, "custom-cipher")
					require.Contains(t, retry, "结果不可用")
					require.Contains(t, retry, "keep-prefix")
					require.Contains(t, retry, "call_keep")
					require.Contains(t, retry, "custom_keep")
					upstream.responses = append(upstream.responses, response(false))
					_, err = svc.Forward(context.Background(), newContext(), account, body)
					require.NoError(t, err)
					require.Len(t, upstream.bodies, 3)
					require.NotContains(t, string(upstream.bodies[2]), "agent-cipher", "后续轮次按摘要恢复，不重复发送已拒绝密文")
					require.NotContains(t, string(upstream.bodies[2]), "tool-cipher")
					require.NotContains(t, string(upstream.bodies[2]), "custom-cipher")
				})
			}
		}
	}
}
