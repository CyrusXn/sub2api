package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type mobileAlertReader interface {
	ListMobileAlerts(context.Context, int64, bool) ([]service.MobileAlert, error)
}

var mobileStreamSlots = make(chan struct{}, 8)

func mobileAlertRequest(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	if !service.AuthorizeMobileAlerts(c.GetHeader("Authorization")) {
		response.Error(c, http.StatusUnauthorized, "手机告警口令无效或功能未启用")
		return 0, false
	}
	cursor, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	if err != nil || cursor < 0 {
		response.BadRequest(c, "手机告警游标无效")
		return 0, false
	}
	return cursor, true
}

func (h *BalanceCenterHandler) MobileAlerts(c *gin.Context) {
	cursor, valid := mobileAlertRequest(c)
	if !valid {
		return
	}
	reader, ok := h.service.(mobileAlertReader)
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "手机告警服务不可用")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	// 微信显式读取邮件镜像；安卓及旧轮询客户端保持原有事件和通知格式。
	alerts, err := reader.ListMobileAlerts(ctx, cursor, c.Query("kind") == "email_mirror")
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "读取手机告警失败")
		return
	}
	response.Success(c, alerts)
}

func (h *BalanceCenterHandler) MobileAlertStream(c *gin.Context) {
	cursor, valid := mobileAlertRequest(c)
	if !valid {
		return
	}
	reader, ok := h.service.(interface {
		mobileAlertReader
		WatchMobileAlerts(context.Context) (<-chan struct{}, error)
	})
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "手机告警推送不可用")
		return
	}
	select {
	case mobileStreamSlots <- struct{}{}:
		defer func() { <-mobileStreamSlots }()
	default:
		response.Error(c, http.StatusServiceUnavailable, "手机告警连接数已满")
		return
	}
	// 定期换连接，限制失效凭据和蓝绿旧槽连接的存活时间。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	wake, err := reader.WatchMobileAlerts(ctx)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "手机告警推送暂不可用")
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	if _, err := io.WriteString(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	c.Writer.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for ctx.Err() == nil {
		// 先订阅再读取历史。历史和实时事件都由同一个持久游标补读。
		query, done := context.WithTimeout(ctx, 5*time.Second)
		alerts, err := reader.ListMobileAlerts(query, cursor, false)
		done()
		if err != nil {
			return
		}
		for _, alert := range alerts {
			data, err := json.Marshal(alert)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				return
			}
			cursor = alert.ID
		}
		if len(alerts) > 0 {
			c.Writer.Flush()
		}
		if len(alerts) == 100 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(c.Writer, ": heartbeat\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
