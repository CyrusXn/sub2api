package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBalanceCenterEventPublisherRespectsSwitchAndNeverReturnsQueueError(t *testing.T) {
	queue := &balanceCenterEventQueueStub{err: errors.New("redis unavailable")}
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:           "true",
		SettingKeyBalanceCenterEventProbeEnabled: "true",
	}}
	svc := NewBalanceCenterEventService(queue, settings, nil)
	require.NoError(t, svc.RefreshSettings(context.Background()))

	require.NotPanics(t, func() { svc.PublishAccountUsed(context.Background(), 17) })
	require.Equal(t, []int64{17}, queue.published)

	settings.values[SettingKeyBalanceCenterEventProbeEnabled] = "false"
	require.NoError(t, svc.RefreshSettings(context.Background()))
	svc.PublishAccountUsed(context.Background(), 18)
	require.Equal(t, []int64{17}, queue.published)
}

func TestBalanceCenterEventPublisherUsesShortIndependentTimeout(t *testing.T) {
	queue := &balanceCenterEventQueueStub{}
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:           "true",
		SettingKeyBalanceCenterEventProbeEnabled: "true",
	}}
	svc := NewBalanceCenterEventService(queue, settings)
	require.NoError(t, svc.RefreshSettings(context.Background()))

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	svc.PublishAccountUsed(canceled, 17)

	require.Len(t, queue.scheduleContextErrors, 1)
	require.NoError(t, queue.scheduleContextErrors[0], "事件投递不得继承已取消的请求上下文")
	require.Len(t, queue.scheduleDeadlines, 1)
	require.WithinDuration(t, time.Now().Add(balanceCenterEventPublishTimeout), queue.scheduleDeadlines[0], 100*time.Millisecond)
}

func TestBalanceCenterEventConsumerOnlyProbesDueAccounts(t *testing.T) {
	queue := &balanceCenterEventQueueStub{due: []int64{17, 23}}
	prober := &balanceCenterAccountProberStub{}
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyBalanceCenterEnabled:           "true",
		SettingKeyBalanceCenterEventProbeEnabled: "true",
	}}
	svc := NewBalanceCenterEventService(queue, settings, prober)
	svc.now = func() time.Time { return time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC) }
	require.NoError(t, svc.RefreshSettings(context.Background()))

	require.NoError(t, svc.ConsumeDue(context.Background()))
	require.Equal(t, []int64{17, 23}, prober.accountIDs)
}

type balanceCenterEventQueueStub struct {
	published             []int64
	due                   []int64
	err                   error
	scheduleContextErrors []error
	scheduleDeadlines     []time.Time
}

func (q *balanceCenterEventQueueStub) Schedule(ctx context.Context, accountID int64, _ time.Time) error {
	q.published = append(q.published, accountID)
	q.scheduleContextErrors = append(q.scheduleContextErrors, ctx.Err())
	if deadline, ok := ctx.Deadline(); ok {
		q.scheduleDeadlines = append(q.scheduleDeadlines, deadline)
	}
	return q.err
}

func (q *balanceCenterEventQueueStub) PopDue(context.Context, time.Time, int64) ([]int64, error) {
	return q.due, q.err
}

type balanceCenterAccountProberStub struct{ accountIDs []int64 }

func (p *balanceCenterAccountProberStub) ProbeAccount(_ context.Context, accountID int64) (*UpstreamBillingProbeSnapshot, error) {
	p.accountIDs = append(p.accountIDs, accountID)
	return nil, nil
}
