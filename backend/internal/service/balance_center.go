package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	SettingKeyBalanceCenterEnabled             = "balance_center_enabled"
	SettingKeyBalanceCenterEventProbeEnabled   = "balance_center_event_probe_enabled"
	SettingKeyBalanceCenterEmailEnabled        = "balance_center_email_enabled"
	SettingKeyBalanceCenterLowBalanceThreshold = "balance_center_low_balance_threshold"

	BalanceCenterAlertLowBalance        = "low_balance"
	BalanceCenterAlertMultiplierChanged = "multiplier_changed"
	BalanceCenterAlertDeliveryQueued    = "queued"
	BalanceCenterAlertDeliveryAccepted  = "accepted"
	BalanceCenterAlertDeliveryFailed    = "failed"

	balanceCenterDefaultLowBalanceThreshold = 5.0
	balanceCenterFloatTolerance             = 1e-9
)

type BalanceCenterSettings struct {
	Enabled             bool    `json:"enabled"`
	EventProbeEnabled   bool    `json:"event_probe_enabled"`
	EmailEnabled        bool    `json:"email_enabled"`
	LowBalanceThreshold float64 `json:"low_balance_threshold"`
}

type BalanceCenterSuccessfulSnapshot struct {
	ConvertedBalance *float64
	RateMultiplier   *float64
}

type BalanceCenterAlertState struct {
	LowBalanceActive   bool
	MultiplierBaseline *float64
}

type BalanceCenterAlertDecision struct {
	Type      string
	OldValue  *float64
	NewValue  *float64
	Threshold *float64
}

type BalanceCenterService struct {
	repository        BalanceCenterRepository
	settingRepo       SettingRepository
	emailSender       BalanceCenterEmailSender
	alertEmailOutbox  AlertEmailOutboxEnqueuer
	liandongEncryptor SecretEncryptor
	liandongClient    *http.Client
}

type BalanceCenterEmailSender interface {
	SendEmail(context.Context, string, string, string) error
}

type BalanceCenterAlertDeliveryInput struct {
	IdentityKey   string
	SiteID        int64
	AccountID     *int64
	SnapshotID    int64
	AlertType     string
	Recipient     string
	Subject       string
	OldValue      *float64
	NewValue      *float64
	Threshold     *float64
	Status        string
	FailureReason string
	AttemptedAt   time.Time
	AcceptedAt    *time.Time
}

type BalanceCenterSnapshot struct {
	ID                     int64           `json:"id"`
	SiteID                 int64           `json:"site_id"`
	AccountID              *int64          `json:"account_id,omitempty"`
	AccountName            string          `json:"account_name,omitempty"`
	LegacyKeyID            *int64          `json:"legacy_key_id,omitempty"`
	SiteName               string          `json:"site_name"`
	NormalizedDomain       string          `json:"normalized_domain"`
	BaseURL                string          `json:"base_url"`
	Source                 string          `json:"source"`
	SourceKey              string          `json:"source_key"`
	Status                 string          `json:"status"`
	Balance                *float64        `json:"balance,omitempty"`
	ConvertedBalance       *float64        `json:"converted_balance,omitempty"`
	RateMultiplier         *float64        `json:"rate_multiplier,omitempty"`
	ConversionScale        float64         `json:"conversion_scale"`
	Currency               string          `json:"currency"`
	Reason                 string          `json:"reason"`
	ProbedAt               time.Time       `json:"probed_at"`
	LastUsedAt             *time.Time      `json:"last_used_at,omitempty"`
	Payload                json.RawMessage `json:"payload,omitempty"`
	RechargeRecipients     []string        `json:"-"`
	RechargeSkip           bool            `json:"-"`
	RechargeKeyFingerprint string          `json:"-"`
}

type BalanceCenterRepository interface {
	PersistSnapshot(context.Context, *BalanceCenterSnapshot) (*BalanceCenterSnapshot, error)
}

type BalanceCenterAlertRepository interface {
	GetAlertState(context.Context, string, int64, *int64) (BalanceCenterAlertState, error)
	SaveAlertState(context.Context, string, int64, *int64, int64, BalanceCenterAlertState, *BalanceCenterAlertDecision, time.Time) error
	RecordAlertDelivery(context.Context, *BalanceCenterAlertDeliveryInput) error
}

func NewBalanceCenterService(repository BalanceCenterRepository, settingRepo SettingRepository) *BalanceCenterService {
	return &BalanceCenterService{repository: repository, settingRepo: settingRepo}
}

