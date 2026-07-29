package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	// These values live in accounts.extra so PR2 does not require a schema migration.
	UpstreamBillingProbeExtraKey        = "upstream_billing_probe"
	UpstreamBillingProbeEnabledExtraKey = "upstream_billing_probe_enabled"

	upstreamBillingProbeDefaultIntervalMinutes = 30
	upstreamBillingProbeMinIntervalMinutes     = 5
	upstreamBillingProbeMaxIntervalMinutes     = 24 * 60
	upstreamBillingProbeCycleInterval          = time.Minute
	upstreamBillingProbeRequestTimeout         = 10 * time.Second
	upstreamBillingProbeMaxBodyBytes           = 64 * 1024
	upstreamBillingProbeMaxPerCycle            = 20
	upstreamBillingProbeConcurrency            = 4
	upstreamBillingProbeMaxDelay               = 24 * time.Hour
	upstreamBillingProbeLeaderLockKey          = "upstream:billing:probe:leader"
	upstreamBillingProbeLeaderLockTTL          = 2 * time.Minute
)

// UpstreamBillingProbeMaxBatchSize limits one manual batch and one runner cycle.
const UpstreamBillingProbeMaxBatchSize = upstreamBillingProbeMaxPerCycle

var (
	ErrUpstreamBillingProbeUnavailable = infraerrors.ServiceUnavailable(
		"UPSTREAM_BILLING_PROBE_UNAVAILABLE", "upstream billing probe is unavailable",
	)
	ErrUpstreamBillingProbeAccountInvalid = infraerrors.BadRequest(
		"UPSTREAM_BILLING_PROBE_ACCOUNT_INVALID", "account is not an OpenAI API key account",
	)
	ErrUpstreamBillingProbeIdentityChanged = infraerrors.Conflict(
		"UPSTREAM_BILLING_PROBE_IDENTITY_CHANGED", "account identity changed during upstream billing probe; retry the probe",
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

	parentCtx    context.Context
	parentCancel context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	cycleMu      sync.Mutex
	probeGroup   singleflight.Group
	probeSlots   chan struct{}
	now          func() time.Time
	lockCache    LeaderLockCache
	db           *sql.DB
	instanceID   string
	webTokenMu   sync.Mutex
	webTokens    map[string]cachedWebAccountToken
}

type cachedWebAccountToken struct {
	token     string
	userID    int
	expiresAt time.Time
}

type upstreamBillingProbeSnapshotWriter interface {
	UpdateUpstreamBillingProbeSnapshot(context.Context, *Account, *UpstreamBillingProbeSnapshot) error
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

// ProvideUpstreamBillingProbeService starts the process-wide periodic runner.
func ProvideUpstreamBillingProbeService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	settingService *SettingService,
	upstreamSites *UpstreamSiteCredentialService,
	lockCache LeaderLockCache,
	db *sql.DB,
) *UpstreamBillingProbeService {
	svc := NewUpstreamBillingProbeService(accountRepo, accountTestService, settingService)
	svc.SetUpstreamSiteCredentialService(upstreamSites)
	svc.SetLeaderLock(lockCache, db)
	svc.Start()
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
	return s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		UpstreamBillingProbeEnabledExtraKey: enabled,
	})
}

func (s *UpstreamBillingProbeService) probeLoadedAccount(ctx context.Context, account *Account, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	now := s.currentTime().UTC()
	if s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "transport_unavailable", 0)
	}
	apiKey := account.GetOpenAIApiKey()
	if apiKey == "" {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "missing_api_key", 0)
	}
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
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
	probeURL := buildOpenAIEndpointURL(normalizedBaseURL, "/v1/sub2api/billing")
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, bytes.NewReader(nil))
	if err != nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "request_build_failed", 0)
	}
	reqCtx := WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI)
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(reqCtx))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "request_failed", 0)
	}
	if resp == nil || resp.Body == nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "empty_response", 0)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
	if readErr != nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_read_failed", retryAfter(resp.Header, now))
	}
	if len(body) > upstreamBillingProbeMaxBodyBytes {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_too_large", retryAfter(resp.Header, now))
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		if !upstreamBillingSupportsWebAccount(normalizedBaseURL) {
			return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "unsupported", retryAfter(resp.Header, now))
		}
		data, balance, statusCode, reason, retryDelay := s.fetchWebAccountBillingData(ctx, account, normalizedBaseURL, apiKey, proxyURL, tlsProfile, now)
		if reason != "" {
			// 网页倍率查询必须体现实时状态，失败时不保留上一轮倍率，避免误导管理员。
			return s.persistProbeFailureCleared(ctx, account, intervalMinutes, now, statusCode, reason, retryDelay, balance)
		}
		return s.persistProbeSuccess(ctx, account, intervalMinutes, now, statusCode, data, balance)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "http_error", retryAfter(resp.Header, now))
	}
	data, err := parseUpstreamBillingProbeResponse(body)
	if err != nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "invalid_response", retryAfter(resp.Header, now))
	}
	balance := s.fetchConfiguredWebAccountBalance(ctx, account, normalizedBaseURL, proxyURL, tlsProfile, now)
	return s.persistProbeSuccess(ctx, account, intervalMinutes, now, resp.StatusCode, data, balance)
}

