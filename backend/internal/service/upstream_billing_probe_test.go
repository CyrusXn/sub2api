package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type upstreamBillingProbeAccountRepo struct {
	AccountRepository
	mu          sync.Mutex
	accounts    map[int64]*Account
	updates     map[int64][]map[string]any
	bulkUpdates []AccountBulkUpdate
}

type staleDueUpstreamBillingProbeAccountRepo struct {
	*upstreamBillingProbeAccountRepo
	due []Account
}

func (r *staleDueUpstreamBillingProbeAccountRepo) ListDueUpstreamBillingProbeAccounts(_ context.Context, _ time.Time, limit int) ([]Account, error) {
	if limit < len(r.due) {
		return append([]Account(nil), r.due[:limit]...), nil
	}
	return append([]Account(nil), r.due...), nil
}

func (r *upstreamBillingProbeAccountRepo) Create(_ context.Context, account *Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.accounts == nil {
		r.accounts = make(map[int64]*Account)
	}
	if account.ID == 0 {
		account.ID = int64(len(r.accounts) + 1)
	}
	r.accounts[account.ID] = account
	return nil
}

func (r *upstreamBillingProbeAccountRepo) Update(_ context.Context, account *Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[account.ID] = account
	return nil
}

func (r *upstreamBillingProbeAccountRepo) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bulkUpdates = append(r.bulkUpdates, updates)
	return int64(len(ids)), nil
}

func (r *upstreamBillingProbeAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return nil, ErrAccountNotFound
	}
	clone := *account
	clone.Credentials = mergeMap(nil, account.Credentials)
	clone.Extra = mergeMap(nil, account.Extra)
	return &clone, nil
}

func (r *upstreamBillingProbeAccountRepo) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if account := r.accounts[id]; account != nil {
			result = append(result, account)
		}
	}
	return result, nil
}

func (r *upstreamBillingProbeAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[id]
	if account == nil {
		return ErrAccountNotFound
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		account.Extra[key] = value
	}
	if r.updates == nil {
		r.updates = make(map[int64][]map[string]any)
	}
	r.updates[id] = append(r.updates[id], updates)
	return nil
}

func (r *upstreamBillingProbeAccountRepo) UpdateUpstreamBillingProbeSnapshot(
	_ context.Context,
	expected *Account,
	snapshot *UpstreamBillingProbeSnapshot,
	rateMultiplier *float64,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := r.accounts[expected.ID]
	if account == nil || account.Platform != expected.Platform || account.Type != expected.Type || !reflect.DeepEqual(account.Credentials, expected.Credentials) {
		return ErrUpstreamBillingProbeIdentityChanged
	}
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	account.Extra[UpstreamBillingProbeExtraKey] = snapshot
	if snapshot.Status == UpstreamBillingProbeStatusOK &&
		rateMultiplier != nil &&
		upstreamBillingRateSyncEnabled(account) {
		value := *rateMultiplier
		account.RateMultiplier = &value
	}
	return nil
}

func (r *upstreamBillingProbeAccountRepo) FindByExtraField(_ context.Context, key string, value any) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Account, 0)
	for _, account := range r.accounts {
		if account.Extra != nil && account.Extra[key] == value {
			result = append(result, *account)
		}
	}
	return result, nil
}

type upstreamBillingProbeSettingRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
}

type balanceCenterRepositorySpy struct {
	mu        sync.Mutex
	snapshots []*BalanceCenterSnapshot
	err       error
}

func (r *balanceCenterRepositorySpy) PersistSnapshot(_ context.Context, snapshot *BalanceCenterSnapshot) (*BalanceCenterSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *snapshot
	clone.Payload = append(json.RawMessage(nil), snapshot.Payload...)
	r.snapshots = append(r.snapshots, &clone)
	if r.err != nil {
		return nil, r.err
	}
	return &clone, nil
}

func (r *balanceCenterRepositorySpy) lastSnapshot() *BalanceCenterSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.snapshots) == 0 {
		return nil
	}
	clone := *r.snapshots[len(r.snapshots)-1]
	clone.Payload = append(json.RawMessage(nil), clone.Payload...)
	return &clone
}

type upstreamBillingProbeHTTPStub struct {
	calls          atomic.Int64
	billingCalls   atomic.Int64
	active         atomic.Int64
	maxActive      atomic.Int64
	beforeResponse func()
}

func (u *upstreamBillingProbeHTTPStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.calls.Add(1)
	isBillingProbe := req != nil && req.URL != nil && req.URL.Path == "/v1/sub2api/billing"
	if isBillingProbe {
		u.billingCalls.Add(1)
	}
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for {
		peak := u.maxActive.Load()
		if active <= peak || u.maxActive.CompareAndSwap(peak, active) {
			break
		}
	}
	if u.beforeResponse != nil && isBillingProbe {
		u.beforeResponse()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"object":"sub2api.key_billing",
			"schema_version":1,
			"billing_scope":"token",
			"group_rate_multiplier":0.8,
			"resolved_rate_multiplier":0.8,
			"peak_rate_enabled":false,
			"effective_rate_multiplier":0.8,
			"observed_at":"2026-07-13T01:00:00Z"
		}`)),
	}, nil
}

func (u *upstreamBillingProbeHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

type webAccountRateHTTPStub struct {
	mu               sync.Mutex
	requests         []string
	standardStatus   int
	standardResponse string
	usageStatus      int
	usageResponse    string
	keysResponse     string
	balanceStatus    int
	balanceResponse  string
	loginStatus      int
	loginResponse    string
	loginPassword    string
}

type newAPIWebAccountHTTPStub struct {
	mu                 sync.Mutex
	requests           []string
	standardStatus     int
	standardResponse   string
	currentAPIKey      string
	currentGroup       string
	groupRate          float64
	rawQuota           float64
	quotaPerUnit       float64
	rejectWebLogin     bool
	requiredAuth       string
	requiredNewAPIUser string
}

func (u *newAPIWebAccountHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.requests = append(u.requests, req.Method+" "+req.URL.Path)
	u.mu.Unlock()

	switch req.URL.Path {
	case "/v1/sub2api/billing":
		status := u.standardStatus
		if status == 0 {
			status = http.StatusNotFound
		}
		body := u.standardResponse
		if body == "" {
			body = "{\"message\":\"not supported\"}"
		}
		return jsonResponse(status, body), nil
	case "/api/user/login":
		if u.rejectWebLogin {
			return jsonResponse(http.StatusInternalServerError, "{\"success\":false,\"message\":\"login should not be used\"}"), nil
		}
		resp := jsonResponse(http.StatusOK, "{\"success\":true,\"data\":{\"id\":9}}")
		resp.Header.Add("Set-Cookie", "session=browser-session; Path=/; HttpOnly")
		return resp, nil
	case "/api/user/token":
		if u.rejectWebLogin {
			return jsonResponse(http.StatusInternalServerError, "{\"success\":false,\"message\":\"token endpoint should not be used\"}"), nil
		}
		if req.Header.Get("Cookie") == "" || req.Header.Get("New-Api-User") != "9" {
			return jsonResponse(http.StatusUnauthorized, "{\"success\":false}"), nil
		}
		return jsonResponse(http.StatusOK, "{\"success\":true,\"data\":\"system-access\"}"), nil
	case "/api/token/":
		if !u.hasExpectedNewAPIWebAuth(req) {
			return jsonResponse(http.StatusUnauthorized, "{\"success\":false}"), nil
		}
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			"{\"success\":true,\"data\":{\"items\":[{\"key\":%q,\"status\":1,\"group\":%q}]}}",
			u.currentAPIKey,
			u.currentGroup,
		)), nil
	case "/api/user/self/groups":
		if !u.hasExpectedNewAPIWebAuth(req) {
			return jsonResponse(http.StatusUnauthorized, "{\"success\":false}"), nil
		}
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			"{\"success\":true,\"data\":{%q:{\"ratio\":%v}}}",
			u.currentGroup,
			u.groupRate,
		)), nil
	case "/api/user/self":
		if !u.hasExpectedNewAPIWebAuth(req) {
			return jsonResponse(http.StatusUnauthorized, "{\"success\":false}"), nil
		}
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			"{\"success\":true,\"data\":{\"quota\":%v,\"status\":1}}",
			u.rawQuota,
		)), nil
	case "/api/status":
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			"{\"success\":true,\"data\":{\"quota_display_type\":\"USD\",\"quota_per_unit\":%v}}",
			u.quotaPerUnit,
		)), nil
	default:
		return jsonResponse(http.StatusNotFound, "not found"), nil
	}
}

func (u *newAPIWebAccountHTTPStub) hasExpectedNewAPIWebAuth(req *http.Request) bool {
	requiredAuth := u.requiredAuth
	if requiredAuth == "" {
		requiredAuth = "system-access"
	}
	requiredUser := u.requiredNewAPIUser
	if requiredUser == "" {
		requiredUser = "9"
	}
	return req.Header.Get("Authorization") == requiredAuth && req.Header.Get("New-Api-User") == requiredUser
}

func (u *newAPIWebAccountHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *newAPIWebAccountHTTPStub) requestPaths() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requests...)
}

func (u *webAccountRateHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.requests = append(u.requests, req.Method+" "+req.URL.Path)
	u.mu.Unlock()

	switch req.URL.Path {
	case "/v1/sub2api/billing":
		status := u.standardStatus
		if status == 0 {
			status = http.StatusNotFound
		}
		body := u.standardResponse
		if body == "" {
			body = "{\"message\":\"not supported\"}"
		}
		return jsonResponse(status, body), nil
	case "/api/v1/auth/login":
		body, _ := io.ReadAll(req.Body)
		expectedPassword := u.loginPassword
		if expectedPassword == "" {
			expectedPassword = "secret"
		}
		var loginPayload map[string]string
		if json.Unmarshal(body, &loginPayload) != nil ||
			loginPayload["email"] != "admin@example.com" ||
			loginPayload["password"] != expectedPassword {
			return jsonResponse(http.StatusUnauthorized, "{\"code\":401,\"message\":\"invalid login\"}"), nil
		}
		if u.loginStatus != 0 {
			responseBody := u.loginResponse
			if responseBody == "" {
				responseBody = "{\"code\":400,\"reason\":\"TURNSTILE_VERIFICATION_FAILED\"}"
			}
			return jsonResponse(u.loginStatus, responseBody), nil
		}
		return jsonResponse(http.StatusOK, "{\"code\":0,\"data\":{\"access_token\":\"web-token\",\"expires_in\":3600}}"), nil
	case "/v1/usage":
		// API Key 余额接口与网页登录独立，用于覆盖启用验证码的站点。
		if req.Header.Get("Authorization") != "Bearer sk-standard" {
			return jsonResponse(http.StatusUnauthorized, "{\"message\":\"invalid API key\"}"), nil
		}
		status := u.usageStatus
		if status == 0 {
			status = http.StatusNotFound
		}
		responseBody := u.usageResponse
		if responseBody == "" {
			responseBody = "{\"message\":\"not supported\"}"
		}
		return jsonResponse(status, responseBody), nil
	case "/api/v1/keys":
		if req.Header.Get("Authorization") != "Bearer web-token" || req.URL.Query().Get("page_size") != "100" {
			return jsonResponse(http.StatusUnauthorized, "{\"code\":401}"), nil
		}
		if u.keysResponse != "" {
			return jsonResponse(http.StatusOK, u.keysResponse), nil
		}
		return jsonResponse(http.StatusOK, "{\"code\":0,\"data\":{\"items\":[{\"key\":\"sk-live\",\"status\":\"active\",\"group_id\":7,\"group\":{\"id\":7,\"rate_multiplier\":0.04}}]}}"), nil
	case "/api/v1/groups/rates":
		if req.Header.Get("Authorization") != "Bearer web-token" {
			return jsonResponse(http.StatusUnauthorized, "{\"code\":401}"), nil
		}
		return jsonResponse(http.StatusOK, "{\"code\":0,\"data\":{\"7\":0.09}}"), nil
	case "/api/v1/auth/me", "/api/v1/user/profile":
		if req.Header.Get("Authorization") != "Bearer web-token" {
			return jsonResponse(http.StatusUnauthorized, "{\"code\":401}"), nil
		}
		status := u.balanceStatus
		if status == 0 {
			status = http.StatusOK
		}
		body := u.balanceResponse
		if body == "" {
			body = "{\"code\":0,\"data\":{\"balance\":12.34}}"
		}
		return jsonResponse(status, body), nil
	default:
		return jsonResponse(http.StatusNotFound, "not found"), nil
	}
}

func (u *webAccountRateHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *webAccountRateHTTPStub) requestPaths() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requests...)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func (r *upstreamBillingProbeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *upstreamBillingProbeSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *upstreamBillingProbeSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func newUpstreamBillingProbeTestService(
	repo AccountRepository,
	upstream HTTPUpstream,
	settingRepo SettingRepository,
) *UpstreamBillingProbeService {
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
		Enabled:           false,
		AllowInsecureHTTP: true,
	}}}
	accountTestService := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: cfg}
	return NewUpstreamBillingProbeService(repo, accountTestService, NewSettingService(settingRepo, cfg))
}

func TestUpstreamBillingProbePersistsSuccessfulBalanceCenterSnapshot(t *testing.T) {
	lastUsedAt := time.Date(2026, time.August, 12, 7, 58, 0, 0, time.UTC)
	account := &Account{
		ID:          61,
		Name:        "VoVo-main-key",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		LastUsedAt:  &lastUsedAt,
		Credentials: map[string]any{
			"api_key":  "sk-sensitive-value",
			"base_url": "https://VOVOAPI.com/v1/",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	centerRepo := &balanceCenterRepositorySpy{}
	svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	svc.SetBalanceCenterRepository(centerRepo)
	now := time.Date(2026, time.August, 12, 8, 0, 0, 123, time.UTC)
	amount := 12.34

	snapshot, err := svc.persistProbeSuccess(context.Background(), account, 30, now, http.StatusOK,
		map[string]any{"resolved_rate_multiplier": 0.7, "secret": "must-not-persist"},
		&UpstreamAccountBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, Amount: &amount, Unit: "USD"},
	)

	require.NoError(t, err)
	require.NotNil(t, snapshot)
	persisted := centerRepo.lastSnapshot()
	require.NotNil(t, persisted)
	require.Equal(t, int64Ptr(account.ID), persisted.AccountID)
	require.Equal(t, account.Name, persisted.SiteName)
	require.Equal(t, "vovoapi.com", persisted.NormalizedDomain)
	require.Equal(t, "https://vovoapi.com", persisted.BaseURL)
	require.Equal(t, "sub2api_probe", persisted.Source)
	require.Equal(t, "sub2api_probe:61:1786521600000000123", persisted.SourceKey)
	require.Equal(t, UpstreamBillingProbeStatusOK, persisted.Status)
	require.NotNil(t, persisted.Balance)
	require.InDelta(t, 12.34, *persisted.Balance, 1e-12)
	require.NotNil(t, persisted.ConvertedBalance)
	require.InDelta(t, 12.34, *persisted.ConvertedBalance, 1e-12)
	require.NotNil(t, persisted.RateMultiplier)
	require.InDelta(t, 0.7, *persisted.RateMultiplier, 1e-12)
	require.Equal(t, 1.0, persisted.ConversionScale)
	require.Equal(t, "USD", persisted.Currency)
	require.Equal(t, now, persisted.ProbedAt)
	require.Equal(t, &lastUsedAt, persisted.LastUsedAt)
	require.JSONEq(t, `{"http_status":200}`, string(persisted.Payload))
	require.NotContains(t, string(persisted.Payload), "sk-sensitive-value")
	require.NotContains(t, string(persisted.Payload), "must-not-persist")
}

func TestUpstreamBillingProbePersistsFailedBalanceCenterSnapshotWithoutStaleValues(t *testing.T) {
	account := &Account{
		ID:          62,
		Name:        "VoVo",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-sensitive-value", "base_url": "https://vovoapi.com/v1"},
	}
	previousAmount := 99.0
	account.Extra = map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{
		Status: UpstreamBillingProbeStatusOK,
		Data:   map[string]any{"resolved_rate_multiplier": 0.5},
		Balance: &UpstreamAccountBalanceSnapshot{
			Status: UpstreamBillingProbeStatusOK,
			Amount: &previousAmount,
		},
	}}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	centerRepo := &balanceCenterRepositorySpy{}
	svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	svc.SetBalanceCenterRepository(centerRepo)
	now := time.Date(2026, time.August, 12, 8, 1, 0, 0, time.UTC)

	result, err := svc.persistProbeFailure(context.Background(), account, 30, now, http.StatusBadGateway, "http_error", 0)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, result.Status)
	persisted := centerRepo.lastSnapshot()
	require.NotNil(t, persisted)
	require.Equal(t, UpstreamBillingProbeStatusFailed, persisted.Status)
	require.Nil(t, persisted.Balance)
	require.Nil(t, persisted.ConvertedBalance)
	require.Nil(t, persisted.RateMultiplier)
	require.Equal(t, "http_error", persisted.Reason)
	require.JSONEq(t, `{"failure_count":1,"http_status":502}`, string(persisted.Payload))
}

func TestBuildBalanceCenterProbeSnapshotKeepsSuccessfulBalanceWhenRateProbeFails(t *testing.T) {
	account := &Account{
		ID:          66,
		Name:        "【派大星】0065Pro",
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.aigo0.com/v1"},
	}
	amount := 4.5
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        UpstreamBillingProbeStatusFailed,
		LastAttemptAt: time.Date(2026, time.August, 28, 1, 0, 0, 0, time.UTC),
		LastError:     "web_auth_failed",
		Balance: &UpstreamAccountBalanceSnapshot{
			Status: UpstreamBillingProbeStatusOK,
			Amount: &amount,
			Unit:   "USD",
		},
	}

	result, err := buildBalanceCenterProbeSnapshot(account, snapshot)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, result.Status)
	require.Equal(t, "web_auth_failed", result.Reason)
	require.NotNil(t, result.ConvertedBalance)
	require.InDelta(t, 4.5, *result.ConvertedBalance, 1e-12)
	require.Nil(t, result.RateMultiplier)
}

func TestUpstreamBillingProbeBalanceCenterSourceKeyIsStable(t *testing.T) {
	account := &Account{ID: 63, Name: "Pite", Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://ai.pite.chat/v1"}}
	now := time.Date(2026, time.August, 12, 8, 2, 0, 456, time.UTC)
	snapshot := &UpstreamBillingProbeSnapshot{Status: UpstreamBillingProbeStatusFailed, LastAttemptAt: now, HTTPStatus: 503, LastError: "http_error"}

	first, err := buildBalanceCenterProbeSnapshot(account, snapshot)
	require.NoError(t, err)
	second, err := buildBalanceCenterProbeSnapshot(account, snapshot)
	require.NoError(t, err)
	require.Equal(t, first.SourceKey, second.SourceKey)
	require.Equal(t, "sub2api_probe:63:1786521720000000456", first.SourceKey)
}

func TestBuildBalanceCenterProbeSnapshotUsesBracketedSiteName(t *testing.T) {
	account := &Account{ID: 65, Name: "【鱼鱼】008 bugteam", Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://sub.anzhiyu.com/v1"}}
	snapshot := &UpstreamBillingProbeSnapshot{Status: UpstreamBillingProbeStatusOK, LastAttemptAt: time.Now()}

	result, err := buildBalanceCenterProbeSnapshot(account, snapshot)

	require.NoError(t, err)
	require.Equal(t, "鱼鱼", result.SiteName)
}

func TestUpstreamBillingProbeIgnoresBalanceCenterPersistenceFailure(t *testing.T) {
	account := &Account{
		ID:          64,
		Name:        "OneBool",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-sensitive-value", "base_url": "https://onebool.com/v1"},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	centerRepo := &balanceCenterRepositorySpy{err: errors.New("database unavailable")}
	svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	svc.SetBalanceCenterRepository(centerRepo)

	result, err := svc.persistProbeFailure(context.Background(), account, 30, time.Now(), http.StatusBadGateway, "http_error", 0)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, result.Status)
	require.NotNil(t, centerRepo.lastSnapshot())
}

func attachUpstreamSiteCredential(svc *UpstreamBillingProbeService, host string) {
	attachUpstreamSiteCredentialWithValues(svc, host, "admin@example.com", "secret")
}

func attachUpstreamSiteCredentialWithValues(svc *UpstreamBillingProbeService, host string, username string, password string) {
	svc.SetUpstreamSiteCredentialService(NewUpstreamSiteCredentialService(
		&upstreamSiteCredentialRepoStub{credentials: map[string]*UpstreamSiteCredential{
			host: {
				Host:               host,
				LoginUsername:      username,
				PasswordCiphertext: "cipher:" + password,
			},
		}},
		upstreamSiteCredentialEncryptorStub{},
	))
}

func TestUpstreamBillingProbeSettingsDefaultsAndValidation(t *testing.T) {
	repo := &upstreamBillingProbeSettingRepo{}
	settingsService := NewSettingService(repo, &config.Config{})

	settings, err := settingsService.GetUpstreamBillingProbeSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 5, settings.IntervalMinutes)

	err = settingsService.SetUpstreamBillingProbeSettings(context.Background(), &UpstreamBillingProbeSettings{
		Enabled:         false,
		IntervalMinutes: 4,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "interval_minutes must be between 5 and 1440")

	err = settingsService.SetUpstreamBillingProbeSettings(context.Background(), &UpstreamBillingProbeSettings{
		Enabled:         false,
		IntervalMinutes: 60,
	})
	require.NoError(t, err)
	settings, err = settingsService.GetUpstreamBillingProbeSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 60, settings.IntervalMinutes)

	repo.values[SettingKeyUpstreamBillingProbeSettings] = `{"interval_minutes":45}`
	settings, err = settingsService.GetUpstreamBillingProbeSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 45, settings.IntervalMinutes)
	repo.values[SettingKeyUpstreamBillingProbeSettings] = `{"enabled":false}`
	settings, err = settingsService.GetUpstreamBillingProbeSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 5, settings.IntervalMinutes)

	repo.values[SettingKeyUpstreamBillingProbeSettings] = `{"enabled":`
	settings, err = settingsService.GetUpstreamBillingProbeSettings(context.Background())
	require.ErrorContains(t, err, "parse upstream billing probe settings")
	require.Nil(t, settings)
}

func TestUpstreamBillingProbeBackgroundCadenceSupportsNearRealTimeEvents(t *testing.T) {
	require.LessOrEqual(t, upstreamBillingProbeCycleInterval, 10*time.Second)
}

func TestUpstreamBillingProbeUsesWebAccountRateForKnownHosts(t *testing.T) {
	account := &Account{
		ID:          41,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-live",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "vovoapi.com")
	fixedNow := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.04, snapshot.Data["group_rate_multiplier"])
	require.Equal(t, 0.09, snapshot.Data["user_rate_multiplier"])
	require.Equal(t, 0.09, snapshot.Data["resolved_rate_multiplier"])
	require.Equal(t, 0.09, snapshot.Data["effective_rate_multiplier"])
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.NotNil(t, snapshot.Balance.Amount)
	require.Equal(t, 12.34, *snapshot.Balance.Amount)
	require.Equal(t, "USD", snapshot.Balance.Unit)
	require.Equal(t, []string{
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"POST /api/v1/auth/login",
		"GET /api/v1/keys",
		"GET /api/v1/groups/rates",
		"GET /api/v1/auth/me",
	}, upstream.requestPaths())
}

func TestUpstreamBillingProbeNormalizesExactHBYHostBeforePersistingAndSyncing(t *testing.T) {
	initialRate := 0.25
	account := &Account{
		ID:                    52,
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeAPIKey,
		Status:                StatusActive,
		Concurrency:           1,
		RateMultiplier:        &initialRate,
		UpstreamRechargeScale: 0.1,
		Credentials: map[string]any{
			"api_key":  "sk-live",
			"base_url": "https://hubway.cc/v1",
		},
		Extra: map[string]any{
			UpstreamBillingProbeEnabledExtraKey:    true,
			UpstreamBillingRateSyncEnabledExtraKey: true,
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	now := time.Date(2026, time.August, 8, 8, 0, 0, 0, time.UTC)
	balanceAmount := 12.34
	data := map[string]any{
		"billing_scope":             "token",
		"group_rate_multiplier":     0.5,
		"user_rate_multiplier":      0.7,
		"resolved_rate_multiplier":  0.7,
		"effective_rate_multiplier": 0.7,
		"peak_rate_multiplier":      1.5,
		"applied_peak_multiplier":   1.5,
	}
	balance := &UpstreamAccountBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, Amount: &balanceAmount, Unit: "USD"}

	snapshot, err := svc.persistProbeSuccess(context.Background(), account, 30, now, http.StatusOK, data, balance)

	require.NoError(t, err)
	require.InDelta(t, 0.05, snapshot.Data["group_rate_multiplier"], 1e-12)
	require.InDelta(t, 0.07, snapshot.Data["user_rate_multiplier"], 1e-12)
	require.InDelta(t, 0.07, snapshot.Data["resolved_rate_multiplier"], 1e-12)
	require.InDelta(t, 0.07, snapshot.Data["effective_rate_multiplier"], 1e-12)
	require.Equal(t, 1.5, snapshot.Data["peak_rate_multiplier"])
	require.Equal(t, 1.5, snapshot.Data["applied_peak_multiplier"])
	require.NotNil(t, snapshot.Balance)
	require.NotNil(t, snapshot.Balance.Amount)
	require.InDelta(t, 1.234, *snapshot.Balance.Amount, 1e-12)
	require.NotNil(t, snapshot.SyncedRateMultiplier)
	require.InDelta(t, 0.07, *snapshot.SyncedRateMultiplier, 1e-12)
	require.NotNil(t, account.RateMultiplier)
	require.InDelta(t, 0.07, *account.RateMultiplier, 1e-12)
}

func TestUpstreamBillingProbeDoesNotNormalizeNonExactHBYHosts(t *testing.T) {
	for _, baseURL := range []string{"https://api.hubway.cc/v1", "https://not-hubway.cc/v1"} {
		t.Run(baseURL, func(t *testing.T) {
			account := &Account{
				ID:          53,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-live", "base_url": baseURL},
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
			amount := 12.34

			snapshot, err := svc.persistProbeSuccess(context.Background(), account, 30, time.Now(), http.StatusOK,
				map[string]any{"resolved_rate_multiplier": 0.5},
				&UpstreamAccountBalanceSnapshot{Status: UpstreamBillingProbeStatusOK, Amount: &amount},
			)

			require.NoError(t, err)
			require.Equal(t, 0.5, snapshot.Data["resolved_rate_multiplier"])
			require.NotNil(t, snapshot.Balance.Amount)
			require.Equal(t, 12.34, *snapshot.Balance.Amount)
		})
	}
}

func TestUpstreamBillingProbePreservesWebLoginPasswordWhitespace(t *testing.T) {
	account := &Account{
		ID:          51,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-live",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{loginPassword: " secret "}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	svc.SetUpstreamSiteCredentialService(NewUpstreamSiteCredentialService(
		&upstreamSiteCredentialRepoStub{credentials: map[string]*UpstreamSiteCredential{
			"vovoapi.com": {
				Host:               "vovoapi.com",
				LoginUsername:      "admin@example.com",
				PasswordCiphertext: "cipher: secret ",
			},
		}},
		upstreamSiteCredentialEncryptorStub{},
	))

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
}

func TestUpstreamBillingProbeKeepsSuccessfulBalanceWhenInnomRateLookupFails(t *testing.T) {
	account := &Account{
		ID:          52,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-live",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		keysResponse: "{\"code\":0,\"data\":{\"items\":[]}}",
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "vovoapi.com")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "key_rate_not_found", snapshot.LastError)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, 12.34, *snapshot.Balance.Amount)
	require.Contains(t, upstream.requestPaths(), "GET /api/v1/auth/me")
}

func TestUpstreamBillingProbeUsesStandardEndpointBeforeWebFallback(t *testing.T) {
	account := &Account{
		ID:          46,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-standard",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &upstreamBillingProbeHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.8, snapshot.Data["effective_rate_multiplier"])
	require.EqualValues(t, 1, upstream.billingCalls.Load())
}

func TestUpstreamBillingProbeUsesStandardRateAndWebAccountBalanceTogether(t *testing.T) {
	account := &Account{
		ID:          50,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-standard",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		standardStatus: http.StatusOK,
		standardResponse: `{
			"object":"sub2api.key_billing",
			"schema_version":1,
			"billing_scope":"token",
			"group_rate_multiplier":0.8,
			"resolved_rate_multiplier":0.8,
			"peak_rate_enabled":false,
			"effective_rate_multiplier":0.8,
			"observed_at":"2026-07-13T01:00:00Z"
		}`,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "vovoapi.com")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.8, snapshot.Data["effective_rate_multiplier"])
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, []string{
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"POST /api/v1/auth/login",
		"GET /api/v1/auth/me",
	}, upstream.requestPaths())
}

func TestUpstreamBillingProbeUsesAPIKeyUsageBalanceWhenWebLoginRequiresTurnstile(t *testing.T) {
	testCases := []struct {
		name    string
		host    string
		baseURL string
		balance float64
	}{
		{name: "派大星", host: "api.aigo0.com", baseURL: "https://api.aigo0.com/v1", balance: 83.56923737},
		{name: "无畏", host: "mxamaxai.com", baseURL: "https://mxamaxai.com", balance: 9.3340916},
		{name: "未来新增站点", host: "future-upstream.example", baseURL: "https://future-upstream.example/v1", balance: 25.5},
	}
	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			account := &Account{
				ID:          int64(54 + index),
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":  "sk-standard",
					"base_url": testCase.baseURL,
				},
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			upstream := &webAccountRateHTTPStub{
				standardStatus: http.StatusOK,
				standardResponse: `{
					"object":"sub2api.key_billing",
					"schema_version":1,
					"billing_scope":"token",
					"group_rate_multiplier":0.07,
					"resolved_rate_multiplier":0.07,
					"peak_rate_enabled":false,
					"effective_rate_multiplier":0.07,
					"observed_at":"2026-08-27T01:00:00Z"
				}`,
				usageStatus:   http.StatusOK,
				usageResponse: fmt.Sprintf(`{"mode":"unrestricted","balance":%v,"remaining":%v,"unit":"USD"}`, testCase.balance, testCase.balance),
				loginStatus:   http.StatusBadRequest,
				loginResponse: `{"code":400,"reason":"TURNSTILE_VERIFICATION_FAILED"}`,
			}
			svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
			attachUpstreamSiteCredential(svc, testCase.host)

			snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

			require.NoError(t, err)
			require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
			require.NotNil(t, snapshot.Balance)
			require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
			require.InDelta(t, testCase.balance, *snapshot.Balance.Amount, 1e-12)
			require.Equal(t, "USD", snapshot.Balance.Unit)
			require.Equal(t, []string{
				"GET /v1/sub2api/billing",
				"GET /v1/usage",
			}, upstream.requestPaths())
		})
	}
}

func TestUpstreamBillingProbeKeepsAPIKeyBalanceWhenRateFallsBackToTurnstileLogin(t *testing.T) {
	account := &Account{
		ID:          60,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-standard",
			"base_url": "https://future-newapi.example/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		standardStatus: http.StatusNotFound,
		usageStatus:    http.StatusOK,
		usageResponse:  `{"balance":18.75,"unit":"USD"}`,
		loginStatus:    http.StatusBadRequest,
		loginResponse:  `{"code":400,"reason":"TURNSTILE_VERIFICATION_FAILED"}`,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "future-newapi.example")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.InDelta(t, 18.75, *snapshot.Balance.Amount, 1e-12)
	require.Equal(t, []string{
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"POST /api/v1/auth/login",
	}, upstream.requestPaths())
}

func TestUpstreamBillingProbeBalanceFailureDoesNotOverrideSuccessfulWebRate(t *testing.T) {
	account := &Account{
		ID:          47,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-live",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{balanceStatus: http.StatusBadGateway}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "vovoapi.com")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.09, snapshot.Data["effective_rate_multiplier"])
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Balance.Status)
	require.Equal(t, "http_error", snapshot.Balance.LastError)
}

func TestUpstreamBillingProbeUsesNewAPIWebLoginForAIGC(t *testing.T) {
	account := &Account{
		ID:          48,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-aigc-current",
			"base_url": "https://api.aigclink.xyz/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &newAPIWebAccountHTTPStub{
		currentAPIKey: "sk-aigc-current",
		currentGroup:  "plus",
		groupRate:     0.09,
		rawQuota:      1750000,
		quotaPerUnit:  500000,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "api.aigclink.xyz")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.09, snapshot.Data["effective_rate_multiplier"])
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.InDelta(t, 3.5, *snapshot.Balance.Amount, 0.000001)
	require.Equal(t, "USD", snapshot.Balance.Unit)
	require.Equal(t, []string{
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"POST /api/user/login",
		"GET /api/user/token",
		"GET /api/user/self",
		"GET /api/status",
		"GET /api/token/",
		"GET /api/user/self/groups",
	}, upstream.requestPaths())
}

func TestUpstreamBillingProbeUsesPiteStoredSystemTokenAndUserID(t *testing.T) {
	account := &Account{
		ID:          364,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-pite-current",
			"base_url": "https://ai.pite.chat/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &newAPIWebAccountHTTPStub{
		// Pite 可能以 200 返回非 billing JSON，仍应回退到账户查询接口。
		standardStatus:   http.StatusOK,
		standardResponse: "{\"success\":false,\"message\":\"unsupported endpoint\"}",
		// Pite 会省略 sk- 并只返回密钥前缀，必须与完整 API Key 正确匹配。
		currentAPIKey:      "pite-cur",
		currentGroup:       "pro20x",
		groupRate:          0.16,
		rawQuota:           880000,
		quotaPerUnit:       500000,
		rejectWebLogin:     true,
		requiredAuth:       "system-access",
		requiredNewAPIUser: "4319",
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredentialWithValues(svc, "ai.pite.chat", "4319", "system-access")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.16, snapshot.Data["effective_rate_multiplier"])
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.InDelta(t, 1.76, *snapshot.Balance.Amount, 0.000001)
	require.Equal(t, "USD", snapshot.Balance.Unit)
	require.Equal(t, []string{
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"GET /api/user/self",
		"GET /api/status",
		"GET /api/token/",
		"GET /api/user/self/groups",
	}, upstream.requestPaths())
}

func TestUpstreamBillingProbeNewAPIKeepsBalanceWhenCurrentKeyCannotBeMatched(t *testing.T) {
	account := &Account{
		ID:          49,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-aigc-current",
			"base_url": "https://api.aigclink.xyz/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &newAPIWebAccountHTTPStub{
		currentAPIKey: "sk-another-key",
		currentGroup:  "plus",
		groupRate:     0.09,
		rawQuota:      500000,
		quotaPerUnit:  500000,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "api.aigclink.xyz")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "key_rate_not_found", snapshot.LastError)
	require.Empty(t, snapshot.Data)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.InDelta(t, 1.0, *snapshot.Balance.Amount, 0.000001)
}

func TestUpstreamBillingProbeMatchesMaskedWebAccountKey(t *testing.T) {
	account := &Account{
		ID:                    44,
		Name:                  "HBY-main-key",
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeAPIKey,
		Status:                StatusActive,
		Concurrency:           1,
		UpstreamRechargeScale: 0.1,
		Credentials: map[string]any{
			"api_key":  "sk-abcdefgh12345678",
			"base_url": "https://hubway.cc/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		keysResponse: "{\"code\":0,\"data\":{\"items\":[{\"masked_key\":\"sk-abcd...5678\",\"status\":\"active\",\"group_id\":7,\"group\":{\"id\":7,\"rate_multiplier\":0.04}}]}}",
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "hubway.cc")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.009, snapshot.Data["effective_rate_multiplier"])
}

func TestUpstreamBillingProbeMatchesUniqueWebAccountKeyName(t *testing.T) {
	account := &Account{
		ID:                    45,
		Name:                  "HBY-main-key",
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeAPIKey,
		Status:                StatusActive,
		Concurrency:           1,
		UpstreamRechargeScale: 0.1,
		Credentials: map[string]any{
			"api_key":  "sk-key-not-returned",
			"base_url": "https://hubway.cc/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		keysResponse: "{\"code\":0,\"data\":{\"items\":[{\"name\":\"hby-main-key\",\"status\":\"active\",\"group_id\":7,\"group\":{\"id\":7,\"rate_multiplier\":0.04}}]}}",
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "hubway.cc")

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.009, snapshot.Data["effective_rate_multiplier"])
}

func TestUpstreamBillingProbeReusesWebLoginTokenButRefreshesKeyRate(t *testing.T) {
	account := &Account{
		ID:          43,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-live",
			"base_url": "https://vovoapi.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "vovoapi.com")

	_, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	_, err = svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)

	require.Equal(t, []string{
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"POST /api/v1/auth/login",
		"GET /api/v1/keys",
		"GET /api/v1/groups/rates",
		"GET /api/v1/auth/me",
		"GET /v1/sub2api/billing",
		"GET /v1/usage",
		"GET /api/v1/keys",
		"GET /api/v1/groups/rates",
		"GET /api/v1/auth/me",
	}, upstream.requestPaths())
}

func TestUpstreamBillingProbeWithoutWebCredentialsKeepsAPIKeyBalanceAndClearsPreviousRate(t *testing.T) {
	receivedAt := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	account := &Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-standard",
			"base_url": "https://hubway.cc/v1",
		},
		Extra: map[string]any{
			UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{
				Status:     UpstreamBillingProbeStatusOK,
				Data:       map[string]any{"effective_rate_multiplier": 0.5},
				ReceivedAt: &receivedAt,
			},
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		usageStatus:   http.StatusOK,
		usageResponse: `{"balance":4.5,"unit":"USD"}`,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "missing_web_login_credentials", snapshot.LastError)
	require.Empty(t, snapshot.Data)
	require.Nil(t, snapshot.ReceivedAt)
	require.Nil(t, snapshot.FreshUntil)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.InDelta(t, 4.5, *snapshot.Balance.Amount, 1e-12)
	require.Equal(t, []string{"GET /v1/sub2api/billing", "GET /v1/usage"}, upstream.requestPaths())
}

func TestUpstreamBillingProbeKeepsAPIKeyBalanceWhenBillingEndpointFails(t *testing.T) {
	account := &Account{
		ID:          67,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-standard",
			"base_url": "https://api.aigo0.com/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &webAccountRateHTTPStub{
		standardStatus: http.StatusBadGateway,
		usageStatus:    http.StatusOK,
		usageResponse:  `{"balance":4.25,"unit":"USD"}`,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "http_error", snapshot.LastError)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.InDelta(t, 4.25, *snapshot.Balance.Amount, 1e-12)
	require.Equal(t, []string{"GET /v1/sub2api/billing", "GET /v1/usage"}, upstream.requestPaths())
}

func TestUpstreamBillingProbeSuccessPersistsSanitizedSnapshot(t *testing.T) {
	initialRate := 0.25
	account := &Account{
		ID:          17,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "sk-sensitive",
			"base_url": "https://upstream.example/v1",
		},
		Extra: map[string]any{
			UpstreamBillingProbeEnabledExtraKey:    true,
			UpstreamBillingRateSyncEnabledExtraKey: true,
		},
		RateMultiplier: &initialRate,
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"object":"sub2api.key_billing",
			"schema_version":1,
			"billing_scope":"token",
			"group_rate_multiplier":0.8,
			"user_rate_multiplier":0.6,
			"resolved_rate_multiplier":0.6,
			"peak_rate_enabled":true,
			"peak_start":"09:00",
			"peak_end":"18:00",
			"peak_rate_multiplier":1.5,
			"applied_peak_multiplier":1.5,
			"effective_rate_multiplier":0.9,
			"timezone":"Asia/Shanghai",
			"observed_at":"2026-07-13T01:00:00Z",
			"unexpected_secret":"must-not-persist"
		}`)),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	fixedNow := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.9, snapshot.Data["effective_rate_multiplier"])
	require.NotContains(t, snapshot.Data, "unexpected_secret")
	require.NotNil(t, snapshot.ReceivedAt)
	require.Equal(t, fixedNow, *snapshot.ReceivedAt)
	require.NotNil(t, snapshot.FreshUntil)
	require.Equal(t, fixedNow.Add(10*time.Minute), *snapshot.FreshUntil)
	require.False(t, snapshot.NextProbeAt.Before(fixedNow.Add(4*time.Minute)))
	require.False(t, snapshot.NextProbeAt.After(fixedNow.Add(6*time.Minute)))
	// 写回的是不含高峰因子的 resolved 倍率（0.6），不是探测那一刻含高峰的
	// effective 倍率（0.9）——否则一个探测周期的峰值会被冻结进静态列。
	require.NotNil(t, account.RateMultiplier)
	require.Equal(t, 0.6, *account.RateMultiplier)
	require.NotNil(t, snapshot.SyncedRateMultiplier)
	require.Equal(t, 0.6, *snapshot.SyncedRateMultiplier)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://upstream.example/v1/sub2api/billing", upstream.requests[0].URL.String())
	require.Equal(t, "https://upstream.example/v1/usage", upstream.requests[1].URL.String())
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Equal(t, "Bearer sk-sensitive", upstream.requests[0].Header.Get("Authorization"))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))

	persisted := decodeUpstreamBillingProbeSnapshot(account.Extra)
	require.NotNil(t, persisted)
	require.Equal(t, snapshot.Status, persisted.Status)
}

