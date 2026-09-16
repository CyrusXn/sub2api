package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceCenterServiceRepositoryStub struct {
	mu         sync.Mutex
	snapshotID int64
	state      BalanceCenterAlertState
	deliveries []*BalanceCenterAlertDeliveryInput
}

func (r *balanceCenterServiceRepositoryStub) persistedCount() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotID
}

func (r *balanceCenterServiceRepositoryStub) PersistSnapshot(_ context.Context, snapshot *BalanceCenterSnapshot) (*BalanceCenterSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshotID++
	clone := *snapshot
	clone.ID = r.snapshotID
	clone.SiteID = 9
	return &clone, nil
}

func (r *balanceCenterServiceRepositoryStub) GetAlertState(context.Context, string, int64, *int64) (BalanceCenterAlertState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneBalanceCenterAlertState(r.state), nil
}

func (r *balanceCenterServiceRepositoryStub) SaveAlertState(_ context.Context, _ string, _ int64, _ *int64, _ int64, state BalanceCenterAlertState, _ *BalanceCenterAlertDecision, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = cloneBalanceCenterAlertState(state)
	return nil
}

func (r *balanceCenterServiceRepositoryStub) RecordAlertDelivery(_ context.Context, input *BalanceCenterAlertDeliveryInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *input
	r.deliveries = append(r.deliveries, &clone)
	return nil
}

type balanceCenterEmailSenderStub struct {
	mu      sync.Mutex
	sends   []string
	failFor map[string]error
}

type balanceCenterAlertOutboxStub struct {
	inputs []*AlertEmailOutboxInput
	err    error
}

func (s *balanceCenterAlertOutboxStub) Enqueue(_ context.Context, input *AlertEmailOutboxInput) error {
	if s.err != nil {
		return s.err
	}
	clone := *input
	s.inputs = append(s.inputs, &clone)
	return nil
}

func (s *balanceCenterEmailSenderStub) SendEmail(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, to+"|"+subject+"|"+body)
	return s.failFor[to]
}

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

func TestBalanceCenterRecipientFailureStillPersistsSnapshot(t *testing.T) {
	repo := &balanceCenterServiceRepositoryStub{}
	svc := NewBalanceCenterService(repo, &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEmailEnabled:  "true",
		SettingKeyOpsEmailNotificationConfig: "invalid-json",
	}})
	persisted, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{ConvertedBalance: float64Ptr(9)})
	require.ErrorContains(t, err, "收件人配置无效")
	require.NotNil(t, persisted)
	require.Equal(t, int64(1), repo.persistedCount())
	require.True(t, persisted.RechargeSkip, "保留充值基线，等收件人配置恢复后重试")
}

func TestEvaluateBalanceCenterAlertsLowBalanceLifecycle(t *testing.T) {
	state := BalanceCenterAlertState{}

	decisions, next := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(4.99),
	}, 5)
	require.Len(t, decisions, 1)
	decision := decisions[0]
	require.Equal(t, BalanceCenterAlertLowBalance, decision.Type)
	require.False(t, next.LowBalanceActive, "邮件接受前不得推进低余额通知状态")

	next = AcceptBalanceCenterAlert(next, decision)
	require.True(t, next.LowBalanceActive)

	decisions, next = EvaluateBalanceCenterAlerts(next, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(4.50),
	}, 5)
	require.Empty(t, decisions, "持续低余额不得重复通知")
	require.True(t, next.LowBalanceActive)

	decisions, next = EvaluateBalanceCenterAlerts(next, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(5),
	}, 5)
	require.Empty(t, decisions, "阈值边界不属于低余额")
	require.False(t, next.LowBalanceActive, "恢复后只静默重置")
}

