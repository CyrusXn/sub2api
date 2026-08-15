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
	ID               int64           `json:"id"`
	SiteID           int64           `json:"site_id"`
	AccountID        *int64          `json:"account_id,omitempty"`
	LegacyKeyID      *int64          `json:"legacy_key_id,omitempty"`
	SiteName         string          `json:"site_name"`
	NormalizedDomain string          `json:"normalized_domain"`
	BaseURL          string          `json:"base_url"`
	Source           string          `json:"source"`
	SourceKey        string          `json:"source_key"`
	Status           string          `json:"status"`
	Balance          *float64        `json:"balance,omitempty"`
	ConvertedBalance *float64        `json:"converted_balance,omitempty"`
	RateMultiplier   *float64        `json:"rate_multiplier,omitempty"`
	ConversionScale  float64         `json:"conversion_scale"`
	Currency         string          `json:"currency"`
	Reason           string          `json:"reason"`
	ProbedAt         time.Time       `json:"probed_at"`
	LastUsedAt       *time.Time      `json:"last_used_at,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
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
	persisted, err := s.repository.PersistSnapshot(ctx, snapshot)
	if err != nil || persisted == nil || persisted.Status != "ok" {
		return persisted, err
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
	title := "倍率变化告警"
	if decision.Type == BalanceCenterAlertLowBalance {
		title = "低余额告警"
	}
	value := func(input *float64) string {
		if input == nil {
			return "-"
		}
		return strconv.FormatFloat(*input, 'f', -1, 64)
	}
	subject := "[余额中心]" + title + " - " + snapshot.SiteName
	body := fmt.Sprintf(`<h2>%s</h2><p>站点：%s</p><p>账号：%s</p><p>域名：%s</p><p>原值：%s</p><p>新值：%s</p><p>原始余额：%s</p><p>折算余额：%s</p><p>阈值：%s</p><p>折算系数：%s</p><p>币种：%s</p><p>探测时间：%s</p>`,
		html.EscapeString(title), html.EscapeString(snapshot.SiteName), html.EscapeString(balanceCenterAccountLabel(snapshot.AccountID)),
		html.EscapeString(snapshot.NormalizedDomain), value(decision.OldValue), value(decision.NewValue), value(snapshot.Balance), value(snapshot.ConvertedBalance), value(decision.Threshold),
		strconv.FormatFloat(snapshot.ConversionScale, 'f', -1, 64), html.EscapeString(snapshot.Currency), snapshot.ProbedAt.UTC().Format(time.RFC3339),
	)
	return subject, body
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
