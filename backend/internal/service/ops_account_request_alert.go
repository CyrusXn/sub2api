package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	OpsAlertMetricAccountRequestFailure = "account_request_failure"

	opsAccountRequestAlertTimeout        = 15 * time.Second
	opsAccountRequestAlertRecoveryWindow = 5 * time.Minute
)

const (
	opsAccountDiagnosisInsufficientBalance = "insufficient_balance"
	opsAccountDiagnosisAllUnavailable      = "all_accounts_unavailable"
	opsAccountDiagnosisPartialFailure      = "partial_account_failure"
	opsAccountDiagnosisUnknown             = "unknown"
)

type opsAccountRequestAlertSignal struct {
	At              time.Time
	Platform        string
	GroupID         *int64
	AccountID       *int64
	AccountName     string
	Phase           string
	Owner           string
	Source          string
	EffectiveStatus int
	BalanceEvidence bool
	ErrorSample     *opsAlertErrorSample
	Attempt         *OpsUpstreamErrorEvent
}

type opsAccountRequestDiagnosis struct {
	Code                 string
	Label                string
	Severity             string
	Priority             int
	Platform             string
	GroupID              *int64
	SignalCount          int
	AffectedAccountCount int
	AvailableAccounts    int64
	TotalAccounts        int64
	TriggerPhase         string
	EffectiveStatus      int
	AccountDetails       []*OpsAlertAccountDetail
	ErrorSamples         []*opsAlertErrorSample
}

// NotifyAccountRequestErrors 将已落库错误转换为脱敏信号并非阻塞入队，绝不保留密钥或未脱敏响应。
func (s *OpsAlertEvaluatorService) NotifyAccountRequestErrors(entries []*OpsInsertErrorLogInput) {
	if s == nil || len(entries) == 0 || s.accountRequestAlertCh == nil {
		return
	}

	signals := make([]*opsAccountRequestAlertSignal, 0, len(entries))
	for _, entry := range entries {
		signals = append(signals, buildOpsAccountRequestAlertSignals(entry)...)
	}
	if len(signals) == 0 {
		return
	}

	select {
	case s.accountRequestAlertCh <- signals:
	default:
		// 队列满通常意味着已有大量同类故障，现有 active event 会负责去重。
	}
}

func buildOpsAccountRequestAlertSignals(entry *OpsInsertErrorLogInput) []*opsAccountRequestAlertSignal {
	base := buildOpsAccountRequestAlertSignal(entry)
	if base == nil {
		return nil
	}

	attempts := base.ErrorSample.Attempts
	signals := make([]*opsAccountRequestAlertSignal, 0, len(attempts)+1)
	upstreamAccountIDs := make(map[int64]struct{}, len(attempts))
	for _, upstream := range attempts {
		if upstream == nil || upstream.AccountID <= 0 {
			continue
		}
		cloned := *base
		accountID := upstream.AccountID
		upstreamAccountIDs[accountID] = struct{}{}
		cloned.AccountID = &accountID
		cloned.AccountName = strings.TrimSpace(upstream.AccountName)
		if platform := strings.TrimSpace(upstream.Platform); platform != "" {
			cloned.Platform = platform
		}
		if upstream.UpstreamStatusCode != 0 {
			cloned.EffectiveStatus = upstream.UpstreamStatusCode
		}
		if upstream.AtUnixMs > 0 {
			cloned.At = time.UnixMilli(upstream.AtUnixMs).UTC()
		}
		cloned.BalanceEvidence = hasOpsUpstreamBalanceEvidence(upstream, cloned.EffectiveStatus)
		cloned.Attempt = upstream
		if !cloned.BalanceEvidence && base.BalanceEvidence && base.AccountID != nil && *base.AccountID == accountID {
			cloned.BalanceEvidence = true
		}
		signals = append(signals, &cloned)
	}
	if base.AccountID != nil && *base.AccountID > 0 {
		if _, exists := upstreamAccountIDs[*base.AccountID]; !exists {
			signals = append(signals, base)
		}
	}
	if len(signals) == 0 {
		signals = append(signals, base)
	}
	return signals
}

