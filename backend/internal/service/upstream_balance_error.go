package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// 此函数只用于已经收到的上游响应：余额属于所选上游账号，而非本站用户。
// 本站用户余额由入站鉴权/计费拦截；这里只读取错误字段，防止用户回显污染分类。
func isUpstreamInsufficientBalanceError(statusCode int, body []byte) bool {
	if statusCode < http.StatusBadRequest || hasOpenAIExplicitRequestErrorCode(body) {
		return false
	}
	if isOpenAIImagesInsufficientBalance(body) {
		return true
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

// 明确的来源和错误语义优先于 HTTP 包装状态；不能将上游欠费伪装成用户欠费。
func allowsOpenAINextAccount(status int, message string, body []byte) bool {
	if isOpenAIRequestError(status, message, body) {
		return false
	}
	if isUpstreamInsufficientBalanceError(status, body) || isOpenAIExplicitUpstreamError(body) ||
		isOpenAIHTTPUpstreamAccessStateError(status, message, body) || isOpenAIExplicitCredentialFailure(body) {
		return true
	}
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return true
	}
	if isUpstreamModelNotFoundError(status, body) || (status == http.StatusBadRequest && isOpenAICompatibleModelNotFound400(body)) {
		return true
	}
	return status == http.StatusBadRequest && isOpenAITransientProcessingError(status, message, body)
}

func isOpenAIExplicitUpstreamError(body []byte) bool {
	for _, value := range upstreamErrorStringValues(body, "error.type", "response.error.type", "detail.type", "type") {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "upstream_error", "server_error", "internal_server_error", "api_error", "overloaded_error":
			return true
		}
	}
	return false
}

// 泛型 invalid_request_error 不能覆盖明确的上游欠费、模型缺失或配额信号；
// 反之明确的上下文/参数错误不能因被包装成 429/5xx 而处罚账号。
func isOpenAIRequestError(status int, message string, body []byte) bool {
	if hasOpenAIExplicitRequestErrorCode(body) {
		return true
	}
	if isUpstreamInsufficientBalanceError(status, body) ||
		isUpstreamModelNotFoundError(status, body) || isOpenAICompatibleModelNotFoundBody(body) ||
		isOpenAIHTTPUpstreamAccessStateError(status, message, body) {
		return false
	}
	if isOpenAIContextWindowError(message, body) {
		return true
	}
	if hit, _, _ := detectOpenAICyberPolicy(body); hit {
		return true
	}
	if isOpenAITransientProcessingError(status, message, body) || isOpenAIRequestScopedCapacityShed(message, body) {
		return false
	}
	if isOpenAIExplicitCredentialFailure(body) {
		return false
	}
	for _, code := range upstreamErrorStringValues(body, "error.code", "response.error.code", "detail.code", "code") {
		switch strings.ToLower(strings.TrimSpace(code)) {
		case "rate_limit_exceeded", "rate_limit_error", "too_many_requests", "server_is_overloaded", "slow_down", "invalid_api_key", "invalid_token":
			return false
		}
	}
	for _, value := range upstreamErrorStringValues(body, "error.type", "response.error.type", "detail.type", "type") {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "authentication_error", "authentication_failed", "unauthorized_error":
			return false
		case "invalid_request_error", "request_error", "validation_error", "invalid_argument", "bad_request":
			return true
		}
	}
	return false
}

// 明确的请求错误码不能被 message 中回显的余额字样覆盖，避免坏请求误停供应侧账号。
func hasOpenAIExplicitRequestErrorCode(body []byte) bool {
	for _, code := range upstreamErrorStringValues(body, "error.code", "response.error.code", "detail.code", "code") {
		switch strings.ToLower(strings.TrimSpace(code)) {
		case "invalid_parameter", "missing_required_parameter", "invalid_argument", "invalid_value", "unsupported_parameter", "context_length_exceeded", "context_too_large":
			return true
		}
	}
	return false
}

func isOpenAIExplicitTransient4xx(status int, body []byte) bool {
	return (status == http.StatusBadRequest || status == http.StatusForbidden) &&
		isOpenAIExplicitUpstreamError(body) && !isOpenAIRequestError(status, "", body) &&
		!isUpstreamInsufficientBalanceError(status, body) &&
		!isOpenAIHTTPUpstreamAccessStateError(status, "", body)
}

func openAIRequestErrorClientStatus(status int) int {
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		return status
	}
	return http.StatusBadRequest
}

const openAIUpstreamInsufficientBalanceMessage = "上游账号余额不足，请充值后手动恢复调度"
const openAIUpstreamInsufficientBalanceReason = GatewayFailureReason("openai_upstream_insufficient_balance")

func isOpenAIUpstreamBalanceStopped(account *Account) bool {
	return account != nil && account.Status == StatusError && account.ErrorMessage == openAIUpstreamInsufficientBalanceMessage
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
