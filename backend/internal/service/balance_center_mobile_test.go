package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mobileBalanceRepositoryStub struct {
	balanceCenterServiceRepositoryStub
	snapshots []int64
}

func (r *mobileBalanceRepositoryStub) CaptureMobileBalanceAlerts(_ context.Context, snapshot *BalanceCenterSnapshot, _ float64) error {
	r.snapshots = append(r.snapshots, snapshot.ID)
	return nil
}

func (r *mobileBalanceRepositoryStub) ListMobileAlerts(context.Context, int64, bool) ([]MobileAlert, error) {
	return nil, nil
}

func TestMobileBalanceCaptureWithEmailDisabled(t *testing.T) {
	t.Setenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256", "0000000000000000000000000000000000000000000000000000000000000000")
	repo := &mobileBalanceRepositoryStub{}
	svc := NewBalanceCenterService(repo, &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:      "false",
		SettingKeyBalanceCenterEmailEnabled: "false",
	}})
	snapshot := &BalanceCenterSnapshot{Status: "ok", ConvertedBalance: float64Ptr(1), ProbedAt: time.Now()}
	_, err := svc.PersistSnapshot(context.Background(), snapshot)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, repo.snapshots)
	require.False(t, repo.state.LowBalanceActive, "手机通知不消费邮件状态")
	events := NewBalanceCenterEventService(nil, svc.settingRepo)
	require.NoError(t, events.RefreshSettings(context.Background()))
	require.True(t, events.enabled.Load())
}

func TestMobileAlertAuthorizationRequiresIndependentToken(t *testing.T) {
	token := "test-only-" + "0123456789012345678901234567890123456789"
	t.Setenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256", "")
	require.False(t, AuthorizeMobileAlerts("Bearer "+token))
	t.Setenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256", "2992fd26365540c1bfe8420360c88e44cfa6f5921f0be7ed3ce9799c19cdc179")
	require.True(t, AuthorizeMobileAlerts("Bearer "+token))
	require.False(t, AuthorizeMobileAlerts("Bearer wrong"))
	require.False(t, AuthorizeMobileAlerts(token))
}