func buildOpsAccountRequestAlertSignal(entry *OpsInsertErrorLogInput) *opsAccountRequestAlertSignal {
	if entry == nil || entry.IsBusinessLimited || entry.IsCountTokens {
		return nil
	}

	phase := strings.ToLower(strings.TrimSpace(entry.ErrorPhase))
	owner := strings.ToLower(strings.TrimSpace(entry.ErrorOwner))
	source := strings.ToLower(strings.TrimSpace(entry.ErrorSource))
	status := opsAccountRequestEffectiveStatus(entry)
	if !isAlertableAccountRequestFailure(entry, phase, owner, status) {
		return nil
	}

	at := entry.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}

	sample := buildOpsAlertErrorSampleFromInput(entry)
	signal := &opsAccountRequestAlertSignal{
		At:              at.UTC(),
		Platform:        strings.TrimSpace(entry.Platform),
		GroupID:         cloneInt64Pointer(entry.GroupID),
		AccountID:       cloneInt64Pointer(entry.AccountID),
		Phase:           phase,
		Owner:           owner,
		Source:          source,
		EffectiveStatus: status,
		BalanceEvidence: hasOpsAccountBalanceEvidence(entry, status),
		ErrorSample:     sample,
	}
	if sample != nil && sample.Detail != nil {
		signal.AccountName = strings.TrimSpace(sample.Detail.AccountName)
	}
	return signal
}

func isAlertableAccountRequestFailure(entry *OpsInsertErrorLogInput, phase string, owner string, status int) bool {
	if entry == nil || (owner != "provider" && owner != "platform") {
		return false
	}

	switch phase {
	case "account_auth", "network":
		return true
	case "routing":
		return status >= 500
	case "upstream":
		if status == 0 || status == 401 || status == 402 || status == 403 || status == 408 {
			return true
		}
		if status >= 500 && status != 529 {
			return true
		}
	}

	for _, upstream := range opsAccountRequestUpstreamErrors(entry) {
		if upstream == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(upstream.Kind)) {
		case "request_error", "retry_exhausted", "credential_failover":
			return true
		}
	}
	return false
}

func opsAccountRequestEffectiveStatus(entry *OpsInsertErrorLogInput) int {
	if entry == nil {
		return 0
	}
	if entry.UpstreamStatusCode != nil && *entry.UpstreamStatusCode != 0 {
		return *entry.UpstreamStatusCode
	}
	upstreamErrors := opsAccountRequestUpstreamErrors(entry)
	for i := len(upstreamErrors) - 1; i >= 0; i-- {
		if upstream := upstreamErrors[i]; upstream != nil && upstream.UpstreamStatusCode != 0 {
			return upstream.UpstreamStatusCode
		}
	}
	return entry.StatusCode
}

func hasOpsAccountBalanceEvidence(entry *OpsInsertErrorLogInput, status int) bool {
	if entry == nil {
		return false
	}
	if status == 402 {
		return true
	}

	values := []string{
		entry.ErrorType,
		entry.ErrorMessage,
		entry.ErrorBody,
	}
	if entry.UpstreamErrorMessage != nil {
		values = append(values, *entry.UpstreamErrorMessage)
	}
	if entry.UpstreamErrorDetail != nil {
		values = append(values, *entry.UpstreamErrorDetail)
	}
	for _, upstream := range opsAccountRequestUpstreamErrors(entry) {
		if upstream == nil {
			continue
		}
		values = append(values,
			upstream.Reason,
			upstream.Message,
			upstream.Detail,
			upstream.UpstreamResponseBody,
		)
	}

	return containsOpsAccountBalanceEvidence(values)
}

func opsAccountRequestUpstreamErrors(entry *OpsInsertErrorLogInput) []*OpsUpstreamErrorEvent {
	if entry == nil {
		return nil
	}
	if len(entry.UpstreamErrors) > 0 {
		return entry.UpstreamErrors
	}
	if entry.UpstreamErrorsJSON == nil {
		return nil
	}
	upstreamErrors, _ := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	return upstreamErrors
}

func hasOpsUpstreamBalanceEvidence(upstream *OpsUpstreamErrorEvent, status int) bool {
	if upstream == nil {
		return false
	}
	if status == 402 {
		return true
	}
	return containsOpsAccountBalanceEvidence([]string{
		upstream.Reason,
		upstream.Message,
		upstream.Detail,
		upstream.UpstreamResponseBody,
	})
}

func containsOpsAccountBalanceEvidence(values []string) bool {
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		for _, marker := range []string{
			"insufficient_balance",
			"insufficient balance",
			"insufficient_account_balance",
			"insufficient account balance",
			"insufficient_quota",
			"insufficient quota",
			"quota_exhausted",
			"quota exhausted",
			"usage_limit_exceeded",
			"usage limit exceeded",
			"credit balance is too low",
			"余额不足",
		} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	return false
}