func TestUpstreamBillingProbeAdaptiveCNUsesChatProtocolBaseURL(t *testing.T) {
	account := &Account{
		ID:          18,
		Platform:    PlatformKimi,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-sensitive",
			"api_protocol": APIProtocolAdaptive,
			"base_url":     "https://legacy-relay.example/v1",
			"api_base_urls": map[string]any{
				APIProtocolChatCompletions: "https://chat-relay.example/v1",
			},
		},
		Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       upstreamBillingProbeValidBody(),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://chat-relay.example/v1/sub2api/billing", upstream.requests[0].URL.String())
	require.Equal(t, "https://chat-relay.example/v1/usage", upstream.requests[1].URL.String())
}

func TestUpstreamBillingProbeSyncsResolvedRateForAllAPIKeyPlatforms(t *testing.T) {
	for _, platform := range []string{
		PlatformOpenAI,
		PlatformAnthropic,
		PlatformGemini,
		PlatformAntigravity,
		PlatformGrok,
	} {
		t.Run(platform, func(t *testing.T) {
			initialRate := 0.25
			account := &Account{
				ID:             17,
				Platform:       platform,
				Type:           AccountTypeAPIKey,
				Status:         StatusActive,
				Concurrency:    1,
				RateMultiplier: &initialRate,
				Credentials: map[string]any{
					"api_key":  "sk-sensitive",
					"base_url": "https://upstream.example",
				},
				Extra: map[string]any{
					UpstreamBillingProbeEnabledExtraKey:    true,
					UpstreamBillingRateSyncEnabledExtraKey: true,
				},
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})

			snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

			require.NoError(t, err)
			require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
			require.NotNil(t, account.RateMultiplier)
			require.Equal(t, 0.8, *account.RateMultiplier)
		})
	}
}

