package service

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

func (s *OpsService) ListAlertEmailDeliveries(ctx context.Context, filter *OpsAlertEmailDeliveryFilter) (*OpsAlertEmailDeliveryList, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	repo, ok := s.opsRepo.(OpsAlertEmailDeliveryRepository)
	if !ok {
		return &OpsAlertEmailDeliveryList{Items: []*OpsAlertEmailDelivery{}, Page: 1, PageSize: 20}, nil
	}
	return repo.ListAlertEmailDeliveries(ctx, filter)
}

func (s *OpsAlertEvaluatorService) recordAlertEmailDelivery(ctx context.Context, input *OpsAlertEmailDeliveryInput) {
	if s == nil || input == nil {
		return
	}
	repo, ok := s.opsRepo.(OpsAlertEmailDeliveryRepository)
	if !ok {
		return
	}
	// 投递审计是最后一道持久化边界，失败原因必须再次脱敏。
	sanitized := *input
	sanitized.Subject = logredact.RedactText(input.Subject)
	sanitized.RuleName = logredact.RedactText(input.RuleName)
	sanitized.TargetSite = logredact.RedactText(input.TargetSite)
	sanitized.AccountSummary = logredact.RedactText(input.AccountSummary)
	sanitized.FailureReason = logredact.RedactText(input.FailureReason)
	_ = repo.InsertAlertEmailDelivery(ctx, &sanitized)
}

// recordDatabaseSilencedAlertEmails 为数据库范围静默保留逐收件人审计，不创建可见告警事件。
func (s *OpsAlertEvaluatorService) recordDatabaseSilencedAlertEmails(
	ctx context.Context,
	rule *OpsAlertRule,
	title string,
	severity string,
	targetSite string,
	accountDetails []*OpsAlertAccountDetail,
	samples []*opsAlertErrorSample,
	now time.Time,
) {
	if s == nil || s.opsService == nil || rule == nil {
		return
	}
	cfg, err := s.opsService.GetEmailNotificationConfig(ctx)
	if err != nil || cfg == nil || len(cfg.Alert.Recipients) == 0 {
		return
	}
	metadata := buildOpsAlertEmailMetadata(accountDetails, samples)
	if strings.TrimSpace(metadata.TargetSite) != "" {
		targetSite = metadata.TargetSite
	}
	detailHTML := buildOpsAlertDetailHTML(accountDetails, samples)
	dayKey := now.In(beijingLocation()).Format("20060102")
	for _, recipient := range cfg.Alert.Recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		s.recordAlertEmailDelivery(ctx, &OpsAlertEmailDeliveryInput{
			IdempotencyKey: "database-silenced:" + fmt.Sprintf("%d", rule.ID) + ":" + dayKey + ":" + notificationEmailHash(recipient),
			RecipientEmail: recipient,
			Status:         OpsAlertEmailStatusSilenced,
			Subject:        "[运维告警][" + severity + "] " + title,
			RuleName:       title,
			Severity:       severity,
			TargetSite:     targetSite,
			AccountSummary: metadata.AccountSummary,
			FailureReason:  "命中数据库范围静默规则",
			DetailHTML:     detailHTML,
			ErrorIDs:       metadata.ErrorIDs,
		})
	}
}

func isOpsAlertQuietHours(cfg OpsEmailAlertConfig, now time.Time) bool {
	if !cfg.QuietHoursEnabled {
		return false
	}
	start, startErr := time.Parse("15:04", strings.TrimSpace(cfg.QuietHoursStart))
	end, endErr := time.Parse("15:04", strings.TrimSpace(cfg.QuietHoursEnd))
	if startErr != nil || endErr != nil {
		return false
	}
	local := now.In(beijingLocation())
	currentMinute := local.Hour()*60 + local.Minute()
	startMinute := start.Hour()*60 + start.Minute()
	endMinute := end.Hour()*60 + end.Minute()
	if startMinute < endMinute {
		return currentMinute >= startMinute && currentMinute < endMinute
	}
	return currentMinute >= startMinute || currentMinute < endMinute
}

func durationUntilNextBeijingDigest(now time.Time) time.Duration {
	localNow := now.In(beijingLocation())
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 8, 0, 0, 0, localNow.Location())
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(localNow)
}

// sendQuietHoursDigestOnce 使用同一分布式锁，避免多实例在 08:00 重复发送汇总。
func (s *OpsAlertEvaluatorService) sendQuietHoursDigestOnce() {
	if s == nil || s.opsService == nil || s.opsRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), opsAlertEvaluatorTimeout)
	defer cancel()
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return
	}
	if !s.opsService.IsMonitoringEnabled(ctx) {
		return
	}
	runtimeCfg := defaultOpsAlertRuntimeSettings()
	if loaded, err := s.opsService.GetOpsAlertRuntimeSettings(ctx); err == nil && loaded != nil {
		runtimeCfg = loaded
	}
	release, ok := s.tryAcquireLeaderLock(ctx, runtimeCfg.DistributedLock)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}
	s.maybeSendQuietHoursDigest(ctx, time.Now().UTC())
}

