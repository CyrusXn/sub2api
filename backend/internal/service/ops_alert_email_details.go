package service

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const opsAlertEmailSampleLimit = 10

type opsAlertErrorSample struct {
	Detail   *OpsErrorLogDetail
	Attempts []*OpsUpstreamErrorEvent
}

type opsAlertEmailMetadata struct {
	ErrorIDs       []int64
	TargetSite     string
	AccountSummary string
}

// buildOpsAlertErrorSampleFromInput 只复制已经过落库边界脱敏的排障字段，不复制请求头、Cookie 或密钥信息。
func buildOpsAlertErrorSampleFromInput(entry *OpsInsertErrorLogInput) *opsAlertErrorSample {
	if entry == nil {
		return nil
	}
	attempts := entry.UpstreamErrors
	if len(attempts) == 0 && entry.UpstreamErrorsJSON != nil {
		attempts, _ = ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	}
	sanitizedAttempts := make([]*OpsUpstreamErrorEvent, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt == nil {
			continue
		}
		cloned := *attempt
		cloned.Platform = redactOpsAlertEmailText(cloned.Platform)
		cloned.AccountName = redactOpsAlertEmailText(cloned.AccountName)
		cloned.UpstreamRequestID = redactOpsAlertEmailText(cloned.UpstreamRequestID)
		cloned.UpstreamURL = safeUpstreamURL(redactOpsAlertEmailText(cloned.UpstreamURL))
		cloned.UpstreamResponseBody = redactOpsAlertEmailText(cloned.UpstreamResponseBody)
		cloned.Kind = redactOpsAlertEmailText(cloned.Kind)
		cloned.Stage = redactOpsAlertEmailText(cloned.Stage)
		cloned.Scope = redactOpsAlertEmailText(cloned.Scope)
		cloned.Reason = redactOpsAlertEmailText(cloned.Reason)
		cloned.Message = redactOpsAlertEmailText(cloned.Message)
		cloned.Detail = redactOpsAlertEmailText(cloned.Detail)
		sanitizedAttempts = append(sanitizedAttempts, &cloned)
	}

	detail := &OpsErrorLogDetail{
		OpsErrorLog: OpsErrorLog{
			ID:               entry.ErrorLogID,
			CreatedAt:        entry.CreatedAt.UTC(),
			Phase:            redactOpsAlertEmailText(entry.ErrorPhase),
			Type:             redactOpsAlertEmailText(entry.ErrorType),
			Owner:            redactOpsAlertEmailText(entry.ErrorOwner),
			Source:           redactOpsAlertEmailText(entry.ErrorSource),
			Severity:         redactOpsAlertEmailText(entry.Severity),
			StatusCode:       entry.StatusCode,
			Platform:         redactOpsAlertEmailText(entry.Platform),
			Model:            redactOpsAlertEmailText(entry.Model),
			ClientRequestID:  redactOpsAlertEmailText(entry.ClientRequestID),
			RequestID:        redactOpsAlertEmailText(entry.RequestID),
			Message:          redactOpsAlertEmailText(entry.ErrorMessage),
			AccountID:        cloneInt64Pointer(entry.AccountID),
			GroupID:          cloneInt64Pointer(entry.GroupID),
			RequestPath:      safeUpstreamURL(redactOpsAlertEmailText(entry.RequestPath)),
			InboundEndpoint:  safeUpstreamURL(redactOpsAlertEmailText(entry.InboundEndpoint)),
			UpstreamEndpoint: safeUpstreamURL(redactOpsAlertEmailText(entry.UpstreamEndpoint)),
			RequestedModel:   redactOpsAlertEmailText(entry.RequestedModel),
			UpstreamModel:    redactOpsAlertEmailText(entry.UpstreamModel),
		},
		ErrorBody:          redactOpsAlertEmailText(entry.ErrorBody),
		UpstreamStatusCode: cloneIntPointer(entry.UpstreamStatusCode),
	}
	if entry.UpstreamErrorMessage != nil {
		detail.UpstreamErrorMessage = redactOpsAlertEmailText(*entry.UpstreamErrorMessage)
	}
	if entry.UpstreamErrorDetail != nil {
		detail.UpstreamErrorDetail = redactOpsAlertEmailText(*entry.UpstreamErrorDetail)
	}
	for _, attempt := range sanitizedAttempts {
		if attempt == nil || attempt.AccountID <= 0 {
			continue
		}
		if detail.AccountID != nil && *detail.AccountID == attempt.AccountID && detail.AccountName == "" {
			detail.AccountName = attempt.AccountName
		}
	}
	return &opsAlertErrorSample{Detail: detail, Attempts: sanitizedAttempts}
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (s *OpsAlertEvaluatorService) collectAlertErrorSamples(ctx context.Context, rule *OpsAlertRule, event *OpsAlertEvent) []*opsAlertErrorSample {
	if s == nil || s.opsRepo == nil || rule == nil || event == nil || !opsAlertMetricUsesErrorSamples(rule.MetricType) {
		return nil
	}
	end := event.FiredAt.UTC()
	if end.IsZero() {
		end = time.Now().UTC()
	}
	windowMinutes := rule.WindowMinutes
	if windowMinutes <= 0 {
		windowMinutes = 1
	}
	start := end.Add(-time.Duration(windowMinutes) * time.Minute)
	platform, groupID, _ := parseOpsAlertRuleScope(rule.Filters)
	filter := &OpsErrorLogFilter{
		StartTime:          &start,
		EndTime:            &end,
		Platform:           platform,
		GroupID:            groupID,
		ExcludeCountTokens: true,
		View:               "all",
		Page:               1,
		PageSize:           opsAlertEmailSampleLimit,
	}
	if strings.TrimSpace(rule.MetricType) == "upstream_error_rate" {
		filter.Owner = "provider"
	}
	list, err := s.opsRepo.ListErrorLogs(ctx, filter)
	if err != nil || list == nil {
		return nil
	}

	samples := make([]*opsAlertErrorSample, 0, len(list.Errors))
	for _, item := range list.Errors {
		if item == nil || item.ID <= 0 {
			continue
		}
		detail, detailErr := s.opsRepo.GetErrorLogByID(ctx, item.ID)
		if detailErr != nil || detail == nil {
			continue
		}
		attempts, _ := ParseOpsUpstreamErrors(detail.UpstreamErrors)
		samples = append(samples, &opsAlertErrorSample{Detail: detail, Attempts: attempts})
	}
	return samples
}

func opsAlertMetricUsesErrorSamples(metric string) bool {
	switch strings.TrimSpace(metric) {
	case "success_rate", "error_rate", "upstream_error_rate", "account_error_count", "account_error_ratio":
		return true
	default:
		return false
	}
}

func buildOpsAlertDetailHTML(accountDetails []*OpsAlertAccountDetail, samples []*opsAlertErrorSample) string {
	var out strings.Builder
	out.WriteString(buildOpsAlertAccountDetailHTML(accountDetails, 0))
	if len(samples) == 0 {
		return out.String()
	}
	out.WriteString(`<div style="margin-top:16px"><div style="font-weight:700;margin-bottom:8px">本次异常请求明细（已脱敏）</div>`)
	for index, sample := range samples {
		if sample == nil || sample.Detail == nil {
			continue
		}
		detail := sample.Detail
		out.WriteString(`<div style="margin:10px 0;padding:12px;border:1px solid #e5e7eb;border-radius:8px;background:#fafafa">`)
		heading := fmt.Sprintf("错误 %d", index+1)
		if detail.ID > 0 {
			heading += fmt.Sprintf(" · 日志 ID %d", detail.ID)
		}
		out.WriteString(`<div style="font-weight:700">` + html.EscapeString(heading) + `</div>`)
		writeOpsAlertEmailLine(&out, "发生时间", detail.CreatedAt.In(beijingLocation()).Format("2006-01-02 15:04:05 MST"))
		writeOpsAlertEmailLine(&out, "请求目标", opsAlertSampleTarget(detail, sample.Attempts))
		writeOpsAlertEmailLine(&out, "本站入口", opsFirstNonEmpty(detail.InboundEndpoint, detail.RequestPath, "-"))
		writeOpsAlertEmailLine(&out, "异常账号", opsAlertSampleAccount(detail, sample.Attempts))
		writeOpsAlertEmailLine(&out, "平台 / 分组", opsAlertSampleScope(detail))
		writeOpsAlertEmailLine(&out, "请求模型 / 上游模型", opsAlertSampleModels(detail))
		writeOpsAlertEmailLine(&out, "错误分类", fmt.Sprintf("阶段：%s；类型：%s；归属：%s；来源：%s", opsAccountRequestPhaseLabel(detail.Phase), opsAlertErrorTypeLabel(detail.Type), opsAlertErrorOwnerLabel(detail.Owner), opsAlertErrorSourceLabel(detail.Source)))
		writeOpsAlertEmailLine(&out, "状态码", opsAlertSampleStatusCodes(detail))
		writeOpsAlertEmailLine(&out, "请求 ID", opsAlertSampleRequestIDs(detail))
		writeOpsAlertEmailPre(&out, "错误信息", joinOpsAlertErrorText(detail))
		if len(sample.Attempts) > 0 {
			writeOpsAlertEmailPre(&out, "上游尝试", formatOpsAlertAttempts(sample.Attempts))
		}
		out.WriteString(`</div>`)
	}
	out.WriteString(`<div style="margin-top:8px;color:#6b7280;font-size:12px">邮件仅包含已脱敏信息，不包含 Key、Token、Cookie、密码、请求头或账号备注。</div></div>`)
	return out.String()
}

func writeOpsAlertEmailLine(out *strings.Builder, label string, value string) {
	out.WriteString(`<div style="margin-top:6px"><strong>`)
	out.WriteString(html.EscapeString(label))
	out.WriteString(`：</strong>`)
	out.WriteString(html.EscapeString(valueOrDash(redactOpsAlertEmailText(value))))
	out.WriteString(`</div>`)
}

func writeOpsAlertEmailPre(out *strings.Builder, label string, value string) {
	out.WriteString(`<div style="margin-top:8px"><strong>`)
	out.WriteString(html.EscapeString(label))
	out.WriteString(`：</strong><pre style="white-space:pre-wrap;word-break:break-word;margin:4px 0 0;padding:8px;background:#f3f4f6;border-radius:6px;font-family:ui-monospace,monospace;font-size:12px">`)
	out.WriteString(html.EscapeString(valueOrDash(redactOpsAlertEmailText(value))))
	out.WriteString(`</pre></div>`)
}

func redactOpsAlertEmailText(value string) string {
	return strings.TrimSpace(logredact.RedactText(value, "token", "key", "headers", "request_headers", "account_note"))
}

func joinOpsAlertErrorText(detail *OpsErrorLogDetail) string {
	if detail == nil {
		return "-"
	}
	parts := uniqueNonEmptyStrings(
		detail.Message,
		detail.UpstreamErrorMessage,
		detail.UpstreamErrorDetail,
		detail.ErrorBody,
	)
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "\n\n")
}