func TestUpstreamBillingProbeOnlyDoesNotChangeAccountRate(t *testing.T) {
	initialRate := 0.25
	account := &Account{
		ID:             18,
		Platform:       PlatformGrok,
		Type:           AccountTypeAPIKey,
		Status:         StatusActive,
		Concurrency:    1,
		RateMultiplier: &initialRate,
		Credentials: map[string]any{
			"api_key":  "sk-sensitive",
			"base_url": "https://upstream.example",
		},
		Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.NotNil(t, account.RateMultiplier)
	require.Equal(t, initialRate, *account.RateMultiplier)
	require.Contains(t, account.Extra, UpstreamBillingProbeExtraKey)
}

func TestUpstreamBillingProbeSyncRateRangeAndPrecision(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  float64
		ok    bool
	}{
		{name: "round to four decimals", value: 0.07654, want: 0.0765, ok: true},
		{name: "maximum", value: upstreamBillingRateSyncMaxMultiplier, want: upstreamBillingRateSyncMaxMultiplier, ok: true},
		// 0 会让 accountCost 恒为 0，账号配额与成本告警全部静默失效，
		// 自动写回一律拒绝（管理员手工设 0 仍然允许）。
		{name: "zero is rejected", value: 0, ok: false},
		{name: "positive below database precision rounds to zero", value: 0.00001, ok: false},
		{name: "just above the write-back ceiling", value: 100.0001, ok: false},
		{name: "column ceiling is far above the write-back ceiling", value: 999999.9999, ok: false},
		{name: "negative", value: -1, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := upstreamBillingProbeSyncRate(map[string]any{"resolved_rate_multiplier": tt.value})
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

// 只读取 resolved（时间无关的基准倍率）：effective 含探测那一刻的高峰系数，
// 写回它会把一个探测周期的峰值/谷值冻结进静态列。
func TestUpstreamBillingProbeSyncRateIgnoresEffectiveRate(t *testing.T) {
	got, ok := upstreamBillingProbeSyncRate(map[string]any{
		"resolved_rate_multiplier":  0.6,
		"effective_rate_multiplier": 0.9,
	})
	require.True(t, ok)
	require.Equal(t, 0.6, got)

	_, ok = upstreamBillingProbeSyncRate(map[string]any{"effective_rate_multiplier": 0.9})
	require.False(t, ok)
}

// 上游声明超出自动写回值域时保持原倍率，但探测本身是成功的：
// 快照照常记 ok，不累计 failure_count、不进入退避。
func TestUpstreamBillingProbeKeepsRateWhenDeclarationOutOfSyncRange(t *testing.T) {
	for _, tt := range []struct {
		name     string
		declared string
	}{
		{name: "zero", declared: "0"},
		{name: "above ceiling", declared: "1000"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			initialRate := 0.25
			account := &Account{
				ID:             21,
				Platform:       PlatformOpenAI,
				Type:           AccountTypeAPIKey,
				Status:         StatusActive,
				Concurrency:    1,
				RateMultiplier: &initialRate,
				Credentials: map[string]any{
					"api_key":  "sk-sensitive",
					"base_url": "https://upstream.example",
				},
				Extra: map[string]any{
					UpstreamBillingProbeEnabledExtraKey:    true,
					UpstreamBillingRateSyncEnabledExtraKey: true,
				},
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{
					"object":"sub2api.key_billing",
					"schema_version":1,
					"billing_scope":"token",
					"group_rate_multiplier":%[1]s,
					"resolved_rate_multiplier":%[1]s,
					"peak_rate_enabled":false,
					"effective_rate_multiplier":%[1]s,
					"observed_at":"2026-07-13T01:00:00Z"
				}`, tt.declared))),
			}}
			svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

			snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

			require.NoError(t, err)
			require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
			require.Zero(t, snapshot.FailureCount)
			require.Nil(t, snapshot.SyncedRateMultiplier)
			require.NotNil(t, account.RateMultiplier)
			require.Equal(t, initialRate, *account.RateMultiplier)
			// 原始声明仍进快照供展示。
			require.Equal(t, snapshot.Data["resolved_rate_multiplier"], snapshot.Data["effective_rate_multiplier"])
		})
	}
}

// 未开启同步的账号只观察上游声明：声明值不适配 accounts.rate_multiplier
// 不得被记成探测失败（否则会累计 failure_count 并进入指数退避）。
func TestUpstreamBillingProbeWithoutSyncIgnoresUnusableDeclaredRate(t *testing.T) {
	account := &Account{
		ID:          22,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-sensitive",
			"base_url": "https://upstream.example",
		},
		Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"object":"sub2api.key_billing",
			"schema_version":1,
			"billing_scope":"token",
			"group_rate_multiplier":0,
			"resolved_rate_multiplier":0,
			"peak_rate_enabled":false,
			"effective_rate_multiplier":0,
			"observed_at":"2026-07-13T01:00:00Z"
		}`)),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Zero(t, snapshot.FailureCount)
	require.Empty(t, snapshot.LastError)
	require.Nil(t, snapshot.SyncedRateMultiplier)
	require.Equal(t, float64(0), snapshot.Data["resolved_rate_multiplier"])
	require.Nil(t, account.RateMultiplier)
}

func TestUpstreamBillingProbeRejectsMissingRequiredMultiplier(t *testing.T) {
	_, err := parseUpstreamBillingProbeResponse([]byte(`{
		"object":"sub2api.key_billing",
		"schema_version":1,
		"billing_scope":"token",
		"group_rate_multiplier":0.8,
		"peak_rate_enabled":false,
		"effective_rate_multiplier":0.8,
		"observed_at":"2026-07-13T01:00:00Z"
	}`))

	require.ErrorContains(t, err, "incomplete billing response")
}

func TestUpstreamBillingProbeDiscardsResultWhenIdentityChangesInFlight(t *testing.T) {
	account := &Account{
		ID:          19,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-old", "base_url": "https://upstream.example"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &upstreamBillingProbeHTTPStub{beforeResponse: func() {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		repo.accounts[account.ID].Credentials = map[string]any{"api_key": "sk-new", "base_url": "https://new.example"}
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)

	require.Nil(t, snapshot)
	require.ErrorIs(t, err, ErrUpstreamBillingProbeIdentityChanged)
	require.NotContains(t, repo.accounts[account.ID].Extra, UpstreamBillingProbeExtraKey)
}

func TestUpstreamBillingProbeRejectsInvalidPeakConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		start    string
		end      string
		timezone string
	}{
		{name: "invalid start", start: "25:00", end: "18:00", timezone: "UTC"},
		{name: "cross midnight", start: "22:00", end: "02:00", timezone: "UTC"},
		{name: "invalid timezone", start: "09:00", end: "18:00", timezone: "Mars/Olympus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"object":"sub2api.key_billing",
				"schema_version":1,
				"billing_scope":"token",
				"group_rate_multiplier":0.8,
				"resolved_rate_multiplier":0.8,
				"peak_rate_enabled":true,
				"peak_start":%q,
				"peak_end":%q,
				"peak_rate_multiplier":1.5,
				"applied_peak_multiplier":1,
				"effective_rate_multiplier":0.8,
				"timezone":%q,
				"observed_at":"2026-07-13T01:00:00Z"
			}`, tt.start, tt.end, tt.timezone)

			_, err := parseUpstreamBillingProbeResponse([]byte(body))
			require.ErrorContains(t, err, "invalid peak billing response")
		})
	}
}

func TestUpstreamBillingProbeRejectsInconsistentMultipliers(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "resolved does not use user override",
			body: `{
				"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",
				"group_rate_multiplier":0.8,"user_rate_multiplier":0.5,"resolved_rate_multiplier":0.8,
				"peak_rate_enabled":false,"effective_rate_multiplier":0.8,"observed_at":"2026-07-13T01:00:00Z"
			}`,
		},
		{
			name: "effective rate does not match resolved rate",
			body: `{
				"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",
				"group_rate_multiplier":0.8,"resolved_rate_multiplier":0.8,
				"peak_rate_enabled":false,"effective_rate_multiplier":1.2,"observed_at":"2026-07-13T01:00:00Z"
			}`,
		},
		{
			name: "applied peak does not match observed window",
			body: `{
				"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",
				"group_rate_multiplier":0.8,"resolved_rate_multiplier":0.8,
				"peak_rate_enabled":true,"peak_start":"09:00","peak_end":"18:00",
				"peak_rate_multiplier":1.5,"applied_peak_multiplier":1,
				"effective_rate_multiplier":0.8,"timezone":"Asia/Shanghai","observed_at":"2026-07-13T01:00:00Z"
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseUpstreamBillingProbeResponse([]byte(tt.body))
			require.ErrorContains(t, err, "inconsistent")
		})
	}
}

func TestUpstreamBillingRateAtHandlesDST(t *testing.T) {
	data := map[string]any{
		"billing_scope":            "token",
		"resolved_rate_multiplier": 1.0,
		"peak_rate_enabled":        true,
		"peak_start":               "02:00",
		"peak_end":                 "04:00",
		"peak_rate_multiplier":     2.0,
		"timezone":                 "America/New_York",
	}
	beforeJump := time.Date(2026, time.March, 8, 6, 30, 0, 0, time.UTC)
	afterJump := time.Date(2026, time.March, 8, 7, 30, 0, 0, time.UTC)

	rate, ok := upstreamBillingRateAt(data, beforeJump)
	require.True(t, ok)
	require.Equal(t, 1.0, rate)
	rate, ok = upstreamBillingRateAt(data, afterJump)
	require.True(t, ok)
	require.Equal(t, 2.0, rate)
}

func TestUpstreamBillingProbeFailurePreservesLastSuccessAndRetryAfter(t *testing.T) {
	receivedAt := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	initialRate := 0.35
	previous := &UpstreamBillingProbeSnapshot{
		Status:       UpstreamBillingProbeStatusOK,
		Data:         map[string]any{"effective_rate_multiplier": 0.5},
		ReceivedAt:   &receivedAt,
		FailureCount: 1,
	}
	account := &Account{
		ID:             18,
		Platform:       PlatformOpenAI,
		Type:           AccountTypeAPIKey,
		Status:         StatusActive,
		Concurrency:    1,
		RateMultiplier: &initialRate,
		Credentials:    map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra: map[string]any{
			UpstreamBillingProbeEnabledExtraKey: true,
			UpstreamBillingProbeExtraKey:        previous,
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"14400"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"do not persist this"}`)),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	fixedNow := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, previous.Data, snapshot.Data)
	require.Equal(t, previous.ReceivedAt, snapshot.ReceivedAt)
	require.NotNil(t, snapshot.FreshUntil)
	require.Equal(t, receivedAt.Add(10*time.Minute), *snapshot.FreshUntil)
	require.Equal(t, 2, snapshot.FailureCount)
	require.Equal(t, "http_error", snapshot.LastError)
	require.False(t, snapshot.NextProbeAt.Before(fixedNow.Add(4*time.Hour)))
	require.NotContains(t, snapshot.LastError, "do not persist")
	require.NotNil(t, account.RateMultiplier)
	require.Equal(t, initialRate, *account.RateMultiplier)
}

func TestUpstreamBillingProbeRetryAfterIsNotShortened(t *testing.T) {
	delay := nextProbeDelay(30, 48*time.Hour)
	require.Equal(t, 48*time.Hour, delay)
}

// unsupported 的重探间隔明显长于普通失败，但始终有上界：上游后来接入 sub2api
// 时最迟一天内会被重新发现，且不会缩短上游 Retry-After 指令。
func TestUpstreamBillingProbeUnsupportedDelayIsStretchedAndBounded(t *testing.T) {
	// 默认 5 分钟 interval：普通失败 4~6 分钟，unsupported 为其 8 倍。
	stretched := unsupportedProbeDelay(upstreamBillingProbeDefaultIntervalMinutes, 0)
	require.Greater(t, stretched, 6*time.Minute)
	require.GreaterOrEqual(t, stretched, 32*time.Minute)
	require.LessOrEqual(t, stretched, 48*time.Minute)

	// 永不超过封顶值，因此 unsupported 账号不会被永久排除在重探之外。
	require.LessOrEqual(t, unsupportedProbeDelay(upstreamBillingProbeMaxIntervalMinutes, 0), upstreamBillingProbeMaxDelay)
	require.Positive(t, unsupportedProbeDelay(upstreamBillingProbeMinIntervalMinutes, 0))

	// Retry-After 更长时原样保留，不被封顶缩短；更短时至少不早于该指令。
	require.Equal(t, 48*time.Hour, unsupportedProbeDelay(30, 48*time.Hour))
	require.GreaterOrEqual(t, unsupportedProbeDelay(30, time.Hour), time.Hour)
}

// 加长退避只把 unsupported 账号移出周期性热队列，手动探测不受影响。
func TestUpstreamBillingProbeUnsupportedBackoffDefersRunnerButNotManualProbe(t *testing.T) {
	// Ollama Cloud 形态：官方域，不发请求直接落 unsupported。
	account := &Account{
		ID:          31,
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-ollama", "base_url": "https://ollama.com/v1"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &upstreamBillingProbeHTTPStub{}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":true,"interval_minutes":30}`,
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)
	start := time.Date(2026, time.July, 26, 2, 0, 0, 0, time.UTC)
	now := start
	svc.now = func() time.Time { return now }

	require.NoError(t, svc.RunDue(context.Background()))
	first := decodeUpstreamBillingProbeSnapshot(account.Extra)
	require.NotNil(t, first)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, first.Status)
	require.Equal(t, start, first.LastAttemptAt)
	require.False(t, first.NextProbeAt.Before(start.Add(192*time.Minute)))
	require.Zero(t, upstream.calls.Load())

	// 一个普通失败早就该重探的时间点（远超 36 分钟），runner 仍跳过该账号。
	now = start.Add(90 * time.Minute)
	require.NoError(t, svc.RunDue(context.Background()))
	deferred := decodeUpstreamBillingProbeSnapshot(account.Extra)
	require.NotNil(t, deferred)
	require.Equal(t, first.LastAttemptAt, deferred.LastAttemptAt)

	// 手动探测无视退避窗口，管理员随时可以重试。
	manual, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, manual.Status)
	require.Equal(t, now, manual.LastAttemptAt)
	require.Equal(t, 2, manual.FailureCount)
}

