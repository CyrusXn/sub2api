//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type identityReplyCreateRepo struct {
	*upstreamBillingProbeAccountRepo
}

func (r *identityReplyCreateRepo) BindGroups(context.Context, int64, []int64) error { return nil }

func TestCreateAccountIdentityReplyDefault(t *testing.T) {
	for _, tc := range []struct {
		name, group, platform string
		extra                 map[string]any
		want                  string
	}{
		{name: "真官key初始默认", group: "【GPT】真官Key", want: expectedPlainChatIntroduction},
		{name: "分组名称忽略空白", group: "真官 KEY", want: expectedPlainChatIntroduction},
		{name: "普通分组默认留空", group: "普通分组"},
		{name: "显式清空不回填", group: "真官key", extra: map[string]any{"identity_reply_content": ""}},
		{name: "自定义不覆盖", group: "真官key", extra: map[string]any{"identity_reply_content": "自定义助手"}, want: "自定义助手"},
		{name: "其他平台不初始化", group: "真官key", platform: PlatformGemini},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform := tc.platform
			if platform == "" {
				platform = PlatformOpenAI
			}
			repo := &identityReplyCreateRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
			groups := &mockGroupRepoForGateway{groups: map[int64]*Group{1: {ID: 1, Platform: platform, Name: tc.group}}}
			created, err := (&adminServiceImpl{accountRepo: repo, groupRepo: groups}).CreateAccount(context.Background(), &CreateAccountInput{
				Name: "identity-test", Platform: platform, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-key"}, Extra: tc.extra,
				GroupIDs: []int64{1}, SkipMixedChannelCheck: true,
			})
			require.NoError(t, err)
			got, _ := created.Extra["identity_reply_content"].(string)
			require.Equal(t, tc.want, got)
			if tc.extra != nil {
				require.Equal(t, tc.extra["identity_reply_content"], created.Extra["identity_reply_content"])
			}
			if platform != PlatformOpenAI || tc.extra != nil {
				require.Zero(t, groups.getByIDLiteCalls)
			}
		})
	}
}

func TestCreateAccountIdentityReplyGroupReadFailure(t *testing.T) {
	repo := &identityReplyCreateRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	_, err := (&adminServiceImpl{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{}}).CreateAccount(context.Background(), &CreateAccountInput{
		Name: "identity-test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key"},
		GroupIDs:    []int64{1}, SkipMixedChannelCheck: true,
	})
	require.ErrorIs(t, err, ErrGroupNotFound)
	require.Empty(t, repo.accounts)
}

func TestOpenAIPlainChatIdentityUsesAccountConfig(t *testing.T) {
	for _, tc := range []struct {
		name, model, question string
		extra                 map[string]any
		want                  string
	}{
		{name: "未配置即使属于真官key也透传"},
		{name: "显式清空", extra: map[string]any{"identity_reply_content": ""}},
		{name: "空白内容", extra: map[string]any{"identity_reply_content": " \n\t "}},
		{name: "错误类型不注入", extra: map[string]any{"identity_reply_content": true}},
		{name: "自定义内容", extra: map[string]any{"identity_reply_content": "  我是自定义助手  "}, want: "我是自定义助手"},
		{name: "引号和换行", extra: map[string]any{"identity_reply_content": "我是\"助手\"\n可以帮你写作。"}, want: "我是\"助手\"\n可以帮你写作。"},
		{name: "自定义身份不依赖模型名称", model: "custom-model", extra: map[string]any{"identity_reply_content": "自定义助手"}, want: "自定义助手"},
		{name: "英文模型身份提问", question: "What model are you?", extra: map[string]any{"identity_reply_content": "Custom assistant"}, want: "Custom assistant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, question := tc.model, tc.question
			if model == "" {
				model = "gpt-6.1-sol"
			}
			if question == "" {
				question = "你是谁"
			}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: tc.extra, Groups: []*Group{{Platform: PlatformOpenAI, Name: "真官key"}}}
			for _, field := range []string{"input", "messages"} {
				for _, adminTest := range []bool{false, true} {
					body := []byte(`{"model":"` + model + `","` + field + `":[{"role":"user","content":"` + question + `"}]}`)
					c, _ := newTestContext()
					c.Set("api_key", &APIKey{Group: &Group{Platform: PlatformOpenAI, Name: "普通分组"}})
					var testPrompt *string
					if adminTest {
						prompt := question
						testPrompt = &prompt
					}
					got, err := applyOpenAIPlainChatIdentity(c, account, body, testPrompt)
					require.NoError(t, err)
					if tc.want == "" {
						require.Equal(t, string(body), string(got))
					} else {
						path := "instructions"
						if field == "messages" {
							path = "messages.0.content"
						}
						require.Contains(t, gjson.GetBytes(got, path).String(), tc.want)
						require.NotContains(t, gjson.GetBytes(got, path).String(), expectedPlainChatIntroduction)
					}
				}
			}
		})
	}
}
