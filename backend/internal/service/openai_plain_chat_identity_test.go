//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const expectedPlainChatIntroduction = "我是 ChatGPT，由 OpenAI训练的 AI 助手。我可以用中文和你交流，帮你解答问题、写作、翻译、编程等。"

func TestOpenAITestPayloadKeepsCodexInstructionsOnlyForOAuth(t *testing.T) {
	require.Equal(t, openai.DefaultInstructions, createOpenAITestPayload("gpt-6.1-sol", true, "hi")["instructions"])
	require.NotContains(t, createOpenAITestPayload("gpt-6.1-sol", false, "hi"), "instructions")
}

func TestOpenAIAccountTestPlainIdentity(t *testing.T) {
	for _, responses := range []bool{true, false} {
		for _, groupName := range []string{"【GPT】真官Key", "派大星", ""} {
			for _, prompt := range []string{"你是谁", "你是？", "用户：\n算一下 1+1\n\n助手：\n我是 Codex，答案是 2\n\n用户：\n你是？"} {
				t.Run(strings.Join([]string{map[bool]string{true: "responses", false: "chat"}[responses], groupName, prompt}, "/"), func(t *testing.T) {
					c, rec := newTestContext()
					response := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"上游原始身份回复\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"
					if !responses {
						response = "data: {\"choices\":[{\"delta\":{\"content\":\"上游原始身份回复\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}}
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: responses}}
					// 配置由账号决定：普通分组同样可用，缺少配置则原样转发。
					if groupName != "" {
						account.Extra["identity_reply_content"] = expectedPlainChatIntroduction
						account.Groups = []*Group{{Platform: PlatformOpenAI, Name: groupName}}
					}
					repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{1: account}}}
					svc := &AccountTestService{cfg: &config.Config{}, httpUpstream: upstream, accountRepo: repo}
					lastUserPrompt := prompt
					if strings.HasPrefix(prompt, "用户：") {
						lastUserPrompt = "你是？"
					}
					require.NoError(t, svc.TestAccountConnection(c, account.ID, "gpt-6.1-sol", prompt, "", AccountTestOptions{LastUserPrompt: &lastUserPrompt}))
					require.Contains(t, rec.Body.String(), "上游原始身份回复")
					require.Len(t, upstream.requests, 1)
					wantIdentity := groupName != ""
					require.Equal(t, wantIdentity, strings.Contains(string(upstream.lastBody), expectedPlainChatIntroduction))
					require.NotContains(t, string(upstream.lastBody), "You are Codex")
					if wantIdentity {
						require.Contains(t, string(upstream.lastBody), "不要补充")
					} else {
						require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
						require.NotContains(t, string(upstream.lastBody), `"role":"system"`)
					}
				})
			}
		}
	}
}