func TestEvaluateBalanceCenterAlertsMultiplierLifecycle(t *testing.T) {
	state := BalanceCenterAlertState{}

	decisions, state := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		RateMultiplier: float64Ptr(1.2),
	}, 5)
	require.Empty(t, decisions, "首次倍率仅建立基线")
	require.Equal(t, 1.2, *state.MultiplierBaseline)

	decisions, unchanged := EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		RateMultiplier: float64Ptr(1.2 + balanceCenterFloatTolerance/2),
	}, 5)
	require.Empty(t, decisions)
	require.Equal(t, 1.2, *unchanged.MultiplierBaseline)

	decisions, unchanged = EvaluateBalanceCenterAlerts(state, BalanceCenterSuccessfulSnapshot{
		RateMultiplier: float64Ptr(1.3),
	}, 5)
	require.Len(t, decisions, 1)
	decision := decisions[0]
	require.Equal(t, BalanceCenterAlertMultiplierChanged, decision.Type)
	require.Equal(t, 1.2, *decision.OldValue)
	require.Equal(t, 1.3, *decision.NewValue)
	require.Equal(t, 1.2, *unchanged.MultiplierBaseline, "邮件接受前不得推进倍率基线")

	accepted := AcceptBalanceCenterAlert(unchanged, decision)
	require.Equal(t, 1.3, *accepted.MultiplierBaseline)
}

func TestEvaluateBalanceCenterAlertsReturnsLowBalanceAndMultiplierChangeTogether(t *testing.T) {
	baseline := 0.1
	decisions, next := EvaluateBalanceCenterAlerts(BalanceCenterAlertState{MultiplierBaseline: &baseline}, BalanceCenterSuccessfulSnapshot{
		ConvertedBalance: float64Ptr(4.99),
		RateMultiplier:   float64Ptr(0.2),
	}, 5)

	require.Len(t, decisions, 2)
	require.Equal(t, BalanceCenterAlertLowBalance, decisions[0].Type)
	require.Equal(t, BalanceCenterAlertMultiplierChanged, decisions[1].Type)
	require.False(t, next.LowBalanceActive)
	require.Equal(t, 0.1, *next.MultiplierBaseline, "邮件接受前不得推进任何通知基线")
}

func TestBuildBalanceCenterAlertEmailUsesDynamicMultiplierDirection(t *testing.T) {
	accountID := int64(935)
	snapshot := &BalanceCenterSnapshot{
		SiteName:    "派大星",
		AccountName: "【派大星】heavy",
		AccountID:   &accountID,
	}

	tests := []struct {
		name        string
		oldValue    float64
		newValue    float64
		wantSubject string
		wantBody    string
	}{
		{
			name:        "倍率上涨",
			oldValue:    0.01,
			newValue:    0.02,
			wantSubject: "派大星-heavy账号-倍率上涨",
			wantBody:    "派大星 heavy账号倍率上涨，0.01->0.02",
		},
		{
			name:        "倍率下降",
			oldValue:    0.02,
			newValue:    0.01,
			wantSubject: "派大星-heavy账号-倍率下降",
			wantBody:    "派大星 heavy账号倍率下降，0.02->0.01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body := buildBalanceCenterAlertEmail(snapshot, BalanceCenterAlertDecision{
				Type:     BalanceCenterAlertMultiplierChanged,
				OldValue: float64Ptr(tt.oldValue),
				NewValue: float64Ptr(tt.newValue),
			})

			require.Equal(t, tt.wantSubject, subject)
			require.Equal(t, tt.wantBody, body)
		})
	}
}

func TestBuildBalanceCenterAlertEmailUsesDynamicLowBalanceValues(t *testing.T) {
	snapshot := &BalanceCenterSnapshot{
		SiteName:         "派大星",
		ConvertedBalance: float64Ptr(3.25),
		ProbedAt:         time.Date(2026, 9, 16, 1, 2, 3, 0, time.UTC),
	}

	subject, body := buildBalanceCenterAlertEmail(snapshot, BalanceCenterAlertDecision{
		Type:      BalanceCenterAlertLowBalance,
		NewValue:  float64Ptr(3.25),
		Threshold: float64Ptr(5),
	})

	require.Equal(t, "派大星-余额低于5元", subject)
	require.Contains(t, body, "派大星余额：3.25元")
	require.Contains(t, body, "2026-09-16 09:02:03（北京时间）")
	require.Contains(t, body, "触发提醒时的余额")
}

