package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	emailSendTimeout = 30 * time.Second

	// Threshold type values
	thresholdTypeFixed      = "fixed"
	thresholdTypePercentage = "percentage"

	// Quota dimension labels
	quotaDimDaily  = "daily"
	quotaDimWeekly = "weekly"
	quotaDimTotal  = "total"

	defaultSiteName = "Sub2API"

	dailyLowBalanceReminderThreshold    = 2.0
	dailyLowBalanceReminderHour         = 9
	dailyLowBalanceReminderTimeout      = 2 * time.Hour
	dailyLowBalanceReminderLockKey      = "balance:daily-low-reminder"
	insufficientBalanceNotifyCooldown   = 3 * time.Minute
	insufficientBalanceNotifyLockPrefix = "balance:insufficient-notify:"

	balanceNotifyRelayKey           = "balance:notify:relay"
	balanceNotifyRelayProcessingKey = "balance:notify:processing"
	balanceNotifyRelayMaxBatches    = 4096
	balanceNotifyRelayTimeout       = time.Second
)

// quotaDimLabels maps dimension names to display labels.
var quotaDimLabels = map[string]string{
	quotaDimDaily:  "日限额",
	quotaDimWeekly: "周限额",
	quotaDimTotal:  "总限额",
}

// AccountQuotaReader provides read access to account quota data.
type AccountQuotaReader interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
}

// LowBalanceReminderRepository 只返回有真实付费使用记录的低余额用户。
type LowBalanceReminderRepository interface {
	ListLowBalanceReminderUsers(ctx context.Context, threshold float64) ([]*User, error)
}

// BalanceNotifyService handles balance and quota threshold notifications.
type BalanceNotifyService struct {
	emailService             *EmailService
	settingRepo              SettingRepository
	accountRepo              AccountQuotaReader
	notificationEmailService *NotificationEmailService
	dailyReminderRepo        LowBalanceReminderRepository
	leaderLockCache          LeaderLockCache
	leaderLockDB             *sql.DB
	redisClient              *redis.Client
	instanceID               string
	dailyStartOnce           sync.Once
	relayStartOnce           sync.Once
	relayStopOnce            sync.Once
	relayStopCh              chan struct{}
	relayWG                  sync.WaitGroup
	relayEventCh             chan *balanceNotifyRelayEvent
	insufficientNotifyGroup  singleflight.Group
	insufficientNotifyMu     sync.Mutex
	insufficientNotifyUntil  map[int64]time.Time
	now                      func() time.Time
	deliveryEnabled          bool
	relayOnly                bool
}

type balanceNotifyRelayEvent struct {
	ID             string                       `json:"id"`
	Type           string                       `json:"type"`
	User           *balanceNotifyRelayUser      `json:"user,omitempty"`
	AccountID      int64                        `json:"account_id,omitempty"`
	AccountName    string                       `json:"account_name,omitempty"`
	Platform       string                       `json:"platform,omitempty"`
	OldBalance     float64                      `json:"old_balance,omitempty"`
	CurrentBalance float64                      `json:"current_balance,omitempty"`
	Cost           float64                      `json:"cost,omitempty"`
	QuotaDims      []balanceNotifyRelayQuotaDim `json:"quota_dims,omitempty"`
}

type balanceNotifyRelayUser struct {
	ID             int64              `json:"id"`
	Email          string             `json:"email"`
	Username       string             `json:"username"`
	NotifyEnabled  bool               `json:"notify_enabled"`
	ThresholdType  string             `json:"threshold_type"`
	Threshold      *float64           `json:"threshold,omitempty"`
	ExtraEmails    []NotifyEmailEntry `json:"extra_emails,omitempty"`
	TotalRecharged float64            `json:"total_recharged"`
}

type balanceNotifyRelayQuotaDim struct {
	Name          string  `json:"name"`
	Enabled       bool    `json:"enabled"`
	Threshold     float64 `json:"threshold"`
	ThresholdType string  `json:"threshold_type"`
	CurrentUsed   float64 `json:"current_used"`
	Limit         float64 `json:"limit"`
}

// NewBalanceNotifyService creates a new BalanceNotifyService.
func NewBalanceNotifyService(emailService *EmailService, settingRepo SettingRepository, accountRepo AccountQuotaReader) *BalanceNotifyService {
	return &BalanceNotifyService{
		emailService:    emailService,
		settingRepo:     settingRepo,
		accountRepo:     accountRepo,
		instanceID:      uuid.NewString(),
		now:             time.Now,
		deliveryEnabled: true,
	}
}

func (s *BalanceNotifyService) SetNotificationEmailService(notificationEmailService *NotificationEmailService) {
	s.notificationEmailService = notificationEmailService
}

// SetDailyReminderDependencies 注入每日余额提醒所需的查询与跨实例互斥能力。
func (s *BalanceNotifyService) SetDailyReminderDependencies(repo LowBalanceReminderRepository, lockCache LeaderLockCache, db *sql.DB) {
	s.dailyReminderRepo = repo
	s.leaderLockCache = lockCache
	s.leaderLockDB = db
}

// ConfigureRelay 配置跨节点通知中继；api_only 仅生产，primary 仅消费。
func (s *BalanceNotifyService) ConfigureRelay(redisClient *redis.Client, relayOnly bool) {
	if s == nil {
		return
	}
	s.redisClient = redisClient
	s.relayOnly = relayOnly
	s.deliveryEnabled = !relayOnly
	if redisClient == nil {
		return
	}
	s.relayStartOnce.Do(func() {
		s.relayStopCh = make(chan struct{})
		s.relayEventCh = make(chan *balanceNotifyRelayEvent, 256)
		s.relayWG.Add(1)
		if relayOnly {
			go s.runBalanceNotifyRelayProducer()
		} else {
			go s.runBalanceNotifyRelayConsumer()
		}
	})
}

