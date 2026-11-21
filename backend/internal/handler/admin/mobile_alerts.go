package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type mobileAlertReader interface {
	ListMobileAlerts(context.Context, int64) ([]service.MobileAlert, error)
}

func (h *BalanceCenterHandler) MobileAlerts(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !service.AuthorizeMobileAlerts(c.GetHeader("Authorization")) {
		response.Error(c, http.StatusUnauthorized, "手机告警口令无效或功能未启用")
		return
	}
	cursor, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	if err != nil || cursor < 0 {
		response.BadRequest(c, "手机告警游标无效")
		return
	}
	reader, ok := h.service.(mobileAlertReader)
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "手机告警服务不可用")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	alerts, err := reader.ListMobileAlerts(ctx, cursor)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "读取手机告警失败")
		return
	}
	response.Success(c, alerts)
}
