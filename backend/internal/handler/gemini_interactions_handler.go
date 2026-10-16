package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	geminiInteractionMaxResponse = 16 << 20
	geminiInteractionTimeout     = 10 * time.Minute
	geminiInteractionRevision    = "2026-05-20"
)

func (h *GatewayHandler) geminiInteractionsPreamble(c *gin.Context) (*service.APIKey, middleware.AuthSubject, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		googleError(c, http.StatusUnauthorized, "Invalid API key")
		return nil, middleware.AuthSubject{}, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID != key.UserID || key.User == nil || key.User.ID != subject.UserID {
		googleError(c, http.StatusUnauthorized, "Invalid user context")
		return nil, middleware.AuthSubject{}, false
	}
	if h.geminiInteractionService == nil {
		googleError(c, http.StatusNotFound, "Gemini Interactions is not enabled")
		return nil, subject, false
	}
	if key.GroupID == nil || key.Group == nil || key.Group.ID != *key.GroupID || key.Group.Platform != service.PlatformGemini || effectiveAPIKeyPlatform(c, key) != service.PlatformGemini {
		googleError(c, http.StatusBadRequest, "Gemini Interactions requires a Gemini API key group")
		return nil, subject, false
	}
	if !validGeminiInteractionQuery(c.Request.Method, c.Request.URL.Query()) {
		googleError(c, http.StatusBadRequest, "Invalid Interactions query parameters")
		return nil, subject, false
	}
	if revision := c.GetHeader("Api-Revision"); revision != "" {
		if _, err := time.Parse("2006-01-02", revision); err != nil {
			googleError(c, http.StatusBadRequest, "Invalid Api-Revision")
			return nil, subject, false
		}
	}
	return key, subject, true
}

