package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	opsAlertEvaluatorJobName = "ops_alert_evaluator"

	opsAlertEvaluatorTimeout         = 45 * time.Second
	opsAlertEvaluatorLeaderLockKey   = "ops:alert:evaluator:leader"
	opsAlertEvaluatorLeaderLockTTL   = 90 * time.Second
	opsAlertEvaluatorSkipLogInterval = 1 * time.Minute

	opsAccountRequestAlertRelayKey        = "ops:alert:account-request:relay"
	opsAccountRequestAlertProcessingKey   = "ops:alert:account-request:processing"
	opsAccountRequestAlertRelayMaxBatches = 4096
	opsAccountRequestAlertRelayTimeout    = time.Second
)

var opsAlertEvaluatorReleaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

type OpsAlertEvaluatorService struct {
	opsService   *OpsService
	opsRepo      OpsRepository
	emailService *EmailService
	alertOutbox  AlertEmailOutboxEnqueuer
	proxyRepo    ProxyRepository

	redisClient *redis.Client
	cfg         *config.Config
	instanceID  string

	stopCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup

	mu         sync.Mutex
	ruleStates map[int64]*opsAlertRuleState

	accountRequestAlertCh      chan []*opsAccountRequestAlertSignal
	accountRequestAlertBatchCh chan *opsAccountRequestAlertBatch
	accountRequestAlertLastAt  time.Time
	relayRequestAlerts         bool

	emailLimiter *slidingWindowLimiter

	skipLogMu sync.Mutex
	skipLogAt time.Time

	warnNoRedisOnce sync.Once
}

type opsAlertRuleState struct {
	LastEvaluatedAt     time.Time
	ConsecutiveBreaches int
}

type opsAccountRequestAlertBatch struct {
	signals []*opsAccountRequestAlertSignal
	payload string
}

func NewOpsAlertEvaluatorService(
	opsService *OpsService,
	opsRepo OpsRepository,
	emailService *EmailService,
	redisClient *redis.Client,
	cfg *config.Config,
	proxyRepo ProxyRepository,
) *OpsAlertEvaluatorService {
	return &OpsAlertEvaluatorService{
		opsService:                 opsService,
		opsRepo:                    opsRepo,
		emailService:               emailService,
		proxyRepo:                  proxyRepo,
		redisClient:                redisClient,
		cfg:                        cfg,
		instanceID:                 uuid.NewString(),
		ruleStates:                 map[int64]*opsAlertRuleState{},
		accountRequestAlertCh:      make(chan []*opsAccountRequestAlertSignal, 256),
		accountRequestAlertBatchCh: make(chan *opsAccountRequestAlertBatch),
		emailLimiter:               newSlidingWindowLimiter(0, time.Hour),
	}
}

// SetAlertEmailOutbox 仅用于上游账号请求异常，将密集事件交给持久化队列汇总。
func (s *OpsAlertEvaluatorService) SetAlertEmailOutbox(outbox AlertEmailOutboxEnqueuer) {
	if s != nil {
		s.alertOutbox = outbox
	}
}

func (s *OpsAlertEvaluatorService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.stopCh == nil {
			s.stopCh = make(chan struct{})
		}
		s.wg.Add(1)
		go s.run()
		if s.redisClient != nil {
			s.wg.Add(1)
			go s.runAccountRequestAlertRelayConsumer()
		}
	})
}

// StartRequestAlertRelay 只转发当前节点的请求故障信号，不在本节点评估或发信。
func (s *OpsAlertEvaluatorService) StartRequestAlertRelay() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.stopCh == nil {
			s.stopCh = make(chan struct{})
		}
		s.relayRequestAlerts = true
		s.wg.Add(1)
		go s.runAccountRequestAlertRelayProducer()
	})
}

func (s *OpsAlertEvaluatorService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
		}
	})
	s.wg.Wait()
}

func (s *OpsAlertEvaluatorService) runAccountRequestAlertRelayProducer() {
	defer s.wg.Done()
	for {
		select {
		case signals := <-s.accountRequestAlertCh:
			s.publishAccountRequestAlertSignals(signals)
		case <-s.stopCh:
			for {
				select {
				case signals := <-s.accountRequestAlertCh:
					s.publishAccountRequestAlertSignals(signals)
				default:
					return
				}
			}
		}
	}
}

func (s *OpsAlertEvaluatorService) publishAccountRequestAlertSignals(signals []*opsAccountRequestAlertSignal) {
	if s == nil || s.redisClient == nil || len(signals) == 0 {
		return
	}
	payload, err := json.Marshal(signals)
	if err != nil {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] encode account request relay failed: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), opsAccountRequestAlertRelayTimeout)
	defer cancel()
	pipe := s.redisClient.TxPipeline()
	pipe.RPush(ctx, opsAccountRequestAlertRelayKey, payload)
	pipe.LTrim(ctx, opsAccountRequestAlertRelayKey, -opsAccountRequestAlertRelayMaxBatches, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] publish account request relay failed: %v", err)
	}
}

func (s *OpsAlertEvaluatorService) runAccountRequestAlertRelayConsumer() {
	defer s.wg.Done()
	s.recoverAccountRequestAlertRelayProcessing()
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*opsAccountRequestAlertRelayTimeout)
		payload, err := s.redisClient.BLMove(
			ctx,
			opsAccountRequestAlertRelayKey,
			opsAccountRequestAlertProcessingKey,
			"LEFT",
			"RIGHT",
			opsAccountRequestAlertRelayTimeout,
		).Result()
		cancel()
		if err != nil {
			if err != redis.Nil && err != context.DeadlineExceeded && err != context.Canceled {
				logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] consume account request relay failed: %v", err)
			}
			continue
		}
		var signals []*opsAccountRequestAlertSignal
		if err := json.Unmarshal([]byte(payload), &signals); err != nil {
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] decode account request relay failed: %v", err)
			s.ackAccountRequestAlertRelay(payload)
			continue
		}
		batch := &opsAccountRequestAlertBatch{signals: signals, payload: payload}
		select {
		case s.accountRequestAlertBatchCh <- batch:
		case <-s.stopCh:
			s.retryAccountRequestAlertRelay(payload)
			return
		}
	}
}

