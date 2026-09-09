package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type balanceCenterAssetService interface {
	ListAssets(context.Context) ([]service.BalanceCenterAsset, error)
	SaveAsset(context.Context, *service.BalanceCenterAsset) error
}

func (h *BalanceCenterHandler) Assets(c *gin.Context) {
	svc, ok := h.service.(balanceCenterAssetService)
	if !ok {
		response.InternalError(c, "资产服务不可用")
		return
	}
	items, err := svc.ListAssets(c.Request.Context())
	h.respond(c, items, err)
}

func (h *BalanceCenterHandler) SaveAsset(c *gin.Context) {
	svc, ok := h.service.(balanceCenterAssetService)
	if !ok {
		response.InternalError(c, "资产服务不可用")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "站点 ID 无效")
		return
	}
	var input service.BalanceCenterAsset
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "资产配置格式无效")
		return
	}
	// 以已鉴权路由中的站点为准，忽略请求体试图选择的其他站点。
	input.SiteID = id
	if err := svc.SaveAsset(c.Request.Context(), &input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, nil)
}

func (h *BalanceCenterHandler) SyncSubscription(c *gin.Context) {
	prober, ok := h.prober.(interface {
		SyncBalanceCenterSubscription(context.Context, int64) (*service.BalanceCenterAsset, error)
	})
	if !ok {
		response.InternalError(c, "订阅同步服务不可用")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "站点 ID 无效")
		return
	}
	result, err := prober.SyncBalanceCenterSubscription(c.Request.Context(), id)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, result)
}