func (s *BalanceNotifyService) Stop() {
	if s == nil {
		return
	}
	s.relayStopOnce.Do(func() {
		if s.relayStopCh != nil {
			close(s.relayStopCh)
		}
	})
	s.relayWG.Wait()
}

func (s *BalanceNotifyService) enqueueRelayEvent(event *balanceNotifyRelayEvent) {
	if s == nil || !s.relayOnly || event == nil || s.relayEventCh == nil {
		return
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	select {
	case s.relayEventCh <- event:
	default:
		slog.Warn("余额通知中继队列已满，丢弃本次通知", "event_type", event.Type)
	}
}

func (s *BalanceNotifyService) runBalanceNotifyRelayProducer() {
	defer s.relayWG.Done()
	for {
		select {
		case event := <-s.relayEventCh:
			s.publishBalanceNotifyRelayEvent(event)
		case <-s.relayStopCh:
			for {
				select {
				case event := <-s.relayEventCh:
					s.publishBalanceNotifyRelayEvent(event)
				default:
					return
				}
			}
		}
	}
}

func (s *BalanceNotifyService) publishBalanceNotifyRelayEvent(event *balanceNotifyRelayEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("编码余额通知中继事件失败", "event_type", event.Type, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), balanceNotifyRelayTimeout)
	defer cancel()
	pipe := s.redisClient.TxPipeline()
	pipe.RPush(ctx, balanceNotifyRelayKey, payload)
	pipe.LTrim(ctx, balanceNotifyRelayKey, -balanceNotifyRelayMaxBatches, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("发布余额通知中继事件失败", "event_type", event.Type, "error", err)
	}
}

