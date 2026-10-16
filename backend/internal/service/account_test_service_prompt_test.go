//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestService_BedrockPreservesTestPrompt(t *testing.T) {
	for _, authMode := range []string{"apikey", "sigv4"} {
		t.Run(authMode, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				prompt string
				want   string
			}{
				{name: "custom", prompt: "  请只回复测试成功  ", want: "请只回复测试成功"},
				{name: "conversation", prompt: "用户：\n记住数字 42\n\n助手：\n已记住\n\n用户：\n刚才的数字是什么？", want: "用户：\n记住数字 42\n\n助手：\n已记住\n\n用户：\n刚才的数字是什么？"},
				{name: "empty", want: "hi"},
				{name: "whitespace", prompt: " \n\t ", want: "hi"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					account := &Account{
						ID:       1904,
						Platform: PlatformAnthropic,
						Type:     AccountTypeBedrock,
						Credentials: map[string]any{
							"auth_mode":             authMode,
							"api_key":               "test-bedrock-key",
							"aws_access_key_id":     "test-akid",
							"aws_secret_access_key": "test-secret",
							"aws_region":            "us-east-1",
						},
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"ok"}]}`)),
					}}
					svc := &AccountTestService{
						accountRepo:  &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}},
						httpUpstream: upstream,
					}
					c, recorder := newTestContext()

					err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-5", tc.prompt, AccountTestModeDefault)

					require.NoError(t, err)
					require.NotNil(t, upstream.lastReq)
					require.Equal(t, tc.want, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.text").String())
					require.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(upstream.lastBody, "anthropic_version").String())
					require.Equal(t, int64(256), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
					require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Exists())
					require.False(t, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.cache_control").Exists())
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/invoke"))
					if authMode == "apikey" {
						require.Equal(t, "Bearer test-bedrock-key", upstream.lastReq.Header.Get("Authorization"))
					} else {
						require.True(t, strings.HasPrefix(upstream.lastReq.Header.Get("Authorization"), "AWS4-HMAC-SHA256 "))
					}
					require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
				})
			}
		})
	}
}

func TestAccountTestService_GrokPreservesTestPrompt(t *testing.T) {
	for _, mode := range []string{AccountTestModeDefault, AccountTestModeGrokText} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				prompt string
				want   string
			}{
				{name: "custom", prompt: "  请只回复测试成功  ", want: "请只回复测试成功"},
				{name: "conversation", prompt: "用户：\n记住数字 42\n\n助手：\n已记住\n\n用户：\n刚才的数字是什么？", want: "用户：\n记住数字 42\n\n助手：\n已记住\n\n用户：\n刚才的数字是什么？"},
				{name: "empty", want: grokQuotaProbeInput},
				{name: "whitespace", prompt: " \n\t ", want: grokQuotaProbeInput},
			} {
				t.Run(tc.name, func(t *testing.T) {
					account := &Account{
						ID:       1905,
						Platform: PlatformGrok,
						Type:     AccountTypeAPIKey,
						Credentials: map[string]any{
							"api_key": "test-grok-key",
						},
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")),
					}}
					svc := &AccountTestService{
						accountRepo:  &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}},
						httpUpstream: upstream,
					}
					c, recorder := newTestContext()

					err := svc.TestAccountConnection(c, account.ID, "grok-4.5", tc.prompt, mode)

					require.NoError(t, err)
					require.NotNil(t, upstream.lastReq)
					expectedBody, err := json.Marshal(map[string]any{"model": "grok-4.5", "input": tc.want, "stream": true})
					require.NoError(t, err)
					require.JSONEq(t, string(expectedBody), string(upstream.lastBody))
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses"))
					require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
				})
			}
		})
	}
}