func (s *OpsAlertEvaluatorService) evaluateAccountRequestAlerts(signals []*opsAccountRequestAlertSignal) bool {
	if s == nil || len(signals) == 0 {
		return true
	}
	if s.opsRepo == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), opsAccountRequestAlertTimeout)
	defer cancel()
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return true
	}
	if s.opsService != nil && !s.opsService.IsMonitoringEnabled(ctx) {
		return true
	}

	runtimeCfg := defaultOpsAlertRuntimeSettings()
	if s.opsService != nil {
		if loaded, err := s.opsService.GetOpsAlertRuntimeSettings(ctx); err == nil && loaded != nil {
			runtimeCfg = loaded
		}
	}
	release, ok := s.tryAcquireLeaderLock(ctx, runtimeCfg.DistributedLock)
	if !ok {
		return false
	}
	if release != nil {
		defer release()
	}

	rules, err := s.opsRepo.ListAlertRules(ctx)
	if err != nil {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] list account request alert rule failed: %v", err)
		return false
	}
	rule := findAccountRequestAlertRule(rules)
	if rule == nil {
		return true
	}

	now := time.Now().UTC()
	s.accountRequestAlertLastAt = now

	dedupeRepo, ok := s.opsRepo.(OpsAlertDedupeRepository)
	if !ok {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] account request dedupe repository unavailable")
		return false
	}
	shouldRetry := false
	seen := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		if signal == nil || signal.AccountID == nil || *signal.AccountID <= 0 {
			continue
		}
		s.hydrateOpsAccountRequestSignal(ctx, signal)
		reason, reasonClass := buildOpsAccountRequestReason(signal)
		dedupeKey := buildOpsAccountRequestDedupeKey(*signal.AccountID, reasonClass, reason)
		if _, exists := seen[dedupeKey]; exists {
			continue
		}
		seen[dedupeKey] = struct{}{}

		detail := buildOpsAccountRequestDetail(signal, reason, reasonClass)
		if detail == nil {
			continue
		}
		if detail.Platform != "" && s.opsService != nil {
			if silenced, silenceErr := s.opsService.IsAlertSilenced(ctx, rule.ID, detail.Platform, detail.GroupID, nil, now); silenceErr == nil && silenced {
				s.recordDatabaseSilencedAlertEmails(ctx, rule, detail.AccountName+"异常", rule.Severity, detail.Platform, []*OpsAlertAccountDetail{detail}, []*opsAlertErrorSample{signal.ErrorSample}, now)
				continue
			}
		}

		latestEvent, latestErr := dedupeRepo.GetLatestAlertEventByDedupeKey(ctx, rule.ID, dedupeKey)
		if latestErr != nil {
			shouldRetry = true
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] get account request dedupe event failed (rule=%d): %v", rule.ID, latestErr)
			continue
		}
		if latestEvent != nil && rule.CooldownMinutes > 0 && now.Sub(latestEvent.FiredAt) < time.Duration(rule.CooldownMinutes)*time.Minute {
			continue
		}

		severity := strings.TrimSpace(rule.Severity)
		if severity == "" {
			severity = "P1"
		}
		if reasonClass == "balance" {
			severity = "P0"
		}
		metricValue := float64(1)
		event := &OpsAlertEvent{
			RuleID:         rule.ID,
			Severity:       severity,
			Status:         OpsAlertStatusFiring,
			Title:          redactOpsAlertEmailText(detail.AccountName + "异常"),
			Description:    reason,
			MetricValue:    float64Ptr(metricValue),
			ThresholdValue: float64Ptr(rule.Threshold),
			Dimensions:     buildOpsAccountRequestSignalDimensions(detail, reasonClass),
			DedupeKey:      dedupeKey,
			FiredAt:        now,
			CreatedAt:      now,
		}
		created, createErr := s.opsRepo.CreateAlertEvent(ctx, event)
		if createErr != nil {
			shouldRetry = true
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] create account request alert failed (rule=%d account=%d): %v", rule.ID, detail.AccountID, createErr)
			continue
		}
		if created == nil || created.ID <= 0 {
			continue
		}
		detail.AlertEventID = created.ID
		if detailRepo, supported := s.opsRepo.(OpsAlertAccountDetailRepository); supported {
			if insertErr := detailRepo.InsertAlertAccountDetails(ctx, []*OpsAlertAccountDetail{detail}); insertErr != nil {
				logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] 保存账号告警明细失败 (event=%d): %v", created.ID, insertErr)
			}
		}
		s.maybeSendAlertEmail(ctx, runtimeCfg, rule, created, []*OpsAlertAccountDetail{detail}, []*opsAlertErrorSample{signal.ErrorSample})
	}
	return !shouldRetry
}