func (s *BalanceNotifyService) runBalanceNotifyRelayConsumer() {
	defer s.relayWG.Done()
	s.recoverBalanceNotifyRelayProcessing()
	for {
		select {
		case <-s.relayStopCh:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*balanceNotifyRelayTimeout)
		payload, err := s.redisClient.BLMove(
			ctx, balanceNotifyRelayKey, balanceNotifyRelayProcessingKey,
			"LEFT", "RIGHT", balanceNotifyRelayTimeout,
		).Result()
		cancel()
		if err != nil {
			if err != redis.Nil && err != context.DeadlineExceeded && err != context.Canceled {
				slog.Error("消费余额通知中继事件失败", "error", err)
			}
			continue
		}
		select {
		case <-s.relayStopCh:
			s.retryBalanceNotifyRelay(payload)
			return
		default:
		}
		var event balanceNotifyRelayEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			slog.Error("解码余额通知中继事件失败", "error", err)
			s.ackBalanceNotifyRelay(payload)
			continue
		}
		if s.handleBalanceNotifyRelayEvent(&event) {
			s.ackBalanceNotifyRelay(payload)
			continue
		}
		s.retryBalanceNotifyRelay(payload)
		timer := time.NewTimer(balanceNotifyRelayTimeout)
		select {
		case <-timer.C:
		case <-s.relayStopCh:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (s *BalanceNotifyService) recoverBalanceNotifyRelayProcessing() {
	ctx, cancel := context.WithTimeout(context.Background(), balanceNotifyRelayTimeout)
	defer cancel()
	for {
		payload, err := s.redisClient.RPopLPush(ctx, balanceNotifyRelayProcessingKey, balanceNotifyRelayKey).Result()
		if err == redis.Nil {
			return
		}
		if err != nil {
			slog.Error("恢复余额通知中继事件失败", "error", err)
			return
		}
		if payload == "" {
			return
		}
	}
}

func (s *BalanceNotifyService) ackBalanceNotifyRelay(payload string) {
	ctx, cancel := context.WithTimeout(context.Background(), balanceNotifyRelayTimeout)
	defer cancel()
	if err := s.redisClient.LRem(ctx, balanceNotifyRelayProcessingKey, 1, payload).Err(); err != nil {
		slog.Error("确认余额通知中继事件失败", "error", err)
	}
}

func (s *BalanceNotifyService) retryBalanceNotifyRelay(payload string) {
	ctx, cancel := context.WithTimeout(context.Background(), balanceNotifyRelayTimeout)
	defer cancel()
	pipe := s.redisClient.TxPipeline()
	pipe.LRem(ctx, balanceNotifyRelayProcessingKey, 1, payload)
	pipe.RPush(ctx, balanceNotifyRelayKey, payload)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("重试余额通知中继事件失败", "error", err)
	}
}

func (s *BalanceNotifyService) handleBalanceNotifyRelayEvent(event *balanceNotifyRelayEvent) bool {
	if event == nil {
		return true
	}
	switch event.Type {
	case "balance_low":
		return s.handleRelayedBalanceLow(event)
	case "insufficient_balance":
		return s.handleRelayedInsufficientBalance(event)
	case "account_quota":
		return s.handleRelayedAccountQuota(event)
	default:
		return true
	}
}

func (s *BalanceNotifyService) handleRelayedBalanceLow(event *balanceNotifyRelayEvent) bool {
	user := event.User.toUser()
	if user == nil || !s.canNotifyBalance(user) {
		return true
	}
	ctx := context.Background()
	effectiveThreshold, rechargeURL, ok := s.resolveUserEffectiveThreshold(ctx, user)
	if !ok {
		return true
	}
	newBalance := event.OldBalance - event.Cost
	if !crossedDownward(event.OldBalance, newBalance, effectiveThreshold) {
		return true
	}
	return s.sendBalanceLowEmails(
		s.collectBalanceNotifyRecipients(user), user.ID, user.Username, user.Email,
		newBalance, effectiveThreshold, s.getSiteName(ctx), rechargeURL,
	)
}

func (s *BalanceNotifyService) handleRelayedInsufficientBalance(event *balanceNotifyRelayEvent) bool {
	user := event.User.toUser()
	if user == nil || s.emailService == nil || strings.TrimSpace(user.Email) == "" {
		return false
	}
	release, acquired := s.acquireInsufficientBalanceNotifyCooldown(user.ID)
	if !acquired {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
	defer cancel()
	handled := s.sendInsufficientBalanceEmail(
		ctx, user.ID, user.Username, user.Email, event.CurrentBalance,
		s.getSiteName(ctx), s.getInsufficientBalanceRechargeURL(ctx),
	)
	if !handled {
		release()
	}
	return handled
}

func (s *BalanceNotifyService) handleRelayedAccountQuota(event *balanceNotifyRelayEvent) bool {
	ctx := context.Background()
	if !s.isAccountQuotaNotifyEnabled(ctx) {
		return true
	}
	adminEmails := s.getAccountQuotaNotifyEmails(ctx)
	if len(adminEmails) == 0 {
		return true
	}
	dims := make([]quotaDim, 0, len(event.QuotaDims))
	for _, dim := range event.QuotaDims {
		dims = append(dims, quotaDim{
			name: dim.Name, enabled: dim.Enabled, threshold: dim.Threshold,
			thresholdType: dim.ThresholdType, currentUsed: dim.CurrentUsed, limit: dim.Limit,
		})
	}
	account := &Account{ID: event.AccountID, Name: event.AccountName, Platform: event.Platform}
	siteName := s.getSiteName(ctx)
	for _, dim := range dims {
		if !dim.enabled || dim.threshold <= 0 {
			continue
		}
		effectiveThreshold := dim.resolvedThreshold()
		if effectiveThreshold <= 0 {
			continue
		}
		oldUsed := dim.currentUsed - event.Cost
		if oldUsed < effectiveThreshold && dim.currentUsed >= effectiveThreshold {
			if !s.sendQuotaAlertEmails(adminEmails, account.ID, account.Name, account.Platform, dim, dim.currentUsed, siteName) {
				return false
			}
		}
	}
	return true
}

func (u *balanceNotifyRelayUser) toUser() *User {
	if u == nil {
		return nil
	}
	return &User{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		BalanceNotifyEnabled:       u.NotifyEnabled,
		BalanceNotifyThresholdType: u.ThresholdType,
		BalanceNotifyThreshold:     u.Threshold,
		BalanceNotifyExtraEmails:   u.ExtraEmails,
		TotalRecharged:             u.TotalRecharged,
	}
}

func balanceNotifyRelayUserFrom(user *User) *balanceNotifyRelayUser {
	if user == nil {
		return nil
	}
	return &balanceNotifyRelayUser{
		ID:             user.ID,
		Email:          user.Email,
		Username:       user.Username,
		NotifyEnabled:  user.BalanceNotifyEnabled,
		ThresholdType:  user.BalanceNotifyThresholdType,
		Threshold:      user.BalanceNotifyThreshold,
		ExtraEmails:    user.BalanceNotifyExtraEmails,
		TotalRecharged: user.TotalRecharged,
	}
}

// Start 启动北京时间每天 09:00 的低余额提醒任务。
func (s *BalanceNotifyService) Start() {
	if s == nil || s.dailyReminderRepo == nil {
		return
	}
	s.dailyStartOnce.Do(func() {
		go s.runDailyLowBalanceReminder()
	})
}

// resolveBalanceThreshold returns the effective balance threshold.
// For percentage type, it computes threshold = totalRecharged * percentage / 100.
func resolveBalanceThreshold(threshold float64, thresholdType string, totalRecharged float64) float64 {
	if thresholdType == thresholdTypePercentage && totalRecharged > 0 {
		return totalRecharged * threshold / 100
	}
	return threshold
}

// CheckBalanceAfterDeduction checks if balance crossed below threshold after deduction.
// Notification is sent only on first crossing: oldBalance >= threshold && newBalance < threshold.
func (s *BalanceNotifyService) CheckBalanceAfterDeduction(ctx context.Context, user *User, oldBalance, cost float64) {
	if s == nil || user == nil {
		return
	}
	if s.relayOnly {
		s.enqueueRelayEvent(&balanceNotifyRelayEvent{
			Type:       "balance_low",
			User:       balanceNotifyRelayUserFrom(user),
			OldBalance: oldBalance,
			Cost:       cost,
		})
		return
	}
	if !s.deliveryEnabled || !s.canNotifyBalance(user) {
		return
	}
	effectiveThreshold, rechargeURL, ok := s.resolveUserEffectiveThreshold(ctx, user)
	if !ok {
		return
	}
	newBalance := oldBalance - cost
	if !crossedDownward(oldBalance, newBalance, effectiveThreshold) {
		return
	}
	s.dispatchBalanceLowEmail(ctx, user, newBalance, effectiveThreshold, rechargeURL)
}

// NotifyUserInsufficientBalance 在请求因用户自身余额不足而被拒绝时通知该用户。
// 同一用户三分钟内的连续重试合并为一封，避免客户端自动重试造成邮件轰炸。
func (s *BalanceNotifyService) NotifyUserInsufficientBalance(_ context.Context, user *User, currentBalance float64) {
	if s == nil || user == nil || strings.TrimSpace(user.Email) == "" {
		return
	}
	if s.relayOnly {
		s.enqueueRelayEvent(&balanceNotifyRelayEvent{
			Type:           "insufficient_balance",
			User:           balanceNotifyRelayUserFrom(user),
			CurrentBalance: currentBalance,
		})
		return
	}
	if !s.deliveryEnabled || s.emailService == nil {
		return
	}

	userID := user.ID
	userName := user.Username
	userEmail := user.Email
	singleflightKey := strconv.FormatInt(userID, 10)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("发送用户余额不足提醒时发生异常", "user_id", userID, "recover", recovered)
			}
		}()
		_, _, _ = s.insufficientNotifyGroup.Do(singleflightKey, func() (any, error) {
			release, acquired := s.acquireInsufficientBalanceNotifyCooldown(userID)
			if !acquired {
				return nil, nil
			}
			backgroundCtx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
			defer cancel()
			handled := s.sendInsufficientBalanceEmail(
				backgroundCtx, userID, userName, userEmail, currentBalance,
				s.getSiteName(backgroundCtx), s.getInsufficientBalanceRechargeURL(backgroundCtx),
			)
			if !handled {
				release()
			}
			return nil, nil
		})
	}()
}

