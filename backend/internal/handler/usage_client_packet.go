package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) ReportClientFirstPacket(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req struct {
		APIKeyID     int64  `json:"api_key_id"`
		RequestID    string `json:"request_id"`
		Milliseconds *int   `json:"milliseconds"`
	}
	if c.ShouldBindJSON(&req) != nil || req.APIKeyID <= 0 || req.Milliseconds == nil || !service.ValidClientFirstPacket(req.RequestID, *req.Milliseconds) {
		response.BadRequest(c, "Invalid client first-packet timing")
		return
	}
	// 仓储同时约束本人、Key 和服务端关联 ID；管理员也不能改写别人的计时。
	if err := h.usageService.ReportClientFirstPacket(c.Request.Context(), subject.UserID, req.APIKeyID, req.RequestID, *req.Milliseconds); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"recorded": true})
}