// hydrateOpsAccountRequestSignal 从已落库的脱敏错误记录补齐用户和 API Key 名称。
func (s *OpsAlertEvaluatorService) hydrateOpsAccountRequestSignal(ctx context.Context, signal *opsAccountRequestAlertSignal) {
	if s == nil || s.opsRepo == nil || signal == nil || signal.ErrorSample == nil || signal.ErrorSample.Detail == nil {
		return
	}
	errorLogID := signal.ErrorSample.Detail.ID
	if errorLogID <= 0 {
		return
	}
	detail, err := s.opsRepo.GetErrorLogByID(ctx, errorLogID)
	if err != nil || detail == nil {
		return
	}
	attempts, _ := ParseOpsUpstreamErrors(detail.UpstreamErrors)
	if len(attempts) == 0 {
		attempts = signal.ErrorSample.Attempts
	}
	signal.ErrorSample = &opsAlertErrorSample{Detail: detail, Attempts: attempts}
	if signal.AccountName == "" && detail.AccountID != nil && signal.AccountID != nil && *detail.AccountID == *signal.AccountID {
		signal.AccountName = strings.TrimSpace(detail.AccountName)
	}
}

// buildOpsAccountRequestReason 只从已脱敏字段生成一行直白原因，不复制完整上游响应。
func buildOpsAccountRequestReason(signal *opsAccountRequestAlertSignal) (string, string) {
	if signal == nil {
		return "账号调用异常", "unknown"
	}
	if signal.BalanceEvidence {
		return "余额不足", "balance"
	}

	specific := opsAccountRequestSpecificReason(signal)
	phase := strings.ToLower(strings.TrimSpace(signal.Phase))
	switch phase {
	case "account_auth":
		return appendOpsAccountReason("账号认证失败", specific), "account_auth"
	case "network":
		return appendOpsAccountReason("网络连接失败", specific), "network"
	}
	if signal.EffectiveStatus >= 400 {
		fallback := "上游请求失败"
		if signal.EffectiveStatus >= 500 {
			fallback = "上游服务异常"
		}
		return fmt.Sprintf("%d：%s", signal.EffectiveStatus, valueOrFallback(specific, fallback)), fmt.Sprintf("http_%d", signal.EffectiveStatus)
	}
	if phase == "routing" {
		return appendOpsAccountReason("账号路由失败", specific), "routing"
	}
	return valueOrFallback(specific, "账号调用异常"), "unknown"
}

func appendOpsAccountReason(prefix string, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" || strings.EqualFold(detail, prefix) {
		return prefix
	}
	return prefix + "：" + detail
}

func valueOrFallback(value string, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func opsAccountRequestSpecificReason(signal *opsAccountRequestAlertSignal) string {
	values := make([]string, 0, 10)
	if signal != nil && signal.Attempt != nil {
		values = append(values, signal.Attempt.Detail, signal.Attempt.Message, signal.Attempt.Reason, signal.Attempt.UpstreamResponseBody)
	}
	if signal != nil && signal.ErrorSample != nil && signal.ErrorSample.Detail != nil {
		detail := signal.ErrorSample.Detail
		values = append(values, detail.UpstreamErrorDetail, detail.UpstreamErrorMessage, detail.Message, detail.ErrorBody)
	}
	for _, value := range values {
		if reason := normalizeOpsAccountReasonText(value); reason != "" {
			return reason
		}
	}
	return ""
}

func normalizeOpsAccountReasonText(value string) string {
	value = redactOpsAlertEmailText(value)
	if value == "" {
		return ""
	}
	if extracted := extractOpsAccountReasonFromJSON(value); extracted != "" {
		value = extracted
	}
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSpace(value)
	if len(value) > 240 {
		value = strings.TrimSpace(value[:240]) + "..."
	}
	return value
}

func extractOpsAccountReasonFromJSON(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return ""
	}
	return findOpsAccountReasonJSONValue(decoded)
}

func findOpsAccountReasonJSONValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"message", "detail", "reason", "error_description"} {
			if text, ok := typed[key].(string); ok && strings.TrimSpace(text) != "" {
				return redactOpsAlertEmailText(text)
			}
		}
		if nested, ok := typed["error"]; ok {
			if text := findOpsAccountReasonJSONValue(nested); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range typed {
			if text := findOpsAccountReasonJSONValue(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func buildOpsAccountRequestDedupeKey(accountID int64, reasonClass string, reason string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(redactOpsAlertEmailText(reason)), " "))
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("account:%d:%s:%s", accountID, strings.TrimSpace(reasonClass), hex.EncodeToString(sum[:8]))
}

func buildOpsAccountRequestDetail(signal *opsAccountRequestAlertSignal, reason string, reasonClass string) *OpsAlertAccountDetail {
	if signal == nil || signal.AccountID == nil || *signal.AccountID <= 0 {
		return nil
	}
	detail := &OpsAlertAccountDetail{
		AccountID:   *signal.AccountID,
		AccountName: redactOpsAlertEmailText(signal.AccountName),
		Platform:    redactOpsAlertEmailText(signal.Platform),
		GroupID:     cloneInt64Pointer(signal.GroupID),
		Diagnosis:   strings.TrimSpace(reasonClass),
		ErrorPhase:  redactOpsAlertEmailText(signal.Phase),
		StatusCode:  signal.EffectiveStatus,
		OccurredAt:  signal.At.UTC(),
		ErrorReason: redactOpsAlertEmailText(reason),
	}
	if sample := signal.ErrorSample; sample != nil && sample.Detail != nil {
		logDetail := sample.Detail
		detail.ErrorLogID = logDetail.ID
		detail.UserID = cloneInt64Pointer(logDetail.UserID)
		detail.UserEmail = redactOpsAlertEmailText(logDetail.UserEmail)
		detail.APIKeyID = cloneInt64Pointer(logDetail.APIKeyID)
		detail.APIKeyName = redactOpsAlertEmailText(logDetail.APIKeyName)
		detail.RequestID = redactOpsAlertEmailText(logDetail.RequestID)
		detail.ClientRequestID = redactOpsAlertEmailText(logDetail.ClientRequestID)
		detail.ErrorMessage = truncateString(redactOpsAlertEmailText(joinOpsAlertErrorText(logDetail)), 2000)
		detail.RequestedModel = redactOpsAlertEmailText(opsFirstNonEmpty(logDetail.RequestedModel, logDetail.Model))
		detail.UpstreamModel = redactOpsAlertEmailText(logDetail.UpstreamModel)
		if detail.AccountName == "" && logDetail.AccountID != nil && *logDetail.AccountID == detail.AccountID {
			detail.AccountName = redactOpsAlertEmailText(logDetail.AccountName)
		}
		if detail.Platform == "" {
			detail.Platform = redactOpsAlertEmailText(logDetail.Platform)
		}
		if detail.GroupID == nil {
			detail.GroupID = cloneInt64Pointer(logDetail.GroupID)
		}
		detail.GroupName = redactOpsAlertEmailText(logDetail.GroupName)
	}
	if detail.AccountName == "" {
		detail.AccountName = fmt.Sprintf("账号 #%d", detail.AccountID)
	}
	return detail
}

func buildOpsAccountRequestSignalDimensions(detail *OpsAlertAccountDetail, reasonClass string) map[string]any {
	dimensions := map[string]any{
		"diagnosis":  strings.TrimSpace(reasonClass),
		"account_id": detail.AccountID,
	}
	if detail.Platform != "" {
		dimensions["platform"] = detail.Platform
	}
	if detail.GroupID != nil && *detail.GroupID > 0 {
		dimensions["group_id"] = *detail.GroupID
	}
	return dimensions
}

func findAccountRequestAlertRule(rules []*OpsAlertRule) *OpsAlertRule {
	for _, rule := range rules {
		if rule != nil && rule.Enabled && rule.ID > 0 && strings.TrimSpace(rule.MetricType) == OpsAlertMetricAccountRequestFailure {
			return rule
		}
	}
	return nil
}

func (s *OpsAlertEvaluatorService) diagnoseAccountRequestSignals(ctx context.Context, signals []*opsAccountRequestAlertSignal) *opsAccountRequestDiagnosis {
	type bucket struct {
		platform string
		groupID  *int64
		signals  []*opsAccountRequestAlertSignal
	}

	buckets := map[string]*bucket{}
	for _, signal := range signals {
		if signal == nil {
			continue
		}
		groupID := int64(0)
		if signal.GroupID != nil {
			groupID = *signal.GroupID
		}
		key := fmt.Sprintf("%s:%d", strings.ToLower(signal.Platform), groupID)
		if buckets[key] == nil {
			buckets[key] = &bucket{platform: signal.Platform, groupID: cloneInt64Pointer(signal.GroupID)}
		}
		buckets[key].signals = append(buckets[key].signals, signal)
	}

	var selected *opsAccountRequestDiagnosis
	for _, item := range buckets {
		if item == nil || len(item.signals) == 0 {
			continue
		}
		current := s.diagnoseAccountRequestBucket(ctx, item.platform, item.groupID, item.signals)
		if current == nil {
			continue
		}
		if selected == nil || current.Priority > selected.Priority || (current.Priority == selected.Priority && current.SignalCount > selected.SignalCount) {
			selected = current
		}
	}
	return selected
}

func (s *OpsAlertEvaluatorService) diagnoseAccountRequestBucket(
	ctx context.Context,
	platform string,
	groupID *int64,
	signals []*opsAccountRequestAlertSignal,
) *opsAccountRequestDiagnosis {
	if len(signals) == 0 {
		return nil
	}

	failedAccounts := map[int64]struct{}{}
	latestSignals := map[int64]*opsAccountRequestAlertSignal{}
	balanceEvidence := false
	for _, signal := range signals {
		if signal == nil {
			continue
		}
		if signal.AccountID != nil && *signal.AccountID > 0 {
			accountID := *signal.AccountID
			failedAccounts[accountID] = struct{}{}
			previous := latestSignals[accountID]
			if previous == nil || signal.At.After(previous.At) {
				latestSignals[accountID] = signal
			}
		}
		balanceEvidence = balanceEvidence || signal.BalanceEvidence
	}

	totalAccounts := int64(0)
	availableAccounts := int64(0)
	var availability *OpsAccountAvailability
	if s.opsService != nil {
		loaded, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err == nil && loaded != nil {
			availability = loaded
			totalAccounts = int64(len(loaded.Accounts))
			for _, account := range loaded.Accounts {
				if account != nil && account.IsAvailable {
					availableAccounts++
				}
			}
		}
	}

	code := opsAccountDiagnosisUnknown
	if balanceEvidence {
		code = opsAccountDiagnosisInsufficientBalance
	} else if totalAccounts > 0 && availableAccounts == 0 {
		code = opsAccountDiagnosisAllUnavailable
	} else if availableAccounts > 0 {
		code = opsAccountDiagnosisPartialFailure
	}
	label, severity, priority := opsAccountDiagnosisMetadata(code)
	first := signals[0]
	details := buildOpsAccountRequestDetails(platform, groupID, code, latestSignals, availability)
	return &opsAccountRequestDiagnosis{
		Code:                 code,
		Label:                label,
		Severity:             severity,
		Priority:             priority,
		Platform:             strings.TrimSpace(platform),
		GroupID:              cloneInt64Pointer(groupID),
		SignalCount:          len(signals),
		AffectedAccountCount: len(failedAccounts),
		AvailableAccounts:    availableAccounts,
		TotalAccounts:        totalAccounts,
		TriggerPhase:         first.Phase,
		EffectiveStatus:      first.EffectiveStatus,
		AccountDetails:       details,
		ErrorSamples:         uniqueOpsAlertErrorSamples(signals),
	}
}

// uniqueOpsAlertErrorSamples 去除同一次请求按账号拆分信号后产生的重复详情。
func uniqueOpsAlertErrorSamples(signals []*opsAccountRequestAlertSignal) []*opsAlertErrorSample {
	seen := make(map[*opsAlertErrorSample]struct{}, len(signals))
	samples := make([]*opsAlertErrorSample, 0, len(signals))
	for _, signal := range signals {
		if signal == nil || signal.ErrorSample == nil {
			continue
		}
		if _, exists := seen[signal.ErrorSample]; exists {
			continue
		}
		seen[signal.ErrorSample] = struct{}{}
		samples = append(samples, signal.ErrorSample)
	}
	return samples
}

func buildOpsAccountRequestDetails(
	platform string,
	groupID *int64,
	diagnosis string,
	latestSignals map[int64]*opsAccountRequestAlertSignal,
	availability *OpsAccountAvailability,
) []*OpsAlertAccountDetail {
	accountIDs := make([]int64, 0, len(latestSignals))
	for accountID := range latestSignals {
		accountIDs = append(accountIDs, accountID)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })

	groupName := ""
	if availability != nil && availability.Group != nil {
		groupName = strings.TrimSpace(availability.Group.GroupName)
	}

	details := make([]*OpsAlertAccountDetail, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		signal := latestSignals[accountID]
		if signal == nil {
			continue
		}

		detail := &OpsAlertAccountDetail{
			AccountID:   accountID,
			AccountName: strings.TrimSpace(signal.AccountName),
			Platform:    strings.TrimSpace(platform),
			GroupID:     cloneInt64Pointer(groupID),
			GroupName:   groupName,
			Diagnosis:   diagnosis,
			ErrorPhase:  signal.Phase,
			StatusCode:  signal.EffectiveStatus,
			OccurredAt:  signal.At.UTC(),
		}
		if detail.AccountName == "" {
			detail.AccountName = fmt.Sprintf("账号 #%d", accountID)
		}

		if availability != nil {
			if account := availability.Accounts[accountID]; account != nil {
				if name := strings.TrimSpace(account.AccountName); name != "" && strings.TrimSpace(signal.AccountName) == "" {
					detail.AccountName = name
				}
				if detail.Platform == "" {
					detail.Platform = strings.TrimSpace(account.Platform)
				}
				if detail.GroupID == nil && account.GroupID > 0 {
					resolvedGroupID := account.GroupID
					detail.GroupID = &resolvedGroupID
				}
				if detail.GroupName == "" {
					detail.GroupName = strings.TrimSpace(account.GroupName)
				}
			}
		}
		details = append(details, detail)
	}
	return details
}