func (s *BalanceNotifyService) sendInsufficientBalanceEmail(ctx context.Context, userID int64, userName, userEmail string, balance float64, siteName, rechargeURL string) bool {
	displayName := userName
	if strings.TrimSpace(displayName) == "" {
		displayName = userEmail
	}
	subject := fmt.Sprintf("[%s] 余额不足提醒", sanitizeEmailHeader(siteName))
	body := s.buildInsufficientBalanceEmailBody(html.EscapeString(displayName), balance, html.EscapeString(siteName), rechargeURL)
	if err := s.emailService.SendEmail(ctx, userEmail, subject, body); err != nil {
		slog.Error("发送余额不足请求提醒失败", "user_id", userID, "error", err)
		return false
	}
	slog.Info("余额不足请求提醒发送成功", "user_id", userID)
	return true
}

// getInsufficientBalanceRechargeURL 只读取充值地址，不使用余额提醒开关或阈值阻断事务邮件。
func (s *BalanceNotifyService) getInsufficientBalanceRechargeURL(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return ""
	}
	rechargeURL, err := s.settingRepo.GetValue(ctx, SettingKeyBalanceLowNotifyRechargeURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(rechargeURL)
}

func (s *BalanceNotifyService) acquireInsufficientBalanceNotifyCooldown(userID int64) (func(), bool) {
	now := s.currentTime()
	reservationID := uuid.NewString()
	lockKey := insufficientBalanceNotifyLockPrefix + strconv.FormatInt(userID, 10)
	owner := s.instanceID + ":" + reservationID

	if s.leaderLockCache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		acquired, err := s.leaderLockCache.TryAcquireLeaderLock(ctx, lockKey, owner, insufficientBalanceNotifyCooldown)
		cancel()
		if err == nil {
			if !acquired {
				return func() {}, false
			}
			return func() {
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer releaseCancel()
				if releaseErr := s.leaderLockCache.ReleaseLeaderLock(releaseCtx, lockKey, owner); releaseErr != nil {
					slog.Warn("释放余额不足邮件冷却锁失败", "user_id", userID, "error", releaseErr)
				}
			}, true
		}
		slog.Warn("获取余额不足邮件冷却锁失败，回退进程内冷却", "user_id", userID, "error", err)
	}

	s.insufficientNotifyMu.Lock()
	defer s.insufficientNotifyMu.Unlock()
	if until := s.insufficientNotifyUntil[userID]; now.Before(until) {
		return func() {}, false
	}
	if s.insufficientNotifyUntil == nil {
		s.insufficientNotifyUntil = make(map[int64]time.Time)
	}
	until := now.Add(insufficientBalanceNotifyCooldown)
	s.insufficientNotifyUntil[userID] = until
	return func() {
		s.insufficientNotifyMu.Lock()
		defer s.insufficientNotifyMu.Unlock()
		if s.insufficientNotifyUntil[userID].Equal(until) {
			delete(s.insufficientNotifyUntil, userID)
		}
	}, true
}

func (s *BalanceNotifyService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *BalanceNotifyService) runDailyLowBalanceReminder() {
	for {
		nextRun := nextDailyLowBalanceReminder(time.Now())
		timer := time.NewTimer(time.Until(nextRun))
		<-timer.C
		s.sendDailyLowBalanceReminders()
	}
}

func nextDailyLowBalanceReminder(now time.Time) time.Time {
	localNow := now.In(beijingLocation())
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), dailyLowBalanceReminderHour, 0, 0, 0, localNow.Location())
	if !localNow.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func balanceReminderDay(now time.Time) string {
	return now.In(beijingLocation()).Format("2006-01-02")
}

func (s *BalanceNotifyService) sendDailyLowBalanceReminders() {
	ctx, cancel := context.WithTimeout(context.Background(), dailyLowBalanceReminderTimeout)
	defer cancel()

	release, acquired := tryAcquireSingletonLeaderLock(
		ctx,
		s.leaderLockCache,
		s.leaderLockDB,
		dailyLowBalanceReminderLockKey,
		s.instanceID,
		dailyLowBalanceReminderTimeout,
	)
	if !acquired {
		return
	}
	defer release()

	enabled, _, rechargeURL := s.getBalanceNotifyConfig(ctx)
	if !enabled {
		return
	}
	users, err := s.dailyReminderRepo.ListLowBalanceReminderUsers(ctx, dailyLowBalanceReminderThreshold)
	if err != nil {
		slog.Error("查询每日低余额提醒用户失败", "error", err)
		return
	}
	siteName := s.getSiteName(ctx)
	for _, user := range users {
		if user == nil || strings.TrimSpace(user.Email) == "" {
			continue
		}
		s.sendBalanceLowEmails(
			[]string{user.Email},
			user.ID,
			user.Username,
			user.Email,
			user.Balance,
			dailyLowBalanceReminderThreshold,
			siteName,
			rechargeURL,
		)
	}
}

// canNotifyBalance checks nil guards and user-level toggle.
func (s *BalanceNotifyService) canNotifyBalance(user *User) bool {
	if user == nil || s.emailService == nil || s.settingRepo == nil {
		return false
	}
	return user.BalanceNotifyEnabled
}

