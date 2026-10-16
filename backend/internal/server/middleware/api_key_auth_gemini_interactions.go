package middleware

import (
	"net/http"
	"strings"
)

// 资源归属仍由新 handler 校验，其他协议不使用此计费资格豁免。
func isGeminiInteractionResourceRequest(method, path string) bool {
	const prefix = "/v1beta/interactions/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if parts[0] == "" {
		return false
	}
	if len(parts) == 1 {
		return method == http.MethodGet || method == http.MethodDelete
	}
	return len(parts) == 2 && parts[1] == "cancel" && method == http.MethodPost
}
