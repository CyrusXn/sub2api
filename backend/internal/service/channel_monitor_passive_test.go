//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type passiveRepoStub struct {
	ChannelMonitorV2Repository
	calls int
	end   time.Time
	err   error
}

func (r *passiveRepoStub) RefreshPassiveMinutes(ctx context.Context, end time.Time) error {
	r.calls++
	r.end = end
	r.err = ctx.Err()
	return r.err
}

func TestChannelMonitorPassiveCollectionIgnoresLegacyMode(t *testing.T) {
	for _, mode := range []string{ChannelMonitorModeV1, ChannelMonitorModeV2} {
		for _, enabled := range []bool{true, false} {
			repo := &passiveRepoStub{}
			worker := NewChannelMonitorV2Aggregator(repo, nil, channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: enabled, Mode: mode}})
			worker.runPassiveOnce()
			if enabled {
				require.Equal(t, 1, repo.calls, mode)
				require.Equal(t, repo.end.Truncate(time.Minute), repo.end)
			} else {
				require.Zero(t, repo.calls, mode)
			}
		}
	}
}

func TestChannelMonitorPassiveStopCancelsQuery(t *testing.T) {
	repo := &passiveRepoStub{}
	worker := NewChannelMonitorV2Aggregator(repo, nil, channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true}})
	worker.ctx, worker.cancel = context.WithCancel(context.Background())
	worker.Stop()
	worker.runPassiveOnce()
	require.ErrorIs(t, repo.err, context.Canceled)
}

func TestChannelMonitorRunnerProviderNeverSchedules(t *testing.T) {
	svc := NewChannelMonitorService(nil, nil)
	runner := ProvideChannelMonitorRunner(svc, nil, nil, &config.Config{})
	require.Nil(t, svc.scheduler)
	require.False(t, runner.started)
	require.Empty(t, runner.tasks)
	runner.Stop()
}
