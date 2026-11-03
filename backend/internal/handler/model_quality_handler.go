package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type modelQualityKeyReader interface {
	GetByID(context.Context, int64) (*service.APIKey, error)
}
type ModelQualityHandler struct {
	service *service.ModelQualityService
	keys    modelQualityKeyReader
}

func NewModelQualityHandler(svc *service.ModelQualityService, keys *service.APIKeyService) *ModelQualityHandler {
	return &ModelQualityHandler{service: svc, keys: keys}
}

func (h *ModelQualityHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 || page > 100000 {
		response.BadRequest(c, "无效页码")
		return
	}
	theme, status := c.Query("theme"), c.Query("status")
	if theme != "" && theme != "pelican" && theme != "candy" && theme != "custom" {
		response.BadRequest(c, "无效主题")
		return
	}
	if status != "" && status != "running" && status != "completed" && status != "failed" {
		response.BadRequest(c, "无效状态")
		return
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	runs, total, err := h.service.List(c.Request.Context(), page, theme, status, role == service.RoleAdmin)
	if err != nil {
		response.InternalError(c, "读取检测记录失败")
		return
	}
	response.Success(c, gin.H{"items": runs, "total": total, "page": page, "page_size": 12})
}

func (h *ModelQualityHandler) Create(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.ModelQualityMaxOutput+16*1024)
	var input struct {
		APIKeyID  int64  `json:"api_key_id"`
		Model     string `json:"model"`
		Theme     string `json:"theme"`
		Prompt    string `json:"prompt"`
		Content   string `json:"content"`
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.APIKeyID <= 0 || !modelQualityTextModel(input.Model) || (input.Status != "completed" && input.Status != "failed") || len(input.RequestID) > 100 {
		response.BadRequest(c, "检测结果信息无效")
		return
	}
	prompt, err := service.ModelQualityPrompt(input.Theme, input.Prompt)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	content := ""
	message := ""
	if input.Status == "completed" {
		content, err = service.ModelQualityOutput(input.Theme, input.Content)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	} else {
		message = "模型请求失败，请检查密钥、额度或上游账号状态"
	}
	key, err := h.keys.GetByID(c.Request.Context(), input.APIKeyID)
	if err != nil || key == nil || key.UserID != subject.UserID {
		response.Forbidden(c, "仅可使用本人的密钥")
		return
	}
	if !key.IsActive() || key.IsExpired() || key.IsQuotaExhausted() || key.Group == nil || key.GroupID == nil || !key.Group.ModelAllowlist.Allows(input.Model) {
		response.BadRequest(c, "密钥不可用或未绑定分组")
		return
	}
	author, err := service.NewModelQualityAuthor()
	if err != nil {
		response.InternalError(c, "创建匿名作者标识失败")
		return
	}
	requestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	if input.RequestID != "" {
		requestID = input.RequestID
	}
	run := service.ModelQualityRun{UserID: subject.UserID, APIKeyID: key.ID, RequestID: requestID, Author: author, GroupName: key.Group.Name, Model: strings.TrimSpace(input.Model), Theme: input.Theme, Prompt: prompt, Content: content, Status: input.Status, Error: message}
	if err = h.service.Create(c.Request.Context(), &run); err != nil {
		response.InternalError(c, "创建检测记录失败")
		return
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	response.Success(c, run.Visible(role == service.RoleAdmin))
}

func modelQualityTextModel(model string) bool {
	if strings.TrimSpace(model) == "" || len(model) > 200 {
		return false
	}
	lower := strings.ToLower(model)
	for _, hint := range []string{"image", "dall-e", "flux", "seedream", "video", "seedance", "whisper", "tts", "audio", "embedding", "rerank", "moderation", "imagen", "veo"} {
		if strings.Contains(lower, hint) {
			return false
		}
	}
	return true
}
