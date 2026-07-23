package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

var validOpsAlertEmailDeliveryStatuses = map[string]struct{}{
	service.OpsAlertEmailStatusSent:        {},
	service.OpsAlertEmailStatusFailed:      {},
	service.OpsAlertEmailStatusQuietHours:  {},
	service.OpsAlertEmailStatusDisabled:    {},
	service.OpsAlertEmailStatusRateLimited: {},
	service.OpsAlertEmailStatusSilenced:    {},
}

// ListAlertEmailDeliveries 返回运维告警邮件的逐收件人投递记录。
func (h *OpsHandler) ListAlertEmailDeliveries(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" {
		if _, ok := validOpsAlertEmailDeliveryStatuses[status]; !ok {
			response.Error(c, http.StatusBadRequest, "无效的邮件投递状态")
			return
		}
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	result, err := h.opsService.ListAlertEmailDeliveries(c.Request.Context(), &service.OpsAlertEmailDeliveryFilter{
		Status:   status,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