func opsAccountDiagnosisMetadata(code string) (label string, severity string, priority int) {
	switch code {
	case opsAccountDiagnosisInsufficientBalance:
		return "余额或额度不足", "P0", 4
	case opsAccountDiagnosisAllUnavailable:
		return "全部账号不可用", "P0", 3
	case opsAccountDiagnosisPartialFailure:
		return "部分账号异常", "P1", 2
	default:
		return "原因未知", "P1", 1
	}
}

func opsAccountDiagnosisPriority(code string) int {
	_, _, priority := opsAccountDiagnosisMetadata(strings.TrimSpace(code))
	return priority
}

func buildAccountRequestAlertDescription(diagnosis *opsAccountRequestDiagnosis) string {
	if diagnosis == nil {
		return ""
	}
	scope := "全部平台"
	if diagnosis.Platform != "" {
		scope = fmt.Sprintf("平台 %s", diagnosis.Platform)
	}
	if diagnosis.GroupID != nil && *diagnosis.GroupID > 0 {
		scope = fmt.Sprintf("%s、分组 %d", scope, *diagnosis.GroupID)
	}
	description := fmt.Sprintf(
		"%s检测到账号请求故障。判因：%s；失败请求 %d 次，涉及账号 %d 个，当前可用账号 %d/%d。触发阶段：%s，状态码：%d。请检查上游余额、账号状态和网络连接。",
		scope,
		diagnosis.Label,
		diagnosis.SignalCount,
		diagnosis.AffectedAccountCount,
		diagnosis.AvailableAccounts,
		diagnosis.TotalAccounts,
		opsAccountRequestPhaseLabel(diagnosis.TriggerPhase),
		diagnosis.EffectiveStatus,
	)
	if summary := buildOpsAccountDetailText(diagnosis.AccountDetails, 10); summary != "" {
		description += " 异常账号：" + summary + "。"
	}
	return description
}

