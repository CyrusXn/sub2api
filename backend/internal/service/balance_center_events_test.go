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
	published []int64
	due       []int64
	err       error
}

func (q *balanceCenterEventQueueStub) Schedule(_ context.Context, accountID int64, _ time.Time) error {
	q.published = append(q.published, accountID)
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