func TestBuildBalanceCenterAlertEmailFallsBackToAccountID(t *testing.T) {
	accountID := int64(935)
	snapshot := &BalanceCenterSnapshot{SiteName: "派大星", AccountID: &accountID}

	subject, body := buildBalanceCenterAlertEmail(snapshot, BalanceCenterAlertDecision{
		Type:     BalanceCenterAlertMultiplierChanged,
		OldValue: float64Ptr(0.01),
		NewValue: float64Ptr(0.02),
	})

	require.Equal(t, "派大星-935账号-倍率上涨", subject)
	require.Equal(t, "派大星 935账号倍率上涨，0.01->0.02", body)
}

func TestBalanceCenterServiceProcessesAlertsAndRetriesFailedSMTP(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:             "true",
		SettingKeyBalanceCenterEmailEnabled:        "true",
		SettingKeyBalanceCenterLowBalanceThreshold: "5",
		SettingKeyOpsEmailNotificationConfig:       `{"alert":{"recipients":["ops@example.com"]}}`,
	}}
	repo := &balanceCenterServiceRepositoryStub{}
	email := &balanceCenterEmailSenderStub{}
	svc := NewBalanceCenterService(repo, settings)
	svc.SetEmailSender(email)
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	accountID := int64(17)

	_, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		AccountID:        &accountID,
		SiteName:         "HBY",
		NormalizedDomain: "hubway.cc",
		Source:           "sub2api_probe",
		SourceKey:        "first",
		Status:           "ok",
		ConvertedBalance: float64Ptr(4),
		RateMultiplier:   float64Ptr(0.1),
		ConversionScale:  0.1,
		Currency:         "USD",
		ProbedAt:         now,
	})
	require.NoError(t, err)
	require.Len(t, email.sends, 1, "首次倍率只建基线，低余额立即发送")
	require.Contains(t, email.sends[0], "HBY-余额低于5元|HBY余额：4元")
	require.True(t, repo.state.LowBalanceActive)
	require.Equal(t, 0.1, *repo.state.MultiplierBaseline)
	require.Equal(t, BalanceCenterAlertDeliveryAccepted, repo.deliveries[0].Status)

	email.failFor = map[string]error{"ops@example.com": errors.New("smtp timeout")}
	_, err = svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		AccountID:        &accountID,
		SiteName:         "HBY",
		NormalizedDomain: "hubway.cc",
		Source:           "sub2api_probe",
		SourceKey:        "second",
		Status:           "ok",
		ConvertedBalance: float64Ptr(4),
		RateMultiplier:   float64Ptr(0.2),
		ConversionScale:  0.1,
		Currency:         "USD",
		ProbedAt:         now.Add(time.Minute),
	})
	require.Error(t, err)
	require.Equal(t, 0.1, *repo.state.MultiplierBaseline, "SMTP 未接受不得推进倍率基线")
	require.Equal(t, BalanceCenterAlertDeliveryFailed, repo.deliveries[1].Status)

	email.failFor = nil
	_, err = svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		AccountID:        &accountID,
		SiteName:         "HBY",
		NormalizedDomain: "hubway.cc",
		Source:           "sub2api_probe",
		SourceKey:        "third",
		Status:           "ok",
		ConvertedBalance: float64Ptr(4),
		RateMultiplier:   float64Ptr(0.2),
		ConversionScale:  0.1,
		Currency:         "USD",
		ProbedAt:         now.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, 0.2, *repo.state.MultiplierBaseline)
	require.Equal(t, BalanceCenterAlertDeliveryAccepted, repo.deliveries[2].Status)
}

