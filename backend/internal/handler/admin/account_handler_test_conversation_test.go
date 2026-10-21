package admin

import "testing"

func TestFormatTestConversationKeepsRolesAndFallback(t *testing.T) {
	got := formatTestConversation([]TestAccountMessage{
		{Role: "user", Content: "你是谁"},
		{Role: "assistant", Content: "我是测试账号"},
		{Role: "user", Content: "继续"},
	}, "fallback")
	want := "用户：\n你是谁\n\n助手：\n我是测试账号\n\n用户：\n继续"
	if got != want {
		t.Fatalf("conversation = %q, want %q", got, want)
	}
}

func TestFormatTestConversationUsesPromptWhenHistoryEmpty(t *testing.T) {
	if got := formatTestConversation(nil, "你是谁"); got != "你是谁" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestLastTestUserPromptPreservesMessageBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		messages       []TestAccountMessage
		fallback, want string
	}{
		{name: "裸探针", fallback: "你是谁", want: "你是谁"},
		{name: "最后用户消息", messages: []TestAccountMessage{{Role: "user", Content: "旧问题"}, {Role: "assistant", Content: "我是 Codex"}, {Role: "user", Content: "你是？"}}, want: "你是？"},
		{name: "正文中的角色标记", messages: []TestAccountMessage{{Role: "user", Content: "请翻译：\n用户：\n你是谁"}}, want: "请翻译：\n用户：\n你是谁"},
		{name: "助手结尾不回溯", messages: []TestAccountMessage{{Role: "user", Content: "你是谁"}, {Role: "assistant", Content: "旧回答"}}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastTestUserPrompt(tc.messages, tc.fallback); got != tc.want {
				t.Fatalf("最后用户内容 = %q，期望 %q", got, tc.want)
			}
		})
	}
}