// resolveUserEffectiveThreshold reads global + user config, returns the effective threshold.
// Returns ok=false when notifications should be skipped.
func (s *BalanceNotifyService) resolveUserEffectiveThreshold(ctx context.Context, user *User) (effectiveThreshold float64, rechargeURL string, ok bool) {
	globalEnabled, globalThreshold, rechargeURL := s.getBalanceNotifyConfig(ctx)
	if !globalEnabled {
		return 0, "", false
	}
	threshold := globalThreshold
	if user.BalanceNotifyThreshold != nil {
		threshold = *user.BalanceNotifyThreshold
	}
	if threshold <= 0 {
		return 0, "", false
	}
	effectiveThreshold = resolveBalanceThreshold(threshold, user.BalanceNotifyThresholdType, user.TotalRecharged)
	if effectiveThreshold <= 0 {
		return 0, "", false
	}
	return effectiveThreshold, rechargeURL, true
}

// crossedDownward returns true when oldV was at-or-above threshold but newV dropped below it.
func crossedDownward(oldV, newV, threshold float64) bool {
	return oldV >= threshold && newV < threshold
}

// dispatchBalanceLowEmail collects recipients and sends the alert in a goroutine.
func (s *BalanceNotifyService) dispatchBalanceLowEmail(ctx context.Context, user *User, newBalance, threshold float64, rechargeURL string) {
	siteName := s.getSiteName(ctx)
	recipients := s.collectBalanceNotifyRecipients(user)
	slog.Info("CheckBalanceAfterDeduction: sending notification",
		"user_id", user.ID, "recipients", recipients, "new_balance", newBalance, "threshold", threshold)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in balance notification", "recover", r)
			}
		}()
		s.sendBalanceLowEmails(recipients, user.ID, user.Username, user.Email, newBalance, threshold, siteName, rechargeURL)
	}()
}

// quotaDim describes one quota dimension for notification checking.
type quotaDim struct {
	name          string
	enabled       bool
	threshold     float64
	thresholdType string // "fixed" (default) or "percentage"
	currentUsed   float64
	limit         float64
}

// resolvedThreshold converts the user-facing "remaining" threshold into a usage-based trigger point.
// The threshold represents how much quota REMAINS when the alert fires:
//   - Fixed ($): threshold=400, limit=1000 → fires when usage reaches 600 (remaining drops to 400)
//   - Percentage (%): threshold=30, limit=1000 → fires when usage reaches 700 (remaining drops to 30%)
func (d quotaDim) resolvedThreshold() float64 {
	if d.limit <= 0 {
		return 0
	}
	if d.thresholdType == thresholdTypePercentage {
		return d.limit * (1 - d.threshold/100)
	}
	return d.limit - d.threshold
}

// buildQuotaDims returns the three quota dimensions for notification checking.
func buildQuotaDims(account *Account) []quotaDim {
	return []quotaDim{
		{quotaDimDaily, account.GetQuotaNotifyDailyEnabled(), account.GetQuotaNotifyDailyThreshold(), account.GetQuotaNotifyDailyThresholdType(), account.GetQuotaDailyUsed(), account.GetQuotaDailyLimit()},
		{quotaDimWeekly, account.GetQuotaNotifyWeeklyEnabled(), account.GetQuotaNotifyWeeklyThreshold(), account.GetQuotaNotifyWeeklyThresholdType(), account.GetQuotaWeeklyUsed(), account.GetQuotaWeeklyLimit()},
		{quotaDimTotal, account.GetQuotaNotifyTotalEnabled(), account.GetQuotaNotifyTotalThreshold(), account.GetQuotaNotifyTotalThresholdType(), account.GetQuotaUsed(), account.GetQuotaLimit()},
	}
}

// buildQuotaDimsFromState builds quota dimensions using DB transaction state instead of account snapshot.
// Notification settings (enabled, threshold, thresholdType) come from the account; usage values from quotaState.
func buildQuotaDimsFromState(account *Account, state *AccountQuotaState) []quotaDim {
	return []quotaDim{
		{quotaDimDaily, account.GetQuotaNotifyDailyEnabled(), account.GetQuotaNotifyDailyThreshold(), account.GetQuotaNotifyDailyThresholdType(), state.DailyUsed, state.DailyLimit},
		{quotaDimWeekly, account.GetQuotaNotifyWeeklyEnabled(), account.GetQuotaNotifyWeeklyThreshold(), account.GetQuotaNotifyWeeklyThresholdType(), state.WeeklyUsed, state.WeeklyLimit},
		{quotaDimTotal, account.GetQuotaNotifyTotalEnabled(), account.GetQuotaNotifyTotalThreshold(), account.GetQuotaNotifyTotalThresholdType(), state.TotalUsed, state.TotalLimit},
	}
}

// CheckAccountQuotaAfterIncrement checks if any quota dimension crossed above its notify threshold.
// When quotaState is non-nil (from DB transaction RETURNING), it is used directly for threshold
// checking, avoiding a separate DB read. Otherwise it falls back to fetching fresh account data.
func (s *BalanceNotifyService) CheckAccountQuotaAfterIncrement(ctx context.Context, account *Account, cost float64, quotaState *AccountQuotaState) {
	if s == nil || account == nil || cost <= 0 {
		return
	}
	if s.relayOnly {
		dims := buildQuotaDims(account)
		if quotaState != nil {
			dims = buildQuotaDimsFromState(account, quotaState)
		}
		relayDims := make([]balanceNotifyRelayQuotaDim, 0, len(dims))
		for _, dim := range dims {
			relayDims = append(relayDims, balanceNotifyRelayQuotaDim{
				Name: dim.name, Enabled: dim.enabled, Threshold: dim.threshold,
				ThresholdType: dim.thresholdType, CurrentUsed: dim.currentUsed, Limit: dim.limit,
			})
		}
		s.enqueueRelayEvent(&balanceNotifyRelayEvent{
			Type: "account_quota", AccountID: account.ID, AccountName: account.Name,
			Platform: account.Platform, Cost: cost, QuotaDims: relayDims,
		})
		return
	}
	if !s.deliveryEnabled || s.emailService == nil || s.settingRepo == nil {
		return
	}
	if !s.isAccountQuotaNotifyEnabled(ctx) {
		return
	}
	adminEmails := s.getAccountQuotaNotifyEmails(ctx)
	if len(adminEmails) == 0 {
		return
	}

	siteName := s.getSiteName(ctx)
	var dims []quotaDim
	if quotaState != nil {
		dims = buildQuotaDimsFromState(account, quotaState)
	} else {
		freshAccount := s.fetchFreshAccount(ctx, account)
		dims = buildQuotaDims(freshAccount)
		account = freshAccount // use fresh data for alert metadata
	}
	s.checkQuotaDimCrossings(account, dims, cost, adminEmails, siteName)
}