func TestBalanceCenterServiceAlertsForSuccessfulBalanceInPartialSnapshot(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:             "true",
		SettingKeyBalanceCenterEmailEnabled:        "true",
		SettingKeyBalanceCenterLowBalanceThreshold: "5",
		SettingKeyOpsEmailNotificationConfig:       `{"alert":{"recipients":["ops@example.com"]}}`,
	}}
	repo := &balanceCenterServiceRepositoryStub{}
	outbox := &balanceCenterAlertOutboxStub{}
	svc := NewBalanceCenterService(repo, settings)
	svc.SetAlertEmailOutbox(outbox)
	accountID := int64(66)

	_, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		AccountID:        &accountID,
		SiteName:         "派大星",
		NormalizedDomain: "api.aigo0.com",
		Source:           "sub2api_probe",
		SourceKey:        "partial-balance",
		Status:           UpstreamBillingProbeStatusFailed,
		ConvertedBalance: float64Ptr(4.5),
		ConversionScale:  1,
		Currency:         "USD",
		Reason:           "web_auth_failed",
		ProbedAt:         time.Date(2026, time.August, 28, 1, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	require.Len(t, outbox.inputs, 1)
	require.Equal(t, BalanceCenterAlertLowBalance, outbox.inputs[0].AlertType)
	require.True(t, repo.state.LowBalanceActive)
}

func TestBalanceCenterServiceSendsBothAlertTypesAndIgnoresQuietHours(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:             "true",
		SettingKeyBalanceCenterEmailEnabled:        "true",
		SettingKeyBalanceCenterLowBalanceThreshold: "5",
		SettingKeyOpsEmailNotificationConfig:       `{"alert":{"enabled":false,"recipients":["ops@example.com"],"quiet_hours_enabled":true,"quiet_hours_start":"00:00","quiet_hours_end":"23:59"}}`,
	}}
	baseline := 0.1
	repo := &balanceCenterServiceRepositoryStub{state: BalanceCenterAlertState{MultiplierBaseline: &baseline}}
	email := &balanceCenterEmailSenderStub{}
	svc := NewBalanceCenterService(repo, settings)
	svc.SetEmailSender(email)
	accountID := int64(18)

	_, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		AccountID:        &accountID,
		SiteName:         "VoVo",
		NormalizedDomain: "vovoapi.com",
		Source:           "sub2api_probe",
		SourceKey:        "both",
		Status:           "ok",
		ConvertedBalance: float64Ptr(4.99),
		RateMultiplier:   float64Ptr(0.2),
		ConversionScale:  1,
		Currency:         "USD",
		ProbedAt:         time.Now().UTC(),
	})

	require.NoError(t, err)
	require.Len(t, email.sends, 2, "余额中心不继承运维告警总开关或夜间静默")
	require.True(t, repo.state.LowBalanceActive)
	require.Equal(t, 0.2, *repo.state.MultiplierBaseline)
}

func TestBalanceCenterServiceEmailDisabledKeepsNotificationStatePending(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:             "true",
		SettingKeyBalanceCenterEmailEnabled:        "false",
		SettingKeyBalanceCenterLowBalanceThreshold: "5",
		SettingKeyOpsEmailNotificationConfig:       `{"alert":{"recipients":["ops@example.com"]}}`,
	}}
	repo := &balanceCenterServiceRepositoryStub{}
	email := &balanceCenterEmailSenderStub{}
	svc := NewBalanceCenterService(repo, settings)
	svc.SetEmailSender(email)

	_, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		SiteName:         "Pite",
		NormalizedDomain: "ai.pite.chat",
		Source:           "sub2api_probe",
		SourceKey:        "disabled",
		Status:           "ok",
		ConvertedBalance: float64Ptr(1),
		RateMultiplier:   float64Ptr(0.3),
		ConversionScale:  1,
		ProbedAt:         time.Now().UTC(),
	})

	require.NoError(t, err)
	require.Empty(t, email.sends)
	require.False(t, repo.state.LowBalanceActive)
	require.Equal(t, 0.3, *repo.state.MultiplierBaseline, "首次倍率基线与邮件开关无关")
}

