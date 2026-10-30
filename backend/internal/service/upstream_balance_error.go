package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// 余额拒绝与请求绑定，不能因代理包装成 429/5xx 就切号或污染账号健康状态。
// 只读取错误字段，防止回显的用户输入改变调度结果。
func isUpstreamInsufficientBalanceError(statusCode int, body []byte) bool {
	if statusCode < http.StatusBadRequest {
		return false
	}
	values := upstreamErrorStringValues(body, "error.code", "error.message", "error.type", "error", "message", "code", "type", "detail", "detail.code", "detail.message", "response.error.code", "response.error.type", "response.error.message")
	if !gjson.ValidBytes(body) {
		values = append(values, string(body))
	}
	for _, value := range values {
		normalized := strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(value))
		for _, marker := range []string{"insufficient balance", "insufficient account balance", "insufficient quota", "credit balance is too low", "balance is insufficient", "billing hard limit reached", "余额不足", "额度不足"} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	return false
}

func upstreamBalanceClientStatus(status int) int {
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return http.StatusForbidden
	}
	return status
}

func writeOpenAIBalanceError(c *gin.Context, status int) {
	MarkResponseCommitted(c)
	writeOpenAIErrorJSON(c, upstreamBalanceClientStatus(status), gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": "insufficient_balance", "message": "Insufficient account balance",
	}})
}

// 状态处置与切号资格分开：认证、权限和普通参数错误不通过切换账号重放。
func allowsOpenAINextAccount(status int, message string, body []byte) bool {
	if isUpstreamInsufficientBalanceError(status, body) {
		return false
	}
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return true
	}
	if isUpstreamModelNotFoundError(status, body) || (status == http.StatusBadRequest && isOpenAICompatibleModelNotFound400(body)) {
		return true
	}
	return status == http.StatusBadRequest && isOpenAITransientProcessingError(status, message, body)
}

// 对象字段可能混有用户回显，不能将整个 JSON 对象转成字符串参与错误分类。
func upstreamErrorStringValues(body []byte, paths ...string) []string {
	values := make([]string, 0, len(paths))
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			continue
		}
		text := strings.TrimSpace(value.String())
		if gjson.Valid(text) && strings.HasPrefix(text, "{") {
			// 有些中转将错误对象编码在 message 字符串中；仍只信任其中的错误字段。
			for _, nested := range []string{"error.code", "error.type", "error.message", "message"} {
				if field := gjson.Get(text, nested); field.Type == gjson.String {
					values = append(values, field.String())
				}
			}
			continue
		}
		values = append(values, text)
	}
	return values
}