func (s *OpsAlertEvaluatorService) recoverAccountRequestAlertRelayProcessing() {
	ctx, cancel := context.WithTimeout(context.Background(), opsAccountRequestAlertRelayTimeout)
	defer cancel()
	for {
		payload, err := s.redisClient.RPopLPush(ctx, opsAccountRequestAlertProcessingKey, opsAccountRequestAlertRelayKey).Result()
		if err == redis.Nil {
			return
		}
		if err != nil {
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] recover account request relay failed: %v", err)
			return
		}
		if payload == "" {
			return
		}
	}
}

func (s *OpsAlertEvaluatorService) ackAccountRequestAlertRelay(payload string) {
	ctx, cancel := context.WithTimeout(context.Background(), opsAccountRequestAlertRelayTimeout)
	defer cancel()
	if err := s.redisClient.LRem(ctx, opsAccountRequestAlertProcessingKey, 1, payload).Err(); err != nil {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] ack account request relay failed: %v", err)
	}
}

func (s *OpsAlertEvaluatorService) retryAccountRequestAlertRelay(payload string) {
	ctx, cancel := context.WithTimeout(context.Background(), opsAccountRequestAlertRelayTimeout)
	defer cancel()
	pipe := s.redisClient.TxPipeline()
	pipe.LRem(ctx, opsAccountRequestAlertProcessingKey, 1, payload)
	pipe.RPush(ctx, opsAccountRequestAlertRelayKey, payload)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] retry account request relay failed: %v", err)
	}
}

func (s *OpsAlertEvaluatorService) run() {
	defer s.wg.Done()

	// Start immediately to produce early feedback in ops dashboard.
	timer := time.NewTimer(0)
	defer timer.Stop()
	// 启动时补发遗漏汇总，之后固定在北京时间 08:00 检查夜间静默记录。
	digestTimer := time.NewTimer(0)
	defer digestTimer.Stop()

	for {
		select {
		case <-timer.C:
			interval := s.getInterval()
			s.evaluateOnce(interval)
			timer.Reset(interval)
		case signals := <-s.accountRequestAlertCh:
			s.evaluateAccountRequestAlerts(signals)
		case batch := <-s.accountRequestAlertBatchCh:
			if batch == nil {
				continue
			}
			if s.evaluateAccountRequestAlerts(batch.signals) {
				s.ackAccountRequestAlertRelay(batch.payload)
			} else {
				s.retryAccountRequestAlertRelay(batch.payload)
				timer := time.NewTimer(opsAccountRequestAlertRelayTimeout)
				select {
				case <-timer.C:
				case <-s.stopCh:
					if !timer.Stop() {
						<-timer.C
					}
					return
				}
			}
		case <-digestTimer.C:
			s.sendQuietHoursDigestOnce()
			digestTimer.Reset(durationUntilNextBeijingDigest(time.Now().UTC()))
		case <-s.stopCh:
			return
		}
	}
}