func validGeminiInteractionQuery(method string, query url.Values) bool {
	for key, values := range query {
		if len(values) != 1 {
			return false
		}
		value := values[0]
		switch key {
		case "alt":
			if value != "sse" {
				return false
			}
		case "stream", "include_input":
			if method != http.MethodGet || value != "true" && value != "false" {
				return false
			}
		case "last_event_id":
			if method != http.MethodGet || query.Get("stream") != "true" || value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func geminiInteractionModelAllowed(c *gin.Context, key *service.APIKey, model string) bool {
	if !key.Group.ModelAllowlist.Allows(model) {
		googleError(c, http.StatusForbidden, "Model is not allowed for this API key group")
		return false
	}
	return true
}

// GeminiInteractionsCreate 保留原生 input/steps 协议，只有已持久化的资源才暴露给客户端。
func (h *GatewayHandler) GeminiInteractionsCreate(c *gin.Context) {
	key, subject, ok := h.geminiInteractionsPreamble(c)
	if !ok {
		return
	}
	body, ok := readGeminiCachedContentBody(c)
	if !ok {
		return
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		googleError(c, http.StatusBadRequest, "Invalid Interactions JSON body")
		return
	}
	request := gjson.ParseBytes(body)
	modelValue := request.Get("model")
	model := strings.TrimSpace(modelValue.String())
	input := request.Get("input")
	valid := modelValue.Type == gjson.String && model != "" && service.IsSafeGeminiModelPathSegment(strings.TrimPrefix(model, "models/")) && (input.Type == gjson.String || input.IsArray())
	// 不同 JSON 解码器对重复键取首值或末值不同，不能让准入与上游执行对象分歧。
	seen := make(map[string]struct{})
	request.ForEach(func(key, _ gjson.Result) bool {
		if _, duplicate := seen[key.Str]; duplicate {
			valid = false
			return false
		}
		seen[key.Str] = struct{}{}
		return true
	})
	for _, field := range []string{"stream", "background", "store"} {
		if v := request.Get(field); v.Exists() && v.Type != gjson.True && v.Type != gjson.False {
			valid = false
		}
	}
	if v := request.Get("generation_config"); v.Exists() && !v.IsObject() {
		valid = false
	}
	previousValue := request.Get("previous_interaction_id")
	if previousValue.Exists() && (previousValue.Type != gjson.String || previousValue.String() == "") {
		valid = false
	}
	// 恢复结算必须能读取资源；禁用上游存储会使断连后的费用无法追踪。
	if request.Get("store").Type == gjson.False {
		valid = false
	}
	if !valid {
		googleError(c, http.StatusBadRequest, "Interactions requires a billable model, input and valid native options; store=false is not supported")
		return
	}
	if !geminiInteractionModelAllowed(c, key, model) {
		return
	}
	var previous *service.GeminiInteraction
	boundID := int64(0)
	if previousValue.Exists() {
		previous, ok = h.ownedGeminiInteraction(c, key, subject, previousValue.String())
		if !ok {
			return
		}
		if previous.UpstreamID == "" {
			googleError(c, http.StatusConflict, "Previous interaction is not yet available")
			return
		}
		boundID = previous.AccountID
		var err error
		body, err = sjson.SetBytes(body, "previous_interaction_id", previous.UpstreamID)
		if err != nil {
			googleError(c, http.StatusBadRequest, "Invalid previous interaction")
			return
		}
	}
	stream := request.Get("stream").Bool()
	setOpsRequestContext(c, model, stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(stream, false)))
	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(context.WithValue(c.Request.Context(), ctxkey.UserID, subject.UserID))
	c.Request = c.Request.WithContext(pricingCtx)
	log := requestLogger(c, "handler.gemini.interactions", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", key.ID))
	if decision := h.checkSecurityAudit(c, log, key, subject, service.ContentModerationProtocolGemini, model, geminiInteractionAuditBody(request)); decision != nil && !decision.AllowNextStage {
		googleSecurityAuditError(c, decision)
		return
	}
	mapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), key.GroupID, model)
	routingModel := strings.TrimPrefix(model, "models/")
	if mapping.Mapped {
		routingModel = mapping.MappedModel
	}
	helper := NewConcurrencyHelper(h.concurrencyHelper.concurrencyService, SSEPingFormatNone, 0)
	started := false
	userRelease, err := helper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, false, &started)
	if err != nil {
		googleConcurrencyError(c, err, "user")
		return
	}
	if userRelease != nil {
		defer userRelease()
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if !h.checkGeminiCachedContentBilling(c, log, key, subscription) {
		return
	}
	inflightRelease, err := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, key, subscription, tokenInflightEstimate(routingModel, body))
	if err != nil {
		writeGeminiCachedContentBillingError(c, err)
		return
	}
	defer inflightRelease()
	excluded, err := h.gatewayService.ExcludedGeminiInteractionAccounts(c.Request.Context(), key.GroupID, boundID)
	if err != nil {
		googleError(c, http.StatusServiceUnavailable, "No available Gemini Interactions accounts")
		return
	}
	selection, err := h.gatewayService.SelectAccountWithLoadAwareness(c.Request.Context(), key.GroupID, "", routingModel, excluded, "", 0)
	if err != nil {
		googleError(c, http.StatusServiceUnavailable, "No available Gemini Interactions accounts")
		return
	}
	if selection.Account == nil || !service.IsGeminiInteractionAccount(selection.Account) || boundID > 0 && selection.Account.ID != boundID {
		if selection.Acquired && selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		googleError(c, http.StatusServiceUnavailable, "The bound Gemini Interactions account is unavailable")
		return
	}
	clientCtx := c.Request.Context()
	// 上游 POST 不可安全重试；客户端断连后仍需获得资源 ID 和最终用量。
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(clientCtx), geminiInteractionTimeout)
	defer cancel()
	c.Request = c.Request.WithContext(opCtx)
	accountRelease, ok := h.acquireGeminiCachedContentAccountSlot(c, log, helper, selection)
	if !ok {
		return
	}
	defer accountRelease()
	if clientCtx.Err() != nil {
		return
	}
	account, vetoed, _ := h.gatewayService.GatewayProfitControlVetoLatest(service.ContextWithSelectionProfitGate(opCtx, selection), selection.Account)
	if vetoed || !service.IsGeminiInteractionAccount(account) {
		googleError(c, http.StatusServiceUnavailable, "Gemini Interactions account admission rejected")
		return
	}
	upstreamModel := account.GetMappedModel(routingModel)
	if !service.IsSafeGeminiModelPathSegment(strings.TrimPrefix(upstreamModel, "models/")) {
		googleError(c, http.StatusServiceUnavailable, "Invalid upstream model configuration")
		return
	}
	body, err = sjson.SetBytes(body, "model", upstreamModel)
	if err != nil {
		googleError(c, http.StatusBadRequest, "Invalid Interactions model")
		return
	}
	id, err := service.GeminiInteractionPublicID()
	if err != nil {
		googleError(c, http.StatusInternalServerError, "Failed to allocate interaction ID")
		return
	}
	revision := c.GetHeader("Api-Revision")
	if revision == "" {
		revision = geminiInteractionRevision
	}
	r := &service.GeminiInteraction{PublicID: id, UserID: subject.UserID, APIKeyID: key.ID, GroupID: *key.GroupID, AccountID: account.ID,
		Model: model, UpstreamModel: upstreamModel, PayloadHash: service.HashUsageRequestPayload([]byte(request.Raw)), Revision: revision, PricingAt: pricingAt,
		BillingGroup: key.Group, ChannelUsage: clientRequestedUsageFields(c, mapping, model, upstreamModel), Status: "pending", Stream: stream, Background: request.Get("background").Bool(),
		UserConcurrency: subject.Concurrency, AccountConcurrency: account.Concurrency}
	if subscription != nil {
		r.SubscriptionID = subscription.ID
	}
	if previous != nil {
		r.PreviousPublicID, r.PreviousUpstreamID = previous.PublicID, previous.UpstreamID
	}
	if r.Background && subscription == nil && (h.cfg == nil || h.cfg.RunMode != config.RunModeSimple) {
		hold, priced := h.gatewayService.EstimateInflightReservation(opCtx, key, tokenInflightEstimate(routingModel, body))
		if !priced || hold <= 0 || math.IsNaN(hold) || math.IsInf(hold, 0) {
			googleError(c, http.StatusServiceUnavailable, "Background interaction pricing is unavailable")
			return
		}
		r.BalanceHold = hold
	}
	if err = h.geminiInteractionService.Save(opCtx, r); err != nil {
		if errors.Is(err, service.ErrGeminiInteractionBusy) {
			googleError(c, http.StatusServiceUnavailable, "Interactions dependencies are being updated; retry later")
			return
		}
		if errors.Is(err, service.ErrInsufficientBalance) {
			writeGeminiCachedContentBillingError(c, err)
			return
		}
		if errors.Is(err, service.ErrGeminiInteractionConcurrency) {
			googleError(c, http.StatusTooManyRequests, "Too many pending Interactions")
			return
		}
		googleError(c, http.StatusInternalServerError, "Failed to persist interaction")
		return
	}
	// POST 结束仍未拿到资源 ID 时不可重发或退款，持久化待核对以释放执行并发。
	defer func() {
		if r.Settled || r.UpstreamID != "" {
			return
		}
		reviewCtx, reviewCancel := context.WithTimeout(context.WithoutCancel(opCtx), 5*time.Second)
		defer reviewCancel()
		if err := h.geminiInteractionService.RequireReview(reviewCtx, r, "create_result_unknown"); err != nil {
			slog.Warn("Gemini Interactions 待核对标记保存失败，将后台恢复", "interaction_id", r.PublicID)
		}
	}()
	setOpsSelectedAccount(c, account.ID, account.Platform)
	resp, err := h.geminiInteractionService.Forward(opCtx, account, http.MethodPost, "", "", c.Request.URL.Query(), revision, body)
	if err != nil {
		if errors.Is(err, service.ErrGeminiInteractionRequestNotSent) {
			if failureErr := h.geminiInteractionService.Failed(opCtx, r); failureErr != nil {
				slog.Warn("Gemini Interactions 未发送结果持久化失败", "interaction_id", r.PublicID)
			}
		} else {
			slog.Warn("Gemini Interactions 创建结果不确定，保留待恢复任务", "interaction_id", r.PublicID)
		}
		googleError(c, http.StatusBadGateway, "Gemini Interactions upstream request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 明确的请求拒绝不会执行模型；5xx/超时结果不确定，保留待恢复现场。
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout {
			if err := h.geminiInteractionService.Failed(opCtx, r); err != nil {
				slog.Warn("Gemini Interactions 拒绝结果持久化失败", "interaction_id", r.PublicID)
			}
		} else {
			slog.Warn("Gemini Interactions 上游结果不确定，保留待恢复任务", "interaction_id", r.PublicID)
		}
		writeGeminiInteractionUpstreamError(c, resp.StatusCode)
		return
	}
	h.writeGeminiInteractionResponse(c, clientCtx, opCtx, r, resp, true)
}

