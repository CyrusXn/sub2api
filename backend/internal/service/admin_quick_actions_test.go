package service

import "testing"

func TestAdminQuickActions(t *testing.T) {
	for _, raw := range []string{"", "null", "broken"} {
		if got := ParseAdminQuickActions(raw); len(got) != 5 {
			t.Fatalf("未配置时应提供五个默认菜单: %q", raw)
		}
	}
	if got := ParseAdminQuickActions("[]"); len(got) != 0 {
		t.Fatal("明确清空后不应恢复默认菜单")
	}
	for _, address := range []string{"javascript:alert(1)", "//evil.test", "/\\evil.test", "https://user:password@example.com", "data:text/html,test", "https://"} {
		if ValidateAdminQuickActions([]AdminQuickAction{{Name: "菜单", URL: address}}) == nil {
			t.Fatalf("应拒绝不安全 URL: %s", address)
		}
	}
	items := []AdminQuickAction{{Name: " 用户 ", URL: " /admin/users "}, {Name: "文档", URL: "https://example.com/docs"}}
	if err := ValidateAdminQuickActions(items); err != nil || items[0].Name != "用户" || items[0].URL != "/admin/users" {
		t.Fatalf("菜单校验或空格清理失败: %v", err)
	}
}
