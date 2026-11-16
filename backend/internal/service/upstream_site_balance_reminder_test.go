package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type upstreamSiteBalanceReminderRepoSpy struct {
	UpstreamSiteBalanceReminderRepository
	ensured  []UpstreamSiteBalanceObservation
	saved    []UpstreamSiteBalanceObservation
	reminder *UpstreamSiteBalanceReminder
}

func (r *upstreamSiteBalanceReminderRepoSpy) EnsureUpstreamSiteBalanceObservations(_ context.Context, items []UpstreamSiteBalanceObservation) error {
	r.ensured = append(r.ensured, items...)
	return nil
}

func (r *upstreamSiteBalanceReminderRepoSpy) SaveUpstreamSiteBalanceObservation(_ context.Context, item UpstreamSiteBalanceObservation) (*UpstreamSiteBalanceReminder, error) {
	r.saved = append(r.saved, item)
	return r.reminder, nil
}

func TestProbeUpstreamSiteBalanceUsesAPIKeyBeforeWebLogin(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Concurrency: 1, UpstreamRechargeScale: 0.1,
		Credentials: map[string]any{"api_key": "sk-standard", "base_url": "https://site.example/v1"},
	}
	upstream := &webAccountRateHTTPStub{
		usageStatus: http.StatusOK, usageResponse: `{"mode":"unrestricted","balance":20,"unit":"USD"}`,
		loginStatus: http.StatusBadRequest, loginResponse: `{"code":400,"reason":"TURNSTILE_VERIFICATION_FAILED"}`,
	}
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
	attachUpstreamSiteCredential(svc, "site.example")
	repo := &upstreamSiteBalanceReminderRepoSpy{}
	bus := &balanceCenterReminderBusStub{}
	svc.SetUpstreamSiteBalanceReminderDependencies(repo, bus)
	now := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	observation := UpstreamSiteBalanceObservation{Host: "site.example", DisplayName: "测试站点", BaseURL: "https://site.example"}

	require.NoError(t, svc.probeUpstreamSiteBalance(context.Background(), observation, account))
	require.Len(t, repo.saved, 1)
	require.Equal(t, UpstreamSiteBalanceObservation{
		Host: "site.example", DisplayName: "测试站点", BaseURL: "https://site.example",
		Balance: 20, Currency: "USD", ObservedAt: now,
	}, repo.saved[0])
	require.Empty(t, bus.events)

	now = now.Add(UpstreamSiteBalancePollInterval)
	upstream.usageResponse = `{"mode":"unrestricted","balance":25,"unit":"USD"}`
	repo.reminder = &UpstreamSiteBalanceReminder{
		Host: observation.Host, DisplayName: observation.DisplayName, OldBalance: 20, NewBalance: 25,
		Delta: 5, Currency: "USD", DetectedAt: now, Version: 1,
	}
	require.NoError(t, svc.probeUpstreamSiteBalance(context.Background(), observation, account))
	require.Len(t, repo.saved, 2)
	require.Equal(t, 25.0, repo.saved[1].Balance)
	require.Equal(t, []UpstreamSiteBalanceReminderEvent{{Type: "upsert", Reminder: repo.reminder}}, bus.events)
	require.Equal(t, []string{"GET /v1/usage", "GET /v1/usage"}, upstream.requestPaths())
	require.Equal(t, 0.1, account.UpstreamRechargeScale)
	require.Empty(t, account.Extra)
}

func TestProbeUpstreamSiteBalanceDoesNotRequireWebCredentialsForAPIKey(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-standard", "base_url": "https://site.example/v1"},
	}
	upstream := &webAccountRateHTTPStub{usageStatus: http.StatusOK, usageResponse: `{"mode":"unrestricted","balance":0,"unit":"USD"}`}
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
	svc.SetUpstreamSiteCredentialService(NewUpstreamSiteCredentialService(&upstreamSiteCredentialRepoStub{}, upstreamSiteCredentialEncryptorStub{}))
	repo := &upstreamSiteBalanceReminderRepoSpy{}
	svc.SetUpstreamSiteBalanceReminderDependencies(repo, nil)

	err := svc.probeUpstreamSiteBalance(context.Background(), UpstreamSiteBalanceObservation{
		Host: "site.example", BaseURL: "https://site.example",
	}, account)

	require.NoError(t, err)
	require.Len(t, repo.saved, 1)
	require.Zero(t, repo.saved[0].Balance)
	require.Equal(t, []string{"GET /v1/usage"}, upstream.requestPaths())
}