func TestUpstreamBillingProbeEmptyResponseIsPersistedAsFailure(t *testing.T) {
	account := &Account{
		ID:          21,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, &httpUpstreamRecorder{}, &upstreamBillingProbeSettingRepo{})
	fixedNow := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "empty_response", snapshot.LastError)
	require.False(t, snapshot.NextProbeAt.Before(fixedNow.Add(4*time.Minute)))
	require.False(t, snapshot.NextProbeAt.After(fixedNow.Add(6*time.Minute)))

	snapshot, err = svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.FailureCount)
	require.False(t, snapshot.NextProbeAt.Before(fixedNow.Add(4*time.Minute)))
	require.False(t, snapshot.NextProbeAt.After(fixedNow.Add(6*time.Minute)))
}

func TestUpstreamBillingProbeUnsupportedAndAccountToggle(t *testing.T) {
	account := &Account{
		ID:          19,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.openai.com"},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("not found")),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	fixedNow := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	require.NoError(t, svc.SetAccountEnabled(context.Background(), account.ID, true))
	require.Equal(t, true, account.Extra[UpstreamBillingProbeEnabledExtraKey])
	account.Extra[UpstreamBillingRateSyncEnabledExtraKey] = true
	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Status)
	require.Equal(t, "unsupported", snapshot.LastError)
	// unsupported 走加长退避：默认 5 分钟 interval => (4~6) * 8 分钟。
	require.False(t, snapshot.NextProbeAt.Before(fixedNow.Add(32*time.Minute)))
	require.False(t, snapshot.NextProbeAt.After(fixedNow.Add(48*time.Minute)))

	snapshot, err = svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.FailureCount)
	require.False(t, snapshot.NextProbeAt.Before(fixedNow.Add(32*time.Minute)))
	require.False(t, snapshot.NextProbeAt.After(fixedNow.Add(48*time.Minute)))
	require.NoError(t, svc.SetAccountEnabled(context.Background(), account.ID, false))
	require.Equal(t, false, account.Extra[UpstreamBillingProbeEnabledExtraKey])
	require.Equal(t, false, account.Extra[UpstreamBillingRateSyncEnabledExtraKey])

	invalid := &Account{ID: 20, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo.accounts[invalid.ID] = invalid
	err = svc.SetAccountEnabled(context.Background(), invalid.ID, true)
	require.True(t, errors.Is(err, ErrUpstreamBillingProbeAccountInvalid))
}

