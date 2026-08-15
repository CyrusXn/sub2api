package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	alertEmailOutboxDefaultBatchWindow  = time.Minute
	alertEmailOutboxDefaultRetryDelay   = 2 * time.Minute
	alertEmailOutboxDefaultClaimLease   = 2 * time.Minute
	alertEmailOutboxDefaultPollInterval = time.Second
	alertEmailOutboxDefaultBatchSize    = 100

	AlertEmailSourceBalanceCenter = "balance_center"
	AlertEmailSourceOpsAlert      = "ops_alert"
)

type AlertEmailOutboxInput struct {
	SourceType  string
	SourceID    string
	SourceKey   string
	AlertType   string
	Recipient   string
	Subject     string
	BodyHTML    string
	CreatedAt   time.Time
	AvailableAt time.Time
}

type AlertEmailOutboxItem struct {
	ID           int64
	SourceType   string
	SourceID     string
	SourceKey    string
	AlertType    string
	Recipient    string
	Subject      string
	BodyHTML     string
	AttemptCount int
	CreatedAt    time.Time
}

type AlertEmailOutboxOptions struct {
	BatchWindow  time.Duration
	RetryDelay   time.Duration
	ClaimLease   time.Duration
	PollInterval time.Duration
	BatchSize    int
}

type AlertEmailOutboxRepository interface {
	EnqueueAlertEmail(context.Context, *AlertEmailOutboxInput) error
	ClaimAlertEmailBatch(context.Context, time.Time, time.Duration, int) ([]*AlertEmailOutboxItem, error)
	MarkAlertEmailBatchSent(context.Context, []int64, time.Time) error
	RetryAlertEmailBatch(context.Context, []int64, string, time.Time) error
}

type AlertEmailOutboxEnqueuer interface {
	Enqueue(context.Context, *AlertEmailOutboxInput) error
}

type AlertEmailOutboxService struct {
	repository AlertEmailOutboxRepository
	sender     BalanceCenterEmailSender
	options    AlertEmailOutboxOptions
	stopCh     chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

func NewAlertEmailOutboxService(repository AlertEmailOutboxRepository, sender BalanceCenterEmailSender, options AlertEmailOutboxOptions) *AlertEmailOutboxService {
	options = normalizeAlertEmailOutboxOptions(options)
	return &AlertEmailOutboxService{
		repository: repository,
		sender:     sender,
		options:    options,
		stopCh:     make(chan struct{}),
	}
}

// Enqueue 以业务幂等键持久化告警，只有成功入队后调用方才推进告警状态。
func (s *AlertEmailOutboxService) Enqueue(ctx context.Context, input *AlertEmailOutboxInput) error {
	if s == nil || s.repository == nil || input == nil {
		return errors.New("告警邮件队列内容无效")
	}
	queued := *input
	queued.SourceType = strings.TrimSpace(queued.SourceType)
	queued.SourceID = strings.TrimSpace(queued.SourceID)
	queued.SourceKey = strings.TrimSpace(queued.SourceKey)
	queued.AlertType = strings.TrimSpace(queued.AlertType)
	queued.Recipient = strings.ToLower(strings.TrimSpace(queued.Recipient))
	queued.Subject = strings.TrimSpace(queued.Subject)
	queued.BodyHTML = strings.TrimSpace(queued.BodyHTML)
	if queued.SourceType == "" || queued.SourceKey == "" || queued.AlertType == "" || queued.Recipient == "" || queued.Subject == "" || queued.BodyHTML == "" {
		return errors.New("告警邮件队列内容无效")
	}
	if queued.CreatedAt.IsZero() {
		queued.CreatedAt = time.Now().UTC()
	} else {
		queued.CreatedAt = queued.CreatedAt.UTC()
	}
	if queued.AvailableAt.IsZero() {
		queued.AvailableAt = queued.CreatedAt.Add(s.options.BatchWindow)
	} else {
		queued.AvailableAt = queued.AvailableAt.UTC()
	}
	return s.repository.EnqueueAlertEmail(ctx, &queued)
}

// RunOnce 认领一个收件人的到期窗口，并在同一封邮件中发送该窗口的全部告警。
func (s *AlertEmailOutboxService) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if s == nil || s.repository == nil || s.sender == nil {
		return 0, errors.New("告警邮件队列服务不可用")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	items, err := s.repository.ClaimAlertEmailBatch(ctx, now, s.options.ClaimLease, s.options.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("认领告警邮件失败: %w", err)
	}
	if len(items) == 0 {
		return 0, nil
	}

	byRecipient := make(map[string][]*AlertEmailOutboxItem)
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.Recipient) == "" {
			continue
		}
		recipient := strings.ToLower(strings.TrimSpace(item.Recipient))
		byRecipient[recipient] = append(byRecipient[recipient], item)
	}
	recipients := make([]string, 0, len(byRecipient))
	for recipient := range byRecipient {
		recipients = append(recipients, recipient)
	}
	sort.Strings(recipients)

	processed := 0
	var runErrors []error
	for _, recipient := range recipients {
		batch := byRecipient[recipient]
		processed += len(batch)
		ids := alertEmailOutboxIDs(batch)
		subject, body := buildAlertEmailOutboxMessage(batch)
		if sendErr := s.sender.SendEmail(ctx, recipient, subject, body); sendErr != nil {
			reason := truncateString(redactOpsAlertEmailText(sendErr.Error()), 1000)
			if retryErr := s.repository.RetryAlertEmailBatch(ctx, ids, reason, now.Add(s.options.RetryDelay)); retryErr != nil {
				runErrors = append(runErrors, fmt.Errorf("重试告警邮件批次失败: %w", retryErr))
			}
			runErrors = append(runErrors, fmt.Errorf("发送告警汇总邮件失败: %w", sendErr))
			continue
		}
		if markErr := s.repository.MarkAlertEmailBatchSent(ctx, ids, now); markErr != nil {
			runErrors = append(runErrors, fmt.Errorf("确认告警邮件批次失败: %w", markErr))
		}
	}
	return processed, errors.Join(runErrors...)
}

