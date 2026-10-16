package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *BalanceCenterHandler) InteractionReviews(c *gin.Context) {
	page, err1 := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, err2 := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err1 != nil || err2 != nil || page < 1 || page > 1000000 || size < 1 || size > 100 {
		response.BadRequest(c, "分页参数无效")
		return
	}
	if h.interactions == nil {
		response.Error(c, 503, "待核对服务暂不可用")
		return
	}
	items, total, err := h.interactions.ListReview(c.Request.Context(), page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (h *BalanceCenterHandler) ResolveInteractionReview(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "任务 ID 无效")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "缺少管理员身份")
		return
	}
	var input service.GeminiInteractionResolution
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "处置参数无效")
		return
	}
	if h.interactions == nil {
		response.Error(c, 503, "待核对服务暂不可用")
		return
	}
	settled, err := h.interactions.ResolveReview(c.Request.Context(), id, subject.UserID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"settled": settled})
}
