package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	// These values live in accounts.extra so PR2 does not require a schema migration.
	UpstreamBillingProbeExtraKey           = "upstream_billing_probe"
	UpstreamBillingProbeEnabledExtraKey    = "upstream_billing_probe_enabled"
	UpstreamBillingRateSyncEnabledExtraKey = "upstream_billing_rate_sync_enabled"

	// 默认五分钟兜底探测空闲账号；到期队列每十秒检查一次，活跃账号仍通过一分钟合并事件优先触发。
	upstreamBillingProbeDefaultIntervalMinutes = 5
	upstreamBillingProbeMinIntervalMinutes     = 5
	upstreamBillingProbeMaxIntervalMinutes     = 24 * 60
	upstreamBillingProbeCycleInterval          = 10 * time.Second
	upstreamBillingProbeRequestTimeout         = 10 * time.Second
	upstreamBillingProbeMaxBodyBytes           = 64 * 1024
	upstreamBillingProbeMaxPerCycle            = 20
	upstreamBillingProbeConcurrency            = 4
	upstreamBillingProbeMaxDelay               = 24 * time.Hour
	// unsupported 账号的重探间隔倍数：上游不是 sub2api 中转就不会突然长出
	// /v1/sub2api/billing，按常规 interval 重排只会持续占满每周期
	// upstreamBillingProbeMaxPerCycle 个名额。
	upstreamBillingProbeUnsupportedDelayFactor = 8
	upstreamBillingProbeAccountRateScale       = 10000.0
	upstreamBillingProbeLeaderLockKey          = "upstream:billing:probe:leader"
	upstreamBillingProbeLeaderLockTTL          = 2 * time.Minute
)

// UpstreamBillingProbeMaxBatchSize limits one manual batch and one runner cycle.
const UpstreamBillingProbeMaxBatchSize = upstreamBillingProbeMaxPerCycle

// upstreamBillingRateSyncMaxMultiplier bounds the value the automatic
// write-back may push into accounts.rate_multiplier.
//
// No other code path bounds that column from above — admins may type any
// non-negative number and the only ceiling is the DECIMAL(10,4) column itself
// (999999.9999). That ceiling is meaningless as a guard: rate_multiplier
// scales the per-request account cost that feeds quota_used, so a single
// declared 999999 would exhaust any account quota on the first request and
// poison cost reporting. 100 is picked as a deliberately generous bound: it is
// two orders of magnitude above the 1.0 default and far above any plausible
// upstream resale markup, so no legitimate declaration is rejected while an
// absurd or hostile one cannot reach the quota control plane unattended.
// It only constrains the automatic path; manual edits keep their old range.
const upstreamBillingRateSyncMaxMultiplier = 100.0

var (
	ErrUpstreamBillingProbeUnavailable = infraerrors.ServiceUnavailable(
		"UPSTREAM_BILLING_PROBE_UNAVAILABLE", "upstream billing probe is unavailable",
	)
	ErrUpstreamBillingProbeAccountInvalid = infraerrors.BadRequest(
		"UPSTREAM_BILLING_PROBE_ACCOUNT_INVALID", "account is not an API key account",
	)
	ErrUpstreamBillingProbeIdentityChanged = infraerrors.Conflict(
		"UPSTREAM_BILLING_PROBE_IDENTITY_CHANGED", "account identity changed during upstream billing probe; retry the probe",
	)
	ErrUpstreamBillingRateSyncBulkConflict = infraerrors.Conflict(
		"UPSTREAM_BILLING_RATE_SYNC_BULK_CONFLICT",
		"account rate multiplier cannot be changed in bulk while upstream billing rate sync is enabled",
	)
	ErrUpstreamBillingRateSyncConflict = infraerrors.Conflict(
		"UPSTREAM_BILLING_RATE_SYNC_CONFLICT",
		"account rate multiplier cannot be changed while upstream billing rate sync is enabled",
	)
	ErrUpstreamBillingManualRateInvalid = infraerrors.BadRequest(
		"INVALID_UPSTREAM_BILLING_MANUAL_RATE_MULTIPLIER", "upstream billing manual rate multiplier must be a finite number >= 0",
	)
	ErrUpstreamBillingManualRateAccountInvalid = infraerrors.BadRequest(
		"UPSTREAM_BILLING_MANUAL_RATE_ACCOUNT_INVALID", "manual upstream billing rate is only available for OpenAI API key accounts",
	)
	ErrUpstreamBillingManualRateRequiresFailedProbe = infraerrors.BadRequest(
		"UPSTREAM_BILLING_MANUAL_RATE_REQUIRES_FAILED_PROBE", "manual upstream billing rate requires a failed probe snapshot",
	)
	ErrUpstreamBillingManualRateUnavailable = infraerrors.Conflict(
		"UPSTREAM_BILLING_MANUAL_RATE_UNAVAILABLE", "the account no longer has a failed upstream billing probe snapshot; reload and retry",
	)
)

const (
	UpstreamBillingProbeStatusOK          = "ok"
	UpstreamBillingProbeStatusUnsupported = "unsupported"
	UpstreamBillingProbeStatusFailed      = "failed"
)

// UpstreamBillingProbeSettings controls the periodic probe runner.
type UpstreamBillingProbeSettings struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

// UpstreamBillingProbeSnapshot is persisted in accounts.extra. Data is kept as
// a sanitized map so future response fields do not require a database change.
type UpstreamBillingProbeSnapshot struct {
	Status        string                          `json:"status"`
	Data          map[string]any                  `json:"data,omitempty"`
	Balance       *UpstreamAccountBalanceSnapshot `json:"balance,omitempty"`
	ReceivedAt    *time.Time                      `json:"received_at,omitempty"`
	FreshUntil    *time.Time                      `json:"fresh_until,omitempty"`
	LastAttemptAt time.Time                       `json:"last_attempt_at"`
	NextProbeAt   time.Time                       `json:"next_probe_at"`
	FailureCount  int                             `json:"failure_count,omitempty"`
	HTTPStatus    int                             `json:"http_status,omitempty"`
	LastError     string                          `json:"last_error,omitempty"`
	// SyncedRateMultiplier records the value this probe wrote into
	// accounts.rate_multiplier. It is only set when the account opted into rate
	// sync and the declared value passed the write-back range check, so the
	// stored snapshot always answers "did this probe move the account rate, and
	// to what" without a separate history table.
	SyncedRateMultiplier *float64 `json:"synced_rate_multiplier,omitempty"`
	// ManualRateMultiplier 仅在失败探测时作为管理员兜底，不代表上游自动声明值。
	ManualRateMultiplier *float64 `json:"manual_rate_multiplier,omitempty"`
}