func ProvideBalanceCenterService(repository BalanceCenterRepository, settingRepo SettingRepository, emailService *EmailService, encryptor SecretEncryptor, alertOutbox *AlertEmailOutboxService) *BalanceCenterService {
	service := NewBalanceCenterService(repository, settingRepo)
	service.SetEmailSender(emailService)
	service.SetAlertEmailOutbox(alertOutbox)
	service.SetLiandongDependencies(encryptor, nil)
	return service
}

func (s *BalanceCenterService) SetEmailSender(sender BalanceCenterEmailSender) {
	if s != nil {
		s.emailSender = sender
	}
}

// SetAlertEmailOutbox 将告警投递切换为持久化汇总队列，探测协程不再直接等待 SMTP。
func (s *BalanceCenterService) SetAlertEmailOutbox(outbox AlertEmailOutboxEnqueuer) {
	if s != nil {
		s.alertEmailOutbox = outbox
	}
}

func (s *BalanceCenterService) PersistSnapshot(ctx context.Context, snapshot *BalanceCenterSnapshot) (*BalanceCenterSnapshot, error) {
	if s == nil || s.repository == nil {
		return nil, errors.New("余额中心仓储不可用")
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, nil
	}
	var recipientErr error
	if snapshot != nil {
		// 充值流水和通知由仓储在同一事务写入，避免入账成功但通知丢失。
		copy := *snapshot
		copy.RechargeRecipients = nil
		if settings.EmailEnabled {
			copy.RechargeRecipients, recipientErr = s.balanceCenterAlertRecipients(ctx)
			// 配置临时不可读时仍保存余额，但保留充值基线供下次重试，避免漏掉通知。
			copy.RechargeSkip = recipientErr != nil
		}
		snapshot = &copy
	}
	persisted, err := s.repository.PersistSnapshot(ctx, snapshot)
	if err != nil || persisted == nil {
		return persisted, err
	}
	if recipientErr != nil {
		return persisted, recipientErr
	}
	// 倍率失败不应阻断独立成功的余额告警；完全没有成功余额的失败快照仍只做留痕。
	if persisted.Status != "ok" && persisted.ConvertedBalance == nil {
		return persisted, nil
	}
	alertRepo, ok := s.repository.(BalanceCenterAlertRepository)
	if !ok {
		return persisted, errors.New("余额告警仓储不可用")
	}
	identityKey := balanceCenterAlertIdentityKey(persisted)
	state, err := alertRepo.GetAlertState(ctx, identityKey, persisted.SiteID, persisted.AccountID)
	if err != nil {
		return persisted, fmt.Errorf("读取余额告警状态失败: %w", err)
	}
	decisions, next := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: persisted.ConvertedBalance,
		RateMultiplier:   persisted.RateMultiplier,
	}, settings.LowBalanceThreshold)
	if err := alertRepo.SaveAlertState(ctx, identityKey, persisted.SiteID, persisted.AccountID, persisted.ID, next, nil, persisted.ProbedAt); err != nil {
		return persisted, fmt.Errorf("保存余额告警基线失败: %w", err)
	}
	if len(decisions) == 0 || !settings.EmailEnabled || (s.alertEmailOutbox == nil && s.emailSender == nil) {
		return persisted, nil
	}
	recipients, err := s.balanceCenterAlertRecipients(ctx)
	if err != nil {
		return persisted, err
	}
	if len(recipients) == 0 {
		return persisted, nil
	}

	current := next
	var sendErrors []error
	for i := range decisions {
		decision := decisions[i]
		accepted := true
		subject, body := buildBalanceCenterAlertEmail(persisted, decision)
		for _, recipient := range recipients {
			attemptedAt := time.Now().UTC()
			delivery := &BalanceCenterAlertDeliveryInput{
				IdentityKey: identityKey, SiteID: persisted.SiteID, AccountID: persisted.AccountID,
				SnapshotID: persisted.ID, AlertType: decision.Type, Recipient: recipient, Subject: subject,
				OldValue: decision.OldValue, NewValue: decision.NewValue, Threshold: decision.Threshold,
				Status: BalanceCenterAlertDeliveryAccepted, AttemptedAt: attemptedAt,
			}
			if s.alertEmailOutbox != nil {
				delivery.Status = BalanceCenterAlertDeliveryQueued
				enqueueErr := s.alertEmailOutbox.Enqueue(ctx, &AlertEmailOutboxInput{
					SourceType: AlertEmailSourceBalanceCenter,
					SourceID:   strconv.FormatInt(persisted.ID, 10),
					SourceKey:  fmt.Sprintf("snapshot:%d:%s", persisted.ID, decision.Type),
					AlertType:  decision.Type,
					Recipient:  recipient,
					Subject:    subject,
					BodyHTML:   body,
					CreatedAt:  attemptedAt,
				})
				if enqueueErr != nil {
					accepted = false
					delivery.Status = BalanceCenterAlertDeliveryFailed
					delivery.FailureReason = "告警邮件入队失败"
					sendErrors = append(sendErrors, fmt.Errorf("写入%s告警邮件队列失败: %w", balanceCenterAlertTypeName(decision.Type), enqueueErr))
				}
			} else if sendErr := s.emailSender.SendEmail(ctx, recipient, subject, body); sendErr != nil {
				accepted = false
				delivery.Status = BalanceCenterAlertDeliveryFailed
				delivery.FailureReason = "SMTP 未接受"
				sendErrors = append(sendErrors, fmt.Errorf("发送余额告警邮件失败: %w", sendErr))
			} else {
				delivery.AcceptedAt = &attemptedAt
			}
			if auditErr := alertRepo.RecordAlertDelivery(ctx, delivery); auditErr != nil {
				sendErrors = append(sendErrors, fmt.Errorf("记录余额告警投递失败: %w", auditErr))
			}
		}
		if accepted {
			current = AcceptBalanceCenterAlert(current, decision)
			if saveErr := alertRepo.SaveAlertState(ctx, identityKey, persisted.SiteID, persisted.AccountID, persisted.ID, current, &decision, persisted.ProbedAt); saveErr != nil {
				sendErrors = append(sendErrors, fmt.Errorf("推进余额告警状态失败: %w", saveErr))
			}
		}
	}
	return persisted, errors.Join(sendErrors...)
}

