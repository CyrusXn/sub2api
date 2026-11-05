package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIEncryptedHistoryWSTerminalRecovery(t *testing.T) {
	const messageFailure = `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"invalid_request_error","message":"Encrypted function output content could not be decrypted or decoded. The conversation history carries encrypted reasoning from another line; start a new conversation and retry."}}}`
	const codeFailure = `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"The encrypted content could not be verified."}}}`
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, failure, prelude string
			noCipher, stillFails   bool
		}{
			{name: "message_only", failure: messageFailure},
			{name: "nested_code", failure: codeFailure},
			{name: "buffered_created", failure: messageFailure, prelude: `{"type":"response.created","response":{"id":"resp_bad","status":"in_progress"}}`},
			{name: "bounded_retry", failure: messageFailure, stillFails: true},
			{name: "missing_cipher", failure: messageFailure, noCipher: true},
			{name: "delivered_output", failure: messageFailure, prelude: `{"type":"response.output_text.delta","delta":"already-delivered"}`},
			{name: "unrelated", failure: `{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"type":"invalid_request_error","message":"Invalid parameter"}}}`},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				var mu sync.Mutex
				var requests [][]byte
				upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						t.Errorf("升级 WebSocket 失败: %v", err)
						return
					}
					defer conn.Close()
					_, body, err := conn.ReadMessage()
					if err != nil {
						t.Errorf("读取测试请求失败: %v", err)
						return
					}
					mu.Lock()
					requests = append(requests, body)
					attempt := len(requests)
					mu.Unlock()
					if attempt == 1 || tc.stillFails {
						if tc.prelude != "" {
							_ = conn.WriteMessage(websocket.TextMessage, []byte(tc.prelude))
						}
						_ = conn.WriteMessage(websocket.TextMessage, []byte(tc.failure))
						return
					}
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_recovered","status":"completed","model":"gpt-5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`))
				}))
				defer server.Close()
				cfg := &config.Config{}
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.OpenAIWS.Enabled = true
				cfg.Gateway.OpenAIWS.APIKeyEnabled = true
				cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
				defer svc.CloseOpenAIWSPool()
				account := &Account{ID: 10, Name: "隔离密文终止事件测试", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test-only", "base_url": server.URL},
					Extra:       map[string]any{"responses_websockets_v2_enabled": true}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				input := []byte(fmt.Sprintf(`{"model":"gpt-5","stream":%t,"input":[{"type":"reasoning","encrypted_content":"foreign-cipher","summary":[{"type":"summary_text","text":"keep-summary"}]},{"type":"message","role":"user","content":"keep-message"}]}`, stream))
				if tc.noCipher {
					input = []byte(strings.ReplaceAll(string(input), `"encrypted_content":"foreign-cipher",`, ""))
				}
				result, err := svc.Forward(context.Background(), c, account, input)
				mu.Lock()
				captured := append([][]byte(nil), requests...)
				mu.Unlock()
				if tc.stillFails || tc.noCipher || tc.name == "unrelated" || stream && tc.name == "delivered_output" {
					expectedCalls := 1
					if tc.stillFails {
						expectedCalls = 2
					}
					require.Len(t, captured, expectedCalls, "恢复必须有次数上限，且已交付正文不能重放")
					require.NotContains(t, rec.Body.String(), "resp_recovered")
					if stream && tc.name == "delivered_output" {
						require.Contains(t, rec.Body.String(), "already-delivered")
						require.Contains(t, rec.Body.String(), "response.failed")
					} else {
						require.Error(t, err)
					}
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, captured, 2, "单独 response.failed 也应执行一次密文恢复")
				require.Equal(t, "foreign-cipher", gjson.GetBytes(captured[0], "input.0.encrypted_content").String())
				require.False(t, gjson.GetBytes(captured[1], "input.0.encrypted_content").Exists())
				require.Equal(t, "keep-summary", gjson.GetBytes(captured[1], "input.0.summary.0.text").String())
				require.Equal(t, "keep-message", gjson.GetBytes(captured[1], "input.1.content").String())
				require.Contains(t, rec.Body.String(), "resp_recovered")
				require.NotContains(t, rec.Body.String(), "resp_bad")
			})
		}
	}
}