func TestProbeUpstreamSiteBalanceFallsBackToWebForNonWalletUsage(t *testing.T) {
	for _, usageResponse := range []string{
		`{"mode":"quota_limited","balance":999,"unit":"USD"}`,
		`{"balance":999,"unit":"USD"}`,
		`{"mode":"unrestricted","remaining":999,"unit":"USD","subscription":{}}`,
		`{"mode":"unrestricted","balance":-1,"unit":"USD"}`,
		`{"mode":"unrestricted","balance":"NaN","unit":"USD"}`,
		`{"mode":"unrestricted","balance":"+Inf","unit":"USD"}`,
		`{"mode":"unrestricted","balance":999}`,
	} {
		t.Run(usageResponse, func(t *testing.T) {
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-standard", "base_url": "https://site.example/v1"},
			}
			upstream := &webAccountRateHTTPStub{usageStatus: http.StatusOK, usageResponse: usageResponse}
			svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
			attachUpstreamSiteCredential(svc, "site.example")
			repo := &upstreamSiteBalanceReminderRepoSpy{}
			svc.SetUpstreamSiteBalanceReminderDependencies(repo, nil)

			err := svc.probeUpstreamSiteBalance(context.Background(), UpstreamSiteBalanceObservation{
				Host: "site.example", BaseURL: "https://site.example",
			}, account)

			require.NoError(t, err)
			require.Len(t, repo.saved, 1)
			require.Equal(t, 12.34, repo.saved[0].Balance)
			require.Equal(t, []string{"GET /v1/usage", "POST /api/v1/auth/login", "GET /api/v1/auth/me"}, upstream.requestPaths())
		})
	}
}

func TestRefreshUpstreamSiteBalanceTargetsIncludesSitesWithoutWebPassword(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"api_key": "sk-standard", "base_url": "https://site.example/v1"},
	}
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}, &webAccountRateHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	svc.SetUpstreamSiteCredentialService(NewUpstreamSiteCredentialService(&upstreamSiteCredentialRepoStub{
		sites: []UpstreamSiteCredentialSummary{{Host: "site.example", WebsiteURL: "https://site.example", AccountIDs: []int64{1}}},
	}, upstreamSiteCredentialEncryptorStub{}))
	repo := &upstreamSiteBalanceReminderRepoSpy{}
	svc.SetUpstreamSiteBalanceReminderDependencies(repo, nil)

	require.NoError(t, svc.refreshUpstreamSiteBalanceTargets(context.Background(), time.Now()))
	require.Len(t, repo.ensured, 1)
	require.Same(t, account, svc.upstreamSiteBalanceAccounts["site.example"])
}

func TestRefreshUpstreamSiteBalanceTargetsPrefersActiveAPIKey(t *testing.T) {
	accounts := map[int64]*Account{
		1: {ID: 1, Type: AccountTypeOAuth, Status: StatusActive},
		2: {ID: 2, Type: AccountTypeAPIKey, Status: StatusDisabled, Credentials: map[string]any{"api_key": "sk-inactive"}},
		3: {ID: 3, Type: AccountTypeAPIKey, Status: StatusActive},
		4: {ID: 4, Type: AccountTypeAPIKey, Status: StatusActive, Credentials: map[string]any{"api_key": "sk-standard"}},
	}
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: accounts}, &webAccountRateHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	svc.SetUpstreamSiteCredentialService(NewUpstreamSiteCredentialService(&upstreamSiteCredentialRepoStub{
		sites: []UpstreamSiteCredentialSummary{{Host: "site.example", WebsiteURL: "https://site.example", AccountIDs: []int64{99, 1, 2, 3, 4}}},
	}, upstreamSiteCredentialEncryptorStub{}))
	svc.SetUpstreamSiteBalanceReminderDependencies(&upstreamSiteBalanceReminderRepoSpy{}, nil)

	require.NoError(t, svc.refreshUpstreamSiteBalanceTargets(context.Background(), time.Now()))
	require.Same(t, accounts[4], svc.upstreamSiteBalanceAccounts["site.example"])
}

func TestProbeUpstreamSiteBalancePreservesCachedWebSessionAndBoundedRetry(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			account := &Account{ID: 1, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sk-standard", "base_url": "https://site.example"}}
			upstream := &webAccountRateHTTPStub{balanceStatus: status}
			svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
			attachUpstreamSiteCredential(svc, "site.example")
			key := webAccountTokenCacheKey("https://site.example", "admin@example.com")
			svc.webTokens = map[string]cachedWebAccountToken{key: {token: "web-token", expiresAt: time.Now().Add(-time.Hour)}}
			repo := &upstreamSiteBalanceReminderRepoSpy{}
			svc.SetUpstreamSiteBalanceReminderDependencies(repo, nil)

			err := svc.probeUpstreamSiteBalance(context.Background(), UpstreamSiteBalanceObservation{Host: "site.example", BaseURL: "https://site.example"}, account)
			paths := []string{"GET /v1/usage", "GET /api/v1/auth/me"}
			if status == http.StatusUnauthorized {
				paths = append(paths, "POST /api/v1/auth/login", "GET /api/v1/auth/me")
			}
			require.Equal(t, paths, upstream.requestPaths())
			if status == http.StatusOK {
				require.NoError(t, err)
				require.Len(t, repo.saved, 1)
			} else {
				require.Error(t, err)
				require.Empty(t, repo.saved)
			}
		})
	}
}