func balanceCenterAlertTypeName(alertType string) string {
	if alertType == BalanceCenterAlertMultiplierChanged {
		return "倍率变化"
	}
	return "余额不足"
}

func (s *BalanceCenterService) balanceCenterAlertRecipients(ctx context.Context) ([]string, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsEmailNotificationConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取余额告警收件人失败: %w", err)
	}
	var config OpsEmailNotificationConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, errors.New("余额告警收件人配置无效")
	}
	seen := make(map[string]struct{})
	result := make([]string, 0, len(config.Alert.Recipients))
	for _, item := range config.Alert.Recipients {
		address := strings.TrimSpace(strings.ToLower(item))
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	sort.Strings(result)
	return result, nil
}

func balanceCenterAlertIdentityKey(snapshot *BalanceCenterSnapshot) string {
	if snapshot.AccountID != nil {
		return fmt.Sprintf("account:%d", *snapshot.AccountID)
	}
	return fmt.Sprintf("site:%d", snapshot.SiteID)
}

func buildBalanceCenterAlertEmail(snapshot *BalanceCenterSnapshot, decision BalanceCenterAlertDecision) (string, string) {
	value := func(input *float64) string {
		if input == nil {
			return "-"
		}
		return strconv.FormatFloat(*input, 'f', -1, 64)
	}
	siteName := strings.TrimSpace(snapshot.SiteName)
	if decision.Type == BalanceCenterAlertLowBalance {
		body := fmt.Sprintf("%s余额：%s元", html.EscapeString(siteName), value(snapshot.ConvertedBalance))
		if !snapshot.ProbedAt.IsZero() {
			body += fmt.Sprintf("<br>采集时间：%s（北京时间）。以上为触发提醒时的余额，当前余额请查看余额中心。", snapshot.ProbedAt.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05"))
		}
		return fmt.Sprintf("%s-余额低于%s元", siteName, value(decision.Threshold)),
			body
	}

	direction := "上涨"
	if decision.OldValue != nil && decision.NewValue != nil && *decision.NewValue < *decision.OldValue {
		direction = "下降"
	}
	accountName := balanceCenterAlertAccountLabel(snapshot)
	return fmt.Sprintf("%s-%s账号-倍率%s", siteName, accountName, direction),
		fmt.Sprintf("%s %s账号倍率%s，%s->%s", html.EscapeString(siteName), html.EscapeString(accountName), direction, value(decision.OldValue), value(decision.NewValue))
}

func balanceCenterAlertAccountLabel(snapshot *BalanceCenterSnapshot) string {
	accountName := strings.TrimSpace(snapshot.AccountName)
	if siteName := strings.TrimSpace(snapshot.SiteName); siteName != "" {
		accountName = strings.TrimSpace(strings.TrimPrefix(accountName, "【"+siteName+"】"))
	}
	if accountName != "" {
		return accountName
	}
	return balanceCenterAccountLabel(snapshot.AccountID)
}

func balanceCenterAccountLabel(accountID *int64) string {
	if accountID == nil {
		return "历史账号"
	}
	return strconv.FormatInt(*accountID, 10)
}

func (s *BalanceCenterService) GetSettings(ctx context.Context) (*BalanceCenterSettings, error) {
	settings := &BalanceCenterSettings{LowBalanceThreshold: balanceCenterDefaultLowBalanceThreshold}
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyBalanceCenterEnabled,
		SettingKeyBalanceCenterEventProbeEnabled,
		SettingKeyBalanceCenterEmailEnabled,
		SettingKeyBalanceCenterLowBalanceThreshold,
	})
	if err != nil {
		return nil, fmt.Errorf("读取余额中心设置失败: %w", err)
	}
	settings.Enabled = parseBalanceCenterBool(values[SettingKeyBalanceCenterEnabled])
	settings.EventProbeEnabled = parseBalanceCenterBool(values[SettingKeyBalanceCenterEventProbeEnabled])
	settings.EmailEnabled = parseBalanceCenterBool(values[SettingKeyBalanceCenterEmailEnabled])
	// 页面只暴露邮件开关；开启邮件即需要采集，兼容历史隐藏总开关关闭的配置。
	settings.Enabled = settings.Enabled || settings.EmailEnabled
	settings.EventProbeEnabled = settings.EventProbeEnabled || settings.EmailEnabled
	if value, parseErr := strconv.ParseFloat(values[SettingKeyBalanceCenterLowBalanceThreshold], 64); parseErr == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
		settings.LowBalanceThreshold = value
	}
	return settings, nil
}

