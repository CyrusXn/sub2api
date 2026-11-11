package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"golang.org/x/sync/errgroup"
)

const upstreamSiteBalanceIncreaseTolerance = 1e-9

const UpstreamSiteBalancePollInterval = 5 * time.Minute

const (
	upstreamSiteBalanceTargetsRefreshInterval = 5 * time.Minute
	upstreamSiteBalanceMaxSitesPerCycle       = 20
	upstreamSiteBalanceProbeConcurrency       = 4
	upstreamSiteBalanceLeaderLockKey          = "upstream:site-balance:reminder:leader"
	upstreamSiteBalanceLeaderLockTTL          = 10 * time.Minute
)

type UpstreamSiteBalanceReminder struct {
	Host        string    `json:"host"`
	DisplayName string    `json:"display_name"`
	OldBalance  float64   `json:"old_balance"`
	NewBalance  float64   `json:"new_balance"`
	Delta       float64   `json:"delta"`
	Currency    string    `json:"currency"`
	DetectedAt  time.Time `json:"detected_at"`
	Version     int64     `json:"version"`
}

type UpstreamSiteBalanceObservation struct {
	Host         string
	DisplayName  string
	BaseURL      string
	Balance      float64
	Currency     string
	ObservedAt   time.Time
	FailureCount int
}

type UpstreamSiteBalanceReminderRepository interface {
	EnsureUpstreamSiteBalanceObservations(context.Context, []UpstreamSiteBalanceObservation) error
	ListDueUpstreamSiteBalanceObservations(context.Context, time.Time, int) ([]UpstreamSiteBalanceObservation, error)
	SaveUpstreamSiteBalanceObservation(context.Context, UpstreamSiteBalanceObservation) (*UpstreamSiteBalanceReminder, error)
	DeferUpstreamSiteBalanceObservation(context.Context, string, time.Time, string) error
	ListActiveUpstreamSiteBalanceReminders(context.Context) ([]UpstreamSiteBalanceReminder, error)
	ResolveUpstreamSiteBalanceReminder(context.Context, string, time.Time) (int64, bool, error)
	ListUpstreamSiteBalanceReminderSnapshot(context.Context) ([]UpstreamSiteBalanceReminder, map[string]int64, error)
}

type UpstreamSiteBalanceReminderEvent struct {
	Type      string                        `json:"type"`
	Reminder  *UpstreamSiteBalanceReminder  `json:"reminder,omitempty"`
	Host      string                        `json:"host,omitempty"`
	Version   int64                         `json:"version,omitempty"`
	Reminders []UpstreamSiteBalanceReminder `json:"reminders,omitempty"`
	Versions  map[string]int64              `json:"versions,omitempty"`
}

type UpstreamSiteBalanceReminderBus interface {
	PublishUpstreamSiteBalanceReminder(context.Context, UpstreamSiteBalanceReminderEvent) error
	SubscribeUpstreamSiteBalanceReminders(context.Context) (<-chan UpstreamSiteBalanceReminderEvent, error)
}

func EvaluateUpstreamBalanceIncrease(previous *float64, current float64) (bool, float64) {
	if previous == nil {
		return false, 0
	}
	delta := current - *previous
	if delta <= upstreamSiteBalanceIncreaseTolerance {
		return false, 0
	}
	return true, delta
}

