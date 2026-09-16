package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const SettingKeyAdminQuickActions = "admin_quick_actions"

type AdminQuickAction struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func DefaultAdminQuickActions() []AdminQuickAction {
	return []AdminQuickAction{
		{Name: "用户管理", URL: "/admin/users"},
		{Name: "账号管理", URL: "/admin/accounts"},
		{Name: "余额中心", URL: "/admin/balance-center"},
		{Name: "使用记录", URL: "/admin/usage"},
		{Name: "分组管理", URL: "/admin/groups"},
	}
}

// 常用菜单仅接受站内路径或 HTTP(S) 地址，阻止脚本和协议相对地址。
func ValidateAdminQuickActions(items []AdminQuickAction) error {
	if len(items) > 20 {
		return fmt.Errorf("常用功能最多配置 20 项")
	}
	for i := range items {
		items[i].Name = strings.TrimSpace(items[i].Name)
		items[i].URL = strings.TrimSpace(items[i].URL)
		item := items[i]
		if item.Name == "" || utf8.RuneCountInString(item.Name) > 50 {
			return fmt.Errorf("常用功能名称需为 1 至 50 个字符")
		}
		u, err := url.Parse(item.URL)
		internal := strings.HasPrefix(item.URL, "/") && !strings.HasPrefix(item.URL, "//")
		invalidCharacter := strings.ContainsRune(item.URL, '\\') || strings.IndexFunc(item.URL, func(r rune) bool { return r <= ' ' || r == 127 }) >= 0
		if item.URL == "" || len(item.URL) > 2048 || invalidCharacter || err != nil || u.User != nil || (!internal && ((u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "")) {
			return fmt.Errorf("常用功能 URL 必须是站内路径或 HTTP(S) 地址")
		}
	}
	return nil
}

func ParseAdminQuickActions(raw string) []AdminQuickAction {
	var items []AdminQuickAction
	if json.Unmarshal([]byte(raw), &items) != nil || items == nil || ValidateAdminQuickActions(items) != nil {
		return DefaultAdminQuickActions()
	}
	return items
}