func formatOpsAlertAttempts(attempts []*OpsUpstreamErrorEvent) string {
	lines := make([]string, 0, len(attempts))
	for index, attempt := range attempts {
		if attempt == nil {
			continue
		}
		parts := []string{fmt.Sprintf("尝试 %d", index+1)}
		if target := strings.TrimSpace(attempt.UpstreamURL); target != "" {
			parts = append(parts, "目标 "+target)
		}
		if attempt.AccountID > 0 {
			parts = append(parts, fmt.Sprintf("账号 %s（ID %d）", valueOrDash(attempt.AccountName), attempt.AccountID))
		}
		if attempt.UpstreamStatusCode != 0 {
			parts = append(parts, fmt.Sprintf("状态码 %d", attempt.UpstreamStatusCode))
		}
		if attempt.UpstreamRequestID != "" {
			parts = append(parts, "上游请求 ID "+attempt.UpstreamRequestID)
		}
		if attempt.Kind != "" {
			parts = append(parts, "类型 "+opsAlertAttemptKindLabel(attempt.Kind))
		}
		if attempt.Stage != "" {
			parts = append(parts, "阶段 "+opsAccountRequestPhaseLabel(attempt.Stage))
		}
		if attempt.Reason != "" {
			parts = append(parts, "原因 "+opsAlertAttemptReasonLabel(attempt.Reason))
		}
		if attempt.Message != "" {
			parts = append(parts, "信息 "+attempt.Message)
		}
		if attempt.Detail != "" {
			parts = append(parts, "详情 "+attempt.Detail)
		}
		if attempt.UpstreamResponseBody != "" {
			parts = append(parts, "响应 "+attempt.UpstreamResponseBody)
		}
		lines = append(lines, strings.Join(parts, "；"))
	}
	if len(lines) == 0 {
		return "-"
	}
	return strings.Join(lines, "\n")
}

