package handler

import (
	"context"
	"errors"
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
	service  *service.ModelQualityService
	keys     modelQualityKeyReader
	official *service.ModelQualityOfficialService
}

func NewModelQualityHandler(svc *service.ModelQualityService, keys *service.APIKeyService, official *service.ModelQualityOfficialService) *ModelQualityHandler {
	return &ModelQualityHandler{service: svc, keys: keys, official: official}
}

func (h *ModelQualityHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 || page > 100000 {
		response.BadRequest(c, "无效页码")
		return
	}
	theme, status := c.Query("theme"), c.Query("status")
	if theme != "" && theme != "pelican" && theme != "candy" && theme != "official" && theme != "custom" {
		response.BadRequest(c, "无效主题")
		return
	}
	if status != "" && status != "running" && status != "completed" && status != "failed" {
		response.BadRequest(c, "无效状态")
		return
	}
	group, model := c.Query("group"), c.Query("model")
	if len(group) > 400 || len(model) > 200 {
		response.BadRequest(c, "分组或模型筛选过长")
		return
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	runs, total, err := h.service.List(c.Request.Context(), page, theme, status, group, model, role == service.RoleAdmin)
	if err != nil {
		response.InternalError(c, "读取检测记录失败")
		return
	}
	filters, err := h.service.FilterOptions(c.Request.Context(), theme)
	if err != nil {
		response.InternalError(c, "读取检测筛选项失败")
		return
	}
	response.Success(c, gin.H{"items": runs, "total": total, "page": page, "page_size": 12, "filters": filters})
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
	if err := c.ShouldBindJSON(&input); err != nil || input.APIKeyID <= 0 || !modelQualityTextModel(input.Model) || (input.Status != "running" && input.Status != "completed" && input.Status != "failed") || len(input.RequestID) > 100 || input.Theme == "official" {
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
	} else if input.Status == "failed" {
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
	author := ""
	if key.User != nil {
		author = key.User.Email
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

func (h *ModelQualityHandler) Update(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效记录")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.ModelQualityMaxOutput+16*1024)
	var input struct {
		Status    string `json:"status"`
		Content   string `json:"content"`
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Content) > service.ModelQualityMaxOutput || len(input.RequestID) > 100 || (input.Status != "running" && input.Status != "completed" && input.Status != "failed") || (input.Status == "completed" && strings.TrimSpace(input.Content) == "") {
		response.BadRequest(c, "检测进度信息无效")
		return
	}
	err = h.service.Update(c.Request.Context(), id, subject.UserID, input.Status, input.Content, input.RequestID)
	if errors.Is(err, service.ErrModelQualityNotRunning) {
		response.NotFound(c, "检测记录不存在或已结束")
		return
	}
	if err != nil {
		response.InternalError(c, "更新检测记录失败")
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func modelQualityTextModel(model string) bool {
	return service.IsModelQualityTextModel(model)
}

func (h *ModelQualityHandler) OfficialStatus(c *gin.Context) {
	role, _ := middleware.GetUserRoleFromContext(c)
	cfg, err := h.official.Status(c.Request.Context(), role == service.RoleAdmin)
	if err != nil {
		response.InternalError(c, "读取官key检测配置失败")
		return
	}
	response.Success(c, cfg)
}

func (h *ModelQualityHandler) ConfigureOfficial(c *gin.Context) {
	role, _ := middleware.GetUserRoleFromContext(c)
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || role != service.RoleAdmin {
		response.Forbidden(c, "仅管理员可以配置官key检测")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Enabled  bool   `json:"enabled"`
		APIKeyID int64  `json:"api_key_id"`
		Model    string `json:"model"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "无效配置")
		return
	}
	if err := h.official.Configure(c.Request.Context(), subject.UserID, input.APIKeyID, input.Model, input.Enabled); err != nil {
		response.BadRequest(c, "配置失败，请检查本人密钥、模型或服务状态")
		return
	}
	h.OfficialStatus(c)
}