func (s *OpsAlertEvaluatorService) getInterval() time.Duration {
	// Default.
	interval := 60 * time.Second

	if s == nil || s.opsService == nil {
		return interval
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg, err := s.opsService.GetOpsAlertRuntimeSettings(ctx)
	if err != nil || cfg == nil {
		return interval
	}
	if cfg.EvaluationIntervalSeconds <= 0 {
		return interval
	}
	if cfg.EvaluationIntervalSeconds < 1 {
		return interval
	}
	if cfg.EvaluationIntervalSeconds > int((24 * time.Hour).Seconds()) {
		return interval
	}
	return time.Duration(cfg.EvaluationIntervalSeconds) * time.Second
}

func (s *OpsAlertEvaluatorService) evaluateOnce(interval time.Duration) {
	if s == nil || s.opsRepo == nil {
		return
	}
	if s.cfg != nil && !s.cfg.Ops.Enabled {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), opsAlertEvaluatorTimeout)
	defer cancel()

	if s.opsService != nil && !s.opsService.IsMonitoringEnabled(ctx) {
		return
	}

	runtimeCfg := defaultOpsAlertRuntimeSettings()
	if s.opsService != nil {
		if loaded, err := s.opsService.GetOpsAlertRuntimeSettings(ctx); err == nil && loaded != nil {
			runtimeCfg = loaded
		}
	}

	release, ok := s.tryAcquireLeaderLock(ctx, runtimeCfg.DistributedLock)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}

	startedAt := time.Now().UTC()
	runAt := startedAt

	rules, err := s.opsRepo.ListAlertRules(ctx)
	if err != nil {
		s.recordHeartbeatError(runAt, time.Since(startedAt), err)
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] list rules failed: %v", err)
		return
	}

	rulesTotal := len(rules)
	rulesEnabled := 0
	rulesEvaluated := 0
	eventsCreated := 0
	eventsResolved := 0
	emailsSent := 0

	now := time.Now().UTC()
	safeEnd := now.Truncate(time.Minute)
	if safeEnd.IsZero() {
		safeEnd = now
	}

	systemMetrics, _ := s.opsRepo.GetLatestSystemMetrics(ctx, 1)

	// Cleanup stale state for removed rules.
	s.pruneRuleStates(rules)

	for _, rule := range rules {
		if rule == nil || !rule.Enabled || rule.ID <= 0 {
			continue
		}
		rulesEnabled++
		if strings.TrimSpace(rule.MetricType) == OpsAlertMetricAccountRequestFailure {
			// 该规则由错误落库事件即时触发，定时任务仅负责恢复活跃事件。
			rulesEvaluated++
			continue
		}

		scopePlatform, scopeGroupID, scopeRegion := parseOpsAlertRuleScope(rule.Filters)

		windowMinutes := rule.WindowMinutes
		if windowMinutes <= 0 {
			windowMinutes = 1
		}
		windowStart := safeEnd.Add(-time.Duration(windowMinutes) * time.Minute)
		windowEnd := safeEnd

		metricValue, ok := s.computeRuleMetric(ctx, rule, systemMetrics, windowStart, windowEnd, scopePlatform, scopeGroupID)
		if !ok {
			s.resetRuleState(rule.ID, now)
			continue
		}
		rulesEvaluated++

		breachedNow := compareMetric(metricValue, rule.Operator, rule.Threshold)
		required := requiredSustainedBreaches(rule.SustainedMinutes, interval)
		consecutive := s.updateRuleBreaches(rule.ID, now, interval, breachedNow)

		activeEvent, err := s.opsRepo.GetActiveAlertEvent(ctx, rule.ID)
		if err != nil {
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] get active event failed (rule=%d): %v", rule.ID, err)
			continue
		}

		if breachedNow && consecutive >= required {
			if activeEvent != nil {
				continue
			}

			// Scoped silencing: if a matching silence exists, skip creating a firing event.
			if s.opsService != nil {
				platform := strings.TrimSpace(scopePlatform)
				region := scopeRegion
				if platform != "" {
					if ok, err := s.opsService.IsAlertSilenced(ctx, rule.ID, platform, scopeGroupID, region, now); err == nil && ok {
						s.recordDatabaseSilencedAlertEmails(ctx, rule, strings.TrimSpace(rule.Name), strings.TrimSpace(rule.Severity), platform, nil, nil, now)
						continue
					}
				}
			}

			latestEvent, err := s.opsRepo.GetLatestAlertEvent(ctx, rule.ID)
			if err != nil {
				logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] get latest event failed (rule=%d): %v", rule.ID, err)
				continue
			}
			if latestEvent != nil && rule.CooldownMinutes > 0 {
				cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
				if now.Sub(latestEvent.FiredAt) < cooldown {
					continue
				}
			}

			firedEvent := &OpsAlertEvent{
				RuleID:         rule.ID,
				Severity:       strings.TrimSpace(rule.Severity),
				Status:         OpsAlertStatusFiring,
				Title:          fmt.Sprintf("%s: %s", strings.TrimSpace(rule.Severity), strings.TrimSpace(rule.Name)),
				Description:    buildOpsAlertDescription(rule, metricValue, windowMinutes, scopePlatform, scopeGroupID),
				MetricValue:    float64Ptr(metricValue),
				ThresholdValue: float64Ptr(rule.Threshold),
				Dimensions:     buildOpsAlertDimensions(scopePlatform, scopeGroupID),
				FiredAt:        now,
				CreatedAt:      now,
			}

			created, err := s.opsRepo.CreateAlertEvent(ctx, firedEvent)
			if err != nil {
				logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] create event failed (rule=%d): %v", rule.ID, err)
				continue
			}

			eventsCreated++
			if created != nil && created.ID > 0 {
				if s.maybeSendAlertEmail(ctx, runtimeCfg, rule, created, nil, nil) {
					emailsSent++
				}
			}
			continue
		}

		// Not breached: resolve active event if present.
		if activeEvent != nil {
			resolvedAt := now
			if err := s.opsRepo.UpdateAlertEventStatus(ctx, activeEvent.ID, OpsAlertStatusResolved, &resolvedAt); err != nil {
				logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] resolve event failed (event=%d): %v", activeEvent.ID, err)
			} else {
				eventsResolved++
			}
		}
	}
	if s.resolveRecoveredAccountRequestAlert(ctx, rules, now) {
		eventsResolved++
	}
	result := truncateString(fmt.Sprintf("rules=%d enabled=%d evaluated=%d created=%d resolved=%d emails_sent=%d", rulesTotal, rulesEnabled, rulesEvaluated, eventsCreated, eventsResolved, emailsSent), 2048)
	s.recordHeartbeatSuccess(runAt, time.Since(startedAt), result)
}

func (s *OpsAlertEvaluatorService) pruneRuleStates(rules []*OpsAlertRule) {
	s.mu.Lock()
	defer s.mu.Unlock()

	live := map[int64]struct{}{}
	for _, r := range rules {
		if r != nil && r.ID > 0 {
			live[r.ID] = struct{}{}
		}
	}
	for id := range s.ruleStates {
		if _, ok := live[id]; !ok {
			delete(s.ruleStates, id)
		}
	}
}

func (s *OpsAlertEvaluatorService) resetRuleState(ruleID int64, now time.Time) {
	if ruleID <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.ruleStates[ruleID]
	if !ok {
		state = &opsAlertRuleState{}
		s.ruleStates[ruleID] = state
	}
	state.LastEvaluatedAt = now
	state.ConsecutiveBreaches = 0
}

func (s *OpsAlertEvaluatorService) updateRuleBreaches(ruleID int64, now time.Time, interval time.Duration, breached bool) int {
	if ruleID <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.ruleStates[ruleID]
	if !ok {
		state = &opsAlertRuleState{}
		s.ruleStates[ruleID] = state
	}

	if !state.LastEvaluatedAt.IsZero() && interval > 0 {
		if now.Sub(state.LastEvaluatedAt) > interval*2 {
			state.ConsecutiveBreaches = 0
		}
	}

	state.LastEvaluatedAt = now
	if breached {
		state.ConsecutiveBreaches++
	} else {
		state.ConsecutiveBreaches = 0
	}
	return state.ConsecutiveBreaches
}