func (s *AlertEmailOutboxService) Start() {
	if s == nil || s.repository == nil || s.sender == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.run()
	})
}

func (s *AlertEmailOutboxService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.wg.Wait()
	})
}

func (s *AlertEmailOutboxService) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.options.PollInterval)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := s.RunOnce(ctx, time.Now().UTC())
		cancel()
		if err != nil {
			slog.Warn("处理告警邮件汇总队列失败", "error", redactOpsAlertEmailText(err.Error()))
		}
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func normalizeAlertEmailOutboxOptions(options AlertEmailOutboxOptions) AlertEmailOutboxOptions {
	if options.BatchWindow <= 0 {
		options.BatchWindow = alertEmailOutboxDefaultBatchWindow
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = alertEmailOutboxDefaultRetryDelay
	}
	if options.ClaimLease <= 0 {
		options.ClaimLease = alertEmailOutboxDefaultClaimLease
	}
	if options.PollInterval <= 0 {
		options.PollInterval = alertEmailOutboxDefaultPollInterval
	}
	if options.BatchSize <= 0 {
		options.BatchSize = alertEmailOutboxDefaultBatchSize
	}
	return options
}

func alertEmailOutboxIDs(items []*AlertEmailOutboxItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if item != nil && item.ID > 0 {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func buildAlertEmailOutboxMessage(items []*AlertEmailOutboxItem) (string, string) {
	if len(items) == 1 {
		return items[0].Subject, items[0].BodyHTML
	}
	var body strings.Builder
	body.WriteString("<h2>上游告警汇总</h2>")
	for _, item := range items {
		if item == nil {
			continue
		}
		body.WriteString("<section><h3>")
		body.WriteString(html.EscapeString(item.Subject))
		body.WriteString("</h3>")
		body.WriteString(item.BodyHTML)
		body.WriteString("</section>")
	}
	return fmt.Sprintf("[上游告警汇总] %d 条异常", len(items)), body.String()
}