func (s *OpsAlertEvaluatorService) maybeSendQuietHoursDigest(ctx context.Context, now time.Time) {
	if s == nil || s.emailService == nil || s.opsService == nil {
		return
	}
	repo, ok := s.opsRepo.(OpsAlertEmailDeliveryRepository)
	if !ok {
		return
	}
	cfg, err := s.opsService.GetEmailNotificationConfig(ctx)
	if err != nil || cfg == nil || !cfg.Alert.Enabled || !cfg.Alert.QuietDigestEnabled || isOpsAlertQuietHours(cfg.Alert, now) {
		return
	}
	pending, err := repo.ListPendingQuietAlertEmails(ctx, now.UTC().Add(-48*time.Hour))
	if err != nil || len(pending) == 0 {
		return
	}

	byRecipient := make(map[string][]*OpsAlertEmailDelivery)
	for _, item := range pending {
		if item == nil || strings.TrimSpace(item.RecipientEmail) == "" {
			continue
		}
		recipient := strings.TrimSpace(item.RecipientEmail)
		byRecipient[recipient] = append(byRecipient[recipient], item)
	}
	recipients := make([]string, 0, len(byRecipient))
	for recipient := range byRecipient {
		recipients = append(recipients, recipient)
	}
	sort.Strings(recipients)

	for _, recipient := range recipients {
		items := byRecipient[recipient]
		if len(items) == 0 {
			continue
		}
		subject := fmt.Sprintf("[运维告警汇总] 夜间静默告警 %d 条", len(items))
		body := buildOpsQuietDigestEmailBody(items)
		sentAt := time.Now().UTC()
		if sendErr := s.emailService.SendEmail(ctx, recipient, subject, body); sendErr != nil {
			s.recordAlertEmailDelivery(ctx, &OpsAlertEmailDeliveryInput{
				IdempotencyKey: "quiet-digest-failed:" + sentAt.Format("200601021504") + ":" + notificationEmailHash(recipient),
				RecipientEmail: recipient,
				Status:         OpsAlertEmailStatusFailed,
				Subject:        subject,
				RuleName:       "夜间静默告警汇总",
				FailureReason:  truncateString(sendErr.Error(), 1000),
				IsDigest:       true,
			})
			continue
		}

		ids := make([]int64, 0, len(items))
		errorIDs := make([]int64, 0)
		for _, item := range items {
			ids = append(ids, item.ID)
			errorIDs = append(errorIDs, item.ErrorIDs...)
		}
		if err := repo.MarkQuietAlertEmailsDigested(ctx, ids, sentAt); err != nil {
			continue
		}
		for _, item := range items {
			if item != nil && item.AlertEventID != nil && *item.AlertEventID > 0 {
				_ = s.opsRepo.UpdateAlertEventEmailSent(ctx, *item.AlertEventID, true)
			}
		}
		s.recordAlertEmailDelivery(ctx, &OpsAlertEmailDeliveryInput{
			IdempotencyKey: "quiet-digest-sent:" + sentAt.In(beijingLocation()).Format("20060102") + ":" + notificationEmailHash(recipient),
			RecipientEmail: recipient,
			Status:         OpsAlertEmailStatusSent,
			Subject:        subject,
			RuleName:       "夜间静默告警汇总",
			ErrorIDs:       uniqueInt64s(errorIDs),
			IsDigest:       true,
			SentAt:         &sentAt,
		})
	}
}

func buildOpsQuietDigestEmailBody(items []*OpsAlertEmailDelivery) string {
	var body strings.Builder
	body.WriteString(`<html lang="zh-CN"><body style="font-family:Arial,sans-serif;color:#111827"><h2>夜间静默告警汇总</h2>`)
	body.WriteString(`<p>以下告警在北京时间夜间静默时段内触发，未即时发送。</p>`)
	body.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:13px"><thead><tr>`)
	for _, title := range []string{"时间", "级别", "规则", "目标站点", "异常账号", "说明"} {
		body.WriteString(`<th style="text-align:left;padding:8px;border:1px solid #e5e7eb;background:#f3f4f6">` + html.EscapeString(title) + `</th>`)
	}
	body.WriteString(`</tr></thead><tbody>`)
	for _, item := range items {
		if item == nil {
			continue
		}
		body.WriteString(`<tr>`)
		values := []string{
			item.CreatedAt.In(beijingLocation()).Format("01-02 15:04"),
			item.Severity,
			item.RuleName,
			item.TargetSite,
			item.AccountSummary,
			item.FailureReason,
		}
		for _, value := range values {
			body.WriteString(`<td style="vertical-align:top;padding:8px;border:1px solid #e5e7eb;word-break:break-word">` + html.EscapeString(valueOrDash(value)) + `</td>`)
		}
		body.WriteString(`</tr>`)
		if strings.TrimSpace(item.DetailHTML) != "" {
			body.WriteString(`<tr><td colspan="6" style="padding:10px;border:1px solid #e5e7eb;background:#fafafa">` + item.DetailHTML + `</td></tr>`)
		}
	}
	body.WriteString(`</tbody></table>`)
	body.WriteString(`<p style="color:#6b7280;font-size:12px">本邮件不包含 Key、Token、Cookie、密码或未脱敏原始响应。</p></body></html>`)
	return body.String()
}