func requiredSustainedBreaches(sustainedMinutes int, interval time.Duration) int {
	if sustainedMinutes <= 0 {
		return 1
	}
	if interval <= 0 {
		return sustainedMinutes
	}
	required := int(math.Ceil(float64(sustainedMinutes*60) / interval.Seconds()))
	if required < 1 {
		return 1
	}
	return required
}

func parseOpsAlertRuleScope(filters map[string]any) (platform string, groupID *int64, region *string) {
	if filters == nil {
		return "", nil, nil
	}
	if v, ok := filters["platform"]; ok {
		if s, ok := v.(string); ok {
			platform = strings.TrimSpace(s)
		}
	}
	if v, ok := filters["group_id"]; ok {
		switch t := v.(type) {
		case float64:
			if t > 0 {
				id := int64(t)
				groupID = &id
			}
		case int64:
			if t > 0 {
				id := t
				groupID = &id
			}
		case int:
			if t > 0 {
				id := int64(t)
				groupID = &id
			}
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
			if err == nil && n > 0 {
				groupID = &n
			}
		}
	}
	if v, ok := filters["region"]; ok {
		if s, ok := v.(string); ok {
			vv := strings.TrimSpace(s)
			if vv != "" {
				region = &vv
			}
		}
	}
	return platform, groupID, region
}

func (s *OpsAlertEvaluatorService) computeRuleMetric(
	ctx context.Context,
	rule *OpsAlertRule,
	systemMetrics *OpsSystemMetricsSnapshot,
	start time.Time,
	end time.Time,
	platform string,
	groupID *int64,
) (float64, bool) {
	if rule == nil {
		return 0, false
	}
	switch strings.TrimSpace(rule.MetricType) {
	case "cpu_usage_percent":
		if systemMetrics != nil && systemMetrics.CPUUsagePercent != nil {
			return *systemMetrics.CPUUsagePercent, true
		}
		return 0, false
	case "memory_usage_percent":
		if systemMetrics != nil && systemMetrics.MemoryUsagePercent != nil {
			return *systemMetrics.MemoryUsagePercent, true
		}
		return 0, false
	case "concurrency_queue_depth":
		if systemMetrics != nil && systemMetrics.ConcurrencyQueueDepth != nil {
			return float64(*systemMetrics.ConcurrencyQueueDepth), true
		}
		return 0, false
	case "group_available_accounts":
		if groupID == nil || *groupID <= 0 {
			return 0, false
		}
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		if availability.Group == nil {
			return 0, true
		}
		return float64(availability.Group.AvailableCount), true
	case "group_available_ratio":
		if groupID == nil || *groupID <= 0 {
			return 0, false
		}
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		return computeGroupAvailableRatio(availability.Group), true
	case "account_rate_limited_count":
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		return float64(countAccountsByCondition(availability.Accounts, func(acc *AccountAvailability) bool {
			return acc.IsRateLimited
		})), true
	case "account_error_count":
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		return float64(countAccountsByCondition(availability.Accounts, func(acc *AccountAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})), true
	case "account_temp_unscheduled_count":
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		now := time.Now().UTC()
		return float64(countAccountsByCondition(availability.Accounts, func(acc *AccountAvailability) bool {
			return acc.TempUnschedulableUntil != nil && now.Before(*acc.TempUnschedulableUntil)
		})), true
	case "group_rate_limit_ratio":
		if groupID == nil || *groupID <= 0 {
			return 0, false
		}
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		if availability.Group == nil || availability.Group.TotalAccounts <= 0 {
			return 0, true
		}
		return (float64(availability.Group.RateLimitCount) / float64(availability.Group.TotalAccounts)) * 100, true
	case "account_error_ratio":
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		total := int64(len(availability.Accounts))
		if total <= 0 {
			return 0, true
		}
		errorCount := countAccountsByCondition(availability.Accounts, func(acc *AccountAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})
		return (float64(errorCount) / float64(total)) * 100, true
	case "overload_account_count":
		if s == nil || s.opsService == nil {
			return 0, false
		}
		availability, err := s.opsService.GetAccountAvailability(ctx, platform, groupID)
		if err != nil || availability == nil {
			return 0, false
		}
		return float64(countAccountsByCondition(availability.Accounts, func(acc *AccountAvailability) bool {
			return acc.IsOverloaded
		})), true
	case "proxy_expired_count":
		if s == nil || s.proxyRepo == nil {
			return 0, false
		}
		n, err := s.proxyRepo.CountExpired(ctx)
		if err != nil {
			return 0, false
		}
		return float64(n), true
	case "proxy_expiring_soon_count":
		if s == nil || s.proxyRepo == nil {
			return 0, false
		}
		n, err := s.proxyRepo.CountExpiringSoon(ctx, time.Now())
		if err != nil {
			return 0, false
		}
		return float64(n), true
	}

	overview, err := s.opsRepo.GetDashboardOverview(ctx, &OpsDashboardFilter{
		StartTime: start,
		EndTime:   end,
		Platform:  platform,
		GroupID:   groupID,
		QueryMode: OpsQueryModeRaw,
	})
	if err != nil {
		return 0, false
	}
	if overview == nil {
		return 0, false
	}

	switch strings.TrimSpace(rule.MetricType) {
	case "success_rate":
		if overview.RequestCountSLA <= 0 {
			return 0, false
		}
		return overview.SLA * 100, true
	case "error_rate":
		if overview.RequestCountSLA <= 0 {
			return 0, false
		}
		return overview.ErrorRate * 100, true
	case "upstream_error_rate":
		if overview.RequestCountSLA <= 0 {
			return 0, false
		}
		return overview.UpstreamErrorRate * 100, true
	default:
		return 0, false
	}
}

func compareMetric(value float64, operator string, threshold float64) bool {
	switch strings.TrimSpace(operator) {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return false
	}
}