func (s *UpstreamBillingProbeService) probeDueUpstreamSiteBalances(ctx context.Context) {
	if s == nil || s.upstreamSites == nil || s.upstreamSiteBalanceReminderRepo == nil {
		return
	}
	now := s.currentTime().UTC()
	if err := s.refreshUpstreamSiteBalanceTargets(ctx, now); err != nil {
		logger.LegacyPrintf("service.upstream_billing_probe", "site_balance_targets_refresh_failed: error_type=%T", err)
		return
	}
	due, err := s.upstreamSiteBalanceReminderRepo.ListDueUpstreamSiteBalanceObservations(ctx, now, upstreamSiteBalanceMaxSitesPerCycle)
	if err != nil || len(due) == 0 {
		if err != nil {
			logger.LegacyPrintf("service.upstream_billing_probe", "site_balance_due_list_failed: error_type=%T", err)
		}
		return
	}
	release, acquired, err := s.tryAcquireLeaderLockWithTTL(ctx, upstreamSiteBalanceLeaderLockKey, upstreamSiteBalanceLeaderLockTTL)
	if err != nil || !acquired {
		if err != nil {
			logger.LegacyPrintf("service.upstream_billing_probe", "site_balance_lock_failed: error_type=%T", err)
		}
		return
	}
	defer release()

	// 获取分布式锁后重新读取，避免等待锁期间其它实例已处理同一批站点。
	due, err = s.upstreamSiteBalanceReminderRepo.ListDueUpstreamSiteBalanceObservations(ctx, s.currentTime().UTC(), upstreamSiteBalanceMaxSitesPerCycle)
	if err != nil {
		logger.LegacyPrintf("service.upstream_billing_probe", "site_balance_due_list_failed: error_type=%T", err)
		return
	}
	var group errgroup.Group
	group.SetLimit(upstreamSiteBalanceProbeConcurrency)
	for _, observation := range due {
		observation := observation
		if err := ctx.Err(); err != nil {
			return
		}
		account := s.upstreamSiteBalanceAccounts[observation.Host]
		if account == nil {
			_ = s.upstreamSiteBalanceReminderRepo.DeferUpstreamSiteBalanceObservation(ctx, observation.Host, s.currentTime().UTC().Add(24*time.Hour), "site_not_configured")
			continue
		}
		group.Go(func() error {
			if err := s.probeUpstreamSiteBalance(ctx, observation, account); err != nil {
				delay := upstreamSiteBalanceFailureDelay(observation.FailureCount + 1)
				if deferErr := s.upstreamSiteBalanceReminderRepo.DeferUpstreamSiteBalanceObservation(ctx, observation.Host, s.currentTime().UTC().Add(delay), safeUpstreamSiteBalanceFailure(err)); deferErr != nil {
					logger.LegacyPrintf("service.upstream_billing_probe", "site_balance_failure_state_save_failed: error_type=%T", deferErr)
				}
			}
			return nil
		})
	}
	_ = group.Wait()
}

func (s *UpstreamBillingProbeService) refreshUpstreamSiteBalanceTargets(ctx context.Context, now time.Time) error {
	if s.accountRepo == nil {
		return errors.New("account_repository_unavailable")
	}
	if now.Before(s.upstreamSiteBalanceRefreshAt) {
		return nil
	}
	sites, err := s.upstreamSites.List(ctx, false)
	if err != nil {
		return err
	}
	accountIDs := make([]int64, 0, len(sites))
	configured := make([]UpstreamSiteCredentialSummary, 0, len(sites))
	seen := make(map[int64]struct{}, len(sites))
	observations := make([]UpstreamSiteBalanceObservation, 0, len(sites))
	for _, site := range sites {
		if !site.HasPassword || len(site.AccountIDs) == 0 {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(site.Host))
		if host == "" {
			continue
		}
		configured = append(configured, site)
		observations = append(observations, UpstreamSiteBalanceObservation{
			Host: host, DisplayName: strings.TrimSpace(site.DisplayName), BaseURL: site.WebsiteURL,
		})
		id := site.AccountIDs[0]
		if id > 0 {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				accountIDs = append(accountIDs, id)
			}
		}
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return err
	}
	accountByID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			accountByID[account.ID] = account
		}
	}
	targets := make(map[string]*Account, len(configured))
	for _, site := range configured {
		if len(site.AccountIDs) > 0 {
			if account := accountByID[site.AccountIDs[0]]; account != nil {
				targets[strings.ToLower(strings.TrimSpace(site.Host))] = account
			}
		}
	}
	if err := s.upstreamSiteBalanceReminderRepo.EnsureUpstreamSiteBalanceObservations(ctx, observations); err != nil {
		return err
	}
	s.upstreamSiteBalanceAccounts = targets
	s.upstreamSiteBalanceRefreshAt = now.Add(upstreamSiteBalanceTargetsRefreshInterval)
	return nil
}

