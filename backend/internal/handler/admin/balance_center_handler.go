package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type balanceCenterAdminService interface {
	ListOverview(context.Context) ([]service.BalanceCenterOverviewItem, error)
	ListSites(context.Context) ([]service.BalanceCenterSite, error)
	CreateSite(context.Context, *service.BalanceCenterSiteInput) (*service.BalanceCenterSite, error)
	RenameSite(context.Context, int64, string) error
	ListSnapshots(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterSnapshot], error)
	GetSettings(context.Context) (*service.BalanceCenterSettings, error)
	UpdateSettings(context.Context, *service.BalanceCenterSettings) error
	ListManualRows(context.Context) ([]service.BalanceCenterManualRow, error)
	ReplaceManualRows(context.Context, []service.BalanceCenterManualRow) error
	ListRechargeEvents(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterRechargeEvent], error)
	GetRechargeSummary(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterRechargeSummary, error)
	CreateRechargeEvent(context.Context, *service.BalanceCenterRechargeEvent) (*service.BalanceCenterRechargeEvent, error)
	DeleteRechargeEvent(context.Context, int64) error
	ListReconciliations(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterReconciliation], error)
	CreateReconciliation(context.Context, *service.BalanceCenterReconciliation) (*service.BalanceCenterReconciliation, error)
	ListAlerts(context.Context, service.BalanceCenterListFilter) (*service.BalanceCenterPage[service.BalanceCenterAlertDelivery], error)
	SaveLiandongSession(context.Context, string) (*service.BalanceCenterLiandongSessionResult, error)
	SyncLiandong(context.Context) (*service.BalanceCenterLiandongSyncResult, error)
	SyncAutomaticRecords(context.Context) (*service.BalanceCenterLiandongSyncResult, error)
}

type balanceCenterAccountProber interface {
	ProbeAccounts(context.Context, []int64) []service.UpstreamBillingProbeResult
}

// BalanceCenterHandler 提供只面向管理员的余额中心接口。
type BalanceCenterHandler struct {
	service       balanceCenterAdminService
	prober        balanceCenterAccountProber
	interactions  *service.GeminiInteractionService
	reminders     balanceCenterUpstreamReminderService
	nextThreshold balanceCenterNextThresholdService
}

type balanceCenterUpstreamReminderService interface {
	ListUpstreamSiteBalanceReminderSnapshot(context.Context) ([]service.UpstreamSiteBalanceReminder, map[string]int64, error)
	SubscribeUpstreamSiteBalanceReminders(context.Context) (<-chan service.UpstreamSiteBalanceReminderEvent, error)
}

type balanceCenterNextThresholdService interface {
	SetNextLowBalanceThreshold(context.Context, int64, float64) error
}

var balanceCenterReminderUpgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return isAllowedOpsWSOrigin(r) },
	Subprotocols: []string{"sub2api-admin"},
}

func NewBalanceCenterHandler(balanceService *service.BalanceCenterService, prober *service.UpstreamBillingProbeService, interactions *service.GeminiInteractionService) *BalanceCenterHandler {
	return &BalanceCenterHandler{service: balanceService, prober: prober, interactions: interactions, reminders: balanceService, nextThreshold: balanceService}
}

func (h *BalanceCenterHandler) SetNextLowBalanceThreshold(c *gin.Context) {
	if h == nil || h.nextThreshold == nil {
		response.Error(c, http.StatusServiceUnavailable, "余额提醒服务不可用")
		return
	}
	var input struct {
		AccountID int64    `json:"account_id"`
		Threshold *float64 `json:"threshold"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.AccountID <= 0 || input.Threshold == nil {
		response.BadRequest(c, "账号或下一提醒阈值无效")
		return
	}
	if err := h.nextThreshold.SetNextLowBalanceThreshold(c.Request.Context(), input.AccountID, *input.Threshold); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"account_id": input.AccountID, "threshold": *input.Threshold, "scheduled": true})
}

func (h *BalanceCenterHandler) UpstreamSiteBalanceReminderWS(c *gin.Context) {
	if h == nil || h.reminders == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "余额提醒服务不可用"})
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	events, err := h.reminders.SubscribeUpstreamSiteBalanceReminders(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "余额提醒推送暂不可用"})
		return
	}
	active, versions, err := h.reminders.ListUpstreamSiteBalanceReminderSnapshot(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "余额提醒状态暂不可用"})
		return
	}
	conn, err := balanceCenterReminderUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := conn.WriteJSON(service.UpstreamSiteBalanceReminderEvent{Type: "snapshot", Reminders: active, Versions: versions}); err != nil {
		return
	}

	readDone := make(chan struct{})
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := conn.WriteJSON(event); err != nil {
				logger.LegacyPrintf("handler.admin.balance_center", "upstream_balance_reminder_ws_write_failed: error_type=%T", err)
				return
			}
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				return
			}
		}
	}
}

func (h *BalanceCenterHandler) Overview(c *gin.Context) {
	items, err := h.service.ListOverview(c.Request.Context())
	h.respond(c, items, err)
}

func (h *BalanceCenterHandler) Sites(c *gin.Context) {
	items, err := h.service.ListSites(c.Request.Context())
	h.respond(c, items, err)
}

func (h *BalanceCenterHandler) CreateSite(c *gin.Context) {
	var input service.BalanceCenterSiteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "站点格式无效")
		return
	}
	item, err := h.service.CreateSite(c.Request.Context(), &input)
	h.respond(c, item, err)
}

func (h *BalanceCenterHandler) RenameSite(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "站点 ID 无效")
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "站点格式无效")
		return
	}
	h.respond(c, nil, h.service.RenameSite(c.Request.Context(), id, input.Name))
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

func (h *BalanceCenterHandler) RechargeSummary(c *gin.Context) {
	filter, err := parseBalanceCenterFilter(c, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.GetRechargeSummary(c.Request.Context(), filter)
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

func (h *BalanceCenterHandler) SaveLiandongSession(c *gin.Context) {
	var input struct {
		Curl string `json:"curl" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Curl) == "" {
		response.BadRequest(c, "请粘贴联动小铺 curl 请求")
		return
	}
	result, err := h.service.SaveLiandongSession(c.Request.Context(), input.Curl)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, result)
}

func (h *BalanceCenterHandler) SyncLiandong(c *gin.Context) {
	result, err := h.service.SyncLiandong(c.Request.Context())
	h.respond(c, result, err)
}

func (h *BalanceCenterHandler) SyncAutomaticRecords(c *gin.Context) {
	result, err := h.service.SyncAutomaticRecords(c.Request.Context())
	h.respond(c, result, err)
}