func buildOpsAlertDimensions(platform string, groupID *int64) map[string]any {
	dims := map[string]any{}
	if strings.TrimSpace(platform) != "" {
		dims["platform"] = strings.TrimSpace(platform)
	}
	if groupID != nil && *groupID > 0 {
		dims["group_id"] = *groupID
	}
	if len(dims) == 0 {
		return nil
	}
	return dims
}

func buildOpsAlertDescription(rule *OpsAlertRule, value float64, windowMinutes int, platform string, groupID *int64) string {
	if rule == nil {
		return ""
	}
	scope := "overall"
	if strings.TrimSpace(platform) != "" {
		scope = fmt.Sprintf("platform=%s", strings.TrimSpace(platform))
	}
	if groupID != nil && *groupID > 0 {
		scope = fmt.Sprintf("%s group_id=%d", scope, *groupID)
	}
	if windowMinutes <= 0 {
		windowMinutes = 1
	}
	return fmt.Sprintf("%s %s %.2f (current %.2f) over last %dm (%s)",
		strings.TrimSpace(rule.MetricType),
		strings.TrimSpace(rule.Operator),
		rule.Threshold,
		value,
		windowMinutes,
		strings.TrimSpace(scope),
	)
}

func (s *OpsAlertEvaluatorService) maybeSendAlertEmail(ctx context.Context, runtimeCfg *OpsAlertRuntimeSettings, rule *OpsAlertRule, event *OpsAlertEvent, accountDetails []*OpsAlertAccountDetail, providedSamples []*opsAlertErrorSample) bool {
	if s == nil || s.emailService == nil || s.opsService == nil || event == nil || rule == nil {
		return false
	}
	if event.EmailSent {
		return false
	}
	// 成功率/错误率继续用于看板和事件统计，但不再发送缺少具体账号原因的聚合邮件。
	if shouldSuppressAggregateRateAlertEmail(rule.MetricType) {
		return false
	}

	emailCfg, err := s.opsService.GetEmailNotificationConfig(ctx)
	if err != nil || emailCfg == nil {
		return false
	}
	if len(emailCfg.Alert.Recipients) == 0 {
		return false
	}

	alertTitle := redactOpsAlertEmailText(event.Title)
	if alertTitle == "" {
		alertTitle = redactOpsAlertEmailText(rule.Name)
	}
	accountRequestAlert := strings.TrimSpace(rule.MetricType) == OpsAlertMetricAccountRequestFailure
	subject := buildOpsAlertEmailSubject(rule, event)
	samples := providedSamples
	if len(samples) == 0 {
		samples = s.collectAlertErrorSamples(ctx, rule, event)
	}
	detailHTML := buildOpsAlertDetailHTML(accountDetails, samples)
	metadata := buildOpsAlertEmailMetadata(accountDetails, samples)
	body := buildOpsAlertEmailBody(rule, event, detailHTML)
	now := time.Now().UTC()

	recordOutcome := func(recipient string, status string, failureReason string, sentAt *time.Time) {
		eventID := event.ID
		s.recordAlertEmailDelivery(ctx, &OpsAlertEmailDeliveryInput{
			AlertEventID:   &eventID,
			IdempotencyKey: fmt.Sprintf("alert:%d:%s:%s", event.ID, notificationEmailHash(recipient), status),
			RecipientEmail: recipient,
			Status:         status,
			Subject:        subject,
			RuleName:       alertTitle,
			Severity:       event.Severity,
			TargetSite:     metadata.TargetSite,
			AccountSummary: metadata.AccountSummary,
			FailureReason:  truncateString(redactOpsAlertEmailText(failureReason), 1000),
			ErrorIDs:       metadata.ErrorIDs,
			DetailHTML:     detailHTML,
			SentAt:         sentAt,
		})
	}

	if !rule.NotifyEmail || !emailCfg.Alert.Enabled {
		reason := "运维告警邮件总开关未开启"
		if !rule.NotifyEmail {
			reason = "该告警规则未开启邮件通知"
		}
		for _, recipient := range emailCfg.Alert.Recipients {
			if addr := strings.TrimSpace(recipient); addr != "" {
				recordOutcome(addr, OpsAlertEmailStatusDisabled, reason, nil)
			}
		}
		return false
	}
	if !shouldSendOpsAlertEmailByMinSeverity(strings.TrimSpace(emailCfg.Alert.MinSeverity), strings.TrimSpace(event.Severity)) {
		for _, recipient := range emailCfg.Alert.Recipients {
			if addr := strings.TrimSpace(recipient); addr != "" {
				recordOutcome(addr, OpsAlertEmailStatusDisabled, "告警级别低于邮件通知阈值", nil)
			}
		}
		return false
	}
	if runtimeCfg != nil && runtimeCfg.Silencing.Enabled && isOpsAlertSilenced(now, rule, event, runtimeCfg.Silencing) {
		for _, recipient := range emailCfg.Alert.Recipients {
			if addr := strings.TrimSpace(recipient); addr != "" {
				recordOutcome(addr, OpsAlertEmailStatusSilenced, "命中运维告警静默规则", nil)
			}
		}
		return false
	}
	if isOpsAlertQuietHours(emailCfg.Alert, now) {
		for _, recipient := range emailCfg.Alert.Recipients {
			if addr := strings.TrimSpace(recipient); addr != "" {
				recordOutcome(addr, OpsAlertEmailStatusQuietHours, event.Description, nil)
			}
		}
		return false
	}

	// 按当前配置更新逐小时限流器。
	s.emailLimiter.SetLimit(emailCfg.Alert.RateLimitPerHour)

	anyDispatched := false
	anySent := false
	for _, to := range emailCfg.Alert.Recipients {
		addr := strings.TrimSpace(to)
		if addr == "" {
			continue
		}
		if !(accountRequestAlert && s.alertOutbox != nil) && !s.emailLimiter.Allow(now) {
			recordOutcome(addr, OpsAlertEmailStatusRateLimited, "超过每小时邮件发送上限", nil)
			continue
		}
		if accountRequestAlert && s.alertOutbox != nil {
			enqueueErr := s.alertOutbox.Enqueue(ctx, &AlertEmailOutboxInput{
				SourceType: AlertEmailSourceOpsAlert,
				SourceID:   strconv.FormatInt(event.ID, 10),
				SourceKey:  fmt.Sprintf("event:%d", event.ID),
				AlertType:  OpsAlertMetricAccountRequestFailure,
				Recipient:  addr,
				Subject:    subject,
				BodyHTML:   body,
				CreatedAt:  now,
			})
			if enqueueErr != nil {
				recordOutcome(addr, OpsAlertEmailStatusFailed, enqueueErr.Error(), nil)
				continue
			}
			anyDispatched = true
			recordOutcome(addr, OpsAlertEmailStatusQueued, "", nil)
			continue
		}

		var sendErr error
		if accountRequestAlert {
			// 账号异常使用固定直白主题和原因优先正文，避免自定义聚合模板重新加入 P0/P1。
			sendErr = s.emailService.SendEmail(ctx, addr, subject, body)
		} else if s.emailService.notificationEmailService != nil {
			sendErr = s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
				Event:          NotificationEmailEventOpsAlert,
				Locale:         notificationEmailLocaleChinese,
				RecipientEmail: addr,
				RecipientName:  emailRecipientName(addr),
				SourceType:     "ops_alert",
				SourceID:       fmt.Sprintf("%d", event.ID),
				Variables:      opsAlertEmailVariables(rule, event, detailHTML),
				RawHTMLVariables: map[string]string{
					"account_detail_html": detailHTML,
				},
			})
			if sendErr != nil && shouldFallbackNotificationEmail(sendErr) {
				sendErr = s.emailService.SendEmail(ctx, addr, subject, body)
			}
		} else {
			sendErr = s.emailService.SendEmail(ctx, addr, subject, body)
		}
		if sendErr != nil {
			recordOutcome(addr, OpsAlertEmailStatusFailed, sendErr.Error(), nil)
			continue
		}
		anySent = true
		anyDispatched = true
		sentAt := time.Now().UTC()
		recordOutcome(addr, OpsAlertEmailStatusSent, "", &sentAt)
	}

	if anySent {
		_ = s.opsRepo.UpdateAlertEventEmailSent(context.Background(), event.ID, true)
	}
	return anyDispatched
}