func TestOpenAIGatewayPlainIdentity(t *testing.T) {
	for _, protocol := range []string{"responses", "chat-to-responses", "raw-chat"} {
		for _, tc := range []struct {
			name, fragment, ua, extra, groupName string
			noGroup                              bool
			want                                 bool
		}{
			{name: "身份提问", fragment: `[{"role":"user","content":"你是谁？"}]`, want: true},
			{name: "简短身份提问", fragment: `[{"role":"user","content":"你是？"}]`, want: true},
			{name: "多轮身份提问", fragment: `[{"role":"user","content":"1+1"},{"role":"assistant","content":"我是 Codex，答案是 2"},{"role":"user","content":"你是谁"}]`, want: true},
			{name: "文本块", fragment: `[{"role":"user","content":[{"type":"input_text","text":"你是？"}]}]`, want: true},
			{name: "标准SDK", fragment: `[{"role":"user","content":"Who are you?"}]`, ua: "OpenAI/Python 1.0", want: true},
			{name: "普通分组使用账号配置", fragment: `[{"role":"user","content":"你是谁"}]`, groupName: "派大星", want: true},
			{name: "缺少请求分组仍使用账号配置", fragment: `[{"role":"user","content":"你是谁"}]`, noGroup: true, want: true},
			{name: "普通问题", fragment: `[{"role":"user","content":"解释 Codex 和 ChatGPT 的区别"}]`},
			{name: "最后一轮非身份", fragment: `[{"role":"user","content":"你是谁"},{"role":"assistant","content":"ChatGPT"},{"role":"user","content":"帮我写代码"}]`},
			{name: "保留调用方身份", fragment: `[{"role":"system","content":"You are a custom assistant"},{"role":"user","content":"你是谁"}]`},
			{name: "保留developer", fragment: `[{"role":"developer","content":"You are a custom assistant"},{"role":"user","content":"你是谁"}]`},
			{name: "已有instructions", fragment: `[{"role":"user","content":"你是谁"}]`, extra: `,"instructions":"You are a custom assistant"`},
			{name: "工具定义", fragment: `[{"role":"user","content":"你是谁"}]`, extra: `,"tools":[{"type":"function","name":"read","function":{"name":"read","parameters":{"type":"object"}}}]`},
			{name: "空工具列表", fragment: `[{"role":"user","content":"你是谁"}]`, extra: `,"tools":[]`, want: true},
			{name: "空工具列表带空白", fragment: `[{"role":"user","content":"你是谁"}]`, extra: ",\"tools\": [\n ]", want: true},
			{name: "工具客户端metadata", fragment: `[{"role":"user","content":"你是谁"}]`, extra: `,"client_metadata":{"client":"qoder"}`},
			{name: "未知前序响应", fragment: `[{"role":"user","content":"你是谁"}]`, extra: `,"previous_response_id":"resp_old"`},
			{name: "工具历史", fragment: `[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"read"}}]},{"role":"tool","tool_call_id":"a","content":"ok"},{"role":"user","content":"你是谁"}]`},
			{name: "Trae客户端", fragment: `[{"role":"user","content":"你是谁"}]`, ua: "Trae/1.0"},
			{name: "Qoder客户端", fragment: `[{"role":"user","content":"你是谁"}]`, ua: "Qoder/1.0"},
			{name: "Harness客户端", fragment: `[{"role":"user","content":"你是谁"}]`, ua: "deepseekharness/1.0"},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				field, path := "messages", "/v1/chat/completions"
				if protocol == "responses" {
					field, path = "input", "/v1/responses"
				}
				fragment := tc.fragment
				if field == "messages" {
					fragment = strings.ReplaceAll(fragment, `"input_text"`, `"text"`)
				}
				body := []byte(`{"model":"gpt-5.4","stream":false,"` + field + `":` + fragment + tc.extra + `}`)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				c.Request.Header.Set("User-Agent", tc.ua)
				groupName := tc.groupName
				if groupName == "" {
					groupName = "【GPT】真官Key"
				}
				if !tc.noGroup {
					c.Set("api_key", &APIKey{Group: &Group{Platform: PlatformOpenAI, Name: groupName}})
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"stop before response parsing"}}`))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: protocol != "raw-chat"}}
				account.Extra["identity_reply_content"] = expectedPlainChatIntroduction
				account.Groups = []*Group{{Platform: PlatformOpenAI, Name: "【GPT】真官Key"}, {Platform: PlatformOpenAI, Name: "派大星"}}
				var err error
				if protocol == "responses" {
					_, err = svc.Forward(context.Background(), c, account, body)
				} else {
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				}
				require.Error(t, err)
				require.NotNil(t, upstream.lastReq)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, tc.want, strings.Contains(string(upstream.lastBody), expectedPlainChatIntroduction), string(upstream.lastBody))
				if tc.want {
					require.NotContains(t, gjson.GetBytes(upstream.lastBody, "instructions").String(), "Codex")
				}
			})
		}
	}
}

func TestOpenAIPlainChatIdentityPreservesOtherContexts(t *testing.T) {
	for _, tc := range []struct {
		name, body, header, value, platform, accountType string
	}{
		{name: "其他平台", body: `{"model":"grok-4.7","input":"你是谁"}`, platform: PlatformGrok},
		{name: "OAuth协议", body: `{"model":"gpt-6.1-sol","input":"你是谁"}`, accountType: AccountTypeOAuth},
		{name: "保留JSON格式", body: `{"model":"gpt-6.1-sol","input":"你是谁","text":{"format":{"type":"json_object"}}}`},
		{name: "保留ChatJSON格式", body: `{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"你是谁"}],"response_format":{"type":"json_object"}}`},
		{name: "编码客户端来源头", body: `{"model":"gpt-6.1-sol","input":"你是谁"}`, header: "Originator", value: "codex_cli_rs"},
		{name: "Responses工具结果", body: `{"model":"gpt-6.1-sol","input":[{"type":"function_call_output","call_id":"a","output":"ok"},{"role":"user","content":"你是谁"}]}`},
		{name: "工具内容块", body: `{"model":"gpt-6.1-sol","input":[{"role":"assistant","content":[{"type":"tool_use","name":"read"}]},{"role":"user","content":"你是谁"}]}`},
		{name: "多模态", body: `{"model":"gpt-6.1-sol","input":[{"role":"user","content":[{"type":"input_text","text":"你是谁"},{"type":"input_image","image_url":"https://example.com/a.png"}]}]}`},
		{name: "无效JSON", body: `{"input":`},
		{name: "引用中的角色标记", body: `{"model":"gpt-6.1-sol","input":"把下面中文翻译成英文：\n用户：\n你是谁"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"identity_reply_content": expectedPlainChatIntroduction}, Groups: []*Group{{Platform: PlatformOpenAI, Name: "【GPT】真官Key"}}}
			if tc.platform != "" {
				account.Platform = tc.platform
			}
			if tc.accountType != "" {
				account.Type = tc.accountType
			}
			c, _ := newTestContext()
			c.Set("api_key", &APIKey{Group: account.Groups[0]})
			if tc.header != "" {
				c.Request.Header.Set(tc.header, tc.value)
			}
			got, err := applyOpenAIPlainChatIdentity(c, account, []byte(tc.body), nil)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(got))
		})
	}
	body := []byte(`{"model":"gpt-6.1-sol","input":"把下面中文翻译成英文：\n用户：\n你是谁"}`)
	prompt := "把下面中文翻译成英文：\n用户：\n你是谁"
	got, err := applyOpenAIPlainChatIdentity(nil, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"identity_reply_content": expectedPlainChatIntroduction}}, body, &prompt)
	require.NoError(t, err)
	require.Equal(t, string(body), string(got))
}