func (h *GatewayHandler) ownedGeminiInteraction(c *gin.Context, key *service.APIKey, subject middleware.AuthSubject, id string) (*service.GeminiInteraction, bool) {
	r, err := h.geminiInteractionService.GetOwned(c.Request.Context(), key.ID, subject.UserID, *key.GroupID, id)
	if err != nil {
		if errors.Is(err, service.ErrGeminiInteractionNotFound) {
			googleError(c, http.StatusNotFound, "Interaction not found")
		} else {
			googleError(c, http.StatusInternalServerError, "Failed to read interaction")
		}
		return nil, false
	}
	if !geminiInteractionModelAllowed(c, key, r.Model) {
		return nil, false
	}
	return r, true
}

func (h *GatewayHandler) GeminiInteractionsGet(c *gin.Context) {
	h.geminiInteractionResource(c, http.MethodGet, "")
}
func (h *GatewayHandler) GeminiInteractionsCancel(c *gin.Context) {
	h.geminiInteractionResource(c, http.MethodPost, "cancel")
}
func (h *GatewayHandler) GeminiInteractionsDelete(c *gin.Context) {
	h.geminiInteractionResource(c, http.MethodDelete, "")
}

func (h *GatewayHandler) geminiInteractionResource(c *gin.Context, method, action string) {
	key, subject, ok := h.geminiInteractionsPreamble(c)
	if !ok {
		return
	}
	r, ok := h.ownedGeminiInteraction(c, key, subject, c.Param("interactionID"))
	if !ok {
		return
	}
	if revision := c.GetHeader("Api-Revision"); revision != "" && revision != r.Revision {
		googleError(c, http.StatusBadRequest, "Api-Revision does not match the interaction")
		return
	}
	if r.UpstreamID == "" {
		googleError(c, http.StatusConflict, "Interaction is not yet available")
		return
	}
	clientCtx := c.Request.Context()
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(clientCtx), geminiInteractionTimeout)
	defer cancel()
	account, err := h.geminiInteractionService.Account(opCtx, r.AccountID)
	if err != nil || !service.IsGeminiInteractionAccount(account) {
		googleError(c, http.StatusServiceUnavailable, "The bound Gemini Interactions account is unavailable")
		return
	}
	if method == http.MethodDelete {
		// 删除前读取最新用量，避免后台刚完成而本地尚未结算时删除恢复入口。
		current, err := h.geminiInteractionService.Forward(opCtx, account, http.MethodGet, r.UpstreamID, "", nil, r.Revision, nil)
		if err != nil {
			googleError(c, http.StatusBadGateway, "Failed to refresh interaction before deletion")
			return
		}
		if current.StatusCode < 200 || current.StatusCode >= 300 {
			current.Body.Close()
			writeGeminiInteractionUpstreamError(c, current.StatusCode)
			return
		}
		out, err := h.observeGeminiInteractionJSON(opCtx, r, current)
		current.Body.Close()
		if err != nil {
			googleError(c, http.StatusBadGateway, "Invalid Gemini Interactions response")
			return
		}
		if !service.GeminiInteractionTerminal(gjson.GetBytes(out, "status").String()) {
			googleError(c, http.StatusConflict, "Cancel the active interaction before deleting it")
			return
		}
		if r.Usage == nil {
			googleError(c, http.StatusConflict, "Interaction usage is not yet available")
			return
		}
		if err := h.geminiInteractionService.Settle(opCtx, r); err != nil || !r.Settled {
			googleError(c, http.StatusConflict, "Interaction settlement is pending")
			return
		}
	}
	resp, err := h.geminiInteractionService.Forward(opCtx, account, method, r.UpstreamID, action, c.Request.URL.Query(), r.Revision, nil)
	if err != nil {
		googleError(c, http.StatusBadGateway, "Gemini Interactions upstream request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeGeminiInteractionUpstreamError(c, resp.StatusCode)
		return
	}
	if method == http.MethodDelete {
		if err := h.geminiInteractionService.Delete(opCtx, r); err != nil {
			googleError(c, http.StatusInternalServerError, "Failed to delete interaction")
			return
		}
		c.Status(http.StatusNoContent)
		c.Writer.WriteHeaderNow()
		return
	}
	h.writeGeminiInteractionResponse(c, clientCtx, opCtx, r, resp, false)
}

