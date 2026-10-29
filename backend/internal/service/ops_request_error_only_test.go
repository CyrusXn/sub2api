package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpsRequestErrorOnlyPersistsAndQueriesWithoutAlerts(t *testing.T) {
	cfg := &config.Config{Ops: config.OpsConfig{RequestErrorsEnabled: true}}
	inserted, notified := 0, 0
	repo := &opsRepoMock{InsertErrorLogFn: func(context.Context, *OpsInsertErrorLogInput) (int64, error) {
		inserted++
		return 1, nil
	}}
	svc := NewOpsService(repo, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.SetAccountRequestAlertSink(func([]*OpsInsertErrorLogInput) { notified++ })
	ctx := context.Background()
	require.ErrorIs(t, svc.RequireMonitoringEnabled(ctx), ErrOpsDisabled)
	require.NoError(t, svc.RecordError(ctx, &OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}))
	require.Equal(t, 1, inserted)
	require.Zero(t, notified)
	_, err := svc.GetErrorLogs(ctx, &OpsErrorLogFilter{})
	require.NoError(t, err)
	_, err = svc.GetErrorLogByID(ctx, 1)
	require.NoError(t, err)
	cfg.Ops.RequestErrorsEnabled = false
	require.ErrorIs(t, svc.RequireRequestErrorLoggingEnabled(ctx), ErrOpsDisabled)
}
