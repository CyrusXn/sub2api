package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ModelFactoryHandler struct {
	service       *service.ModelFactoryService
	apiKeyService *service.APIKeyService
}

func NewModelFactoryHandler(svc *service.ModelFactoryService, apiKeyService *service.APIKeyService) *ModelFactoryHandler {
	return &ModelFactoryHandler{service: svc, apiKeyService: apiKeyService}
}

type modelFactoryGroupDTO struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Platform       string   `json:"platform"`
	RateMultiplier float64  `json:"rate_multiplier"`
	Models         []string `json:"models"`
}

// Get 返回当前用户可用分组及账号模型并集。
func (h *ModelFactoryHandler) Get(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	groupsResult, err := h.service.ListGroups(c.Request.Context(), groups)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]modelFactoryGroupDTO, 0, len(groupsResult))
	for _, g := range groupsResult {
		out = append(out, modelFactoryGroupDTO{ID: g.ID, Name: g.Name, Platform: g.Platform, RateMultiplier: g.RateMultiplier, Models: g.Models})
	}
	response.Success(c, gin.H{"groups": out})
}
