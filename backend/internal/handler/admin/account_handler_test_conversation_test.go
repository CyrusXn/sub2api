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