// fetchFreshAccount loads the latest account from DB; falls back to the snapshot on error.
func (s *BalanceNotifyService) fetchFreshAccount(ctx context.Context, snapshot *Account) *Account {
	if s.accountRepo == nil {
		return snapshot
	}
	fresh, err := s.accountRepo.GetByID(ctx, snapshot.ID)
	if err != nil {
		slog.Warn("failed to fetch fresh account for quota notify, using snapshot",
			"account_id", snapshot.ID, "error", err)
		return snapshot
	}
	return fresh
}

// checkQuotaDimCrossings iterates pre-built quota dimensions and sends alerts for threshold crossings.
// Pre-increment value is reconstructed as currentUsed - cost to detect the crossing moment.
func (s *BalanceNotifyService) checkQuotaDimCrossings(account *Account, dims []quotaDim, cost float64, adminEmails []string, siteName string) {
	for _, dim := range dims {
		if !dim.enabled || dim.threshold <= 0 {
			continue
		}
		effectiveThreshold := dim.resolvedThreshold()
		if effectiveThreshold <= 0 {
			continue
		}
		newUsed := dim.currentUsed
		oldUsed := dim.currentUsed - cost
		if oldUsed < effectiveThreshold && newUsed >= effectiveThreshold {
			s.asyncSendQuotaAlert(adminEmails, account.ID, account.Name, account.Platform, dim, newUsed, effectiveThreshold, siteName)
		}
	}
}

// asyncSendQuotaAlert sends quota alert email in a goroutine with panic recovery.
func (s *BalanceNotifyService) asyncSendQuotaAlert(adminEmails []string, accountID int64, accountName, platform string, dim quotaDim, newUsed, effectiveThreshold float64, siteName string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in quota notification", "recover", r)
			}
		}()
		s.sendQuotaAlertEmails(adminEmails, accountID, accountName, platform, dim, newUsed, siteName)
	}()
}

// getBalanceNotifyConfig reads global balance notification settings.
func (s *BalanceNotifyService) getBalanceNotifyConfig(ctx context.Context) (enabled bool, threshold float64, rechargeURL string) {
	keys := []string{SettingKeyBalanceLowNotifyEnabled, SettingKeyBalanceLowNotifyThreshold, SettingKeyBalanceLowNotifyRechargeURL}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return false, 0, ""
	}
	enabled = settings[SettingKeyBalanceLowNotifyEnabled] == "true"
	if v := settings[SettingKeyBalanceLowNotifyThreshold]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			threshold = f
		}
	}
	rechargeURL = settings[SettingKeyBalanceLowNotifyRechargeURL]
	return
}

// isAccountQuotaNotifyEnabled checks the global account quota notification toggle.
func (s *BalanceNotifyService) isAccountQuotaNotifyEnabled(ctx context.Context) bool {
	val, err := s.settingRepo.GetValue(ctx, SettingKeyAccountQuotaNotifyEnabled)
	if err != nil {
		return false
	}
	return val == "true"
}

// getAccountQuotaNotifyEmails reads admin notification emails from settings,
// filtering out disabled and unverified entries.
func (s *BalanceNotifyService) getAccountQuotaNotifyEmails(ctx context.Context) []string {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAccountQuotaNotifyEmails)
	if err != nil || strings.TrimSpace(raw) == "" || raw == "[]" {
		return nil
	}

	entries := ParseNotifyEmails(raw)
	if len(entries) == 0 {
		return nil
	}

	return filterVerifiedEmails(entries)
}

// getSiteName reads site name from settings with fallback.
func (s *BalanceNotifyService) getSiteName(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return defaultSiteName
	}
	name, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || name == "" {
		return defaultSiteName
	}
	return name
}

// filterVerifiedEmails returns deduplicated, non-disabled, verified emails.
func filterVerifiedEmails(entries []NotifyEmailEntry) []string {
	var recipients []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.Disabled || !entry.Verified {
			continue
		}
		email := strings.TrimSpace(entry.Email)
		if email == "" {
			continue
		}
		lower := strings.ToLower(email)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		recipients = append(recipients, email)
	}
	return recipients
}

// collectBalanceNotifyRecipients returns verified, non-disabled email recipients.
// Only emails with verified=true and disabled=false are included.
func (s *BalanceNotifyService) collectBalanceNotifyRecipients(user *User) []string {
	return filterVerifiedEmails(user.BalanceNotifyExtraEmails)
}

// sendEmails sends an email to all recipients with shared timeout and error logging.
func (s *BalanceNotifyService) sendEmails(recipients []string, subject, body string, logAttrs ...any) bool {
	if len(recipients) == 0 {
		slog.Warn("sendEmails: no recipients", "subject", subject)
		return true
	}
	if s.emailService == nil {
		return false
	}
	allSucceeded := true
	for _, to := range recipients {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		if err := s.emailService.SendEmail(ctx, to, subject, body); err != nil {
			attrs := append([]any{"to", to, "error", err}, logAttrs...)
			slog.Error("failed to send notification", attrs...)
			allSucceeded = false
		} else {
			slog.Info("notification email sent successfully", "to", to, "subject", subject)
		}
		cancel()
	}
	return allSucceeded
}

