package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ModelFactoryHandler struct{ service *service.ModelFactoryService }

func NewModelFactoryHandler(svc *service.ModelFactoryService) *ModelFactoryHandler {
	return &ModelFactoryHandler{service: svc}
}

// Get 向游客和登录用户返回相同的公开模型与价格白名单，不暴露账号凭据。
func (h *ModelFactoryHandler) Get(c *gin.Context) {
	groups, err := h.service.ListGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]modelPlazaGroup, 0, len(groups))
	for i := range groups {
		out = append(out, toModelPlazaGroupDTO(&groups[i], nil))
	}
	response.Success(c, modelPlazaResponse{Description: "", Groups: out})
}