func writeGeminiInteractionUpstreamError(c *gin.Context, status int) {
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	googleError(c, status, "Gemini Interactions upstream request failed")
}

func (h *GatewayHandler) writeGeminiInteractionResponse(c *gin.Context, clientCtx, opCtx context.Context, r *service.GeminiInteraction, resp *http.Response, creating bool) {
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		h.writeGeminiInteractionSSE(c, clientCtx, opCtx, r, resp, creating)
		return
	}
	out, err := h.observeGeminiInteractionJSON(opCtx, r, resp)
	if err != nil {
		if creating {
			h.cleanupGeminiInteractionCreate(opCtx, r)
		}
		googleError(c, http.StatusBadGateway, "Failed to persist Gemini Interactions response")
		return
	}
	if err = h.geminiInteractionService.Settle(opCtx, r); err != nil {
		googleError(c, http.StatusServiceUnavailable, "Interaction settlement is pending")
		return
	}
	if clientCtx.Err() == nil {
		c.Data(resp.StatusCode, "application/json", out)
	}
}

func (h *GatewayHandler) observeGeminiInteractionJSON(ctx context.Context, r *service.GeminiInteraction, resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, geminiInteractionMaxResponse+1))
	if err != nil || len(body) > geminiInteractionMaxResponse {
		return nil, errors.New("invalid interaction response size")
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() || gjson.GetBytes(body, "id").Type != gjson.String || gjson.GetBytes(body, "id").String() == "" {
		return nil, errors.New("invalid interaction response ID")
	}
	return h.geminiInteractionService.Observe(ctx, r, body)
}