func (s *UpstreamBillingProbeService) probeUpstreamSiteBalance(ctx context.Context, observation UpstreamSiteBalanceObservation, account *Account) error {
	if s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return fmt.Errorf("transport_unavailable")
	}
	credential, err := s.upstreamSites.Resolve(ctx, observation.BaseURL)
	if err != nil {
		return fmt.Errorf("credential_unavailable")
	}
	if credential == nil {
		return fmt.Errorf("credential_unavailable")
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return fmt.Errorf("proxy_unavailable")
		}
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile *tlsfingerprint.Profile
	if s.accountTestService.tlsFPProfileService != nil {
		tlsProfile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	now := s.currentTime().UTC()
	token, userID, err := s.upstreamSiteBalanceToken(ctx, account, observation.BaseURL, credential.Username, credential.Password, proxyURL, tlsProfile, now)
	if err != nil {
		return err
	}
	result := s.fetchSiteBalanceWithToken(ctx, account, observation.BaseURL, token, userID, proxyURL, tlsProfile, now)
	if result == nil {
		return fmt.Errorf("balance_unavailable")
	}
	if result.Status != UpstreamBillingProbeStatusOK || result.Amount == nil {
		if isUpstreamSiteBalanceLoginExpired(result) && !isPiteUpstreamSite(observation.Host) {
			s.clearUpstreamSiteBalanceToken(observation.BaseURL, credential.Username)
			// 只有明确的 401 登录失效才重登一次；403、网络和格式错误都直接退避。
			token, userID, err = s.upstreamSiteBalanceToken(ctx, account, observation.BaseURL, credential.Username, credential.Password, proxyURL, tlsProfile, now)
			if err != nil {
				return err
			}
			result = s.fetchSiteBalanceWithToken(ctx, account, observation.BaseURL, token, userID, proxyURL, tlsProfile, s.currentTime().UTC())
			if result == nil || result.Status != UpstreamBillingProbeStatusOK || result.Amount == nil {
				return errors.New("auth_or_balance_unavailable")
			}
		} else {
			return fmt.Errorf("balance_unavailable")
		}
	}
	currency := strings.TrimSpace(result.Unit)
	if currency == "" {
		currency = "USD"
	}
	reminder, err := s.upstreamSiteBalanceReminderRepo.SaveUpstreamSiteBalanceObservation(ctx, UpstreamSiteBalanceObservation{
		Host: observation.Host, DisplayName: observation.DisplayName, BaseURL: observation.BaseURL,
		Balance: *result.Amount, Currency: currency, ObservedAt: s.currentTime().UTC(),
	})
	if err != nil {
		return errors.New("observation_save_failed")
	}
	if reminder != nil && s.upstreamSiteBalanceReminderBus != nil {
		if err := s.upstreamSiteBalanceReminderBus.PublishUpstreamSiteBalanceReminder(ctx, UpstreamSiteBalanceReminderEvent{Type: "upsert", Reminder: reminder}); err != nil {
			logger.LegacyPrintf("service.upstream_billing_probe", "site_balance_reminder_publish_failed: error_type=%T", err)
		}
	}
	return nil
}