// sendBalanceLowEmails sends balance low notification to all recipients.
func (s *BalanceNotifyService) sendBalanceLowEmails(recipients []string, userID int64, userName, userEmail string, balance, threshold float64, siteName, rechargeURL string) bool {
	displayName := userName
	if displayName == "" {
		displayName = userEmail
	}
	if s.notificationEmailService != nil {
		fallbackRecipients := make([]string, 0, len(recipients))
		deliveryFailed := false
		for _, to := range recipients {
			ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
			err := s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
				Event:          NotificationEmailEventBalanceLow,
				Locale:         notificationEmailLocaleChinese,
				RecipientEmail: to,
				RecipientName:  displayName,
				UserID:         userID,
				SourceType:     "balance_low",
				SourceID:       firstNonEmpty(strconv.FormatInt(userID, 10), userEmail),
				ReminderKey:    balanceReminderDay(s.currentTime()),
				Variables: map[string]string{
					"current_balance": fmt.Sprintf("%.2f", balance),
					"threshold":       fmt.Sprintf("%.2f", threshold),
					"recharge_url":    rechargeURL,
				},
			})
			cancel()
			if err == nil {
				continue
			}
			if shouldFallbackNotificationEmail(err) {
				slog.Warn("template balance low notification failed; falling back to built-in body", "to", to, "err", err.Error())
				fallbackRecipients = append(fallbackRecipients, to)
			} else {
				slog.Warn("template balance low notification delivery failed; not sending fallback to avoid duplicates", "to", to, "err", err.Error())
				deliveryFailed = true
			}
		}
		if len(fallbackRecipients) == 0 {
			return !deliveryFailed
		}
		if !s.sendBuiltInBalanceLowEmails(fallbackRecipients, displayName, userEmail, balance, threshold, siteName, rechargeURL) {
			return false
		}
		return !deliveryFailed
	}
	return s.sendBuiltInBalanceLowEmails(recipients, displayName, userEmail, balance, threshold, siteName, rechargeURL)
}

func (s *BalanceNotifyService) sendBuiltInBalanceLowEmails(recipients []string, displayName, userEmail string, balance, threshold float64, siteName, rechargeURL string) bool {
	subject := fmt.Sprintf("[%s] 余额不足提醒", sanitizeEmailHeader(siteName))
	body := s.buildBalanceLowEmailBody(html.EscapeString(displayName), balance, threshold, html.EscapeString(siteName), rechargeURL)
	return s.sendEmails(recipients, subject, body, "user_email", userEmail, "balance", balance)
}

// sendQuotaAlertEmails sends quota alert notification to admin emails.
func (s *BalanceNotifyService) sendQuotaAlertEmails(adminEmails []string, accountID int64, accountName, platform string, dim quotaDim, used float64, siteName string) bool {
	dimLabel := quotaDimLabels[dim.name]
	if dimLabel == "" {
		dimLabel = dim.name
	}

	thresholdDisplay := fmt.Sprintf("$%.2f", dim.threshold)
	if dim.thresholdType == thresholdTypePercentage {
		thresholdDisplay = fmt.Sprintf("%.0f%%", dim.threshold)
	}
	remaining := dim.limit - used
	if remaining < 0 {
		remaining = 0
	}

	if s.notificationEmailService != nil {
		fallbackRecipients := make([]string, 0, len(adminEmails))
		deliveryFailed := false
		for _, to := range adminEmails {
			ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
			err := s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
				Event:          NotificationEmailEventAccountQuotaAlert,
				Locale:         notificationEmailLocaleChinese,
				RecipientEmail: to,
				RecipientName:  emailRecipientName(to),
				SourceType:     "account_quota",
				SourceID:       fmt.Sprintf("%d-%s", accountID, dim.name),
				ReminderKey:    balanceReminderDay(time.Now()),
				Variables: map[string]string{
					"account_id":      strconv.FormatInt(accountID, 10),
					"account_name":    accountName,
					"platform":        platform,
					"quota_dimension": dimLabel,
					"quota_used":      fmt.Sprintf("%.2f", used),
					"quota_limit":     fmt.Sprintf("%.2f", dim.limit),
					"quota_remaining": fmt.Sprintf("%.2f", remaining),
					"quota_threshold": thresholdDisplay,
				},
			})
			cancel()
			if err == nil {
				continue
			}
			if shouldFallbackNotificationEmail(err) {
				slog.Warn("template account quota alert failed; falling back to built-in body", "to", to, "account_id", accountID, "dimension", dim.name, "err", err.Error())
				fallbackRecipients = append(fallbackRecipients, to)
			} else {
				slog.Warn("template account quota alert delivery failed; not sending fallback to avoid duplicates", "to", to, "account_id", accountID, "dimension", dim.name, "err", err.Error())
				deliveryFailed = true
			}
		}
		if len(fallbackRecipients) == 0 {
			return !deliveryFailed
		}
		if !s.sendBuiltInQuotaAlertEmails(fallbackRecipients, accountID, accountName, platform, dim, used, siteName, dimLabel, remaining, thresholdDisplay) {
			return false
		}
		return !deliveryFailed
	}
	return s.sendBuiltInQuotaAlertEmails(adminEmails, accountID, accountName, platform, dim, used, siteName, dimLabel, remaining, thresholdDisplay)
}

func (s *BalanceNotifyService) sendBuiltInQuotaAlertEmails(adminEmails []string, accountID int64, accountName, platform string, dim quotaDim, used float64, siteName, dimLabel string, remaining float64, thresholdDisplay string) bool {
	subject := fmt.Sprintf("[%s] 账号限额告警 - %s", sanitizeEmailHeader(siteName), sanitizeEmailHeader(accountName))
	body := s.buildQuotaAlertEmailBody(accountID, html.EscapeString(accountName), html.EscapeString(platform), html.EscapeString(dimLabel), used, dim.limit, remaining, thresholdDisplay, html.EscapeString(siteName))
	return s.sendEmails(adminEmails, subject, body, "account", accountName, "dimension", dim.name)
}