func TestProbeUpstreamSiteBalanceRejectsChangedHostAndMissingProxy(t *testing.T) {
	proxyID := int64(9)
	for _, tt := range []struct {
		name    string
		baseURL string
		proxyID *int64
		want    string
	}{
		{name: "站点已变化", baseURL: "https://other.example", want: "account_identity_changed"},
		{name: "代理不可用", baseURL: "https://site.example", proxyID: &proxyID, want: "proxy_unavailable"},
		{name: "无效地址", baseURL: ":invalid", want: "invalid_base_url"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &webAccountRateHTTPStub{}
			svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
			repo := &upstreamSiteBalanceReminderRepoSpy{}
			svc.SetUpstreamSiteBalanceReminderDependencies(repo, nil)
			account := &Account{ID: 1, Type: AccountTypeAPIKey, ProxyID: tt.proxyID, Credentials: map[string]any{"api_key": "sk-standard", "base_url": tt.baseURL}}
			err := svc.probeUpstreamSiteBalance(context.Background(), UpstreamSiteBalanceObservation{Host: "site.example", BaseURL: "https://site.example"}, account)
			require.EqualError(t, err, tt.want)
			require.Empty(t, upstream.requestPaths())
			require.Empty(t, repo.saved)
		})
	}
}

func TestSafeUpstreamSiteBalanceFailureRejectsUnsafeFields(t *testing.T) {
	var nilProbeError *upstreamSiteBalanceProbeError
	require.Equal(t, "probe_failed", safeUpstreamSiteBalanceFailure(nilProbeError))
	require.Equal(t, "probe_failed", safeUpstreamSiteBalanceFailure(&upstreamSiteBalanceProbeError{stage: "private-stage"}))
	require.Equal(t, "login_failed_http_403", safeUpstreamSiteBalanceFailure(&upstreamSiteBalanceProbeError{stage: "login", reason: "private-response", httpStatus: 403}))
	require.Equal(t, "balance_failed", safeUpstreamSiteBalanceFailure(&upstreamSiteBalanceProbeError{stage: "balance", httpStatus: 999}))
	require.Equal(t, "probe_failed", safeUpstreamSiteBalanceFailure(nil))
}

func TestProbeUpstreamSiteBalanceFailureKeepsSafeStageAndHTTPStatus(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-standard", "base_url": "https://site.example/v1"},
	}
	for _, tt := range []struct {
		name     string
		upstream *webAccountRateHTTPStub
		want     string
	}{
		{name: "登录失败", upstream: &webAccountRateHTTPStub{loginStatus: http.StatusBadRequest}, want: "login_http_error_http_400"},
		{name: "余额接口拒绝", upstream: &webAccountRateHTTPStub{balanceStatus: http.StatusForbidden}, want: "balance_web_forbidden_http_403"},
		{name: "余额格式异常", upstream: &webAccountRateHTTPStub{balanceResponse: `{"code":0,"data":{}}`}, want: "balance_invalid_response_http_200"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, tt.upstream, &upstreamBillingProbeSettingRepo{})
			attachUpstreamSiteCredential(svc, "site.example")
			repo := &upstreamSiteBalanceReminderRepoSpy{}
			svc.SetUpstreamSiteBalanceReminderDependencies(repo, nil)
			err := svc.probeUpstreamSiteBalance(context.Background(), UpstreamSiteBalanceObservation{Host: "site.example", BaseURL: "https://site.example"}, account)
			require.Error(t, err)
			require.Equal(t, tt.want, safeUpstreamSiteBalanceFailure(err))
			require.Empty(t, repo.saved)
		})
	}
	require.Equal(t, "probe_failed", safeUpstreamSiteBalanceFailure(errors.New("upstream returned private credentials")))
}

func TestEvaluateUpstreamBalanceIncrease(t *testing.T) {
	tests := []struct {
		name     string
		previous *float64
		current  float64
		want     float64
	}{
		{name: "first observation is baseline", current: 20},
		{name: "increase creates reminder", previous: float64Ptr(10), current: 15, want: 5},
		{name: "unchanged balance does not create reminder", previous: float64Ptr(10), current: 10},
		{name: "decrease does not create reminder", previous: float64Ptr(10), current: 8},
		{name: "floating point noise does not create reminder", previous: float64Ptr(10), current: 10.0000000001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			increase, delta := EvaluateUpstreamBalanceIncrease(tt.previous, tt.current)
			require.Equal(t, tt.want > 0, increase)
			if increase {
				require.InDelta(t, tt.want, delta, 1e-9)
			} else {
				require.Zero(t, delta)
			}
		})
	}
}

func TestIsUpstreamSiteLoginExpired(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{name: "unauthorized status", statusCode: http.StatusUnauthorized, body: `{}`, want: true},
		{name: "explicit unauthenticated response", statusCode: http.StatusForbidden, body: `{"message":"未登录"}`, want: true},
		{name: "ordinary forbidden response", statusCode: http.StatusForbidden, body: `{"message":"无权限"}`},
		{name: "ordinary server response", statusCode: http.StatusInternalServerError, body: `{"message":"upstream error"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamSiteLoginExpired(tt.statusCode, []byte(tt.body)))
		})
	}
}