// SSE 按完整事件解析，多行 data 合并后再落库；断流的半个事件不能触发结算。
func (h *GatewayHandler) writeGeminiInteractionSSE(c *gin.Context, clientCtx, opCtx context.Context, r *service.GeminiInteraction, resp *http.Response, creating bool) {
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, geminiInteractionMaxResponse+1))
	scanner.Buffer(make([]byte, 64<<10), geminiInteractionMaxResponse+1)
	var lines, data []string
	var pending bytes.Buffer
	total, started, failed, writable := 0, false, false, true
	write := func(payload []byte) {
		if !writable || clientCtx.Err() != nil {
			return
		}
		if !started {
			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("X-Accel-Buffering", "no")
			c.Status(resp.StatusCode)
			started = true
		}
		if _, err := c.Writer.Write(payload); err != nil {
			writable = false
			return
		}
		c.Writer.Flush()
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > geminiInteractionMaxResponse {
			failed = true
			break
		}
		if line != "" {
			lines = append(lines, line)
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
			continue
		}
		var frame bytes.Buffer
		if len(data) > 0 {
			payload := []byte(strings.Join(data, "\n"))
			out, err := h.geminiInteractionService.Observe(opCtx, r, payload)
			if err != nil {
				if creating {
					h.cleanupGeminiInteractionCreate(opCtx, r)
				}
				failed = true
				break
			}
			if err = h.geminiInteractionService.Settle(opCtx, r); err != nil {
				failed = true
				break
			}
			wroteData := false
			for _, original := range lines {
				if strings.HasPrefix(original, "data:") {
					if !wroteData {
						for _, part := range bytes.Split(out, []byte("\n")) {
							frame.WriteString("data: ")
							frame.Write(part)
							frame.WriteByte('\n')
						}
						wroteData = true
					}
				} else {
					frame.WriteString(original)
					frame.WriteByte('\n')
				}
			}
		} else {
			for _, original := range lines {
				frame.WriteString(original)
				frame.WriteByte('\n')
			}
		}
		frame.WriteByte('\n')
		pending.Write(frame.Bytes())
		if r.UpstreamID != "" {
			write(pending.Bytes())
			pending.Reset()
		}
		lines, data = nil, nil
	}
	if scanner.Err() != nil || len(lines) > 0 {
		failed = true
	}
	if r.UpstreamID == "" {
		failed = true
	}
	if failed && clientCtx.Err() == nil {
		if !started {
			googleError(c, http.StatusBadGateway, "Invalid Gemini Interactions event stream")
		} else {
			write([]byte("event: error\ndata: {\"error\":{\"code\":502,\"message\":\"Gemini Interactions stream interrupted\"}}\n\n"))
		}
	}
	if failed {
		slog.Warn("Gemini Interactions 流未完整读取，保留待恢复任务", "interaction_id", r.PublicID)
	}
}