// UpstreamAccountBalanceSnapshot 独立记录网页登录余额，避免余额失败覆盖已成功的倍率。
type UpstreamAccountBalanceSnapshot struct {
	Status        string     `json:"status"`
	Amount        *float64   `json:"amount,omitempty"`
	Unit          string     `json:"unit,omitempty"`
	ReceivedAt    *time.Time `json:"received_at,omitempty"`
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	HTTPStatus    int        `json:"http_status,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// UpstreamBillingProbeResult is returned by manual probe endpoints.
type UpstreamBillingProbeResult struct {
	AccountID int64                         `json:"account_id"`
	Snapshot  *UpstreamBillingProbeSnapshot `json:"snapshot,omitempty"`
	Error     string                        `json:"error,omitempty"`
}

// UpstreamBillingRateSnapshotItem is the compact representation used by the
// account table's background refresh. It intentionally excludes credentials,
// runtime counters, and usage data from the response.
type UpstreamBillingRateSnapshotItem struct {
	AccountID int64                         `json:"account_id"`
	Snapshot  *UpstreamBillingProbeSnapshot `json:"snapshot"`
}

// BuildUpstreamBillingRateSnapshotItems projects account rows into the
// read-only payload used by the rate refresh endpoint. Decode snapshots here
// so malformed or legacy extra data is handled consistently with probe logic.
func BuildUpstreamBillingRateSnapshotItems(accounts []Account) []UpstreamBillingRateSnapshotItem {
	items := make([]UpstreamBillingRateSnapshotItem, 0, len(accounts))
	for _, account := range accounts {
		var snapshot *UpstreamBillingProbeSnapshot
		// The billing endpoint is supported by every API-key platform; limiting
		// this projection to OpenAI would make the background refresh erase the
		// other platforms' persisted snapshots from the table.
		if account.Type == AccountTypeAPIKey {
			snapshot = decodeUpstreamBillingProbeSnapshot(account.Extra)
		}
		items = append(items, UpstreamBillingRateSnapshotItem{
			AccountID: account.ID,
			Snapshot:  snapshot,
		})
	}
	return items
}

type upstreamBillingProbeResponse struct {
	Object                  string   `json:"object"`
	SchemaVersion           int      `json:"schema_version"`
	BillingScope            string   `json:"billing_scope"`
	GroupRateMultiplier     *float64 `json:"group_rate_multiplier"`
	UserRateMultiplier      *float64 `json:"user_rate_multiplier"`
	ResolvedRateMultiplier  *float64 `json:"resolved_rate_multiplier"`
	PeakRateEnabled         *bool    `json:"peak_rate_enabled"`
	PeakStart               *string  `json:"peak_start"`
	PeakEnd                 *string  `json:"peak_end"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier"`
	AppliedPeakMultiplier   *float64 `json:"applied_peak_multiplier"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	Timezone                *string  `json:"timezone"`
	ObservedAt              string   `json:"observed_at"`
}

// GetUpstreamBillingProbeSettings returns defaults when the setting is absent.
func (s *SettingService) GetUpstreamBillingProbeSettings(ctx context.Context) (*UpstreamBillingProbeSettings, error) {
	defaults := defaultUpstreamBillingProbeSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyUpstreamBillingProbeSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return nil, fmt.Errorf("get upstream billing probe settings: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return defaults, nil
	}
	settings := *defaults
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return nil, fmt.Errorf("parse upstream billing probe settings: %w", err)
	}
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = defaults.IntervalMinutes
	}
	normalizeUpstreamBillingProbeSettings(&settings)
	return &settings, nil
}

// SetUpstreamBillingProbeSettings validates and persists the runner settings.
func (s *SettingService) SetUpstreamBillingProbeSettings(ctx context.Context, settings *UpstreamBillingProbeSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("setting repository is unavailable")
	}
	if settings == nil {
		return infraerrors.BadRequest("INVALID_UPSTREAM_BILLING_PROBE_SETTINGS", "settings cannot be nil")
	}
	if settings.IntervalMinutes < upstreamBillingProbeMinIntervalMinutes || settings.IntervalMinutes > upstreamBillingProbeMaxIntervalMinutes {
		return infraerrors.BadRequest(
			"INVALID_UPSTREAM_BILLING_PROBE_INTERVAL",
			fmt.Sprintf("interval_minutes must be between %d and %d", upstreamBillingProbeMinIntervalMinutes, upstreamBillingProbeMaxIntervalMinutes),
		)
	}
	normalizeUpstreamBillingProbeSettings(settings)
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal upstream billing probe settings: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeyUpstreamBillingProbeSettings, string(data))
}

func defaultUpstreamBillingProbeSettings() *UpstreamBillingProbeSettings {
	return &UpstreamBillingProbeSettings{Enabled: true, IntervalMinutes: upstreamBillingProbeDefaultIntervalMinutes}
}

func normalizeUpstreamBillingProbeSettings(settings *UpstreamBillingProbeSettings) {
	if settings.IntervalMinutes < upstreamBillingProbeMinIntervalMinutes {
		settings.IntervalMinutes = upstreamBillingProbeMinIntervalMinutes
	}
	if settings.IntervalMinutes > upstreamBillingProbeMaxIntervalMinutes {
		settings.IntervalMinutes = upstreamBillingProbeMaxIntervalMinutes
	}
}

// UpstreamBillingProbeService discovers a remote Sub2API billing snapshot.
type UpstreamBillingProbeService struct {
	accountRepo        AccountRepository
	accountTestService *AccountTestService
	settingService     *SettingService
	upstreamSites      *UpstreamSiteCredentialService

	parentCtx           context.Context
	parentCancel        context.CancelFunc
	wg                  sync.WaitGroup
	mu                  sync.Mutex
	started             bool
	stopped             bool
	cycleMu             sync.Mutex
	probeGroup          singleflight.Group
	probeSlots          chan struct{}
	now                 func() time.Time
	lockCache           LeaderLockCache
	db                  *sql.DB
	instanceID          string
	webTokenMu          sync.Mutex
	webTokens           map[string]cachedWebAccountToken
	balanceCenterEvents *BalanceCenterEventService
	balanceCenterStore  interface {
		PersistSnapshot(context.Context, *BalanceCenterSnapshot) (*BalanceCenterSnapshot, error)
	}
}

type cachedWebAccountToken struct {
	token     string
	userID    int
	expiresAt time.Time
}

type upstreamBillingProbeSnapshotWriter interface {
	UpdateUpstreamBillingProbeSnapshot(context.Context, *Account, *UpstreamBillingProbeSnapshot, *float64) error
}

type upstreamBillingManualRateMultiplierUpdater interface {
	UpdateUpstreamBillingManualRateMultiplier(context.Context, *Account, *float64) error
}

type upstreamBillingProbeDueAccountLister interface {
	ListDueUpstreamBillingProbeAccounts(context.Context, time.Time, int) ([]Account, error)
}

func NewUpstreamBillingProbeService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	settingService *SettingService,
) *UpstreamBillingProbeService {
	ctx, cancel := context.WithCancel(context.Background())
	return &UpstreamBillingProbeService{
		accountRepo:        accountRepo,
		accountTestService: accountTestService,
		settingService:     settingService,
		parentCtx:          ctx,
		parentCancel:       cancel,
		probeSlots:         make(chan struct{}, upstreamBillingProbeConcurrency),
		now:                time.Now,
		instanceID:         uuid.NewString(),
		webTokens:          make(map[string]cachedWebAccountToken),
	}
}

func (s *UpstreamBillingProbeService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// SetUpstreamSiteCredentialService 注入按域名共享的网页登录凭据。
func (s *UpstreamBillingProbeService) SetUpstreamSiteCredentialService(credentials *UpstreamSiteCredentialService) {
	if s == nil {
		return
	}
	s.upstreamSites = credentials
}

func (s *UpstreamBillingProbeService) SetBalanceCenterEventService(events *BalanceCenterEventService) {
	if s == nil {
		return
	}
	s.balanceCenterEvents = events
	if events != nil {
		events.SetProber(s)
	}
}

// SetBalanceCenterRepository 注入统一余额快照仓储；写入失败不反向影响原探测链路。
func (s *UpstreamBillingProbeService) SetBalanceCenterRepository(repository interface {
	PersistSnapshot(context.Context, *BalanceCenterSnapshot) (*BalanceCenterSnapshot, error)
}) {
	if s == nil {
		return
	}
	s.balanceCenterStore = repository
}

// ProvideUpstreamBillingProbeService starts the process-wide periodic runner.
func ProvideUpstreamBillingProbeService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	settingService *SettingService,
	upstreamSites *UpstreamSiteCredentialService,
	balanceCenterEvents *BalanceCenterEventService,
	balanceCenterService *BalanceCenterService,
	lockCache LeaderLockCache,
	db *sql.DB,
	cfg *config.Config,
) *UpstreamBillingProbeService {
	svc := NewUpstreamBillingProbeService(accountRepo, accountTestService, settingService)
	svc.SetUpstreamSiteCredentialService(upstreamSites)
	svc.SetBalanceCenterEventService(balanceCenterEvents)
	svc.SetBalanceCenterRepository(balanceCenterService)
	svc.SetLeaderLock(lockCache, db)
	if cfg.ShouldStartBackgroundTask(config.BackgroundTaskPeriodicSideEffect) {
		svc.Start()
	}
	return svc
}

func (s *UpstreamBillingProbeService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go s.runLoop()
}

func (s *UpstreamBillingProbeService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.parentCancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *UpstreamBillingProbeService) runLoop() {
	defer s.wg.Done()
	_ = s.RunDue(s.parentCtx)
	ticker := time.NewTicker(upstreamBillingProbeCycleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
			if err := s.RunDue(s.parentCtx); err != nil {
				logger.LegacyPrintf("service.upstream_billing_probe", "run_due_failed: err=%v", err)
			}
		}
	}
}

// RunDue executes at most one bounded batch of due accounts.
func (s *UpstreamBillingProbeService) RunDue(ctx context.Context) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()

	// 订阅到期与请求活跃度无关，按自身开关定期同步，不依赖某个模型账号被探测。
	s.syncDueBalanceCenterSubscriptions(ctx)
	s.consumeBalanceCenterDueEvents(ctx)

	settings, err := s.getSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	runRelease, acquired, lockErr := s.tryAcquireLeaderLock(ctx, upstreamBillingProbeLeaderLockKey)
	if lockErr != nil {
		return fmt.Errorf("acquire upstream billing probe leader lock: %w", lockErr)
	}
	if !acquired {
		return nil
	}
	defer runRelease()

	lockNow := time.Now()
	cadenceRelease, acquired, lockErr := s.tryAcquireLeaderLock(ctx, upstreamBillingProbeLeaderLockKeyAt(lockNow))
	if lockErr != nil {
		return fmt.Errorf("acquire upstream billing probe cadence lock: %w", lockErr)
	}
	if !acquired {
		return nil
	}
	defer releaseUpstreamBillingProbeLeaderLock(cadenceRelease, lockNow.Truncate(upstreamBillingProbeCycleInterval).Add(upstreamBillingProbeCycleInterval))

	now := s.currentTime()
	accounts, err := s.listDueAccounts(ctx, now)
	if err != nil {
		return fmt.Errorf("list enabled upstream billing probes: %w", err)
	}
	due := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := accounts[i]
		if !isUpstreamBillingProbeAccount(&account) || !account.IsActive() || !upstreamBillingProbeEnabled(&account) {
			continue
		}
		snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra)
		if snapshot != nil && !snapshot.NextProbeAt.IsZero() && now.Before(snapshot.NextProbeAt) {
			continue
		}
		due = append(due, account)
	}
	sort.SliceStable(due, func(i, j int) bool {
		left := decodeUpstreamBillingProbeSnapshot(due[i].Extra)
		right := decodeUpstreamBillingProbeSnapshot(due[j].Extra)
		leftUnset := left == nil || left.NextProbeAt.IsZero()
		rightUnset := right == nil || right.NextProbeAt.IsZero()
		if leftUnset && rightUnset {
			return due[i].ID < due[j].ID
		}
		if leftUnset {
			return true
		}
		if rightUnset {
			return false
		}
		return left.NextProbeAt.Before(right.NextProbeAt)
	})
	if len(due) > upstreamBillingProbeMaxPerCycle {
		due = due[:upstreamBillingProbeMaxPerCycle]
	}

	var group errgroup.Group
	for i := range due {
		accountID := due[i].ID
		group.Go(func() error {
			if _, probeErr := s.probeScheduledAccount(ctx, accountID, settings.IntervalMinutes); probeErr != nil {
				logger.LegacyPrintf("service.upstream_billing_probe", "probe_due_failed: account_id=%d err=%v", accountID, probeErr)
			}
			return nil
		})
	}
	return group.Wait()
}

func (s *UpstreamBillingProbeService) consumeBalanceCenterDueEvents(ctx context.Context) {
	if s == nil || s.balanceCenterEvents == nil {
		return
	}
	// 余额中心事件探测是新功能的旁路观察者；即使 Redis 或设置读取失败，也不能影响旧兜底探测。
	if err := s.balanceCenterEvents.RefreshSettings(ctx); err != nil {
		logger.LegacyPrintf("service.upstream_billing_probe", "balance_center_refresh_settings_failed: err=%v", err)
		return
	}
	if err := s.balanceCenterEvents.ConsumeDue(ctx); err != nil {
		logger.LegacyPrintf("service.upstream_billing_probe", "balance_center_consume_due_failed: err=%v", err)
	}
}

func (s *UpstreamBillingProbeService) listDueAccounts(ctx context.Context, now time.Time) ([]Account, error) {
	if lister, ok := s.accountRepo.(upstreamBillingProbeDueAccountLister); ok {
		return lister.ListDueUpstreamBillingProbeAccounts(ctx, now, upstreamBillingProbeMaxPerCycle)
	}
	// Non-production repositories and older adapters keep the generic path. The
	// runner still truncates before issuing network requests.
	return s.accountRepo.FindByExtraField(ctx, UpstreamBillingProbeEnabledExtraKey, true)
}

func (s *UpstreamBillingProbeService) getSettings(ctx context.Context) (*UpstreamBillingProbeSettings, error) {
	if s.settingService == nil {
		return defaultUpstreamBillingProbeSettings(), nil
	}
	return s.settingService.GetUpstreamBillingProbeSettings(ctx)
}

func (s *UpstreamBillingProbeService) GetSettings(ctx context.Context) (*UpstreamBillingProbeSettings, error) {
	return s.getSettings(ctx)
}

func (s *UpstreamBillingProbeService) UpdateSettings(ctx context.Context, settings *UpstreamBillingProbeSettings) error {
	if s == nil || s.settingService == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	return s.settingService.SetUpstreamBillingProbeSettings(ctx, settings)
}

// ProbeAccount performs one manual or scheduled probe. Manual calls ignore both switches.
func (s *UpstreamBillingProbeService) ProbeAccount(ctx context.Context, accountID int64) (*UpstreamBillingProbeSnapshot, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	settings, err := s.getSettings(ctx)
	if err != nil {
		return nil, err
	}
	return s.probeAccount(ctx, accountID, settings.IntervalMinutes)
}

func (s *UpstreamBillingProbeService) probeAccount(ctx context.Context, accountID int64, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	return s.probeAccountWithMode(ctx, accountID, intervalMinutes, false)
}

func (s *UpstreamBillingProbeService) probeScheduledAccount(ctx context.Context, accountID int64, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	return s.probeAccountWithMode(ctx, accountID, intervalMinutes, true)
}

func (s *UpstreamBillingProbeService) probeAccountWithMode(ctx context.Context, accountID int64, intervalMinutes int, requireEnabled bool) (*UpstreamBillingProbeSnapshot, error) {
	key := strconv.FormatInt(accountID, 10)
	value, err, _ := s.probeGroup.Do(key, func() (any, error) {
		select {
		case s.probeSlots <- struct{}{}:
			defer func() { <-s.probeSlots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		account, loadErr := s.accountRepo.GetByID(ctx, accountID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !isUpstreamBillingProbeAccount(account) {
			return nil, ErrUpstreamBillingProbeAccountInvalid
		}
		if requireEnabled {
			if !account.IsActive() || !upstreamBillingProbeEnabled(account) {
				return nil, nil
			}
			if snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra); snapshot != nil &&
				!snapshot.NextProbeAt.IsZero() && s.currentTime().Before(snapshot.NextProbeAt) {
				return nil, nil
			}
		}
		return s.probeLoadedAccount(ctx, account, intervalMinutes)
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	snapshot, ok := value.(*UpstreamBillingProbeSnapshot)
	if !ok {
		return nil, fmt.Errorf("invalid upstream billing probe result")
	}
	return snapshot, nil
}

// ProbeAccounts performs a bounded manual batch with the same concurrency limit as the runner.
func (s *UpstreamBillingProbeService) ProbeAccounts(ctx context.Context, accountIDs []int64) []UpstreamBillingProbeResult {
	if len(accountIDs) > upstreamBillingProbeMaxPerCycle {
		accountIDs = accountIDs[:upstreamBillingProbeMaxPerCycle]
	}
	results := make([]UpstreamBillingProbeResult, len(accountIDs))
	if s == nil || s.accountRepo == nil {
		for i, accountID := range accountIDs {
			results[i] = UpstreamBillingProbeResult{AccountID: accountID, Error: ErrUpstreamBillingProbeUnavailable.Error()}
		}
		return results
	}
	settings, settingsErr := s.getSettings(ctx)
	if settingsErr != nil {
		for i, accountID := range accountIDs {
			results[i] = UpstreamBillingProbeResult{AccountID: accountID, Error: safeProbeError(settingsErr)}
		}
		return results
	}
	var group errgroup.Group
	for i, accountID := range accountIDs {
		i, accountID := i, accountID
		results[i].AccountID = accountID
		group.Go(func() error {
			snapshot, err := s.probeAccount(ctx, accountID, settings.IntervalMinutes)
			if err != nil {
				results[i].Error = safeProbeError(err)
				return nil
			}
			results[i].Snapshot = snapshot
			return nil
		})
	}
	_ = group.Wait()
	return results
}

func upstreamBillingProbeLeaderLockKeyAt(now time.Time) string {
	return fmt.Sprintf("%s:%d", upstreamBillingProbeLeaderLockKey, now.Unix()/int64(upstreamBillingProbeCycleInterval/time.Second))
}

func (s *UpstreamBillingProbeService) tryAcquireLeaderLock(ctx context.Context, key string) (func(), bool, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.lockCache != nil {
		acquired, err := s.lockCache.TryAcquireLeaderLock(lockCtx, key, s.instanceID, upstreamBillingProbeLeaderLockTTL)
		if err != nil {
			return nil, false, err
		}
		if !acquired {
			return nil, false, nil
		}
		return func() {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer releaseCancel()
			_ = s.lockCache.ReleaseLeaderLock(releaseCtx, key, s.instanceID)
		}, true, nil
	}
	if s.db != nil {
		return tryAcquireDBAdvisoryLockWithError(lockCtx, s.db, hashAdvisoryLockID(key))
	}
	return func() {}, true, nil
}

func releaseUpstreamBillingProbeLeaderLock(release func(), releaseAt time.Time) {
	delay := time.Until(releaseAt)
	if delay <= 0 {
		release()
		return
	}
	time.AfterFunc(delay, release)
}

func (s *UpstreamBillingProbeService) SetAccountEnabled(ctx context.Context, accountID int64, enabled bool) error {
	if s == nil || s.accountRepo == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if !isUpstreamBillingProbeAccount(account) {
		return ErrUpstreamBillingProbeAccountInvalid
	}
	updates := map[string]any{UpstreamBillingProbeEnabledExtraKey: enabled}
	if !enabled {
		updates[UpstreamBillingRateSyncEnabledExtraKey] = false
	}
	return s.accountRepo.UpdateExtra(ctx, accountID, updates)
}

func (s *UpstreamBillingProbeService) probeLoadedAccount(ctx context.Context, account *Account, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	now := s.currentTime().UTC()
	if s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "transport_unavailable", 0)
	}
	// 平台放宽后取数直读 credentials：所有 API-key 平台的密钥与自定义上游
	// 统一存放在 credentials.api_key / credentials.base_url。
	apiKey := account.GetCredential("api_key")
	if apiKey == "" {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "missing_api_key", 0)
	}
	baseURL := account.GetCredential("base_url")
	if account.IsCNProvider() && account.IsAdaptiveAPIProtocol() {
		baseURL = account.GetCNProtocolBaseURL(APIProtocolChatCompletions)
	}
	if account.Platform == PlatformOpenAI {
		if baseURL == "" {
			// 保持官方语义：OpenAI 账号无自定义 base 时探官方域（404 → unsupported）。
			baseURL = "https://api.openai.com"
		}
	} else if upstreamBillingProbeTargetIsOfficialAPI(baseURL) {
		// 其他平台 base_url 为空或指向官方 API 根域（前端创建时会把空值
		// 填成官方默认域，且提供 us-east-1.api.x.ai 等官方区域预设）⇒
		// 必无 /v1/sub2api/billing；不发请求，直接记 unsupported，避免
		// 拿账号 Key 周期性请求官方域的不存在路径。
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "unsupported", 0)
	}
	normalizedBaseURL, err := s.accountTestService.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "invalid_base_url", 0)
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "proxy_unavailable", 0)
		}
		if account.Proxy.ID != *account.ProxyID {
			return nil, ErrUpstreamBillingProbeIdentityChanged
		}
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile *tlsfingerprint.Profile
	if s.accountTestService.tlsFPProfileService != nil {
		tlsProfile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	persistFailureWithAPIKeyBalance := func(statusCode int, reason string, retryDelay time.Duration) (*UpstreamBillingProbeSnapshot, error) {
		if !upstreamBillingSupportsWebAccount(normalizedBaseURL) {
			return s.persistProbeFailure(ctx, account, intervalMinutes, now, statusCode, reason, retryDelay)
		}
		balance := s.fetchConfiguredAPIKeyUsageBalance(ctx, account, normalizedBaseURL, apiKey, proxyURL, tlsProfile, now)
		return s.persistProbeFailureWithBalance(ctx, account, intervalMinutes, now, statusCode, reason, retryDelay, balance)
	}
	probeURL := buildOpenAIEndpointURL(normalizedBaseURL, "/v1/sub2api/billing")
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, bytes.NewReader(nil))
	if err != nil {
		return persistFailureWithAPIKeyBalance(0, "request_build_failed", 0)
	}
	// OpenAI 账号保持官方 openai 传输画像；其他平台探测走默认画像。
	profile := HTTPUpstreamProfileDefault
	if account.Platform == PlatformOpenAI {
		profile = HTTPUpstreamProfileOpenAI
	}
	reqCtx := WithHTTPUpstreamProfile(req.Context(), profile)
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(reqCtx))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return persistFailureWithAPIKeyBalance(0, "request_failed", 0)
	}
	if resp == nil || resp.Body == nil {
		return persistFailureWithAPIKeyBalance(0, "empty_response", 0)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
	if readErr != nil {
		return persistFailureWithAPIKeyBalance(resp.StatusCode, "response_read_failed", retryAfter(resp.Header, now))
	}
	if len(body) > upstreamBillingProbeMaxBodyBytes {
		return persistFailureWithAPIKeyBalance(resp.StatusCode, "response_too_large", retryAfter(resp.Header, now))
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		if !upstreamBillingSupportsWebAccount(normalizedBaseURL) {
			return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "unsupported", retryAfter(resp.Header, now))
		}
		data, balance, statusCode, reason, retryDelay := s.fetchPreferredWebAccountBillingData(ctx, account, normalizedBaseURL, apiKey, proxyURL, tlsProfile, now)
		if reason != "" {
			// 网页倍率查询必须体现实时状态，失败时不保留上一轮倍率，避免误导管理员。
			return s.persistProbeFailureCleared(ctx, account, intervalMinutes, now, statusCode, reason, retryDelay, balance)
		}
		return s.persistProbeSuccess(ctx, account, intervalMinutes, now, statusCode, data, balance)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return persistFailureWithAPIKeyBalance(resp.StatusCode, "http_error", retryAfter(resp.Header, now))
	}
	data, err := parseUpstreamBillingProbeResponse(body)
	if err != nil {
		if upstreamBillingSupportsWebAccount(normalizedBaseURL) {
			// 部分 NewAPI 站点会以 200 返回“不支持该接口”的普通 JSON，仍需回退到账户查询链路。
			data, balance, statusCode, reason, retryDelay := s.fetchPreferredWebAccountBillingData(ctx, account, normalizedBaseURL, apiKey, proxyURL, tlsProfile, now)
			if reason != "" {
				return s.persistProbeFailureCleared(ctx, account, intervalMinutes, now, statusCode, reason, retryDelay, balance)
			}
			return s.persistProbeSuccess(ctx, account, intervalMinutes, now, statusCode, data, balance)
		}
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "invalid_response", retryAfter(resp.Header, now))
	}
	var balance *UpstreamAccountBalanceSnapshot
	// 站点管理中的自定义中转优先使用 API Key 查询余额，失败后再回退到网页登录。
	balance = s.fetchConfiguredAPIKeyUsageBalance(ctx, account, normalizedBaseURL, apiKey, proxyURL, tlsProfile, now)
	if balance == nil {
		balance = s.fetchConfiguredWebAccountBalance(ctx, account, normalizedBaseURL, proxyURL, tlsProfile, now)
	}
	return s.persistProbeSuccess(ctx, account, intervalMinutes, now, resp.StatusCode, data, balance)
}

func upstreamBillingSupportsWebAccount(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	// 自定义中转站的协议族由域名凭据配置决定；未知站点同样应进入
	// 现有 NewAPI/Innom 网页查询链路，不能因未维护白名单而直接丢失余额。
	return !upstreamBillingProbeTargetIsOfficialAPI(baseURL)
}
func (s *UpstreamBillingProbeService) persistProbeSuccess(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	data map[string]any,
	balance *UpstreamAccountBalanceSnapshot,
) (*UpstreamBillingProbeSnapshot, error) {
	applyUpstreamRechargeScale(account, data, balance)
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        UpstreamBillingProbeStatusOK,
		Data:          data,
		Balance:       balance,
		ReceivedAt:    probeTimePtr(now),
		FreshUntil:    probeTimePtr(now.Add(2 * time.Duration(intervalMinutes) * time.Minute)),
		LastAttemptAt: now,
		NextProbeAt:   now.Add(nextProbeDelay(intervalMinutes, 0)),
		HTTPStatus:    statusCode,
	}
	// 账号级值域与精度只在真要写回时才有影响：只观察上游声明、未开启同步的
	// 账号不因声明值不适配 accounts.rate_multiplier 而被记成探测失败并进入
	// 指数退避——探测本身成功了，原始声明照常存进快照供展示。
	var syncRate *float64
	previousRate := account.BaseRateMultiplier()
	if upstreamBillingRateSyncEnabled(account) {
		if value, valid := upstreamBillingProbeSyncRate(data); valid {
			syncRate = &value
			snapshot.SyncedRateMultiplier = &value
		} else {
			declared, _ := resolveAccountExtraNumber(data, "resolved_rate_multiplier")
			slog.Warn("upstream_billing_rate_sync_rejected",
				"source", "upstream_billing_probe",
				"account_id", account.ID,
				"declared_resolved_rate_multiplier", declared,
				"max_rate_multiplier", upstreamBillingRateSyncMaxMultiplier,
				"current_rate_multiplier", previousRate,
			)
		}
	}
	if err := s.updateSnapshot(ctx, account, snapshot, syncRate); err != nil {
		return nil, err
	}
	if syncRate != nil {
		// 写回是后台任务的裸 SQL，不经过管理端路由，因此不会产生 audit_logs 行。
		// old_rate_multiplier 是本次探测开始时读到的值（写回的 CAS 不比对该列）。
		slog.Info("upstream_billing_rate_sync_applied",
			"source", "upstream_billing_probe",
			"account_id", account.ID,
			"old_rate_multiplier", previousRate,
			"new_rate_multiplier", *syncRate,
		)
	}
	return snapshot, nil
}

func applyUpstreamRechargeScale(
	account *Account,
	data map[string]any,
	balance *UpstreamAccountBalanceSnapshot,
) {
	if account == nil {
		return
	}
	scale := account.UpstreamRechargeConversionScale()
	if scale == 1 {
		return
	}
	// 统一换算后，展示、自动倍率同步和排序始终消费真实充值口径。
	for _, key := range []string{
		"group_rate_multiplier",
		"user_rate_multiplier",
		"resolved_rate_multiplier",
		"effective_rate_multiplier",
	} {
		if value, ok := resolveAccountExtraNumber(data, key); ok {
			data[key] = value * scale
		}
	}
	if balance != nil && balance.Amount != nil {
		amount := *balance.Amount * scale
		balance.Amount = &amount
	}
}

func (s *UpstreamBillingProbeService) persistProbeFailure(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	reason string,
	retryAfterDuration time.Duration,
) (*UpstreamBillingProbeSnapshot, error) {
	return s.persistProbeFailureWithBalance(ctx, account, intervalMinutes, now, statusCode, reason, retryAfterDuration, nil)
}

func (s *UpstreamBillingProbeService) persistProbeFailureWithBalance(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	reason string,
	retryAfterDuration time.Duration,
	balance *UpstreamAccountBalanceSnapshot,
) (*UpstreamBillingProbeSnapshot, error) {
	applyUpstreamRechargeScale(account, nil, balance)
	previous := decodeUpstreamBillingProbeSnapshot(account.Extra)
	failureCount := 1
	if previous != nil {
		failureCount = previous.FailureCount + 1
	}
	status := UpstreamBillingProbeStatusFailed
	delay := nextProbeDelay(intervalMinutes, retryAfterDuration)
	if reason == "unsupported" {
		status = UpstreamBillingProbeStatusUnsupported
		delay = unsupportedProbeDelay(intervalMinutes, retryAfterDuration)
	}
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        status,
		Balance:       balance,
		LastAttemptAt: now,
		NextProbeAt:   now.Add(delay),
		FailureCount:  failureCount,
		HTTPStatus:    statusCode,
		LastError:     reason,
	}
	if previous != nil && status == UpstreamBillingProbeStatusFailed {
		snapshot.Data = previous.Data
		snapshot.ManualRateMultiplier = previous.ManualRateMultiplier
		snapshot.ReceivedAt = previous.ReceivedAt
		snapshot.FreshUntil = previous.FreshUntil
		if snapshot.FreshUntil == nil && previous.Status == UpstreamBillingProbeStatusOK && previous.ReceivedAt != nil {
			snapshot.FreshUntil = probeTimePtr(previous.ReceivedAt.Add(2 * time.Duration(intervalMinutes) * time.Minute))
		}
	}
	if err := s.updateSnapshot(ctx, account, snapshot, nil); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *UpstreamBillingProbeService) persistProbeFailureCleared(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	reason string,
	retryAfterDuration time.Duration,
	balance *UpstreamAccountBalanceSnapshot,
) (*UpstreamBillingProbeSnapshot, error) {
	applyUpstreamRechargeScale(account, nil, balance)
	failureCount := 1
	if previous := decodeUpstreamBillingProbeSnapshot(account.Extra); previous != nil {
		failureCount = previous.FailureCount + 1
	}
	status := UpstreamBillingProbeStatusFailed
	if reason == "unsupported" {
		status = UpstreamBillingProbeStatusUnsupported
	}
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        status,
		Balance:       balance,
		LastAttemptAt: now,
		NextProbeAt:   now.Add(nextProbeDelay(intervalMinutes, retryAfterDuration)),
		FailureCount:  failureCount,
		HTTPStatus:    statusCode,
		LastError:     reason,
	}
	if previous := decodeUpstreamBillingProbeSnapshot(account.Extra); previous != nil && status == UpstreamBillingProbeStatusFailed {
		// 网页倍率失败会清除自动 data，但不能清除管理员手动兜底值。
		snapshot.ManualRateMultiplier = previous.ManualRateMultiplier
	}
	if err := s.updateSnapshot(ctx, account, snapshot, nil); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *UpstreamBillingProbeService) fetchPreferredWebAccountBillingData(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (map[string]any, *UpstreamAccountBalanceSnapshot, int, string, time.Duration) {
	preferredBalance := s.fetchConfiguredAPIKeyUsageBalance(ctx, account, baseURL, apiKey, proxyURL, tlsProfile, now)
	data, fallbackBalance, statusCode, reason, retryDelay := s.fetchWebAccountBillingData(
		ctx, account, baseURL, apiKey, proxyURL, tlsProfile, now,
	)
	if preferredBalance == nil {
		preferredBalance = fallbackBalance
	}
	return data, preferredBalance, statusCode, reason, retryDelay
}

func (s *UpstreamBillingProbeService) fetchWebAccountBillingData(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (map[string]any, *UpstreamAccountBalanceSnapshot, int, string, time.Duration) {
	if s.upstreamSites == nil {
		return nil, nil, 0, "missing_web_login_credentials", 0
	}
	credential := s.resolveWebAccountCredential(ctx, baseURL)
	if credential == nil {
		return nil, nil, 0, "missing_web_login_credentials", 0
	}
	username := strings.TrimSpace(credential.Username)
	// 密码属于精确凭据，首尾空格可能是密码本身的一部分，登录时不得裁剪。
	password := credential.Password
	host, _, _ := normalizeUpstreamSite(baseURL)
	if upstreamSiteProtocolForHost(host) == "newapi" {
		if isPiteUpstreamSite(host) {
			if token, userID, ok := parsePiteStoredSystemSession(username, password); ok {
				return s.fetchNewAPIAccountDataWithSession(ctx, account, baseURL, apiKey, token, userID, proxyURL, tlsProfile, now)
			}
		}
		return s.fetchNewAPIAccountData(ctx, account, baseURL, apiKey, username, password, proxyURL, tlsProfile, now)
	}
	token, statusCode, reason, retryDelay := s.webAccountAccessToken(ctx, account, baseURL, username, password, proxyURL, tlsProfile, now)
	if reason != "" {
		return nil, nil, statusCode, reason, retryDelay
	}
	groupRate, userRate, statusCode, reason, retryDelay := s.fetchWebAccountCurrentKeyRate(ctx, account, baseURL, apiKey, token, proxyURL, tlsProfile, now)
	if reason == "web_auth_failed" {
		// 网页令牌失效时只清除当前域名和账号的缓存并重登一次，避免循环重试。
		s.clearWebAccountToken(baseURL, username)
		token, statusCode, reason, retryDelay = s.webAccountAccessToken(ctx, account, baseURL, username, password, proxyURL, tlsProfile, now)
		if reason == "" {
			groupRate, userRate, statusCode, reason, retryDelay = s.fetchWebAccountCurrentKeyRate(ctx, account, baseURL, apiKey, token, proxyURL, tlsProfile, now)
		}
	}
	var balance *UpstreamAccountBalanceSnapshot
	if token != "" {
		// 倍率和余额是独立状态；倍率接口失败时仍保留可成功获取的余额。
		balance = s.fetchInnomAccountBalance(ctx, account, baseURL, token, proxyURL, tlsProfile, now)
	}
	if reason != "" {
		return nil, balance, statusCode, reason, retryDelay
	}
	resolvedRate := groupRate
	if userRate != nil {
		resolvedRate = *userRate
	}
	data := map[string]any{
		"object":                    "sub2api.key_billing",
		"schema_version":            1,
		"billing_scope":             "token",
		"group_rate_multiplier":     groupRate,
		"resolved_rate_multiplier":  resolvedRate,
		"peak_rate_enabled":         false,
		"effective_rate_multiplier": resolvedRate,
		"observed_at":               now.UTC().Format(time.RFC3339Nano),
	}
	if userRate != nil {
		data["user_rate_multiplier"] = *userRate
	}
	return data, balance, http.StatusOK, "", 0
}

func (s *UpstreamBillingProbeService) resolveWebAccountCredential(
	ctx context.Context,
	baseURL string,
) *ResolvedUpstreamSiteCredential {
	if s == nil || s.upstreamSites == nil {
		return nil
	}
	credential, err := s.upstreamSites.Resolve(ctx, baseURL)
	if err != nil || credential == nil || strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Password) == "" {
		return nil
	}
	return credential
}

func (s *UpstreamBillingProbeService) fetchConfiguredAPIKeyUsageBalance(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) *UpstreamAccountBalanceSnapshot {
	if !upstreamBillingSupportsWebAccount(baseURL) {
		return nil
	}
	// API Key 余额接口与网页登录配置独立；新站点无需先配置后台账号。
	return s.fetchAPIKeyUsageBalance(ctx, account, baseURL, apiKey, proxyURL, tlsProfile, now)
}

func (s *UpstreamBillingProbeService) fetchConfiguredWebAccountBalance(
	ctx context.Context,
	account *Account,
	baseURL string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) *UpstreamAccountBalanceSnapshot {
	credential := s.resolveWebAccountCredential(ctx, baseURL)
	if credential == nil {
		return nil
	}
	username := strings.TrimSpace(credential.Username)
	// 密码属于精确凭据，首尾空格可能是密码本身的一部分，登录时不得裁剪。
	password := credential.Password
	host, _, _ := normalizeUpstreamSite(baseURL)
	if upstreamSiteProtocolForHost(host) == "newapi" {
		if isPiteUpstreamSite(host) {
			if token, userID, ok := parsePiteStoredSystemSession(username, password); ok {
				return s.fetchNewAPIAccountBalance(ctx, account, baseURL, token, userID, proxyURL, tlsProfile, now)
			}
		}
		token, userID, statusCode, reason, _ := s.newAPIWebAccountAccessToken(
			ctx, account, baseURL, username, password, proxyURL, tlsProfile, now,
		)
		if reason != "" {
			return failedUpstreamAccountBalance(now, statusCode, reason)
		}
		return s.fetchNewAPIAccountBalance(ctx, account, baseURL, token, userID, proxyURL, tlsProfile, now)
	}
	token, statusCode, reason, _ := s.webAccountAccessToken(
		ctx, account, baseURL, username, password, proxyURL, tlsProfile, now,
	)
	if reason != "" {
		return failedUpstreamAccountBalance(now, statusCode, reason)
	}
	return s.fetchInnomAccountBalance(ctx, account, baseURL, token, proxyURL, tlsProfile, now)
}

func (s *UpstreamBillingProbeService) fetchAPIKeyUsageBalance(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) *UpstreamAccountBalanceSnapshot {
	usageURL := buildOpenAIEndpointURL(baseURL, "/v1/usage")
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil
	}
	profile := HTTPUpstreamProfileDefault
	if account.Platform == PlatformOpenAI {
		profile = HTTPUpstreamProfileOpenAI
	}
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(req.Context(), profile)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)

	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil || resp == nil || resp.Body == nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
	if err != nil || len(body) > upstreamBillingProbeMaxBodyBytes || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	amount, ok := numberFromAny(root["balance"])
	unit := strings.TrimSpace(upstreamBillingStringFromAny(root["unit"]))
	if !ok || amount < 0 || unit == "" {
		return nil
	}
	return &UpstreamAccountBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusOK,
		Amount:        &amount,
		Unit:          unit,
		ReceivedAt:    probeTimePtr(now),
		LastAttemptAt: now,
		HTTPStatus:    resp.StatusCode,
	}
}

func (s *UpstreamBillingProbeService) fetchNewAPIAccountData(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	username string,
	password string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (map[string]any, *UpstreamAccountBalanceSnapshot, int, string, time.Duration) {
	token, userID, statusCode, reason, retryDelay := s.newAPIWebAccountAccessToken(
		ctx, account, baseURL, username, password, proxyURL, tlsProfile, now,
	)
	if reason != "" {
		return nil, nil, statusCode, reason, retryDelay
	}
	return s.fetchNewAPIAccountDataWithSession(ctx, account, baseURL, apiKey, token, userID, proxyURL, tlsProfile, now)
}

func (s *UpstreamBillingProbeService) fetchNewAPIAccountDataWithSession(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	token string,
	userID int,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (map[string]any, *UpstreamAccountBalanceSnapshot, int, string, time.Duration) {
	balance := s.fetchNewAPIAccountBalance(ctx, account, baseURL, token, userID, proxyURL, tlsProfile, now)
	rate, statusCode, reason, retryDelay := s.fetchNewAPICurrentKeyRate(
		ctx, account, baseURL, apiKey, token, userID, proxyURL, tlsProfile, now,
	)
	if reason != "" {
		return nil, balance, statusCode, reason, retryDelay
	}
	data := map[string]any{
		"object":                    "sub2api.key_billing",
		"schema_version":            1,
		"billing_scope":             "token",
		"group_rate_multiplier":     rate,
		"resolved_rate_multiplier":  rate,
		"peak_rate_enabled":         false,
		"effective_rate_multiplier": rate,
		"observed_at":               now.UTC().Format(time.RFC3339Nano),
	}
	return data, balance, http.StatusOK, "", 0
}

func isPiteUpstreamSite(host string) bool {
	return strings.EqualFold(strings.TrimSpace(host), "ai.pite.chat")
}

func parsePiteStoredSystemSession(username string, password string) (string, int, bool) {
	userID, err := strconv.Atoi(strings.TrimSpace(username))
	token := strings.TrimSpace(password)
	// Pite 新版查询链路使用站点级 system token 和 New-Api-User，不再走网页登录换 token。
	if err != nil || userID <= 0 || token == "" {
		return "", 0, false
	}
	return token, userID, true
}

func (s *UpstreamBillingProbeService) newAPIWebAccountAccessToken(
	ctx context.Context,
	account *Account,
	baseURL string,
	username string,
	password string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (string, int, int, string, time.Duration) {
	cacheKey := webAccountTokenCacheKey(baseURL, username)
	if cacheKey != "" {
		s.webTokenMu.Lock()
		cached, ok := s.webTokens[cacheKey]
		s.webTokenMu.Unlock()
		if ok && cached.token != "" && cached.userID > 0 && now.Add(30*time.Second).Before(cached.expiresAt) {
			return cached.token, cached.userID, http.StatusOK, "", 0
		}
	}

	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", 0, 0, "request_build_failed", 0
	}
	loginReq, cancelLogin, err := s.newWebAccountRequest(ctx, http.MethodPost, baseURL, "/api/user/login", nil, bytes.NewReader(payload))
	if err != nil {
		return "", 0, 0, "request_build_failed", 0
	}
	defer cancelLogin()
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, statusCode, reason, retryDelay, loginBody := s.doWebAccountRequest(account, loginReq, proxyURL, tlsProfile, now)
	if reason != "" {
		return "", 0, statusCode, reason, retryDelay
	}
	var loginRoot map[string]any
	if err := json.Unmarshal(loginBody, &loginRoot); err != nil || loginRoot["success"] == false {
		return "", 0, statusCode, "web_login_failed", retryDelay
	}
	loginData, _ := loginRoot["data"].(map[string]any)
	userID := intFromAny(loginData["id"])
	if userID <= 0 {
		return "", 0, statusCode, "web_login_failed", retryDelay
	}
	cookies := loginResp.Cookies()
	if len(cookies) == 0 {
		return "", 0, statusCode, "web_login_failed", retryDelay
	}
	cookieParts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		cookieParts = append(cookieParts, cookie.Name+"="+cookie.Value)
	}

	tokenReq, cancelToken, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/user/token", nil, nil)
	if err != nil {
		return "", 0, 0, "request_build_failed", 0
	}
	defer cancelToken()
	tokenReq.Header.Set("Cookie", strings.Join(cookieParts, "; "))
	tokenReq.Header.Set("New-Api-User", strconv.Itoa(userID))
	_, tokenStatus, tokenReason, tokenRetryDelay, tokenBody := s.doWebAccountRequest(account, tokenReq, proxyURL, tlsProfile, now)
	if tokenReason != "" {
		return "", 0, tokenStatus, tokenReason, tokenRetryDelay
	}
	var tokenRoot map[string]any
	if err := json.Unmarshal(tokenBody, &tokenRoot); err != nil || tokenRoot["success"] == false {
		return "", 0, tokenStatus, "web_login_failed", tokenRetryDelay
	}
	token := strings.TrimSpace(upstreamBillingStringFromAny(tokenRoot["data"]))
	if token == "" {
		if tokenData, _ := tokenRoot["data"].(map[string]any); tokenData != nil {
			token = strings.TrimSpace(upstreamBillingFirstNonEmptyString(
				upstreamBillingStringFromAny(tokenData["token"]),
				upstreamBillingStringFromAny(tokenData["access_token"]),
				upstreamBillingStringFromAny(tokenData["key"]),
			))
		}
	}
	if token == "" {
		return "", 0, tokenStatus, "web_login_failed", tokenRetryDelay
	}
	if cacheKey != "" {
		s.webTokenMu.Lock()
		s.webTokens[cacheKey] = cachedWebAccountToken{token: token, userID: userID, expiresAt: now.Add(15 * time.Minute)}
		s.webTokenMu.Unlock()
	}
	return token, userID, tokenStatus, "", 0
}

func (s *UpstreamBillingProbeService) fetchNewAPIAccountBalance(
	ctx context.Context,
	account *Account,
	baseURL string,
	token string,
	userID int,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) *UpstreamAccountBalanceSnapshot {
	selfReq, cancelSelf, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/user/self", nil, nil)
	if err != nil {
		return failedUpstreamAccountBalance(now, 0, "request_build_failed")
	}
	defer cancelSelf()
	applyNewAPIWebAuth(selfReq, token, userID)
	_, statusCode, reason, _, selfBody := s.doWebAccountRequest(account, selfReq, proxyURL, tlsProfile, now)
	if reason != "" {
		return failedUpstreamAccountBalance(now, statusCode, reason)
	}
	var selfRoot map[string]any
	if err := json.Unmarshal(selfBody, &selfRoot); err != nil || selfRoot["success"] == false {
		return failedUpstreamAccountBalance(now, statusCode, "invalid_response")
	}
	selfData, _ := selfRoot["data"].(map[string]any)
	rawQuota, ok := numberFromAny(selfData["quota"])
	if !ok || rawQuota < 0 || intFromAny(selfData["status"]) == 2 {
		return failedUpstreamAccountBalance(now, statusCode, "invalid_response")
	}

	statusReq, cancelStatus, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/status", nil, nil)
	if err != nil {
		return failedUpstreamAccountBalance(now, 0, "request_build_failed")
	}
	defer cancelStatus()
	_, statusHTTPCode, statusReason, _, statusBody := s.doWebAccountRequest(account, statusReq, proxyURL, tlsProfile, now)
	if statusReason != "" {
		return failedUpstreamAccountBalance(now, statusHTTPCode, statusReason)
	}
	var statusRoot map[string]any
	if err := json.Unmarshal(statusBody, &statusRoot); err != nil || statusRoot["success"] == false {
		return failedUpstreamAccountBalance(now, statusHTTPCode, "invalid_response")
	}
	statusData, _ := statusRoot["data"].(map[string]any)
	amount, unit, ok := convertNewAPIQuota(rawQuota, statusData)
	if !ok {
		return failedUpstreamAccountBalance(now, statusHTTPCode, "invalid_response")
	}
	return &UpstreamAccountBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusOK,
		Amount:        &amount,
		Unit:          unit,
		ReceivedAt:    probeTimePtr(now),
		LastAttemptAt: now,
		HTTPStatus:    statusHTTPCode,
	}
}

func convertNewAPIQuota(rawQuota float64, config map[string]any) (float64, string, bool) {
	displayType := strings.ToUpper(strings.TrimSpace(upstreamBillingStringFromAny(config["quota_display_type"])))
	if displayType == "" {
		if displayInCurrency, ok := config["display_in_currency"].(bool); ok && !displayInCurrency {
			displayType = "TOKENS"
		} else {
			displayType = "USD"
		}
	}
	if displayType == "TOKENS" {
		return rawQuota, "TOKENS", true
	}
	quotaPerUnit, ok := numberFromAny(config["quota_per_unit"])
	if !ok || quotaPerUnit <= 0 {
		return 0, "", false
	}
	rate := 1.0
	unit := "USD"
	switch displayType {
	case "CNY":
		rate, ok = numberFromAny(config["usd_exchange_rate"])
		unit = "CNY"
	case "CUSTOM":
		rate, ok = numberFromAny(config["custom_currency_exchange_rate"])
		unit = strings.TrimSpace(upstreamBillingStringFromAny(config["custom_currency_symbol"]))
		if unit == "" {
			unit = "CUSTOM"
		}
	}
	if !ok || rate <= 0 {
		return 0, "", false
	}
	return rawQuota / quotaPerUnit * rate, unit, true
}

func (s *UpstreamBillingProbeService) fetchNewAPICurrentKeyRate(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	token string,
	userID int,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (float64, int, string, time.Duration) {
	tokensReq, cancelTokens, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/token/", url.Values{
		"p":    []string{"1"},
		"size": []string{"100"},
	}, nil)
	if err != nil {
		return 0, 0, "request_build_failed", 0
	}
	defer cancelTokens()
	applyNewAPIWebAuth(tokensReq, token, userID)
	_, statusCode, reason, retryDelay, tokenBody := s.doWebAccountRequest(account, tokensReq, proxyURL, tlsProfile, now)
	if reason != "" {
		return 0, statusCode, reason, retryDelay
	}
	var tokenRoot map[string]any
	if err := json.Unmarshal(tokenBody, &tokenRoot); err != nil || tokenRoot["success"] == false {
		return 0, statusCode, "invalid_response", retryDelay
	}
	tokenData, _ := tokenRoot["data"].(map[string]any)
	items, _ := tokenData["items"].([]any)
	keyName := upstreamBillingFirstNonEmptyString(
		account.GetCredential("key_name"),
		account.GetCredential("api_key_name"),
		account.Name,
	)
	currentKey := matchWebAccountCurrentKey(items, apiKey, keyName)
	parsedBaseURL, _ := url.Parse(baseURL)
	if currentKey == nil && parsedBaseURL != nil && isPiteUpstreamSite(parsedBaseURL.Hostname()) {
		// New API/Pite-style dashboards often hide the token value from /api/token/.
		// 仅 Pite 允许回退到最近使用的活跃 Key；其他 NewAPI 站点不能借用别的 Key 倍率。
		currentKey = selectMostRecentWebAccountKey(items)
	}
	if currentKey == nil {
		return 0, statusCode, "key_rate_not_found", retryDelay
	}
	group := strings.TrimSpace(upstreamBillingStringFromAny(currentKey["group"]))
	if group == "" {
		return 0, statusCode, "key_rate_not_found", retryDelay
	}

	groupsReq, cancelGroups, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/user/self/groups", nil, nil)
	if err != nil {
		return 0, 0, "request_build_failed", 0
	}
	defer cancelGroups()
	applyNewAPIWebAuth(groupsReq, token, userID)
	_, groupsStatus, groupsReason, groupsRetryDelay, groupsBody := s.doWebAccountRequest(account, groupsReq, proxyURL, tlsProfile, now)
	if groupsReason != "" {
		return 0, groupsStatus, groupsReason, groupsRetryDelay
	}
	var groupsRoot map[string]any
	if err := json.Unmarshal(groupsBody, &groupsRoot); err != nil || groupsRoot["success"] == false {
		return 0, groupsStatus, "invalid_response", groupsRetryDelay
	}
	groupsData, _ := groupsRoot["data"].(map[string]any)
	rateValue := groupsData[group]
	if groupConfig, _ := rateValue.(map[string]any); groupConfig != nil {
		rateValue = upstreamBillingFirstNonNil(groupConfig["ratio"], groupConfig["rate_multiplier"], groupConfig["rate"])
	}
	rate, ok := numberFromAny(rateValue)
	if !ok || rate < 0 {
		return 0, groupsStatus, "key_rate_not_found", groupsRetryDelay
	}
	return rate, groupsStatus, "", 0
}

func applyNewAPIWebAuth(req *http.Request, token string, userID int) {
	req.Header.Set("Authorization", token)
	req.Header.Set("New-Api-User", strconv.Itoa(userID))
}

func upstreamBillingFirstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (s *UpstreamBillingProbeService) fetchInnomAccountBalance(
	ctx context.Context,
	account *Account,
	baseURL string,
	token string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) *UpstreamAccountBalanceSnapshot {
	profilePath := "/api/v1/auth/me"
	if parsed, err := url.Parse(baseURL); err == nil {
		switch strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".") {
		case "hubway.cc", "mxamaxai.com", "ai.maok.shop":
			// 这些站点的当前登录态资料接口使用 user/profile，旧 auth/me 不返回余额。
			profilePath = "/api/v1/user/profile"
		}
	}
	req, cancel, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, profilePath, nil, nil)
	if err != nil {
		return failedUpstreamAccountBalance(now, 0, "request_build_failed")
	}
	defer cancel()
	req.Header.Set("Authorization", "Bearer "+token)
	_, statusCode, reason, _, body := s.doWebAccountRequest(account, req, proxyURL, tlsProfile, now)
	if reason != "" {
		return failedUpstreamAccountBalance(now, statusCode, reason)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil || intFromAny(root["code"]) != 0 {
		return failedUpstreamAccountBalance(now, statusCode, "invalid_response")
	}
	payload, _ := root["data"].(map[string]any)
	amount, ok := numberFromAny(payload["balance"])
	if !ok || amount < 0 {
		return failedUpstreamAccountBalance(now, statusCode, "invalid_response")
	}
	return &UpstreamAccountBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusOK,
		Amount:        &amount,
		Unit:          "USD",
		ReceivedAt:    probeTimePtr(now),
		LastAttemptAt: now,
		HTTPStatus:    statusCode,
	}
}

func failedUpstreamAccountBalance(now time.Time, statusCode int, reason string) *UpstreamAccountBalanceSnapshot {
	return &UpstreamAccountBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusFailed,
		LastAttemptAt: now,
		HTTPStatus:    statusCode,
		LastError:     reason,
	}
}

func (s *UpstreamBillingProbeService) webAccountAccessToken(
	ctx context.Context,
	account *Account,
	baseURL string,
	username string,
	password string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (string, int, string, time.Duration) {
	cacheKey := webAccountTokenCacheKey(baseURL, username)
	if cacheKey != "" {
		s.webTokenMu.Lock()
		cached, ok := s.webTokens[cacheKey]
		s.webTokenMu.Unlock()
		if ok && cached.token != "" && now.Add(30*time.Second).Before(cached.expiresAt) {
			return cached.token, http.StatusOK, "", 0
		}
	}

	loginPayload, err := json.Marshal(map[string]string{"email": username, "password": password})
	if err != nil {
		return "", 0, "request_build_failed", 0
	}
	token, expiresIn, statusCode, reason, retryDelay := s.webAccountLogin(ctx, account, baseURL, proxyURL, tlsProfile, loginPayload, now)
	if reason != "" {
		return "", statusCode, reason, retryDelay
	}
	if cacheKey != "" {
		if expiresIn <= 0 {
			expiresIn = 15 * time.Minute
		}
		s.webTokenMu.Lock()
		s.webTokens[cacheKey] = cachedWebAccountToken{token: token, expiresAt: now.Add(expiresIn)}
		s.webTokenMu.Unlock()
	}
	return token, statusCode, "", 0
}

func (s *UpstreamBillingProbeService) clearWebAccountToken(baseURL string, username string) {
	cacheKey := webAccountTokenCacheKey(baseURL, username)
	if cacheKey == "" {
		return
	}
	s.webTokenMu.Lock()
	delete(s.webTokens, cacheKey)
	s.webTokenMu.Unlock()
}

func webAccountTokenCacheKey(baseURL string, username string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Hostname() == "" || strings.TrimSpace(username) == "" {
		return ""
	}
	return strings.ToLower(parsed.Hostname()) + "|" + strings.ToLower(strings.TrimSpace(username))
}

func (s *UpstreamBillingProbeService) webAccountLogin(
	ctx context.Context,
	account *Account,
	baseURL string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	payload []byte,
	now time.Time,
) (string, time.Duration, int, string, time.Duration) {
	req, cancel, err := s.newWebAccountRequest(ctx, http.MethodPost, baseURL, "/api/v1/auth/login", nil, bytes.NewReader(payload))
	if err != nil {
		return "", 0, 0, "request_build_failed", 0
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	resp, statusCode, reason, retryDelay, body := s.doWebAccountRequest(account, req, proxyURL, tlsProfile, now)
	if reason != "" {
		return "", 0, statusCode, reason, retryDelay
	}
	_ = resp
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil || intFromAny(root["code"]) != 0 {
		return "", 0, statusCode, "web_login_failed", retryDelay
	}
	data, _ := root["data"].(map[string]any)
	token := strings.TrimSpace(upstreamBillingStringFromAny(data["access_token"]))
	if token == "" {
		return "", 0, statusCode, "web_login_failed", retryDelay
	}
	expiresSeconds, _ := numberFromAny(data["expires_in"])
	return token, time.Duration(expiresSeconds * float64(time.Second)), statusCode, "", 0
}

func (s *UpstreamBillingProbeService) fetchWebAccountCurrentKeyRate(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	token string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (float64, *float64, int, string, time.Duration) {
	keysReq, cancelKeys, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/v1/keys", url.Values{
		"page":      []string{"1"},
		"page_size": []string{"100"},
	}, nil)
	if err != nil {
		return 0, nil, 0, "request_build_failed", 0
	}
	defer cancelKeys()
	keysReq.Header.Set("Authorization", "Bearer "+token)
	_, statusCode, reason, retryDelay, body := s.doWebAccountRequest(account, keysReq, proxyURL, tlsProfile, now)
	if reason != "" {
		return 0, nil, statusCode, reason, retryDelay
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil || intFromAny(root["code"]) != 0 {
		return 0, nil, statusCode, "invalid_response", retryDelay
	}
	payload, _ := root["data"].(map[string]any)
	items, _ := payload["items"].([]any)
	keyName := upstreamBillingFirstNonEmptyString(
		account.GetCredential("key_name"),
		account.GetCredential("api_key_name"),
		account.Name,
	)
	currentKey := matchWebAccountCurrentKey(items, apiKey, keyName)
	if currentKey == nil {
		return 0, nil, statusCode, "key_rate_not_found", retryDelay
	}
	var groupID string
	var groupRate float64
	group, _ := currentKey["group"].(map[string]any)
	rate, ok := numberFromAny(group["rate_multiplier"])
	if !ok || rate < 0 {
		return 0, nil, statusCode, "invalid_response", retryDelay
	}
	groupRate = rate
	groupID = identifierString(currentKey["group_id"])
	if groupID == "" {
		groupID = identifierString(group["id"])
	}

	ratesReq, cancelRates, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/v1/groups/rates", nil, nil)
	if err != nil {
		return 0, nil, 0, "request_build_failed", 0
	}
	defer cancelRates()
	ratesReq.Header.Set("Authorization", "Bearer "+token)
	_, ratesStatus, ratesReason, ratesRetryDelay, ratesBody := s.doWebAccountRequest(account, ratesReq, proxyURL, tlsProfile, now)
	if ratesReason != "" {
		return 0, nil, ratesStatus, ratesReason, ratesRetryDelay
	}
	var ratesRoot map[string]any
	if err := json.Unmarshal(ratesBody, &ratesRoot); err != nil || intFromAny(ratesRoot["code"]) != 0 {
		return 0, nil, ratesStatus, "invalid_response", ratesRetryDelay
	}
	rates, _ := ratesRoot["data"].(map[string]any)
	if groupID != "" {
		if rate, ok := numberFromAny(rates[groupID]); ok && rate >= 0 {
			return groupRate, &rate, ratesStatus, "", 0
		}
	}
	return groupRate, nil, statusCode, "", 0
}

func (s *UpstreamBillingProbeService) newWebAccountRequest(
	ctx context.Context,
	method string,
	baseURL string,
	path string,
	query url.Values,
	body io.Reader,
) (*http.Request, context.CancelFunc, error) {
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	req, err := http.NewRequestWithContext(probeCtx, method, parsed.String(), body)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	req.Header.Set("Accept", "application/json")
	// 鱼鱼会拒绝默认脚本标识；已通过其网页客户端标识验证登录与订阅查询。
	if strings.EqualFold(parsed.Hostname(), "sub.anzhiyu.com") {
		req.Header.Set("User-Agent", "Mozilla/5.0")
	}
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI)))
	return req, cancel, nil
}

func (s *UpstreamBillingProbeService) doWebAccountRequest(
	account *Account,
	req *http.Request,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	now time.Time,
) (*http.Response, int, string, time.Duration, []byte) {
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return nil, 0, "request_failed", 0, nil
	}
	if resp == nil || resp.Body == nil {
		return resp, 0, "empty_response", 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
	if readErr != nil {
		return resp, resp.StatusCode, "response_read_failed", retryAfter(resp.Header, now), nil
	}
	if len(body) > upstreamBillingProbeMaxBodyBytes {
		return resp, resp.StatusCode, "response_too_large", retryAfter(resp.Header, now), nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp, resp.StatusCode, "web_auth_failed", retryAfter(resp.Header, now), body
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, resp.StatusCode, "http_error", retryAfter(resp.Header, now), body
	}
	return resp, resp.StatusCode, "", 0, body
}

func upstreamBillingFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func webAccountKeyActive(value any) bool {
	if value == nil {
		return true
	}
	if s := strings.ToLower(strings.TrimSpace(upstreamBillingStringFromAny(value))); s != "" {
		return s == "active" || s == "enabled" || s == "1" || s == "true"
	}
	if n, ok := numberFromAny(value); ok {
		return n != 0
	}
	return true
}

func matchWebAccountCurrentKey(items []any, apiKey string, keyName string) map[string]any {
	activeItems := make([]map[string]any, 0, len(items))
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		if item == nil || !webAccountKeyItemActive(item) {
			continue
		}
		activeItems = append(activeItems, item)
	}

	trimmedAPIKey := strings.TrimSpace(apiKey)
	if trimmedAPIKey != "" {
		for _, item := range activeItems {
			for _, candidate := range webAccountKeyIdentityCandidates(item) {
				if strings.EqualFold(candidate, trimmedAPIKey) {
					return item
				}
			}
		}
		for _, item := range activeItems {
			for _, candidate := range webAccountKeyIdentityCandidates(item) {
				if webAccountMaskedKeyMatches(candidate, trimmedAPIKey) {
					return item
				}
			}
		}
	}

	normalizedName := strings.ToLower(strings.TrimSpace(keyName))
	if normalizedName == "" {
		return nil
	}
	var namedMatch map[string]any
	for _, item := range activeItems {
		if !webAccountKeyNameMatches(item, normalizedName) {
			continue
		}
		if namedMatch != nil {
			return nil
		}
		namedMatch = item
	}
	return namedMatch
}

func selectMostRecentWebAccountKey(items []any) map[string]any {
	var selected map[string]any
	var selectedAt float64
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		if item == nil || !webAccountKeyItemActive(item) {
			continue
		}
		if strings.TrimSpace(upstreamBillingStringFromAny(item["group"])) == "" {
			continue
		}
		accessedAt := webAccountKeyLastUsedAt(item)
		if selected == nil || accessedAt >= selectedAt {
			selected = item
			selectedAt = accessedAt
		}
	}
	return selected
}

func webAccountKeyLastUsedAt(item map[string]any) float64 {
	for _, field := range []string{"accessed_time", "last_used_at", "last_used_time", "used_at", "updated_time", "created_time"} {
		if value, ok := numberFromAny(item[field]); ok {
			return value
		}
	}
	return 0
}

func webAccountKeyItemActive(item map[string]any) bool {
	if value, ok := item["status"]; ok {
		return webAccountKeyActive(value)
	}
	if value, ok := item["enabled"].(bool); ok {
		return value
	}
	if value, ok := item["is_active"].(bool); ok {
		return value
	}
	return true
}

func webAccountKeyIdentityCandidates(item map[string]any) []string {
	fields := []string{
		"key", "api_key", "token", "secret", "key_value", "value",
		"masked_key", "key_masked", "key_preview", "preview", "key_prefix", "prefix",
	}
	values := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		if value := strings.TrimSpace(upstreamBillingStringFromAny(item[field])); value != "" {
			values = append(values, value)
		}
	}
	prefix := strings.TrimSpace(upstreamBillingStringFromAny(item["key_prefix"]))
	suffix := strings.TrimSpace(upstreamBillingStringFromAny(item["key_suffix"]))
	if prefix != "" && suffix != "" {
		values = append(values, prefix+"..."+suffix)
	}
	return values
}

func webAccountKeyNameMatches(item map[string]any, normalizedName string) bool {
	for _, field := range []string{"name", "title", "label", "alias", "remark", "note"} {
		value := strings.ToLower(strings.TrimSpace(upstreamBillingStringFromAny(item[field])))
		if value == normalizedName {
			return true
		}
	}
	return false
}

func webAccountMaskedKeyMatches(candidate string, fullAPIKey string) bool {
	masked := strings.TrimSpace(candidate)
	full := strings.TrimSpace(fullAPIKey)
	if masked == "" || full == "" {
		return false
	}
	if strings.EqualFold(masked, full) {
		return true
	}

	lowerMasked := strings.ToLower(masked)
	lowerFull := strings.ToLower(full)
	// Pite 的脱敏值会省略 sk-，同时保留原值比较以兼容其他 NewAPI 站点。
	comparableFullKeys := []string{lowerFull}
	if strings.HasPrefix(lowerFull, "sk-") {
		comparableFullKeys = append(comparableFullKeys, strings.TrimPrefix(lowerFull, "sk-"))
	}
	parts := strings.FieldsFunc(lowerMasked, func(r rune) bool {
		switch r {
		case '.', '*', '•', '·', '_':
			return true
		default:
			return false
		}
	})
	for _, comparableFull := range comparableFullKeys {
		if len(masked) >= 8 && strings.HasPrefix(comparableFull, lowerMasked) {
			return true
		}
		if len(parts) >= 2 {
			prefix := strings.TrimSpace(parts[0])
			suffix := strings.TrimSpace(parts[len(parts)-1])
			if len(prefix) >= 2 && len(suffix) >= 2 && strings.HasPrefix(comparableFull, prefix) && strings.HasSuffix(comparableFull, suffix) {
				return true
			}
		}
		if len(parts) == 1 {
			token := strings.TrimSpace(parts[0])
			if len(token) >= 6 && (strings.HasPrefix(comparableFull, token) || strings.HasSuffix(comparableFull, token)) {
				return true
			}
		}
	}
	return false
}

func upstreamBillingStringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case fmt.Stringer:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func numberFromAny(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, math.IsNaN(v) == false && math.IsInf(v, 0) == false
	case json.Number:
		n, err := v.Float64()
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func intFromAny(value any) int {
	if n, ok := numberFromAny(value); ok {
		return int(n)
	}
	return 0
}

func identifierString(value any) string {
	if s := strings.TrimSpace(upstreamBillingStringFromAny(value)); s != "" {
		return s
	}
	if n, ok := numberFromAny(value); ok && math.Trunc(n) == n {
		return strconv.FormatInt(int64(n), 10)
	}
	return ""
}

func (s *UpstreamBillingProbeService) updateSnapshot(
	ctx context.Context,
	account *Account,
	snapshot *UpstreamBillingProbeSnapshot,
	rateMultiplier *float64,
) error {
	writer, ok := s.accountRepo.(upstreamBillingProbeSnapshotWriter)
	if !ok {
		return ErrUpstreamBillingProbeUnavailable
	}
	if err := writer.UpdateUpstreamBillingProbeSnapshot(ctx, account, snapshot, rateMultiplier); err != nil {
		return err
	}
	s.persistBalanceCenterProbeSnapshot(ctx, account, snapshot)
	return nil
}

func (s *UpstreamBillingProbeService) persistBalanceCenterProbeSnapshot(
	ctx context.Context,
	account *Account,
	snapshot *UpstreamBillingProbeSnapshot,
) {
	if s == nil || s.balanceCenterStore == nil {
		return
	}
	balanceSnapshot, err := buildBalanceCenterProbeSnapshot(account, snapshot)
	if err == nil {
		_, err = s.balanceCenterStore.PersistSnapshot(ctx, balanceSnapshot)
	}
	if err != nil {
		accountID := int64(0)
		if account != nil {
			accountID = account.ID
		}
		// 不记录上游响应或凭据，余额中心旁路失败不能扩大敏感信息暴露面。
		slog.Warn("balance_center_probe_snapshot_persist_failed", "account_id", accountID, "error_type", fmt.Sprintf("%T", err))
	}
}

func buildBalanceCenterProbeSnapshot(account *Account, snapshot *UpstreamBillingProbeSnapshot) (*BalanceCenterSnapshot, error) {
	if account == nil || snapshot == nil {
		return nil, errors.New("余额中心探测快照参数不完整")
	}
	parsed, err := url.Parse(strings.TrimSpace(account.GetCredential("base_url")))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("余额中心探测站点地址无效")
	}
	normalizedDomain := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	baseURL := strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
	accountID := account.ID
	siteName := strings.TrimSpace(account.Name)
	if bracketed := defaultUpstreamSiteDisplayName([]string{account.Name}, ""); bracketed != "" {
		siteName = bracketed
	}
	result := &BalanceCenterSnapshot{
		AccountID:        &accountID,
		SiteName:         siteName,
		NormalizedDomain: normalizedDomain,
		BaseURL:          baseURL,
		Source:           "sub2api_probe",
		SourceKey:        fmt.Sprintf("sub2api_probe:%d:%d", account.ID, snapshot.LastAttemptAt.UnixNano()),
		Status:           snapshot.Status,
		ConversionScale:  balanceCenterProbeConversionScale(account),
		Reason:           snapshot.LastError,
		ProbedAt:         snapshot.LastAttemptAt,
		LastUsedAt:       account.LastUsedAt,
	}
	if result.SiteName == "" {
		result.SiteName = normalizedDomain
	}
	payload := map[string]any{}
	if snapshot.HTTPStatus != 0 {
		payload["http_status"] = snapshot.HTTPStatus
	}
	if snapshot.FailureCount != 0 {
		payload["failure_count"] = snapshot.FailureCount
	}
	result.Payload, err = json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化余额中心探测摘要失败: %w", err)
	}
	// 余额与倍率是独立探测结果；即使倍率链路失败，成功取得的余额仍需进入低余额告警。
	if snapshot.Balance != nil && snapshot.Balance.Status == UpstreamBillingProbeStatusOK && snapshot.Balance.Amount != nil {
		converted := *snapshot.Balance.Amount
		raw := converted / result.ConversionScale
		result.Balance = &raw
		result.ConvertedBalance = &converted
		result.Currency = snapshot.Balance.Unit
	}
	if snapshot.Status != UpstreamBillingProbeStatusOK {
		return result, nil
	}
	if value, ok := resolveAccountExtraNumber(snapshot.Data, "resolved_rate_multiplier"); ok {
		result.RateMultiplier = &value
	}
	return result, nil
}

func balanceCenterProbeConversionScale(account *Account) float64 {
	// 兼容账号级换算字段尚未合入的旧基线；新字段存在时以账号配置为准。
	type conversionScaleProvider interface {
		UpstreamRechargeConversionScale() float64
	}
	if provider, ok := any(account).(conversionScaleProvider); ok {
		return provider.UpstreamRechargeConversionScale()
	}
	parsed, err := url.Parse(strings.TrimSpace(account.GetCredential("base_url")))
	if err == nil && strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".") == "hubway.cc" {
		return 0.1
	}
	return 1
}

func parseUpstreamBillingProbeResponse(body []byte) (map[string]any, error) {
	var response upstreamBillingProbeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Object != "sub2api.key_billing" || response.SchemaVersion != 1 || response.BillingScope != "token" {
		return nil, fmt.Errorf("unexpected billing response schema")
	}
	if response.GroupRateMultiplier == nil || response.ResolvedRateMultiplier == nil ||
		response.PeakRateEnabled == nil || response.EffectiveRateMultiplier == nil {
		return nil, fmt.Errorf("incomplete billing response")
	}
	for _, value := range []float64{
		*response.GroupRateMultiplier,
		*response.ResolvedRateMultiplier,
		*response.EffectiveRateMultiplier,
	} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid billing multiplier")
		}
	}
	if response.UserRateMultiplier != nil && (*response.UserRateMultiplier < 0 || math.IsNaN(*response.UserRateMultiplier) || math.IsInf(*response.UserRateMultiplier, 0)) {
		return nil, fmt.Errorf("invalid user billing multiplier")
	}
	expectedResolved := *response.GroupRateMultiplier
	if response.UserRateMultiplier != nil {
		expectedResolved = *response.UserRateMultiplier
	}
	if !equalBillingMultiplier(*response.ResolvedRateMultiplier, expectedResolved) {
		return nil, fmt.Errorf("inconsistent resolved billing multiplier")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, response.ObservedAt)
	if err != nil || observedAt.IsZero() {
		return nil, fmt.Errorf("invalid observed_at")
	}
	data := map[string]any{
		"object":                    response.Object,
		"schema_version":            response.SchemaVersion,
		"billing_scope":             response.BillingScope,
		"group_rate_multiplier":     *response.GroupRateMultiplier,
		"resolved_rate_multiplier":  *response.ResolvedRateMultiplier,
		"peak_rate_enabled":         *response.PeakRateEnabled,
		"effective_rate_multiplier": *response.EffectiveRateMultiplier,
		"observed_at":               observedAt.UTC().Format(time.RFC3339Nano),
	}
	if response.UserRateMultiplier != nil {
		data["user_rate_multiplier"] = *response.UserRateMultiplier
	}
	if *response.PeakRateEnabled {
		if response.PeakStart == nil || response.PeakEnd == nil || response.Timezone == nil ||
			response.PeakRateMultiplier == nil || response.AppliedPeakMultiplier == nil ||
			*response.PeakStart == "" || *response.PeakEnd == "" || *response.Timezone == "" ||
			*response.PeakRateMultiplier < 0 || *response.AppliedPeakMultiplier < 0 ||
			math.IsNaN(*response.PeakRateMultiplier) || math.IsInf(*response.PeakRateMultiplier, 0) ||
			math.IsNaN(*response.AppliedPeakMultiplier) || math.IsInf(*response.AppliedPeakMultiplier, 0) {
			return nil, fmt.Errorf("incomplete peak billing response")
		}
		data["peak_start"] = *response.PeakStart
		data["peak_end"] = *response.PeakEnd
		data["peak_rate_multiplier"] = *response.PeakRateMultiplier
		data["applied_peak_multiplier"] = *response.AppliedPeakMultiplier
		data["timezone"] = *response.Timezone
	}
	appliedPeak, ok := upstreamBillingPeakMultiplierAt(data, observedAt)
	if !ok {
		return nil, fmt.Errorf("invalid peak billing response")
	}
	if response.PeakRateEnabled != nil && *response.PeakRateEnabled {
		if !equalBillingMultiplier(*response.AppliedPeakMultiplier, appliedPeak) {
			return nil, fmt.Errorf("inconsistent applied peak multiplier")
		}
	} else if response.AppliedPeakMultiplier != nil && !equalBillingMultiplier(*response.AppliedPeakMultiplier, 1) {
		return nil, fmt.Errorf("inconsistent applied peak multiplier")
	}
	if !equalBillingMultiplier(*response.EffectiveRateMultiplier, *response.ResolvedRateMultiplier*appliedPeak) {
		return nil, fmt.Errorf("inconsistent effective billing multiplier")
	}
	return data, nil
}

func upstreamBillingRateAt(data map[string]any, now time.Time) (float64, bool) {
	if scope, _ := data["billing_scope"].(string); scope != "token" {
		return 0, false
	}
	base, ok := resolveAccountExtraNumber(data, "resolved_rate_multiplier")
	if !ok || base < 0 || math.IsNaN(base) || math.IsInf(base, 0) {
		return 0, false
	}
	appliedPeak, ok := upstreamBillingPeakMultiplierAt(data, now)
	if !ok {
		return 0, false
	}
	base *= appliedPeak
	if math.IsNaN(base) || math.IsInf(base, 0) {
		return 0, false
	}
	return base, true
}

// upstreamBillingProbeSyncRate converts the declared multiplier into the value
// the automatic write-back may store in accounts.rate_multiplier, at the
// precision that column supports (DECIMAL(10,4)).
//
// It reads resolved_rate_multiplier, not effective_rate_multiplier: the
// effective value folds in the peak coefficient that happened to apply at the
// instant of the probe, so writing it would freeze one probe cycle's peak (or
// off-peak) factor into a static column, while display and scheduling
// recompute the peak factor for the current time through upstreamBillingRateAt.
//
// The accepted range is deliberately narrower than the column:
//   - 0 is rejected. accountCost multiplies the request cost by this value, so
//     an upstream-declared 0 would stop quota_used from ever growing and every
//     admin-configured account quota and cost alert would silently stop
//     working. Admins may still set 0 by hand; only the automatic path refuses.
//   - anything above upstreamBillingRateSyncMaxMultiplier is rejected.
//
// A rejected declaration leaves the current multiplier untouched; the probe
// still records an OK snapshot carrying the raw declaration for display.
func upstreamBillingProbeSyncRate(data map[string]any) (float64, bool) {
	value, ok := resolveAccountExtraNumber(data, "resolved_rate_multiplier")
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	rounded := math.Round(value*upstreamBillingProbeAccountRateScale) / upstreamBillingProbeAccountRateScale
	if rounded <= 0 || rounded > upstreamBillingRateSyncMaxMultiplier {
		return 0, false
	}
	return rounded, true
}

func upstreamBillingPeakMultiplierAt(data map[string]any, now time.Time) (float64, bool) {
	peakEnabled, ok := data["peak_rate_enabled"].(bool)
	if !ok {
		return 0, false
	}
	if !peakEnabled {
		return 1, true
	}

	start, startOK := data["peak_start"].(string)
	end, endOK := data["peak_end"].(string)
	timezoneName, timezoneOK := data["timezone"].(string)
	peakMultiplier, multiplierOK := resolveAccountExtraNumber(data, "peak_rate_multiplier")
	startMinute, validStart := parseMinutes(start)
	endMinute, validEnd := parseMinutes(end)
	if !startOK || !endOK || !timezoneOK || !multiplierOK || !validStart || !validEnd ||
		startMinute >= endMinute || peakMultiplier < 0 || math.IsNaN(peakMultiplier) || math.IsInf(peakMultiplier, 0) {
		return 0, false
	}
	location, err := time.LoadLocation(timezoneName)
	if err != nil {
		return 0, false
	}

	local := now.In(location)
	minute := local.Hour()*60 + local.Minute()
	if minute >= startMinute && minute < endMinute {
		return peakMultiplier, true
	}
	return 1, true
}

func equalBillingMultiplier(left, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
		return false
	}
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func decodeUpstreamBillingProbeSnapshot(extra map[string]any) *UpstreamBillingProbeSnapshot {
	if extra == nil {
		return nil
	}
	value, ok := extra[UpstreamBillingProbeExtraKey]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot UpstreamBillingProbeSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Status == "" {
		return nil
	}
	if snapshot.Status != UpstreamBillingProbeStatusOK &&
		snapshot.Status != UpstreamBillingProbeStatusUnsupported &&
		snapshot.Status != UpstreamBillingProbeStatusFailed {
		return nil
	}
	return &snapshot
}

// IsUpstreamBillingProbeIdentity 判断账号是否可以开启上游账单探测。
// 只要是带静态 API Key 的账号即可尝试；官方域名仍会在探测前短路，
// 自定义站点则按域名协议配置进入统一的 NewAPI/Innom 查询链路。
func IsUpstreamBillingProbeIdentity(platform, accountType string) bool {
	if accountType != AccountTypeAPIKey || strings.TrimSpace(platform) == "" {
		return false
	}
	// API Key 账号都具备静态密钥和自定义 base_url；平台标签可能来自
	// 未内置的“不知名”站点，但探测协议仍可按域名配置解析。
	return true
}

func isUpstreamBillingProbeAccount(account *Account) bool {
	return account != nil && IsUpstreamBillingProbeIdentity(account.Platform, account.Type)
}

// upstreamBillingProbeOfficialAPIDomains lists the root domains of official
// provider APIs. The create form fills empty base_url values with official
// defaults (and offers official regional presets like us-east-1.api.x.ai),
// so probing them would send the account key to an official API path that
// cannot exist. Matching is by registrable root domain — exact host or any
// subdomain, after stripping the port and a trailing DNS dot — because no
// third-party sub2api relay can live under these domains, while custom
// relays (the only targets that can answer /v1/sub2api/billing) always do
// probe. OpenAI-platform accounts never reach this check: they keep the
// upstream-official behavior of probing api.openai.com.
// ollama.com is a first-class configuration here (Ollama Cloud accounts are
// platform openai/anthropic with base_url https://ollama.com/v1), and it is
// an official provider API just like the rest, so it belongs on this list.
// CN provider domains (moonshot.cn / kimi.com / bigmodel.cn / deepseek.com)
// serve the same role: official APIs that can never host /v1/sub2api/billing,
// so their accounts short-circuit to "unsupported" without a request.
var upstreamBillingProbeOfficialAPIDomains = []string{
	"anthropic.com",
	"googleapis.com",
	"x.ai",
	"grok.com",
	"openai.com",
	"ollama.com",
	"moonshot.cn",
	"kimi.com",
	"bigmodel.cn",
	"deepseek.com",
}

func upstreamBillingProbeTargetIsOfficialAPI(baseURL string) bool {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return true
	}
	for _, domain := range upstreamBillingProbeOfficialAPIDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func upstreamBillingProbeEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[UpstreamBillingProbeEnabledExtraKey].(bool)
	return ok && enabled
}

// upstreamBillingRateSyncEnabled is the probe-side pre-filter deciding whether
// a rate is even proposed for write-back. It is a necessary condition, not the
// authority: the repository CAS re-checks both switches against the row it
// updates, so a switch flipped between load and write can never sneak a rate in.
func upstreamBillingRateSyncEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[UpstreamBillingRateSyncEnabledExtraKey].(bool)
	return ok && enabled && upstreamBillingProbeEnabled(account)
}

func (s *UpstreamBillingProbeService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func nextProbeDelay(intervalMinutes int, retryAfterDuration time.Duration) time.Duration {
	interval := time.Duration(intervalMinutes) * time.Minute
	if interval < upstreamBillingProbeMinIntervalMinutes*time.Minute {
		interval = upstreamBillingProbeMinIntervalMinutes * time.Minute
	}
	if interval > upstreamBillingProbeMaxDelay {
		interval = upstreamBillingProbeMaxDelay
	}
	jitterRange := interval / 5
	if jitterRange > 5*time.Minute {
		jitterRange = 5 * time.Minute
	}
	if jitterRange > 0 {
		interval += time.Duration(rand.Int64N(int64(jitterRange)*2+1)) - jitterRange
	}
	if retryAfterDuration > interval {
		// Retry-After is an explicit upstream instruction; do not shorten it
		// with the local maximum delay.
		return retryAfterDuration
	}
	if interval > upstreamBillingProbeMaxDelay {
		return upstreamBillingProbeMaxDelay
	}
	return interval
}

// unsupportedProbeDelay 拉长 unsupported 账号的重探间隔，让无效候选自然退出
// 热队列，不再和真正接入 sub2api 的中转账号抢每周期的探测名额。
// 仍按 upstreamBillingProbeMaxDelay 封顶，保证上游后来接入 sub2api 时最迟一天
// 内会被重新发现；base 本身已达上限（例如 Retry-After 明确要求更久）时原样返回，
// 不缩短上游指令。
func unsupportedProbeDelay(intervalMinutes int, retryAfterDuration time.Duration) time.Duration {
	base := nextProbeDelay(intervalMinutes, retryAfterDuration)
	if base >= upstreamBillingProbeMaxDelay {
		return base
	}
	stretched := base * upstreamBillingProbeUnsupportedDelayFactor
	if stretched > upstreamBillingProbeMaxDelay {
		return upstreamBillingProbeMaxDelay
	}
	return stretched
}

func retryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

func probeTimePtr(value time.Time) *time.Time {
	return &value
}

func safeProbeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrUpstreamBillingProbeAccountInvalid) {
		return ErrUpstreamBillingProbeAccountInvalid.Error()
	}
	if errors.Is(err, ErrUpstreamBillingProbeUnavailable) {
		return ErrUpstreamBillingProbeUnavailable.Error()
	}
	return "probe_failed"
}