func buildOpsAlertEmailSubject(rule *OpsAlertRule, event *OpsAlertEvent) string {
	if rule == nil || event == nil {
		return "[运维告警]"
	}
	title := redactOpsAlertEmailText(event.Title)
	if title == "" {
		title = redactOpsAlertEmailText(rule.Name)
	}
	if strings.TrimSpace(rule.MetricType) == OpsAlertMetricAccountRequestFailure {
		return "[运维告警]" + title
	}
	return fmt.Sprintf("[运维告警][%s] %s", redactOpsAlertEmailText(event.Severity), title)
}

func shouldSuppressAggregateRateAlertEmail(metricType string) bool {
	switch strings.TrimSpace(metricType) {
	case "success_rate", "error_rate", "upstream_error_rate":
		return true
	default:
		return false
	}
}

func opsAlertEmailVariables(rule *OpsAlertRule, event *OpsAlertEvent, detailHTML string) map[string]string {
	variables := map[string]string{
		"rule_name":           "-",
		"severity":            "-",
		"alert_status":        "-",
		"metric_type":         "-",
		"operator":            "-",
		"metric_value":        "-",
		"threshold_value":     "-",
		"triggered_at":        time.Now().In(beijingLocation()).Format("2006-01-02 15:04:05 MST"),
		"alert_description":   "-",
		"account_detail_html": "",
	}
	if rule != nil {
		variables["rule_name"] = strings.TrimSpace(rule.Name)
		variables["severity"] = strings.TrimSpace(rule.Severity)
		variables["metric_type"] = strings.TrimSpace(rule.MetricType)
		variables["operator"] = strings.TrimSpace(rule.Operator)
		variables["threshold_value"] = fmt.Sprintf("%.2f", rule.Threshold)
		if strings.TrimSpace(rule.Description) != "" {
			variables["alert_description"] = strings.TrimSpace(rule.Description)
		}
	}
	if event != nil {
		if strings.TrimSpace(event.Title) != "" {
			variables["rule_name"] = strings.TrimSpace(event.Title)
		}
		if strings.TrimSpace(event.Severity) != "" {
			variables["severity"] = strings.TrimSpace(event.Severity)
		}
		variables["alert_status"] = strings.TrimSpace(event.Status)
		if event.MetricValue != nil {
			variables["metric_value"] = fmt.Sprintf("%.2f", *event.MetricValue)
		}
		if event.ThresholdValue != nil {
			variables["threshold_value"] = fmt.Sprintf("%.2f", *event.ThresholdValue)
		}
		if !event.FiredAt.IsZero() {
			variables["triggered_at"] = event.FiredAt.In(beijingLocation()).Format("2006-01-02 15:04:05 MST")
		}
		if strings.TrimSpace(event.Description) != "" {
			variables["alert_description"] = strings.TrimSpace(event.Description)
		}
	}
	if strings.TrimSpace(detailHTML) != "" {
		variables["account_detail_html"] = detailHTML
	}
	for key, value := range variables {
		if key != "account_detail_html" {
			variables[key] = redactOpsAlertEmailText(value)
		}
	}
	return variables
}