func opsAlertSampleStatusCodes(detail *OpsErrorLogDetail) string {
	if detail == nil {
		return "-"
	}
	parts := []string{fmt.Sprintf("本站 %d", detail.StatusCode)}
	if detail.UpstreamStatusCode != nil && *detail.UpstreamStatusCode > 0 {
		parts = append(parts, fmt.Sprintf("上游 %d", *detail.UpstreamStatusCode))
	}
	return strings.Join(parts, "；")
}

func opsAlertErrorTypeLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "api_error":
		return "API 请求错误"
	case "upstream_error":
		return "上游服务错误"
	case "invalid_request_error":
		return "请求参数错误"
	case "image_generation_user_error":
		return "生图请求错误"
	case "authentication_error":
		return "认证错误"
	case "rate_limit_error":
		return "请求限流"
	case "network_error":
		return "网络连接错误"
	default:
		return valueOrDash(redactOpsAlertEmailText(value))
	}
}

func opsAlertErrorOwnerLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "provider":
		return "上游服务商"
	case "platform":
		return "本站平台"
	case "client":
		return "用户客户端"
	default:
		return valueOrDash(redactOpsAlertEmailText(value))
	}
}

func opsAlertErrorSourceLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gateway":
		return "本站网关"
	case "upstream_http":
		return "上游 HTTP 接口"
	case "client_request":
		return "用户请求"
	default:
		return valueOrDash(redactOpsAlertEmailText(value))
	}
}

func opsAlertAttemptKindLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "http_error":
		return "上游 HTTP 错误"
	case "request_error":
		return "上游连接错误"
	case "credential_failover":
		return "凭据切换"
	case "failover", "failover_on_400":
		return "账号切换"
	case "retry", "signature_retry", "signature_retry_thinking":
		return "重试"
	case "retry_exhausted", "retry_exhausted_failover":
		return "重试耗尽"
	case "first_output_timeout":
		return "首个响应超时"
	case "stream_error":
		return "流式响应错误"
	case "ws_error":
		return "WebSocket 连接错误"
	case "signature_error":
		return "签名错误"
	case "budget_constraint_error":
		return "额度约束错误"
	default:
		return valueOrDash(redactOpsAlertEmailText(value))
	}
}

func opsAlertAttemptReasonLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "billing_forbidden":
		return "上游计费不可用"
	case "billing_free_tier":
		return "上游免费额度"
	case "billing_inconclusive", "billing_unobserved":
		return "暂时无法确认上游余额"
	case "grok credentials unauthorized":
		return "Grok 账号凭据无效"
	case "grok access or entitlement denied":
		return "Grok 账号无访问权限"
	case "grok upstream temporary error":
		return "Grok 上游临时故障"
	case "missing refresh token":
		return "缺少刷新令牌"
	case "proxy_unavailable":
		return "代理不可用"
	case "maintenance":
		return "服务维护中"
	case "disabled":
		return "账号已禁用"
	default:
		return valueOrDash(redactOpsAlertEmailText(value))
	}
}

