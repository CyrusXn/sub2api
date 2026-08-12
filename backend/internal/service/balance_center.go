package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

const (
	SettingKeyBalanceCenterEnabled             = "balance_center_enabled"
	SettingKeyBalanceCenterEventProbeEnabled   = "balance_center_event_probe_enabled"
	SettingKeyBalanceCenterEmailEnabled        = "balance_center_email_enabled"
	SettingKeyBalanceCenterLowBalanceThreshold = "balance_center_low_balance_threshold"

	BalanceCenterAlertLowBalance        = "low_balance"
	BalanceCenterAlertMultiplierChanged = "multiplier_changed"

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
	repository  BalanceCenterRepository
	settingRepo SettingRepository
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

func NewBalanceCenterService(repository BalanceCenterRepository, settingRepo SettingRepository) *BalanceCenterService {
	return &BalanceCenterService{repository: repository, settingRepo: settingRepo}
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

func EvaluateBalanceCenterAlerts(state BalanceCenterAlertState, snapshot BalanceCenterSuccessfulSnapshot, threshold float64) (BalanceCenterAlertDecision, BalanceCenterAlertState) {
	next := cloneBalanceCenterAlertState(state)
	if snapshot.ConvertedBalance != nil {
		if *snapshot.ConvertedBalance >= threshold {
			next.LowBalanceActive = false
		} else if !state.LowBalanceActive {
			value := *snapshot.ConvertedBalance
			thresholdValue := threshold
			return BalanceCenterAlertDecision{
				Type:      BalanceCenterAlertLowBalance,
				NewValue:  &value,
				Threshold: &thresholdValue,
			}, next
		}
	}

	if snapshot.RateMultiplier == nil {
		return BalanceCenterAlertDecision{}, next
	}
	if state.MultiplierBaseline == nil {
		value := *snapshot.RateMultiplier
		next.MultiplierBaseline = &value
		return BalanceCenterAlertDecision{}, next
	}
	if math.Abs(*snapshot.RateMultiplier-*state.MultiplierBaseline) <= balanceCenterFloatTolerance {
		return BalanceCenterAlertDecision{}, next
	}
	oldValue := *state.MultiplierBaseline
	newValue := *snapshot.RateMultiplier
	return BalanceCenterAlertDecision{
		Type:     BalanceCenterAlertMultiplierChanged,
		OldValue: &oldValue,
		NewValue: &newValue,
	}, next
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