func buildOpsAlertEmailBody(rule *OpsAlertRule, event *OpsAlertEvent, detailHTML string) string {
	if rule == nil || event == nil {
		return ""
	}
	if strings.TrimSpace(rule.MetricType) == OpsAlertMetricAccountRequestFailure {
		return fmt.Sprintf(`
<h2 style="margin:0 0 12px">%s</h2>
<p><b>异常账号</b>：%s</p>
<p><b>发生时间</b>：%s</p>
%s
`,
			htmlEscape(redactOpsAlertEmailText(event.Description)),
			htmlEscape(redactOpsAlertEmailText(event.Title)),
			event.FiredAt.In(beijingLocation()).Format("2006-01-02 15:04:05 MST"),
			detailHTML,
		)
	}
	metric := strings.TrimSpace(rule.MetricType)
	value := "-"
	threshold := fmt.Sprintf("%.2f", rule.Threshold)
	if event.MetricValue != nil {
		value = fmt.Sprintf("%.2f", *event.MetricValue)
	}
	if event.ThresholdValue != nil {
		threshold = fmt.Sprintf("%.2f", *event.ThresholdValue)
	}
	return fmt.Sprintf(`
<h2>运维告警</h2>
<p><b>规则</b>：%s</p>
<p><b>严重级别</b>：%s</p>
<p><b>状态</b>：%s</p>
<p><b>指标</b>：%s %s %s</p>
<p><b>触发时间</b>：%s</p>
<p><b>说明</b>：%s</p>
%s
`,
		htmlEscape(redactOpsAlertEmailText(event.Title)),
		htmlEscape(redactOpsAlertEmailText(event.Severity)),
		htmlEscape(event.Status),
		htmlEscape(metric),
		htmlEscape(rule.Operator),
		htmlEscape(fmt.Sprintf("%s (threshold %s)", value, threshold)),
		event.FiredAt.In(beijingLocation()).Format("2006-01-02 15:04:05 MST"),
		htmlEscape(redactOpsAlertEmailText(event.Description)),
		detailHTML,
	)
}

func buildOpsAlertAccountDetailHTML(details []*OpsAlertAccountDetail, limit int) string {
	if len(details) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(details) {
		limit = len(details)
	}

	var blocks strings.Builder
	for _, detail := range details[:limit] {
		if detail == nil {
			continue
		}
		group := strings.TrimSpace(detail.GroupName)
		if group == "" && detail.GroupID != nil {
			group = fmt.Sprintf("ID %d", *detail.GroupID)
		}
		user := "-"
		if detail.UserID != nil {
			user = fmt.Sprintf("%s（ID %d）", valueOrDash(detail.UserEmail), *detail.UserID)
		} else if strings.TrimSpace(detail.UserEmail) != "" {
			user = detail.UserEmail
		}
		apiKey := "-"
		if detail.APIKeyID != nil {
			apiKey = fmt.Sprintf("%s（ID %d）", valueOrDash(detail.APIKeyName), *detail.APIKeyID)
		} else if strings.TrimSpace(detail.APIKeyName) != "" {
			apiKey = detail.APIKeyName
		}
		models := valueOrDash(detail.RequestedModel) + " / " + valueOrDash(detail.UpstreamModel)
		requestIDs := uniqueNonEmptyStrings(detail.RequestID, detail.ClientRequestID)
		_, _ = fmt.Fprintf(&blocks, `<div style="margin:10px 0;padding:12px;border:1px solid #e5e7eb;border-radius:8px;background:#fafafa">
<div style="font-size:16px;font-weight:700;margin-bottom:8px">%s</div>
<div><strong>错误原因：</strong>%s</div>
<div><strong>账号：</strong>%s（ID %d）</div>
<div><strong>用户：</strong>%s</div>
<div><strong>API Key：</strong>%s</div>
<div><strong>平台 / 分组：</strong>%s / %s</div>
<div><strong>请求模型 / 上游模型：</strong>%s</div>
<div><strong>状态 / 阶段：</strong>%d / %s</div>
<div><strong>请求 ID：</strong>%s</div>
<div><strong>发生时间：</strong>%s</div>
<pre style="white-space:pre-wrap;word-break:break-word;margin:8px 0 0;padding:8px;background:#f3f4f6;border-radius:6px;font-size:12px">%s</pre>
</div>`,
			htmlEscape(redactOpsAlertEmailText(detail.ErrorReason)),
			htmlEscape(redactOpsAlertEmailText(detail.ErrorReason)),
			htmlEscape(redactOpsAlertEmailText(detail.AccountName)),
			detail.AccountID,
			htmlEscape(redactOpsAlertEmailText(user)),
			htmlEscape(redactOpsAlertEmailText(apiKey)),
			htmlEscape(redactOpsAlertEmailText(detail.Platform)),
			htmlEscape(redactOpsAlertEmailText(group)),
			htmlEscape(redactOpsAlertEmailText(models)),
			detail.StatusCode,
			htmlEscape(opsAccountRequestPhaseLabel(detail.ErrorPhase)),
			htmlEscape(redactOpsAlertEmailText(strings.Join(requestIDs, " / "))),
			detail.OccurredAt.In(beijingLocation()).Format("2006-01-02 15:04:05 MST"),
			htmlEscape(redactOpsAlertEmailText(detail.ErrorMessage)),
		)
	}
	if remaining := len(details) - limit; remaining > 0 {
		_, _ = fmt.Fprintf(&blocks, `<div style="color:#6b7280;font-size:12px">另有 %d 个异常账号，请前往运维监控查看完整明细。</div>`, remaining)
	}

	return `<p><strong>异常账号明细</strong>：</p>` + blocks.String()
}

func shouldSendOpsAlertEmailByMinSeverity(minSeverity string, ruleSeverity string) bool {
	minSeverity = strings.ToLower(strings.TrimSpace(minSeverity))
	if minSeverity == "" {
		return true
	}

	eventLevel := opsEmailSeverityForOps(ruleSeverity)
	minLevel := strings.ToLower(minSeverity)

	rank := func(level string) int {
		switch level {
		case "critical":
			return 3
		case "warning":
			return 2
		case "info":
			return 1
		default:
			return 0
		}
	}
	return rank(eventLevel) >= rank(minLevel)
}