func buildAccountRequestAlertTitle(ruleName string, diagnosis *opsAccountRequestDiagnosis) string {
	if diagnosis == nil {
		return strings.TrimSpace(ruleName)
	}
	title := fmt.Sprintf("%s：%s", strings.TrimSpace(ruleName), diagnosis.Label)
	if len(diagnosis.AccountDetails) == 0 || diagnosis.AccountDetails[0] == nil {
		return title
	}
	first := diagnosis.AccountDetails[0]
	accountLabel := strings.TrimSpace(first.AccountName)
	if accountLabel == "" || accountLabel == fmt.Sprintf("账号 #%d", first.AccountID) {
		accountLabel = fmt.Sprintf("账号 #%d", first.AccountID)
	} else {
		accountLabel = fmt.Sprintf("%s #%d", accountLabel, first.AccountID)
	}
	if len(diagnosis.AccountDetails) > 1 {
		accountLabel += fmt.Sprintf(" 等 %d 个账号", len(diagnosis.AccountDetails))
	}
	return fmt.Sprintf("%s（%s）", title, accountLabel)
}

func buildOpsAccountDetailText(details []*OpsAlertAccountDetail, limit int) string {
	if limit <= 0 || limit > len(details) {
		limit = len(details)
	}
	items := make([]string, 0, limit+1)
	for _, detail := range details[:limit] {
		if detail == nil {
			continue
		}
		parts := []string{fmt.Sprintf("%s（ID %d）", strings.TrimSpace(detail.AccountName), detail.AccountID)}
		if detail.Platform != "" {
			parts = append(parts, "平台 "+detail.Platform)
		}
		if detail.GroupName != "" {
			parts = append(parts, "分组 "+detail.GroupName)
		} else if detail.GroupID != nil {
			parts = append(parts, fmt.Sprintf("分组 ID %d", *detail.GroupID))
		}
		parts = append(parts, "阶段 "+opsAccountRequestPhaseLabel(detail.ErrorPhase))
		parts = append(parts, fmt.Sprintf("状态码 %d", detail.StatusCode))
		items = append(items, strings.Join(parts, "，"))
	}
	if remaining := len(details) - limit; remaining > 0 {
		items = append(items, fmt.Sprintf("另有 %d 个账号", remaining))
	}
	return strings.Join(items, "；")
}