func upstreamBillingSupportsWebAccount(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "vovoapi.com",
		"tsyjzzz.com",
		"hubway.cc",
		"pool.chaozhiyuanai.com",
		"onebool.com",
		"sub.anzhiyu.com",
		"api.yigpt.cc",
		"aigw.store",
		"ai-tokens.vip",
		"mzeapi.top",
		"api.enter9.de",
		"api.aigclink.xyz":
		return true
	default:
		return false
	}
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
	if err := s.updateSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
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
	previous := decodeUpstreamBillingProbeSnapshot(account.Extra)
	failureCount := 1
	if previous != nil {
		failureCount = previous.FailureCount + 1
	}
	status := UpstreamBillingProbeStatusFailed
	if reason == "unsupported" {
		status = UpstreamBillingProbeStatusUnsupported
	}
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        status,
		LastAttemptAt: now,
		NextProbeAt:   now.Add(nextProbeDelay(intervalMinutes, retryAfterDuration)),
		FailureCount:  failureCount,
		HTTPStatus:    statusCode,
		LastError:     reason,
	}
	if previous != nil {
		snapshot.Data = previous.Data
		snapshot.ReceivedAt = previous.ReceivedAt
		snapshot.FreshUntil = previous.FreshUntil
		if snapshot.FreshUntil == nil && previous.Status == UpstreamBillingProbeStatusOK && previous.ReceivedAt != nil {
			snapshot.FreshUntil = probeTimePtr(previous.ReceivedAt.Add(2 * time.Duration(intervalMinutes) * time.Minute))
		}
	}
	if err := s.updateSnapshot(ctx, account, snapshot); err != nil {
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
	if err := s.updateSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
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
	currentKey := matchWebAccountCurrentKey(items, apiKey, "")
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
	req, cancel, err := s.newWebAccountRequest(ctx, http.MethodGet, baseURL, "/api/v1/auth/me", nil, nil)
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
	if len(masked) >= 8 && strings.HasPrefix(lowerFull, lowerMasked) {
		return true
	}
	parts := strings.FieldsFunc(lowerMasked, func(r rune) bool {
		switch r {
		case '.', '*', '•', '·', '_':
			return true
		default:
			return false
		}
	})
	if len(parts) >= 2 {
		prefix := strings.TrimSpace(parts[0])
		suffix := strings.TrimSpace(parts[len(parts)-1])
		return len(prefix) >= 2 && len(suffix) >= 2 && strings.HasPrefix(lowerFull, prefix) && strings.HasSuffix(lowerFull, suffix)
	}
	if len(parts) == 1 {
		token := strings.TrimSpace(parts[0])
		return len(token) >= 6 && (strings.HasPrefix(lowerFull, token) || strings.HasSuffix(lowerFull, token))
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

func (s *UpstreamBillingProbeService) updateSnapshot(ctx context.Context, account *Account, snapshot *UpstreamBillingProbeSnapshot) error {
	writer, ok := s.accountRepo.(upstreamBillingProbeSnapshotWriter)
	if !ok {
		return ErrUpstreamBillingProbeUnavailable
	}
	return writer.UpdateUpstreamBillingProbeSnapshot(ctx, account, snapshot)
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

func isUpstreamBillingProbeAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey
}

func upstreamBillingProbeEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[UpstreamBillingProbeEnabledExtraKey].(bool)
	return ok && enabled
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