// sanitizeEmailHeader removes CR/LF characters to prevent SMTP header injection.
func sanitizeEmailHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// balanceLowEmailTemplate 是余额不足提醒的内置中文模板。
// 格式化参数依次为站点名、用户名、余额、阈值和充值按钮。
const balanceLowEmailTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; text-align: center; }
        .balance { font-size: 36px; font-weight: bold; color: #dc2626; margin: 20px 0; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; }
        .recharge-btn { display: inline-block; margin-top: 24px; padding: 12px 32px; background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); color: #fff; text-decoration: none; border-radius: 6px; font-size: 16px; font-weight: bold; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h1>%s</h1></div>
        <div class="content">
	            <p style="font-size: 18px; color: #333;">%s，您的余额不足</p>
	            <div class="balance">$%.2f</div>
	            <div class="info">
	                <p>您的账户余额已低于提醒阈值 <strong>$%.2f</strong>。</p>
	                <p>请及时充值以免服务中断。</p>
	            </div>
            %s
        </div>
        <div class="footer"><p>此邮件由系统自动发送，请勿回复。</p></div>
    </div>
</body>
</html>`

// insufficientBalanceEmailTemplate 是请求因余额不足失败时使用的事务邮件模板，不包含图片或二维码。
// 格式化参数依次为站点名、用户名、余额和充值按钮。
const insufficientBalanceEmailTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; text-align: center; }
        .balance { font-size: 36px; font-weight: bold; color: #dc2626; margin: 20px 0; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; }
        .recharge-btn { display: inline-block; margin-top: 24px; padding: 12px 32px; background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); color: #fff; text-decoration: none; border-radius: 6px; font-size: 16px; font-weight: bold; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h1>%s</h1></div>
        <div class="content">
            <p style="font-size: 18px; color: #333;">%s，您的账户余额不足</p>
            <div class="balance">$%.2f</div>
            <div class="info">
                <p>本次请求因账户余额不足未能完成。</p>
                <p>请充值后重新发起请求。</p>
            </div>
            %s
        </div>
        <div class="footer"><p>此邮件由系统自动发送，请勿回复。</p></div>
    </div>
</body>
</html>`

// quotaAlertEmailTemplate is the HTML template for account quota alert notifications.
// Format args: siteName, accountID, accountName, platform, dimLabel, used, limitStr, remaining, thresholdDisplay.
const quotaAlertEmailTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #ef4444 0%%, #dc2626 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; }
        .metric { display: flex; justify-content: space-between; padding: 12px 0; border-bottom: 1px solid #eee; }
        .metric-label { color: #666; }
        .metric-value { font-weight: bold; color: #333; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; text-align: center; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h1>%s</h1></div>
        <div class="content">
	            <p style="font-size: 18px; color: #333; text-align: center;">账号限额告警</p>
	            <div class="metric"><span class="metric-label">账号 ID</span><span class="metric-value">#%d</span></div>
	            <div class="metric"><span class="metric-label">账号</span><span class="metric-value">%s</span></div>
	            <div class="metric"><span class="metric-label">平台</span><span class="metric-value">%s</span></div>
	            <div class="metric"><span class="metric-label">维度</span><span class="metric-value">%s</span></div>
	            <div class="metric"><span class="metric-label">已使用</span><span class="metric-value">$%.2f</span></div>
	            <div class="metric"><span class="metric-label">限额</span><span class="metric-value">%s</span></div>
	            <div class="metric"><span class="metric-label">剩余额度</span><span class="metric-value">$%.2f</span></div>
	            <div class="metric"><span class="metric-label">提醒阈值</span><span class="metric-value">%s</span></div>
	            <div class="info">
	                <p>账号剩余额度已低于提醒阈值，请及时关注。</p>
	            </div>
        </div>
        <div class="footer"><p>此邮件由系统自动发送，请勿回复。</p></div>
    </div>
</body>
</html>`

// buildBalanceLowEmailBody builds HTML email for balance low notification.
func (s *BalanceNotifyService) buildBalanceLowEmailBody(userName string, balance, threshold float64, siteName, rechargeURL string) string {
	rechargeBlock := ""
	if rechargeURL != "" {
		rechargeBlock = fmt.Sprintf(`<a href="%s" class="recharge-btn">立即充值</a>`, html.EscapeString(rechargeURL))
	}
	return fmt.Sprintf(balanceLowEmailTemplate, siteName, userName, balance, threshold, rechargeBlock)
}

func (s *BalanceNotifyService) buildInsufficientBalanceEmailBody(userName string, balance float64, siteName, rechargeURL string) string {
	rechargeBlock := ""
	if rechargeURL != "" {
		rechargeBlock = fmt.Sprintf(`<a href="%s" class="recharge-btn">立即充值</a>`, html.EscapeString(rechargeURL))
	}
	return fmt.Sprintf(insufficientBalanceEmailTemplate, siteName, userName, balance, rechargeBlock)
}

// buildQuotaAlertEmailBody builds HTML email for account quota alert.
func (s *BalanceNotifyService) buildQuotaAlertEmailBody(accountID int64, accountName, platform, dimLabel string, used, limit, remaining float64, thresholdDisplay, siteName string) string {
	limitStr := fmt.Sprintf("$%.2f", limit)
	if limit <= 0 {
		limitStr = "无限制"
	}
	return fmt.Sprintf(quotaAlertEmailTemplate, siteName, accountID, accountName, platform, dimLabel, used, limitStr, remaining, thresholdDisplay)
}