func buildOpsAlertEmailMetadata(accountDetails []*OpsAlertAccountDetail, samples []*opsAlertErrorSample) opsAlertEmailMetadata {
	errorIDs := make([]int64, 0, len(samples))
	targets := make([]string, 0, len(samples))
	accounts := make([]string, 0, len(accountDetails)+len(samples))
	for _, detail := range accountDetails {
		if detail == nil {
			continue
		}
		accounts = append(accounts, fmt.Sprintf("%s（ID %d）", valueOrDash(detail.AccountName), detail.AccountID))
	}
	for _, sample := range samples {
		if sample == nil || sample.Detail == nil {
			continue
		}
		errorIDs = append(errorIDs, sample.Detail.ID)
		targets = append(targets, opsAlertSampleTarget(sample.Detail, sample.Attempts))
		accounts = append(accounts, opsAlertSampleAccount(sample.Detail, sample.Attempts))
	}
	return opsAlertEmailMetadata{
		ErrorIDs:       uniqueInt64s(errorIDs),
		TargetSite:     strings.Join(uniqueNonEmptyStrings(targets...), "；"),
		AccountSummary: strings.Join(uniqueNonEmptyStrings(accounts...), "；"),
	}
}

func opsAlertSampleTarget(detail *OpsErrorLogDetail, attempts []*OpsUpstreamErrorEvent) string {
	targets := make([]string, 0, len(attempts)+1)
	for _, attempt := range attempts {
		if attempt == nil {
			continue
		}
		if target := normalizedOpsAlertTarget(attempt.UpstreamURL); target != "" {
			targets = append(targets, target)
		}
	}
	if detail != nil {
		if target := normalizedOpsAlertTarget(detail.UpstreamEndpoint); target != "" {
			targets = append(targets, target)
		}
		if len(targets) == 0 && detail.Platform != "" {
			targets = append(targets, detail.Platform+"（未记录上游域名）")
		}
	}
	return strings.Join(uniqueNonEmptyStrings(targets...), "；")
}

func normalizedOpsAlertTarget(raw string) string {
	raw = safeUpstreamURL(redactOpsAlertEmailText(raw))
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		return parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
	}
	return safeUpstreamURL(raw)
}

func opsAlertSampleAccount(detail *OpsErrorLogDetail, attempts []*OpsUpstreamErrorEvent) string {
	accounts := make([]string, 0, len(attempts)+1)
	for _, attempt := range attempts {
		if attempt != nil && attempt.AccountID > 0 {
			accounts = append(accounts, fmt.Sprintf("%s（ID %d）", valueOrDash(attempt.AccountName), attempt.AccountID))
		}
	}
	if detail != nil && detail.AccountID != nil && *detail.AccountID > 0 {
		accounts = append(accounts, fmt.Sprintf("%s（ID %d）", valueOrDash(detail.AccountName), *detail.AccountID))
	}
	if len(accounts) == 0 {
		return "未选出上游账号"
	}
	return strings.Join(uniqueNonEmptyStrings(accounts...), "；")
}

func opsAlertSampleScope(detail *OpsErrorLogDetail) string {
	if detail == nil {
		return "-"
	}
	group := valueOrDash(detail.GroupName)
	if detail.GroupID != nil && *detail.GroupID > 0 {
		group = fmt.Sprintf("%s（ID %d）", group, *detail.GroupID)
	}
	return fmt.Sprintf("%s / %s", valueOrDash(detail.Platform), group)
}

func opsAlertSampleModels(detail *OpsErrorLogDetail) string {
	if detail == nil {
		return "-"
	}
	requested := opsFirstNonEmpty(detail.RequestedModel, detail.Model, "-")
	upstream := opsFirstNonEmpty(detail.UpstreamModel, detail.Model, "-")
	return requested + " / " + upstream
}

func opsAlertSampleRequestIDs(detail *OpsErrorLogDetail) string {
	if detail == nil {
		return "-"
	}
	parts := make([]string, 0, 2)
	if detail.RequestID != "" {
		parts = append(parts, "服务端 "+detail.RequestID)
	}
	if detail.ClientRequestID != "" {
		parts = append(parts, "客户端 "+detail.ClientRequestID)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "；")
}

func uniqueNonEmptyStrings(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func opsFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func beijingLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}