func TestBalanceCenterServiceQueuesAlertBeforeAdvancingNotificationState(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:             "true",
		SettingKeyBalanceCenterEmailEnabled:        "true",
		SettingKeyBalanceCenterLowBalanceThreshold: "5",
		SettingKeyOpsEmailNotificationConfig:       `{"alert":{"recipients":["ops@example.com"]}}`,
	}}
	repo := &balanceCenterServiceRepositoryStub{}
	email := &balanceCenterEmailSenderStub{}
	outbox := &balanceCenterAlertOutboxStub{}
	svc := NewBalanceCenterService(repo, settings)
	svc.SetEmailSender(email)
	svc.SetAlertEmailOutbox(outbox)

	_, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		SiteName: "HBY", NormalizedDomain: "hubway.cc", Source: "sub2api_probe", SourceKey: "queued",
		Status: "ok", ConvertedBalance: float64Ptr(4), RateMultiplier: float64Ptr(0.1),
		ConversionScale: 0.1, Currency: "USD", ProbedAt: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	})

	require.NoError(t, err)
	require.Len(t, outbox.inputs, 1)
	require.Equal(t, AlertEmailSourceBalanceCenter, outbox.inputs[0].SourceType)
	require.Equal(t, "low_balance", outbox.inputs[0].AlertType)
	require.Empty(t, email.sends, "探测协程只负责持久化告警，不直接发送 SMTP")
	require.True(t, repo.state.LowBalanceActive, "成功入队后立即推进状态，避免下一轮重复入队")
	require.Equal(t, BalanceCenterAlertDeliveryQueued, repo.deliveries[0].Status)
}

func TestBalanceCenterServiceKeepsNotificationStateWhenAlertEnqueueFails(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:             "true",
		SettingKeyBalanceCenterEmailEnabled:        "true",
		SettingKeyBalanceCenterLowBalanceThreshold: "5",
		SettingKeyOpsEmailNotificationConfig:       `{"alert":{"recipients":["ops@example.com"]}}`,
	}}
	baseline := 0.1
	repo := &balanceCenterServiceRepositoryStub{state: BalanceCenterAlertState{MultiplierBaseline: &baseline}}
	svc := NewBalanceCenterService(repo, settings)
	svc.SetAlertEmailOutbox(&balanceCenterAlertOutboxStub{err: errors.New("database unavailable")})

	_, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		SiteName: "VoVo", NormalizedDomain: "vovoapi.com", Source: "sub2api_probe", SourceKey: "queue-failed",
		Status: "ok", ConvertedBalance: float64Ptr(10), RateMultiplier: float64Ptr(0.2),
		ConversionScale: 1, Currency: "USD", ProbedAt: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	})

	require.ErrorContains(t, err, "写入倍率变化告警邮件队列失败")
	require.Equal(t, 0.1, *repo.state.MultiplierBaseline)
	require.Equal(t, BalanceCenterAlertDeliveryFailed, repo.deliveries[0].Status)
}

func TestBalanceCenterServiceDisabledDoesNotPersistProbeSnapshot(t *testing.T) {
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled: "false",
	}}
	repo := &balanceCenterServiceRepositoryStub{}
	svc := NewBalanceCenterService(repo, settings)

	persisted, err := svc.PersistSnapshot(context.Background(), &BalanceCenterSnapshot{
		SiteName:         "Pite",
		NormalizedDomain: "ai.pite.chat",
		Source:           "sub2api_probe",
		SourceKey:        "disabled-feature",
		Status:           "ok",
		ProbedAt:         time.Now().UTC(),
	})

	require.NoError(t, err)
	require.Nil(t, persisted)
	require.Zero(t, repo.persistedCount())
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