func (s *UpstreamBillingProbeService) upstreamSiteBalanceToken(ctx context.Context, account *Account, baseURL, username, password, proxyURL string, tlsProfile *tlsfingerprint.Profile, now time.Time) (string, int, error) {
	if isPiteUpstreamSite(strings.TrimSpace(mustUpstreamHost(baseURL))) {
		if token, userID, ok := parsePiteStoredSystemSession(username, password); ok {
			return token, userID, nil
		}
	}
	key := webAccountTokenCacheKey(baseURL, username)
	s.upstreamSiteBalanceTokenMu.Lock()
	if s.upstreamSiteBalanceTokens == nil {
		s.upstreamSiteBalanceTokens = make(map[string]cachedWebAccountToken)
	}
	cached, ok := s.upstreamSiteBalanceTokens[key]
	s.upstreamSiteBalanceTokenMu.Unlock()
	if ok && cached.token != "" {
		return cached.token, cached.userID, nil
	}
	// 复用已存在的网页登录会话，包括接近/已经到期但服务端尚接受的 access token。
	s.webTokenMu.Lock()
	cached, ok = s.webTokens[key]
	s.webTokenMu.Unlock()
	if ok && cached.token != "" {
		s.upstreamSiteBalanceTokenMu.Lock()
		s.upstreamSiteBalanceTokens[key] = cached
		s.upstreamSiteBalanceTokenMu.Unlock()
		return cached.token, cached.userID, nil
	}
	var token string
	var userID int
	var statusCode int
	var reason string
	var retryDelay time.Duration
	if upstreamSiteProtocolForHost(mustUpstreamHost(baseURL)) == "newapi" {
		token, userID, statusCode, reason, retryDelay = s.newAPIWebAccountAccessToken(ctx, account, baseURL, username, password, proxyURL, tlsProfile, now)
	} else {
		token, statusCode, reason, retryDelay = s.webAccountAccessToken(ctx, account, baseURL, username, password, proxyURL, tlsProfile, now)
	}
	_ = statusCode
	_ = retryDelay
	if reason != "" || token == "" {
		return "", 0, fmt.Errorf("login_unavailable")
	}
	entry := cachedWebAccountToken{token: token, userID: userID}
	if key != "" {
		s.upstreamSiteBalanceTokenMu.Lock()
		s.upstreamSiteBalanceTokens[key] = entry
		s.upstreamSiteBalanceTokenMu.Unlock()
	}
	return token, userID, nil
}

func (s *UpstreamBillingProbeService) clearUpstreamSiteBalanceToken(baseURL, username string) {
	key := webAccountTokenCacheKey(baseURL, username)
	s.upstreamSiteBalanceTokenMu.Lock()
	delete(s.upstreamSiteBalanceTokens, key)
	s.upstreamSiteBalanceTokenMu.Unlock()
	s.clearWebAccountToken(baseURL, username)
}

func (s *UpstreamBillingProbeService) fetchSiteBalanceWithToken(ctx context.Context, account *Account, baseURL, token string, userID int, proxyURL string, tlsProfile *tlsfingerprint.Profile, now time.Time) *UpstreamAccountBalanceSnapshot {
	if upstreamSiteProtocolForHost(mustUpstreamHost(baseURL)) == "newapi" {
		return s.fetchNewAPIAccountBalance(ctx, account, baseURL, token, userID, proxyURL, tlsProfile, now)
	}
	return s.fetchInnomAccountBalance(ctx, account, baseURL, token, proxyURL, tlsProfile, now)
}

func upstreamSiteBalanceFailureDelay(failureCount int) time.Duration {
	if failureCount < 1 {
		failureCount = 1
	}
	delay := time.Minute * time.Duration(1<<min(failureCount-1, 6))
	return min(delay, time.Hour)
}

func safeUpstreamSiteBalanceFailure(error) string {
	return "probe_failed"
}

func isUpstreamSiteBalanceLoginExpired(result *UpstreamAccountBalanceSnapshot) bool {
	if result == nil {
		return false
	}
	if result.HTTPStatus == http.StatusUnauthorized {
		return true
	}
	return result.LastError == "web_auth_failed"
}

func isUpstreamSiteLoginExpired(statusCode int, body []byte) bool {
	if statusCode == http.StatusUnauthorized {
		return true
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return false
	}
	if code, ok := numberFromAny(root["code"]); ok && int(code) == http.StatusUnauthorized {
		return true
	}
	for _, key := range []string{"message", "msg", "error"} {
		value := strings.ToLower(strings.TrimSpace(upstreamBillingStringFromAny(root[key])))
		if strings.Contains(value, "未登录") || strings.Contains(value, "登录已过期") || strings.Contains(value, "not logged in") || strings.Contains(value, "unauthorized") {
			return true
		}
	}
	return false
}

func mustUpstreamHost(baseURL string) string {
	host, _, _ := normalizeUpstreamSite(baseURL)
	return host
}