func opsAccountRequestPhaseLabel(phase string) string {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "request":
		return "请求接入"
	case "auth":
		return "用户认证"
	case "account_auth":
		return "账号认证"
	case "network":
		return "网络连接"
	case "routing":
		return "账号路由"
	case "upstream":
		return "上游请求"
	case "inference":
		return "模型推理"
	case "internal":
		return "本站内部处理"
	case "first_output_timeout":
		return "首个响应超时"
	default:
		if strings.TrimSpace(phase) == "" {
			return "未知"
		}
		return strings.TrimSpace(phase)
	}
}

func buildAccountRequestAlertDimensions(diagnosis *opsAccountRequestDiagnosis) map[string]any {
	if diagnosis == nil {
		return nil
	}
	dimensions := map[string]any{
		"diagnosis":               diagnosis.Code,
		"signal_count":            diagnosis.SignalCount,
		"affected_account_count":  diagnosis.AffectedAccountCount,
		"available_account_count": diagnosis.AvailableAccounts,
		"total_account_count":     diagnosis.TotalAccounts,
		"trigger_phase":           diagnosis.TriggerPhase,
		"effective_status":        diagnosis.EffectiveStatus,
	}
	if diagnosis.Platform != "" {
		dimensions["platform"] = diagnosis.Platform
	}
	if diagnosis.GroupID != nil && *diagnosis.GroupID > 0 {
		dimensions["group_id"] = *diagnosis.GroupID
	}
	return dimensions
}

func getAlertDimensionString(dimensions map[string]any, key string) string {
	if dimensions == nil {
		return ""
	}
	value, ok := dimensions[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func getAlertDimensionInt64(dimensions map[string]any, key string) *int64 {
	value := getAlertDimensionString(dimensions, key)
	if value == "" {
		return nil
	}
	var parsed int64
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func (s *OpsAlertEvaluatorService) resolveRecoveredAccountRequestAlert(ctx context.Context, rules []*OpsAlertRule, now time.Time) bool {
	if s == nil || s.opsRepo == nil || s.opsService == nil {
		return false
	}
	rule := findAccountRequestAlertRule(rules)
	if rule == nil {
		return false
	}
	active, err := s.opsRepo.GetActiveAlertEvent(ctx, rule.ID)
	if err != nil || active == nil {
		return false
	}

	lastSignalAt := s.accountRequestAlertLastAt
	if lastSignalAt.Before(active.FiredAt) {
		lastSignalAt = active.FiredAt
	}
	if now.Sub(lastSignalAt) < opsAccountRequestAlertRecoveryWindow {
		return false
	}

	platform := getAlertDimensionString(active.Dimensions, "platform")
	groupID := getAlertDimensionInt64(active.Dimensions, "group_id")
	availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
	if err != nil || availability == nil || len(availability.Accounts) == 0 {
		return false
	}
	for _, account := range availability.Accounts {
		if account != nil && account.IsAvailable {
			resolvedAt := now
			if err := s.opsRepo.UpdateAlertEventStatus(ctx, active.ID, OpsAlertStatusResolved, &resolvedAt); err != nil {
				logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] resolve recovered account request alert failed (event=%d): %v", active.ID, err)
				return false
			}
			return true
		}
	}
	return false
}