func TestUpstreamBillingProbeRunnerIsBoundedAndManualProbeIgnoresSwitches(t *testing.T) {
	accounts := make(map[int64]*Account, 25)
	for id := int64(1); id <= 25; id++ {
		accounts[id] = &Account{
			ID:          id,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
			Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
		}
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: accounts}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":true,"interval_minutes":30}`,
	}}
	upstream := &upstreamBillingProbeHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)
	svc.now = func() time.Time { return time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC) }

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(20), upstream.billingCalls.Load())

	settingsRepo.mu.Lock()
	settingsRepo.values[SettingKeyUpstreamBillingProbeSettings] = `{"enabled":false,"interval_minutes":30}`
	settingsRepo.mu.Unlock()
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(20), upstream.billingCalls.Load())

	accounts[25].Extra[UpstreamBillingProbeEnabledExtraKey] = false
	manualRate := 0.25
	accounts[25].RateMultiplier = &manualRate
	snapshot, err := svc.ProbeAccount(context.Background(), 25)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, int64(21), upstream.billingCalls.Load())
	require.NotNil(t, accounts[25].RateMultiplier)
	require.Equal(t, manualRate, *accounts[25].RateMultiplier)
}

func TestUpstreamBillingProbeRunDueConsumesBalanceCenterEventsWhenFallbackDisabled(t *testing.T) {
	account := &Account{
		ID:          31,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: false},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings:   `{"enabled":false,"interval_minutes":30}`,
		SettingKeyBalanceCenterEnabled:           "true",
		SettingKeyBalanceCenterEventProbeEnabled: "true",
	}}
	upstream := &upstreamBillingProbeHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)
	events := NewBalanceCenterEventService(&balanceCenterEventQueueStub{due: []int64{account.ID}}, settingsRepo, svc)
	svc.SetBalanceCenterEventService(events)
	svc.now = func() time.Time { return time.Date(2026, time.August, 12, 2, 0, 0, 0, time.UTC) }

	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.billingCalls.Load())
}

