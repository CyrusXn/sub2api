package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceCenterSettingsDefaultsAndValidation(t *testing.T) {
	repo := &balanceCenterSettingRepoStub{values: map[string]string{}}
	svc := NewBalanceCenterService(nil, repo)

	settings, err := svc.GetSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.False(t, settings.EventProbeEnabled)
	require.False(t, settings.EmailEnabled)
	require.Equal(t, 5.0, settings.LowBalanceThreshold)

	err = svc.UpdateSettings(context.Background(), &BalanceCenterSettings{LowBalanceThreshold: -1})
	require.Error(t, err)
}

func TestEvaluateBalanceCenterAlertsLowBalanceLifecycle(t *testing.T) {
	state := BalanceCenterAlertState{}

	decision, next := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(4.99),
	}, 5)
	require.Equal(t, BalanceCenterAlertLowBalance, decision.Type)
	require.False(t, next.LowBalanceActive, "邮件接受前不得推进低余额通知状态")

	next = AcceptBalanceCenterAlert(next, decision)
	require.True(t, next.LowBalanceActive)

	decision, next = EvaluateBalanceCenterAlerts(next, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(4.50),
	}, 5)
	require.Empty(t, decision.Type, "持续低余额不得重复通知")
	require.True(t, next.LowBalanceActive)

	decision, next = EvaluateBalanceCenterAlerts(next, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(5),
	}, 5)
	require.Empty(t, decision.Type, "阈值边界不属于低余额")
	require.False(t, next.LowBalanceActive, "恢复后只静默重置")
}

func TestEvaluateBalanceCenterAlertsMultiplierLifecycle(t *testing.T) {
	state := BalanceCenterAlertState{}

	decision, state := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		RateMultiplier: float64Ptr(1.2),
	}, 5)
	require.Empty(t, decision.Type, "首次倍率仅建立基线")
	require.Equal(t, 1.2, *state.MultiplierBaseline)

	decision, unchanged := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		RateMultiplier: float64Ptr(1.2 + balanceCenterFloatTolerance/2),
	}, 5)
	require.Empty(t, decision.Type)
	require.Equal(t, 1.2, *unchanged.MultiplierBaseline)

	decision, unchanged = EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		RateMultiplier: float64Ptr(1.3),
	}, 5)
	require.Equal(t, BalanceCenterAlertMultiplierChanged, decision.Type)
	require.Equal(t, 1.2, *decision.OldValue)
	require.Equal(t, 1.3, *decision.NewValue)
	require.Equal(t, 1.2, *unchanged.MultiplierBaseline, "邮件接受前不得推进倍率基线")

	accepted := AcceptBalanceCenterAlert(unchanged, decision)
	require.Equal(t, 1.3, *accepted.MultiplierBaseline)
}

type balanceCenterSettingRepoStub struct {
	values map[string]string
	err    error
}

func (s *balanceCenterSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (s *balanceCenterSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *balanceCenterSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.err != nil {
		return s.err
	}
	s.values[key] = value
	return nil
}

func (s *balanceCenterSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]string)
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *balanceCenterSettingRepoStub) SetMultiple(_ context.Context, values map[string]string) error {
	if s.err != nil {
		return s.err
	}
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}

func (s *balanceCenterSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return nil, errors.New("not implemented")
}

func (s *balanceCenterSettingRepoStub) Delete(context.Context, string) error {
	return errors.New("not implemented")
}
