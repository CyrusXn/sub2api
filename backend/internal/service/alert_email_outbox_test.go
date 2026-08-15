package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type alertEmailOutboxRepositoryStub struct {
	claimed   []*AlertEmailOutboxItem
	enqueued  []*AlertEmailOutboxInput
	sentIDs   []int64
	failedIDs []int64
	nextRetry time.Time
	claims    atomic.Int32
}

func (r *alertEmailOutboxRepositoryStub) EnqueueAlertEmail(_ context.Context, input *AlertEmailOutboxInput) error {
	clone := *input
	r.enqueued = append(r.enqueued, &clone)
	return nil
}

func (r *alertEmailOutboxRepositoryStub) ClaimAlertEmailBatch(context.Context, time.Time, time.Duration, int) ([]*AlertEmailOutboxItem, error) {
	r.claims.Add(1)
	items := r.claimed
	r.claimed = nil
	return items, nil
}

func TestProvideAlertEmailOutboxServiceStartsOnlyOnPrimary(t *testing.T) {
	apiRepo := &alertEmailOutboxRepositoryStub{}
	apiOnly := ProvideAlertEmailOutboxService(apiRepo, &EmailService{}, &config.Config{DeploymentRole: config.DeploymentRoleAPIOnly})
	t.Cleanup(apiOnly.Stop)
	time.Sleep(20 * time.Millisecond)
	require.Zero(t, apiRepo.claims.Load())

	primaryRepo := &alertEmailOutboxRepositoryStub{}
	primary := ProvideAlertEmailOutboxService(primaryRepo, &EmailService{}, &config.Config{DeploymentRole: config.DeploymentRolePrimary})
	t.Cleanup(primary.Stop)
	require.Eventually(t, func() bool { return primaryRepo.claims.Load() > 0 }, time.Second, 10*time.Millisecond)
}

func (r *alertEmailOutboxRepositoryStub) MarkAlertEmailBatchSent(_ context.Context, ids []int64, _ time.Time) error {
	r.sentIDs = append(r.sentIDs, ids...)
	return nil
}

func (r *alertEmailOutboxRepositoryStub) RetryAlertEmailBatch(_ context.Context, ids []int64, _ string, nextRetry time.Time) error {
	r.failedIDs = append(r.failedIDs, ids...)
	r.nextRetry = nextRetry
	return nil
}

type alertEmailSenderStub struct {
	to      string
	subject string
	body    string
	err     error
}

func (s *alertEmailSenderStub) SendEmail(_ context.Context, to, subject, body string) error {
	s.to, s.subject, s.body = to, subject, body
	return s.err
}

func TestAlertEmailOutboxAggregatesSameRecipientIntoOneEmail(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	repo := &alertEmailOutboxRepositoryStub{claimed: []*AlertEmailOutboxItem{
		{ID: 1, Recipient: "ops@example.com", Subject: "HBY 余额不足", BodyHTML: "<p>HBY</p>", CreatedAt: now.Add(-time.Minute)},
		{ID: 2, Recipient: "ops@example.com", Subject: "VoVo 倍率变化", BodyHTML: "<p>VoVo</p>", CreatedAt: now.Add(-30 * time.Second)},
	}}
	sender := &alertEmailSenderStub{}
	svc := NewAlertEmailOutboxService(repo, sender, AlertEmailOutboxOptions{BatchWindow: time.Minute})

	processed, err := svc.RunOnce(context.Background(), now)

	require.NoError(t, err)
	require.Equal(t, 2, processed)
	require.Equal(t, "ops@example.com", sender.to)
	require.Contains(t, sender.subject, "2 条")
	require.Contains(t, sender.body, "HBY 余额不足")
	require.Contains(t, sender.body, "VoVo 倍率变化")
	require.Equal(t, []int64{1, 2}, repo.sentIDs)
	require.Empty(t, repo.failedIDs)
}

func TestAlertEmailOutboxRetriesWholeBatchAfterSendFailure(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	repo := &alertEmailOutboxRepositoryStub{claimed: []*AlertEmailOutboxItem{{ID: 7, Recipient: "ops@example.com", Subject: "上游异常", BodyHTML: "<p>失败</p>"}}}
	sender := &alertEmailSenderStub{err: errors.New("smtp timeout")}
	svc := NewAlertEmailOutboxService(repo, sender, AlertEmailOutboxOptions{RetryDelay: 2 * time.Minute})

	processed, err := svc.RunOnce(context.Background(), now)

	require.ErrorContains(t, err, "smtp timeout")
	require.Equal(t, 1, processed)
	require.Equal(t, []int64{7}, repo.failedIDs)
	require.Equal(t, now.Add(2*time.Minute), repo.nextRetry)
	require.Empty(t, repo.sentIDs)
}

func TestAlertEmailOutboxEnqueueRejectsSensitiveOrIncompleteInput(t *testing.T) {
	repo := &alertEmailOutboxRepositoryStub{}
	svc := NewAlertEmailOutboxService(repo, &alertEmailSenderStub{}, AlertEmailOutboxOptions{})

	err := svc.Enqueue(context.Background(), &AlertEmailOutboxInput{Recipient: "ops@example.com"})

	require.EqualError(t, err, "告警邮件队列内容无效")
	require.Empty(t, repo.enqueued)
}