func TestUpstreamBillingProbeRunnerRechecksEnabledAfterDueSelection(t *testing.T) {
	account := &Account{
		ID:          26,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: false},
	}
	staleDue := *account
	staleDue.Extra = map[string]any{UpstreamBillingProbeEnabledExtraKey: true}
	baseRepo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	repo := &staleDueUpstreamBillingProbeAccountRepo{upstreamBillingProbeAccountRepo: baseRepo, due: []Account{staleDue}}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":true,"interval_minutes":30}`,
	}}
	upstream := &upstreamBillingProbeHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)

	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, upstream.calls.Load())
	require.NotContains(t, account.Extra, UpstreamBillingProbeExtraKey)
}

func TestUpstreamBillingProbeNeverDowngradesMissingConfiguredProxyToDirect(t *testing.T) {
	proxyID := int64(7)
	for _, tc := range []struct {
		name       string
		proxy      *Proxy
		wantReason string
		wantErr    error
	}{
		{name: "missing hydrated proxy", wantReason: "proxy_unavailable"},
		{name: "mismatched hydrated proxy", proxy: &Proxy{ID: 8, Protocol: "http", Host: "127.0.0.1", Port: 8080}, wantErr: ErrUpstreamBillingProbeIdentityChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{
				ID:          27,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-sensitive", "base_url": "https://upstream.example"},
				ProxyID:     &proxyID,
				Proxy:       tc.proxy,
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			upstream := &upstreamBillingProbeHTTPStub{}
			svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

			snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, snapshot)
			} else {
				require.NoError(t, err)
				require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
				require.Equal(t, tc.wantReason, snapshot.LastError)
			}
			require.Zero(t, upstream.calls.Load())
			if tc.wantErr != nil {
				require.NotContains(t, account.Extra, UpstreamBillingProbeExtraKey)
			}
		})
	}
}

