package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestProvideBalanceNotifyServiceDisablesDeliveryOnAPIOnly(t *testing.T) {
	apiOnly := ProvideBalanceNotifyService(nil, nil, nil, nil, nil, nil, nil, nil, nil, &config.Config{
		DeploymentRole: config.DeploymentRoleAPIOnly,
	})
	require.NotNil(t, apiOnly)
	require.False(t, apiOnly.deliveryEnabled)

	primary := ProvideBalanceNotifyService(nil, nil, nil, nil, nil, nil, nil, nil, nil, &config.Config{
		DeploymentRole: config.DeploymentRolePrimary,
	})
	require.NotNil(t, primary)
	require.True(t, primary.deliveryEnabled)
}

func TestProvideOpsAlertEvaluatorServiceRelaysAPIOnlyRequestAlerts(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc := ProvideOpsAlertEvaluatorService(nil, nil, nil, rdb, &config.Config{
		DeploymentRole: config.DeploymentRoleAPIOnly,
	}, nil)
	require.NotNil(t, svc)
	require.True(t, svc.relayRequestAlerts)

	accountID := int64(42)
	svc.NotifyAccountRequestErrors([]*OpsInsertErrorLogInput{{
		AccountID:  &accountID,
		ErrorPhase: "network",
		ErrorOwner: "provider",
		CreatedAt:  time.Now().UTC(),
	}})

	require.Eventually(t, func() bool {
		return rdb.LLen(context.Background(), opsAccountRequestAlertRelayKey).Val() == 1
	}, time.Second, 10*time.Millisecond)
	svc.Stop()
}

func TestOpsAlertEvaluatorRecoversUnackedRelayPayload(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	const payload = `[{"platform":"openai"}]`
	require.NoError(t, rdb.RPush(ctx, opsAccountRequestAlertProcessingKey, payload).Err())

	svc := NewOpsAlertEvaluatorService(nil, nil, nil, rdb, &config.Config{}, nil)
	svc.recoverAccountRequestAlertRelayProcessing()

	require.Equal(t, int64(0), rdb.LLen(ctx, opsAccountRequestAlertProcessingKey).Val())
	require.Equal(t, []string{payload}, rdb.LRange(ctx, opsAccountRequestAlertRelayKey, 0, -1).Val())
}

func TestOpsAlertEvaluatorRequeuesPayloadWhenConsumerStopsBeforeDispatch(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	const payload = `[{"platform":"openai"}]`
	require.NoError(t, rdb.RPush(ctx, opsAccountRequestAlertRelayKey, payload).Err())

	svc := NewOpsAlertEvaluatorService(nil, nil, nil, rdb, &config.Config{}, nil)
	svc.stopCh = make(chan struct{})
	svc.wg.Add(1)
	go svc.runAccountRequestAlertRelayConsumer()

	require.Eventually(t, func() bool {
		return rdb.LLen(ctx, opsAccountRequestAlertProcessingKey).Val() == 1
	}, time.Second, 10*time.Millisecond)
	svc.Stop()

	require.Equal(t, int64(0), rdb.LLen(ctx, opsAccountRequestAlertProcessingKey).Val())
	require.Equal(t, []string{payload}, rdb.LRange(ctx, opsAccountRequestAlertRelayKey, 0, -1).Val())
}

func TestProvideBalanceNotifyServiceRelaysAPIOnlyNotifications(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc := ProvideBalanceNotifyService(nil, nil, nil, nil, nil, nil, nil, nil, rdb, &config.Config{
		DeploymentRole: config.DeploymentRoleAPIOnly,
	})
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	svc.NotifyUserInsufficientBalance(context.Background(), &User{
		ID:       211,
		Username: "relay-user",
		Email:    "relay@example.com",
	}, 0.25)

	require.Eventually(t, func() bool {
		return rdb.LLen(context.Background(), balanceNotifyRelayKey).Val() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestBalanceNotifyRelayPrimaryAcknowledgesBusinessNoop(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	producer := ProvideBalanceNotifyService(nil, nil, nil, nil, nil, nil, nil, nil, rdb, &config.Config{
		DeploymentRole: config.DeploymentRoleAPIOnly,
	})
	producer.CheckBalanceAfterDeduction(ctx, &User{
		ID: 211, Email: "relay@example.com", BalanceNotifyEnabled: false,
	}, 1, 1)
	require.Eventually(t, func() bool {
		return rdb.LLen(ctx, balanceNotifyRelayKey).Val() == 1
	}, time.Second, 10*time.Millisecond)
	producer.Stop()

	consumer := ProvideBalanceNotifyService(nil, nil, nil, nil, nil, nil, nil, nil, rdb, &config.Config{
		DeploymentRole: config.DeploymentRolePrimary,
	})
	t.Cleanup(consumer.Stop)

	require.Eventually(t, func() bool {
		return rdb.LLen(ctx, balanceNotifyRelayKey).Val() == 0 &&
			rdb.LLen(ctx, balanceNotifyRelayProcessingKey).Val() == 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestBalanceNotifyRelayPrimaryRequeuesDeliveryFailure(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	payload := `{"id":"test","type":"insufficient_balance","user":{"id":211,"email":"relay@example.com"},"current_balance":0.25}`
	require.NoError(t, rdb.RPush(ctx, balanceNotifyRelayKey, payload).Err())

	consumer := ProvideBalanceNotifyService(nil, nil, nil, nil, nil, nil, nil, nil, rdb, &config.Config{
		DeploymentRole: config.DeploymentRolePrimary,
	})
	t.Cleanup(consumer.Stop)

	require.Eventually(t, func() bool {
		return rdb.LLen(ctx, balanceNotifyRelayProcessingKey).Val() == 0 &&
			rdb.LLen(ctx, balanceNotifyRelayKey).Val() == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestProvideUserPlatformQuotaUsageFlusherDoesNotStartOnAPIOnly(t *testing.T) {
	svc := ProvideUserPlatformQuotaUsageFlusher(&config.Config{
		DeploymentRole: config.DeploymentRoleAPIOnly,
		Database: config.DatabaseConfig{
			UserPlatformQuotaFlusherEnabled: true,
		},
	}, nil, nil, nil)
	require.NotNil(t, svc)
	require.False(t, svc.started.Load())
	require.NotPanics(t, svc.Stop)
}

func TestProvideScheduledTestRunnerServiceRespectsDeploymentRole(t *testing.T) {
	apiOnly := ProvideScheduledTestRunnerService(nil, nil, nil, nil, &config.Config{
		DeploymentRole: config.DeploymentRoleAPIOnly,
		Timezone:       "UTC",
	})
	require.NotNil(t, apiOnly)
	require.Nil(t, apiOnly.cron)

	primary := ProvideScheduledTestRunnerService(nil, nil, nil, nil, &config.Config{
		DeploymentRole: config.DeploymentRolePrimary,
		Timezone:       "UTC",
	})
	require.NotNil(t, primary)
	require.NotNil(t, primary.cron)
	primary.Stop()
}