func (s *BalanceCenterService) UpdateSettings(ctx context.Context, settings *BalanceCenterSettings) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("余额中心设置仓储不可用")
	}
	if settings == nil || settings.LowBalanceThreshold < 0 || math.IsInf(settings.LowBalanceThreshold, 0) || math.IsNaN(settings.LowBalanceThreshold) {
		return errors.New("低余额阈值必须是大于或等于 0 的有限数值")
	}
	return s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyBalanceCenterEnabled:             strconv.FormatBool(settings.Enabled),
		SettingKeyBalanceCenterEventProbeEnabled:   strconv.FormatBool(settings.EventProbeEnabled),
		SettingKeyBalanceCenterEmailEnabled:        strconv.FormatBool(settings.EmailEnabled),
		SettingKeyBalanceCenterLowBalanceThreshold: strconv.FormatFloat(settings.LowBalanceThreshold, 'f', -1, 64),
	})
}

func EvaluateBalanceCenterAlerts(state BalanceCenterAlertState, snapshot BalanceCenterSuccessfulSnapshot, threshold float64) ([]BalanceCenterAlertDecision, BalanceCenterAlertState) {
	next := cloneBalanceCenterAlertState(state)
	decisions := make([]BalanceCenterAlertDecision, 0, 2)
	if snapshot.ConvertedBalance != nil {
		if *snapshot.ConvertedBalance >= threshold {
			next.LowBalanceActive = false
		} else if !state.LowBalanceActive {
			value := *snapshot.ConvertedBalance
			thresholdValue := threshold
			decisions = append(decisions, BalanceCenterAlertDecision{
				Type:      BalanceCenterAlertLowBalance,
				NewValue:  &value,
				Threshold: &thresholdValue,
			})
		}
	}

	if snapshot.RateMultiplier == nil {
		return decisions, next
	}
	if state.MultiplierBaseline == nil {
		value := *snapshot.RateMultiplier
		next.MultiplierBaseline = &value
		return decisions, next
	}
	if math.Abs(*snapshot.RateMultiplier-*state.MultiplierBaseline) <= balanceCenterFloatTolerance {
		return decisions, next
	}
	oldValue := *state.MultiplierBaseline
	newValue := *snapshot.RateMultiplier
	decisions = append(decisions, BalanceCenterAlertDecision{
		Type:     BalanceCenterAlertMultiplierChanged,
		OldValue: &oldValue,
		NewValue: &newValue,
	})
	return decisions, next
}

func AcceptBalanceCenterAlert(state BalanceCenterAlertState, decision BalanceCenterAlertDecision) BalanceCenterAlertState {
	next := cloneBalanceCenterAlertState(state)
	switch decision.Type {
	case BalanceCenterAlertLowBalance:
		next.LowBalanceActive = true
	case BalanceCenterAlertMultiplierChanged:
		if decision.NewValue != nil {
			value := *decision.NewValue
			next.MultiplierBaseline = &value
		}
	}
	return next
}

func cloneBalanceCenterAlertState(state BalanceCenterAlertState) BalanceCenterAlertState {
	next := state
	if state.MultiplierBaseline != nil {
		value := *state.MultiplierBaseline
		next.MultiplierBaseline = &value
	}
	return next
}

func parseBalanceCenterBool(value string) bool {
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}