func TestUpstreamBillingProbeRunnerOnlyScansOnLeader(t *testing.T) {
	account := &Account{
		ID:          31,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &upstreamBillingProbeHTTPStub{}
	cache := &fakeLeaderLockCache{}
	lockKey := upstreamBillingProbeLeaderLockKeyAt(time.Now())
	peer := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	peer.instanceID = "peer"
	peer.SetLeaderLock(cache, nil)
	_, acquired, err := peer.tryAcquireLeaderLock(context.Background(), lockKey)
	require.NoError(t, err)
	require.True(t, acquired)
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	svc.SetLeaderLock(cache, nil)

	require.NoError(t, svc.RunDue(context.Background()))
	require.Zero(t, upstream.calls.Load())

	require.NoError(t, cache.ReleaseLeaderLock(context.Background(), lockKey, "peer"))
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.billingCalls.Load())
}

func TestUpstreamBillingProbeLeaderLockFailsClosedOnCacheError(t *testing.T) {
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	svc.SetLeaderLock(&fakeLeaderLockCache{acquireErr: context.DeadlineExceeded}, nil)

	release, acquired, err := svc.tryAcquireLeaderLock(context.Background(), upstreamBillingProbeLeaderLockKeyAt(time.Now()))

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, acquired)
	require.Nil(t, release)
}

func TestUpstreamBillingProbeLeaderLockUsesCadenceBuckets(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	first := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	second := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	first.SetLeaderLock(cache, nil)
	second.SetLeaderLock(cache, nil)
	beforeBoundary := time.Unix(59, 0)
	afterBoundary := beforeBoundary.Add(time.Second)

	releaseFirst, acquired, err := first.tryAcquireLeaderLock(context.Background(), upstreamBillingProbeLeaderLockKeyAt(beforeBoundary))
	require.NoError(t, err)
	require.True(t, acquired)
	releaseSecond, acquired, err := second.tryAcquireLeaderLock(context.Background(), upstreamBillingProbeLeaderLockKeyAt(afterBoundary))
	require.NoError(t, err)
	require.True(t, acquired, "the prior cadence lock must not suppress the next cadence")
	releaseFirst()
	releaseSecond()
}

func TestUpstreamBillingProbeFiveInstancesRunOneConcurrentBatch(t *testing.T) {
	account := &Account{
		ID:          32,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://127.0.0.1:8080"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":true,"interval_minutes":30}`,
	}}
	cache := &fakeLeaderLockCache{}
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var enteredOnce sync.Once
	upstream := &upstreamBillingProbeHTTPStub{beforeResponse: func() {
		enteredOnce.Do(func() { close(entered) })
		<-unblock
	}}

	start := make(chan struct{})
	results := make(chan error, 5)
	for range 5 {
		svc := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)
		svc.SetLeaderLock(cache, nil)
		go func() {
			<-start
			results <- svc.RunDue(context.Background())
		}()
	}
	close(start)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("leader did not start the probe batch")
	}
	for range 4 {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("non-leader instance did not skip the active batch")
		}
	}
	require.Equal(t, int64(1), upstream.billingCalls.Load())
	close(unblock)
	require.NoError(t, <-results)
	require.Equal(t, int64(1), upstream.billingCalls.Load())
}

func TestUpstreamBillingProbeManualBatchesShareConcurrencyLimit(t *testing.T) {
	accounts := make(map[int64]*Account, 12)
	for id := int64(1); id <= 12; id++ {
		accounts[id] = &Account{
			ID:          id,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://127.0.0.1:8080"},
		}
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: accounts}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":true,"interval_minutes":30}`,
	}}
	entered := make(chan struct{}, len(accounts))
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	release := func() { unblockOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	upstream := &upstreamBillingProbeHTTPStub{beforeResponse: func() {
		entered <- struct{}{}
		<-unblock
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)

	results := make(chan []UpstreamBillingProbeResult, 3)
	for batch := 0; batch < 3; batch++ {
		firstID := int64(batch*4 + 1)
		ids := []int64{firstID, firstID + 1, firstID + 2, firstID + 3}
		go func() { results <- svc.ProbeAccounts(context.Background(), ids) }()
	}
	for range upstreamBillingProbeConcurrency {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("shared probe slots did not fill")
		}
	}
	select {
	case <-entered:
		release()
		t.Fatal("parallel manual batches exceeded the service-wide concurrency limit")
	case <-time.After(100 * time.Millisecond):
	}
	release()

	for range 3 {
		select {
		case batchResults := <-results:
			for _, result := range batchResults {
				require.Empty(t, result.Error)
				require.NotNil(t, result.Snapshot)
			}
		case <-time.After(time.Second):
			t.Fatal("manual probe batch did not finish")
		}
	}
	require.Equal(t, int64(upstreamBillingProbeConcurrency), upstream.maxActive.Load())
}

func TestUpstreamBillingProbeManualAndScheduledRequestsShareOneNetworkProbe(t *testing.T) {
	account := &Account{
		ID:          46,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	started := make(chan struct{})
	unblock := make(chan struct{})
	var startedOnce sync.Once
	upstream := &upstreamBillingProbeHTTPStub{beforeResponse: func() {
		startedOnce.Do(func() { close(started) })
		<-unblock
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	errs := make(chan error, 2)
	go func() {
		_, err := svc.probeScheduledAccount(context.Background(), account.ID, 30)
		errs <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scheduled probe did not reach the upstream")
	}
	manualStarted := make(chan struct{})
	go func() {
		close(manualStarted)
		_, err := svc.ProbeAccount(context.Background(), account.ID)
		errs <- err
	}()
	<-manualStarted
	time.Sleep(20 * time.Millisecond)
	close(unblock)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, int64(1), upstream.billingCalls.Load())
}

func TestUpstreamBillingProbeScheduledRechecksAfterWaitingForSlot(t *testing.T) {
	account := &Account{
		ID:          47,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &upstreamBillingProbeHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	for range upstreamBillingProbeConcurrency {
		svc.probeSlots <- struct{}{}
	}
	result := make(chan error, 1)
	go func() {
		_, err := svc.probeScheduledAccount(context.Background(), account.ID, 30)
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	repo.mu.Lock()
	account.Extra[UpstreamBillingProbeEnabledExtraKey] = false
	repo.mu.Unlock()
	<-svc.probeSlots

	require.NoError(t, <-result)
	require.Zero(t, upstream.calls.Load())
}

func TestUpstreamBillingProbeLeaderLockCoversStaggeredInstancesInCadenceWindow(t *testing.T) {
	account := func(id int64) *Account {
		return &Account{
			ID:          id,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://127.0.0.1:8080"},
			Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
		}
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{41: account(41)}}
	settingsRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":true,"interval_minutes":30}`,
	}}
	cache := &fakeLeaderLockCache{}
	upstream := &upstreamBillingProbeHTTPStub{}
	first := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)
	first.SetLeaderLock(cache, nil)

	require.NoError(t, first.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.billingCalls.Load())
	require.Equal(t, first.instanceID, cache.heldBy(upstreamBillingProbeLeaderLockKeyAt(time.Now())))

	repo.mu.Lock()
	repo.accounts[42] = account(42)
	repo.mu.Unlock()
	staggered := newUpstreamBillingProbeTestService(repo, upstream, settingsRepo)
	staggered.SetLeaderLock(cache, nil)
	require.NoError(t, staggered.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.billingCalls.Load(), "a staggered instance must not start a second batch inside the cadence window")
}
