package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type balanceCenterAdminService interface {
	ListOverview(context.Context) ([]service.BalanceCenterOverviewItem, error)
	ListSites(context.Context) ([]service.BalanceCenterSite, error)
	ListSnapshots(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterSnapshot], error)
	GetSettings(context.Context) (*service.BalanceCenterSettings, error)
	UpdateSettings(context.Context, *service.BalanceCenterSettings) error
	ListManualRows(context.Context) ([]service.BalanceCenterManualRow, error)
	ReplaceManualRows(context.Context, []service.BalanceCenterManualRow) error
	ListRechargeEvents(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterRechargeEvent], error)
	CreateRechargeEvent(context.Context, *service.BalanceCenterRechargeEvent) (*service.BalanceCenterRechargeEvent, error)
	DeleteRechargeEvent(context.Context, int64) error
	ListReconciliations(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterReconciliation], error)
	CreateReconciliation(context.Context, *service.BalanceCenterReconciliation) (*service.BalanceCenterReconciliation, error)
	ListAlerts(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterAlertDelivery], error)
}

type balanceCenterAccountProber interface {
	ProbeAccounts(context.Context, []int64) []service.UpstreamBillingProbeResult
}

// BalanceCenterHandler 提供只面向管理员的余额中心接口。
type BalanceCenterHandler struct {
	service balanceCenterAdminService
	prober  balanceCenterAccountProber
}

func NewBalanceCenterHandler(balanceService *service.BalanceCenterService, prober *service.UpstreamBillingProbeService) *BalanceCenterHandler {
	return &BalanceCenterHandler{service: balanceService, prober: prober}
}

func (h *BalanceCenterHandler) Overview(c *gin.Context) {
	items, err := h.service.ListOverview(c.Request.Context())
	h.respond(c, items, err)
}

func (h *BalanceCenterHandler) Sites(c *gin.Context) {
	items, err := h.service.ListSites(c.Request.Context())
	h.respond(c, items, err)
}

func (h *BalanceCenterHandler) Snapshots(c *gin.Context) {
	filter, err := parseBalanceCenterFilter(c, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.ListSnapshots(c.Request.Context(), filter)
	h.respond(c, result, err)
}

func (h *BalanceCenterHandler) GetSettings(c *gin.Context) {
	settings, err := h.service.GetSettings(c.Request.Context())
	h.respond(c, settings, err)
}

func (h *BalanceCenterHandler) UpdateSettings(c *gin.Context) {
	var input service.BalanceCenterSettings
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "余额中心设置格式无效")
		return
	}
	if err := h.service.UpdateSettings(c.Request.Context(), &input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, input)
}

func (h *BalanceCenterHandler) ProbeAccounts(c *gin.Context) {
	var input struct {
		AccountIDs []int64 `json:"account_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.AccountIDs) == 0 {
		response.BadRequest(c, "至少选择一个账号")
		return
	}
	if len(input.AccountIDs) > service.UpstreamBillingProbeMaxBatchSize {
		response.BadRequest(c, "单次探测账号数量超过上限")
		return
	}
	for _, id := range input.AccountIDs {
		if id <= 0 {
			response.BadRequest(c, "账号 ID 无效")
			return
		}
	}
	response.Success(c, h.prober.ProbeAccounts(c.Request.Context(), input.AccountIDs))
}

func (h *BalanceCenterHandler) ManualRows(c *gin.Context) {
	items, err := h.service.ListManualRows(c.Request.Context())
	h.respond(c, items, err)
}

func (h *BalanceCenterHandler) ReplaceManualRows(c *gin.Context) {
	var input struct {
		Items []service.BalanceCenterManualRow `json:"items"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "手工基线格式无效")
		return
	}
	if err := h.service.ReplaceManualRows(c.Request.Context(), input.Items); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"items": input.Items})
}

func (h *BalanceCenterHandler) RechargeEvents(c *gin.Context) {
	filter, err := parseBalanceCenterFilter(c, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.ListRechargeEvents(c.Request.Context(), filter)
	h.respond(c, result, err)
}

func (h *BalanceCenterHandler) CreateRechargeEvent(c *gin.Context) {
	var input service.BalanceCenterRechargeEvent
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "充值记录格式无效")
		return
	}
	item, err := h.service.CreateRechargeEvent(c.Request.Context(), &input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, item)
}

func (h *BalanceCenterHandler) DeleteRechargeEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "充值记录 ID 无效")
		return
	}
	if err := h.service.DeleteRechargeEvent(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *BalanceCenterHandler) Reconciliations(c *gin.Context) {
	filter, err := parseBalanceCenterFilter(c, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.ListReconciliations(c.Request.Context(), filter)
	h.respond(c, result, err)
}

func (h *BalanceCenterHandler) CreateReconciliation(c *gin.Context) {
	var input service.BalanceCenterReconciliation
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "对账记录格式无效")
		return
	}
	item, err := h.service.CreateReconciliation(c.Request.Context(), &input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, item)
}

func (h *BalanceCenterHandler) Alerts(c *gin.Context) {
	filter, err := parseBalanceCenterFilter(c, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.ListAlerts(c.Request.Context(), filter)
	h.respond(c, result, err)
}

func (h *BalanceCenterHandler) respond(c *gin.Context, data any, err error) {
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

func parseBalanceCenterFilter(c *gin.Context, includeTime bool) (service.BalanceCenterListFilter, error) {
	page, pageSize := response.ParsePagination(c)
	filter := service.BalanceCenterListFilter{Page: page, PageSize: pageSize, Status: strings.TrimSpace(c.Query("status"))}
	for name, target := range map[string]*int64{"site_id": &filter.SiteID, "account_id": &filter.AccountID} {
		value := strings.TrimSpace(c.Query(name))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return filter, errors.New(name + " 参数无效")
		}
		*target = parsed
	}
	if !includeTime {
		return filter, nil
	}
	for name, target := range map[string]**time.Time{"start_time": &filter.StartTime, "end_time": &filter.EndTime} {
		value := strings.TrimSpace(c.Query(name))
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, errors.New(name + " 参数必须使用 RFC3339 时间")
		}
		*target = &parsed
	}
	if filter.StartTime != nil && filter.EndTime != nil && filter.StartTime.After(*filter.EndTime) {
		return filter, errors.New("开始时间不能晚于结束时间")
	}
	return filter, nil
}

func (h *BalanceCenterHandler) SyncUnavailable(c *gin.Context) {
	response.Error(c, http.StatusServiceUnavailable, "余额数据同步服务尚未初始化")
}