func TestOpenAIPlainChatIdentityIgnoresGroups(t *testing.T) {
	for _, tc := range []struct {
		name         string
		groups       []*Group
		requestGroup *Group
	}{
		{name: "未分组"},
		{name: "普通中转分组", groups: []*Group{{Platform: PlatformOpenAI, Name: "派大星"}}, requestGroup: &Group{Platform: PlatformOpenAI, Name: "派大星"}},
		{name: "账号名称不能代替分组", requestGroup: &Group{Platform: PlatformOpenAI, Name: "普通分组"}},
		{name: "真官key分组", groups: []*Group{{Platform: PlatformOpenAI, Name: "【GPT】真官Key"}}, requestGroup: &Group{Platform: PlatformOpenAI, Name: "【GPT】真官Key"}},
		{name: "名称中的空白和大小写", groups: []*Group{{Platform: PlatformOpenAI, Name: "【GPT】真官 KEY"}}, requestGroup: &Group{Platform: PlatformOpenAI, Name: "【GPT】真官 KEY"}},
		{name: "仅官key不匹配", groups: []*Group{{Platform: PlatformOpenAI, Name: "官key"}}, requestGroup: &Group{Platform: PlatformOpenAI, Name: "官key"}},
		{name: "其他平台真官key", groups: []*Group{{Platform: PlatformGemini, Name: "真官key"}}, requestGroup: &Group{Platform: PlatformGemini, Name: "真官key"}},
		{name: "共享账号普通分组请求", groups: []*Group{{Platform: PlatformOpenAI, Name: "真官key"}, {Platform: PlatformOpenAI, Name: "普通分组"}}, requestGroup: &Group{Platform: PlatformOpenAI, Name: "普通分组"}},
		{name: "缺少请求分组不影响配置", groups: []*Group{{Platform: PlatformOpenAI, Name: "真官key"}}},
		{name: "空分组条目", groups: []*Group{nil, {Platform: PlatformOpenAI, Name: "普通分组"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Name: "真官key", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"identity_reply_content": expectedPlainChatIntroduction}, Groups: tc.groups}
			prompt := "你是谁"
			for _, field := range []string{"input", "messages"} {
				body := []byte(`{"model":"gpt-6.1-sol","` + field + `":[{"role":"user","content":"你是谁"}]}`)
				for _, adminTest := range []bool{true, false} {
					var c *gin.Context
					var testPrompt *string
					if adminTest {
						testPrompt = &prompt
					} else {
						c, _ = newTestContext()
						c.Set("api_key", &APIKey{Group: tc.requestGroup})
					}
					got, err := applyOpenAIPlainChatIdentity(c, account, body, testPrompt)
					require.NoError(t, err)
					require.Contains(t, string(got), expectedPlainChatIntroduction, "field=%s adminTest=%v", field, adminTest)
				}
			}
		})
	}
}