// 已知资源 ID 但不能持久化时尽力停止生成；不重建、不换号，也不假定取消等于已结算。
func (h *GatewayHandler) cleanupGeminiInteractionCreate(ctx context.Context, r *service.GeminiInteraction) {
	slog.Warn("Gemini Interactions 创建响应未持久化", "interaction_id", r.PublicID)
	if r.UpstreamID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	account, err := h.geminiInteractionService.Account(cleanupCtx, r.AccountID)
	if err != nil || !service.IsGeminiInteractionAccount(account) {
		return
	}
	resp, err := h.geminiInteractionService.Forward(cleanupCtx, account, http.MethodPost, r.UpstreamID, "cancel", nil, r.Revision, nil)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	if _, err = h.observeGeminiInteractionJSON(cleanupCtx, r, resp); err != nil {
		return
	}
	if err = h.geminiInteractionService.Settle(cleanupCtx, r); err != nil {
		slog.Warn("Gemini Interactions 取消后的结算等待恢复", "interaction_id", r.PublicID)
	}
}

// 只构造审查视图，不将 Models 协议字段发往 Interactions 上游。
func geminiInteractionAuditBody(request gjson.Result) []byte {
	parts := make([]any, 0)
	var collect func(gjson.Result)
	collect = func(v gjson.Result) {
		if v.Type == gjson.String {
			parts = append(parts, map[string]any{"text": v.String()})
			return
		}
		if v.IsArray() {
			v.ForEach(func(_, child gjson.Result) bool { collect(child); return true })
			return
		}
		if !v.IsObject() {
			return
		}
		if text := v.Get("text"); text.Type == gjson.String {
			parts = append(parts, map[string]any{"text": text.String()})
		}
		if content := v.Get("content"); content.Exists() {
			collect(content)
		}
		mime, data, uri := v.Get("mime_type").String(), v.Get("data").String(), v.Get("uri").String()
		if mime != "" && data != "" {
			parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": mime, "data": data}})
		}
		if uri != "" {
			parts = append(parts, map[string]any{"fileData": map[string]string{"mimeType": mime, "fileUri": uri}})
		}
	}
	input := request.Get("input")
	var contents []any
	conversation := false
	if input.IsArray() {
		input.ForEach(func(_, turn gjson.Result) bool {
			if turn.Get("role").Exists() {
				conversation = true
				return false
			}
			return true
		})
	}
	if conversation {
		// 保留轮次角色，让现有审查器仍只检查最新用户输入，而非把历史回答改成用户输入。
		input.ForEach(func(_, turn gjson.Result) bool {
			parts = nil
			collect(turn)
			role := turn.Get("role").String()
			if role == "" {
				role = "user"
			}
			if role == "assistant" {
				role = "model"
			}
			contents = append(contents, map[string]any{"role": role, "parts": parts})
			return true
		})
	} else {
		collect(input)
		contents = []any{map[string]any{"role": "user", "parts": parts}}
	}
	view := map[string]any{"contents": contents}
	if system := request.Get("system_instruction"); system.Type == gjson.String {
		view["systemInstruction"] = map[string]any{"parts": []any{map[string]string{"text": system.String()}}}
	}
	body, _ := json.Marshal(view)
	return body
}