func opsEmailSeverityForOps(severity string) string {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case "P0":
		return "critical"
	case "P1":
		return "warning"
	default:
		return "info"
	}
}

func isOpsAlertSilenced(now time.Time, rule *OpsAlertRule, event *OpsAlertEvent, silencing OpsAlertSilencingSettings) bool {
	if !silencing.Enabled {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(silencing.GlobalUntilRFC3339) != "" {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(silencing.GlobalUntilRFC3339)); err == nil {
			if now.Before(t) {
				return true
			}
		}
	}

	for _, entry := range silencing.Entries {
		untilRaw := strings.TrimSpace(entry.UntilRFC3339)
		if untilRaw == "" {
			continue
		}
		until, err := time.Parse(time.RFC3339, untilRaw)
		if err != nil {
			continue
		}
		if now.After(until) {
			continue
		}
		if entry.RuleID != nil && rule != nil && rule.ID > 0 && *entry.RuleID != rule.ID {
			continue
		}
		if len(entry.Severities) > 0 {
			match := false
			for _, s := range entry.Severities {
				if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(event.Severity)) || strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(rule.Severity)) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		return true
	}

	return false
}

func (s *OpsAlertEvaluatorService) tryAcquireLeaderLock(ctx context.Context, lock OpsDistributedLockSettings) (func(), bool) {
	if !lock.Enabled {
		return nil, true
	}
	if s.redisClient == nil {
		s.warnNoRedisOnce.Do(func() {
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] redis not configured; running without distributed lock")
		})
		return nil, true
	}
	key := strings.TrimSpace(lock.Key)
	if key == "" {
		key = opsAlertEvaluatorLeaderLockKey
	}
	ttl := time.Duration(lock.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = opsAlertEvaluatorLeaderLockTTL
	}

	ok, err := s.redisClient.SetNX(ctx, key, s.instanceID, ttl).Result()
	if err != nil {
		// Prefer fail-closed to avoid duplicate evaluators stampeding the DB when Redis is flaky.
		// Single-node deployments can disable the distributed lock via runtime settings.
		s.warnNoRedisOnce.Do(func() {
			logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] leader lock SetNX failed; skipping this cycle: %v", err)
		})
		return nil, false
	}
	if !ok {
		s.maybeLogSkip(key)
		return nil, false
	}
	return func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		_, _ = opsAlertEvaluatorReleaseScript.Run(releaseCtx, s.redisClient, []string{key}, s.instanceID).Result()
	}, true
}

func (s *OpsAlertEvaluatorService) maybeLogSkip(key string) {
	s.skipLogMu.Lock()
	defer s.skipLogMu.Unlock()

	now := time.Now()
	if !s.skipLogAt.IsZero() && now.Sub(s.skipLogAt) < opsAlertEvaluatorSkipLogInterval {
		return
	}
	s.skipLogAt = now
	logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] leader lock held by another instance; skipping (key=%q)", key)
}

func (s *OpsAlertEvaluatorService) recordHeartbeatSuccess(runAt time.Time, duration time.Duration, result string) {
	if s == nil || s.opsRepo == nil {
		return
	}
	now := time.Now().UTC()
	durMs := duration.Milliseconds()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg := strings.TrimSpace(result)
	if msg == "" {
		msg = "ok"
	}
	msg = truncateString(msg, 2048)
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsAlertEvaluatorJobName,
		LastRunAt:      &runAt,
		LastSuccessAt:  &now,
		LastDurationMs: &durMs,
		LastResult:     &msg,
	})
}

func (s *OpsAlertEvaluatorService) recordHeartbeatError(runAt time.Time, duration time.Duration, err error) {
	if s == nil || s.opsRepo == nil || err == nil {
		return
	}
	now := time.Now().UTC()
	durMs := duration.Milliseconds()
	msg := truncateString(err.Error(), 2048)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.opsRepo.UpsertJobHeartbeat(ctx, &OpsUpsertJobHeartbeatInput{
		JobName:        opsAlertEvaluatorJobName,
		LastRunAt:      &runAt,
		LastErrorAt:    &now,
		LastError:      &msg,
		LastDurationMs: &durMs,
	})
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}

type slidingWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	sent   []time.Time
}

func newSlidingWindowLimiter(limit int, window time.Duration) *slidingWindowLimiter {
	if window <= 0 {
		window = time.Hour
	}
	return &slidingWindowLimiter{
		limit:  limit,
		window: window,
		sent:   []time.Time{},
	}
}

func (l *slidingWindowLimiter) SetLimit(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
}

func (l *slidingWindowLimiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.limit <= 0 {
		return true
	}
	cutoff := now.Add(-l.window)
	keep := l.sent[:0]
	for _, t := range l.sent {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	l.sent = keep
	if len(l.sent) >= l.limit {
		return false
	}
	l.sent = append(l.sent, now)
	return true
}

// computeGroupAvailableRatio returns the available percentage for a group.
// Formula: (AvailableCount / TotalAccounts) * 100.
// Returns 0 when TotalAccounts is 0.
func computeGroupAvailableRatio(group *GroupAvailability) float64 {
	if group == nil || group.TotalAccounts <= 0 {
		return 0
	}
	return (float64(group.AvailableCount) / float64(group.TotalAccounts)) * 100
}

// countAccountsByCondition counts accounts that satisfy the given condition.
func countAccountsByCondition(accounts map[int64]*AccountAvailability, condition func(*AccountAvailability) bool) int64 {
	if len(accounts) == 0 || condition == nil {
		return 0
	}
	var count int64
	for _, account := range accounts {
		if account != nil && condition(account) {
			count++
		}
	}
	return count
}
