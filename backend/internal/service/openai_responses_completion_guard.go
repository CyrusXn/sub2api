package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// API Key 中转必须检查终态；真实输出仍即时交付，仅首字前允许切号。
func requireOpenAIResponsesTerminal(account *Account) bool {
	return account != nil && account.IsOpenAIApiKey()
}

// 保留已知的请求级截断原因，其余未知 incomplete / 上游取消按临时故障恢复。
func openAIResponsesUnsuccessfulTerminal(data []byte, eventType string) (message string, retry bool, failed bool) {
	status := strings.TrimSpace(gjson.GetBytes(data, "response.status").String())
	switch eventType {
	case "response.incomplete", "response.cancelled", "response.canceled":
	case "response.completed", "response.done":
		if (status == "" || status == "completed") && !gjson.GetBytes(data, "response.error").IsObject() {
			return "", false, false
		}
	default:
		return "", false, false
	}
	reason := strings.TrimSpace(gjson.GetBytes(data, "response.incomplete_details.reason").String())
	message = extractOpenAISSEErrorMessage(data)
	if message == "" {
		message = "Upstream response did not complete"
		if reason != "" {
			message += ": " + reason
		}
	}
	if reason == "max_output_tokens" || reason == "content_filter" {
		return message, false, true
	}
	return message, openAIStreamFailedEventShouldFailover(data, message), true
}

func markOpenAIResponsesUnsuccessfulTerminal(c *gin.Context, message string, retry bool) {
	if retry {
		MarkOpsStreamFailure(c, "upstream_error", "upstream_response_incomplete", message, http.StatusBadGateway)
		return
	}
	MarkOpsStreamErrorValue(c, OpsStreamError{
		ErrType: "invalid_request_error", Code: "response_incomplete", Message: message,
		IntendedStatus: http.StatusBadRequest, RequestScoped: true,
	})
}

func (s *OpenAIGatewayService) newOpenAIResponsesCompletionFailover(c *gin.Context, account *Account, passthrough bool, resp *http.Response, model string, payload []byte, message string) *UpstreamFailoverError {
	err := s.newOpenAIStreamFailoverErrorWithModel(c, account, passthrough, resp.Header.Get("x-request-id"), payload, message, model, resp.Header)
	// 调用点均已确认没有真实输出，直接失败与占位后失败应采用相同的切号边界。
	err.ExhaustBeforeFirstOutput = true
	// 已经消费到坏终态的账号应换号，避免同一中转重复假首字并耗尽客户端等待时间。
	if !isOpenAIUpstreamCapacityShedEvent(payload) && !isOpenAITransientProcessingError(http.StatusBadRequest, message, payload) && !err.RequestScopedTransient && err.Scope != GatewayFailureScopeRequest && err.Scope != GatewayFailureScopeProvider && err.StatusCode >= http.StatusInternalServerError {
		err.RetryableOnSameAccount = false
	}
	return err
}

func (s *OpenAIGatewayService) writeOpenAIResponsesReadFailure(ctx context.Context, c *gin.Context, account *Account, passthrough bool, resp *http.Response, responseID, model, canonicalModel string, createdAt int64, cause error) error {
	code, message := classifyOpenAIUpstreamStreamReadError(cause)
	payload, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "code": code, "message": message}})
	s.recordOpenAIStreamUpstreamError(c, account, passthrough, resp.Header.Get("x-request-id"), "stream_read_error", payload, message)
	MarkOpsStreamFailure(c, "upstream_error", code, message, http.StatusBadGateway)
	// 正文后不能重放，但上游断流仍须冷却实际发送的模型；客户端取消不归因到账号。
	if account != nil && account.Platform == PlatformOpenAI && (ctx == nil || ctx.Err() == nil) &&
		(c.Request == nil || c.Request.Context().Err() == nil) {
		s.coolOpenAIAccountModelAfterFailure(ctx, account, canonicalModel)
	}
	// 读取可能在上一帧的空行之前中断，先封帧，避免失败事件被并入上一条 data。
	if _, err := c.Writer.WriteString("\n\n" + buildOpenAIResponseFailedSSE(responseID, model, createdAt, payload, message)); err != nil {
		return err
	}
	c.Writer.Flush()
	MarkResponseCommitted(c)
	return nil
}
