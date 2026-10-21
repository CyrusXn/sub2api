//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type interactionHandlerRepo struct {
	mu        sync.Mutex
	records   map[string]*service.GeminiInteraction
	createErr error
	updateErr error
}

func (r *interactionHandlerRepo) Create(ctx context.Context, v *service.GeminiInteraction) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.createErr != nil {
		return r.createErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	v.ID = int64(len(r.records) + 1)
	v.CreatedAt = time.Now()
	cp := *v
	r.records[v.PublicID] = &cp
	return nil
}
func (r *interactionHandlerRepo) GetOwned(_ context.Context, k, u, g int64, id string) (*service.GeminiInteraction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.records[id]
	if v == nil || v.APIKeyID != k || v.UserID != u || v.GroupID != g || v.Deleted {
		return nil, service.ErrGeminiInteractionNotFound
	}
	cp := *v
	return &cp, nil
}
func (r *interactionHandlerRepo) Update(ctx context.Context, v *service.GeminiInteraction) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.updateErr != nil {
		return r.updateErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.records[v.PublicID]
	cp := *v
	if old != nil {
		cp.Settled = old.Settled
		cp.Deleted = old.Deleted
	}
	r.records[v.PublicID] = &cp
	return nil
}
func (r *interactionHandlerRepo) MarkSettled(ctx context.Context, id int64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.records {
		if v.ID == id {
			v.Settled = true
		}
	}
	return nil
}
func (r *interactionHandlerRepo) SoftDelete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.records {
		if v.ID == id {
			v.Deleted = true
		}
	}
	return nil
}
func (r *interactionHandlerRepo) Pending(context.Context, int) ([]*service.GeminiInteraction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*service.GeminiInteraction
	for _, v := range r.records {
		if !v.Settled && !v.Deleted {
			cp := *v
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *interactionHandlerRepo) HasUsageBilling(context.Context, string, int64) (bool, error) {
	return false, nil
}

type interactionKeyRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *interactionKeyRepo) GetByID(context.Context, int64) (*service.APIKey, error) {
	return r.key, nil
}

type interactionUpstream struct {
	service.HTTPUpstream
	calls        []string
	bodies       []string
	response     string
	getResponse  string
	status       int
	methodStatus map[string]int
	stream       bool
	beforeReturn func(*http.Request)
	queries      []url.Values
	revisions    []string
	transportErr error
}

func (u *interactionUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, req.Method+" "+req.URL.Path)
	u.queries = append(u.queries, req.URL.Query())
	u.revisions = append(u.revisions, req.Header.Get("Api-Revision"))
	if u.transportErr != nil {
		return nil, u.transportErr
	}
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.bodies = append(u.bodies, string(b))
	}
	body := u.response
	if req.Method == http.MethodGet && u.getResponse != "" {
		body = u.getResponse
	}
	status := u.status
	if value, ok := u.methodStatus[req.Method]; ok {
		status = value
	}
	if status == 0 {
		status = 200
	}
	ct := "application/json"
	if u.stream {
		ct = "text/event-stream"
	}
	if u.beforeReturn != nil {
		u.beforeReturn(req)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}
func newInteractionEnv(t *testing.T) (*cachedBillingEnv, *interactionHandlerRepo, *interactionUpstream) {
	e := newCachedBillingEnv(t, 500, 900)
	repo := &interactionHandlerRepo{records: map[string]*service.GeminiInteraction{}}
	u := &interactionUpstream{response: `{"id":"up1","model":"gemini-2.5-pro","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"你好"}]}],"usage":{"total_input_tokens":10,"total_output_tokens":3,"total_thought_tokens":2}}`}
	keys := service.NewAPIKeyService(&interactionKeyRepo{key: e.apiKey}, nil, nil, nil, nil, nil, nil)
	compat := service.NewGeminiMessagesCompatService(&cachedBillingAccountRepo{accounts: e.accounts}, nil, nil, nil, nil, nil, u, nil, &config.Config{})
	e.h.geminiInteractionService = service.NewGeminiInteractionService(repo, compat, e.h.gatewayService, keys, nil)
	e.h.apiKeyService = keys
	return e, repo, u
}

func TestGeminiInteractionsCreateImageGenerationGate(t *testing.T) {
	for _, scenario := range []string{"image model", "account alias", "text model", "image enabled"} {
		t.Run(scenario, func(t *testing.T) {
			e, repo, upstream := newInteractionEnv(t)
			e.apiKey.Group.AllowImageGeneration = scenario == "image enabled"
			model := "gemini-3-pro-image"
			if scenario == "text model" {
				model = "gemini-2.5-pro"
			}
			if scenario == "account alias" {
				model = "draw-alias"
				for _, account := range e.accounts {
					account.Credentials["model_mapping"] = map[string]any{model: "gemini-3-pro-image"}
				}
			}
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"`+model+`","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
			if scenario == "text model" || scenario == "image enabled" {
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Len(t, upstream.calls, 1)
			} else {
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				require.Empty(t, upstream.calls)
				require.Empty(t, repo.records, "门禁拒绝不得持久化或预扣费用")
			}
		})
	}
}

type interactionUserGroupRateRepo struct {
	service.UserGroupRateRepository
	userID, groupID int64
	rate            float64
	reads           [][2]int64
}

func (r *interactionUserGroupRateRepo) GetByUserAndGroup(_ context.Context, userID, groupID int64) (*float64, error) {
	r.reads = append(r.reads, [2]int64{userID, groupID})
	if userID != r.userID || groupID != r.groupID {
		return nil, nil
	}
	rate := r.rate
	return &rate, nil
}

func TestGeminiInteractionsCreateProfitGateUsesAuthenticatedUserRate(t *testing.T) {
	for _, tc := range []struct {
		name        string
		userRate    float64
		accountRate float64
		wantStatus  int
		wantCalls   int
	}{
		{name: "discount rejects loss-making account", userRate: 0.1, accountRate: 0.5, wantStatus: http.StatusServiceUnavailable, wantCalls: 0},
		{name: "markup admits profitable account", userRate: 2.0, accountRate: 1.5, wantStatus: http.StatusOK, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, records, upstream := newInteractionEnv(t)
			e.group.ProfitControlEnabled = true
			e.group.ProfitMinMargin = 0
			e.group.ProfitSafetyBuffer = 0
			e.group.RateMultiplier = 1
			e.accounts[900].RateMultiplier = &tc.accountRate
			rates := &interactionUserGroupRateRepo{userID: e.apiKey.UserID, groupID: e.group.ID, rate: tc.userRate}
			accountRepo := &cachedBillingAccountRepo{accounts: e.accounts}
			cfg := &config.Config{}
			snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{e.accounts[900]}}, nil, nil, nil, nil)
			gateway := service.NewGatewayService(accountRepo, &fakeGroupRepo{group: e.group}, e.usage, nil,
				&cachedBillingUserRepo{balance: 500}, nil, rates, nil, cfg, snapshot, nil,
				nil, nil, e.h.billingCacheService, nil, nil, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			compat := service.NewGeminiMessagesCompatService(accountRepo, nil, nil, nil, nil, nil, upstream, nil, cfg)
			e.h.gatewayService = gateway
			e.h.geminiInteractionService = service.NewGeminiInteractionService(records, compat, gateway, e.h.apiKeyService, cfg)
			// 保持任务未结算，让断言只验证真实选号与槽位终检，不依赖额外计费桩。
			upstream.response = `{"id":"up1","status":"in_progress"}`
			if tc.wantCalls > 0 {
				upstream.beforeReturn = func(req *http.Request) {
					require.Equal(t, e.apiKey.UserID, req.Context().Value(ctxkey.UserID), "上游请求必须保留已鉴权用户上下文")
				}
			}
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			require.Len(t, upstream.calls, tc.wantCalls)
			require.Equal(t, [][2]int64{{e.apiKey.UserID, e.group.ID}}, rates.reads, "利润门必须读取同一用户与分组的真实专属倍率")
			require.Len(t, records.records, tc.wantCalls)
			require.Empty(t, e.usage.totals())
		})
	}
}

func TestGeminiInteractionsCreatePropagatesAuthenticatedUserID(t *testing.T) {
	e, _, upstream := newInteractionEnv(t)
	upstream.beforeReturn = func(req *http.Request) {
		require.Equal(t, e.apiKey.UserID, req.Context().Value(ctxkey.UserID))
	}
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, upstream.calls, 1)
}

func TestGeminiInteractionsInvalidCreateNeverCallsUpstream(t *testing.T) {
	for _, body := range []string{`{`, `null`, `[]`, `{}`, `{"agent":"deep-research","input":"test"}`, `{"model":12,"input":"test"}`, `{"model":"../bad","input":"test"}`, `{"model":"gemini-2.5-pro","input":"test","stream":"true"}`, `{"model":"gemini-2.5-pro","input":"test","previous_interaction_id":10}`, `{"model":"gemini-2.5-pro","input":"test","store":false}`, `{"model":"gemini-2.5-pro","model":"gemini-other","input":"test"}`, `{"model":"gemini-2.5-pro","input":"safe","input":"other"}`} {
		t.Run(body, func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			rec := e.serve(http.MethodPost, "/v1beta/interactions", body, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, 400, rec.Code, rec.Body.String())
			require.Empty(t, u.calls)
			require.Empty(t, repo.records)
		})
	}
}

func TestGeminiInteractionsRejectInsufficientBalanceAndAllowlist(t *testing.T) {
	t.Run("balance", func(t *testing.T) {
		e, _, u := newInteractionEnv(t)
		e.h.billingCacheService = service.NewBillingCacheService(nil, &cachedBillingUserRepo{balance: 0}, nil, nil, nil, nil, &config.Config{}, nil)
		t.Cleanup(e.h.billingCacheService.Stop)
		e.apiKey.User.Balance = 0
		rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
		require.Equal(t, 403, rec.Code, rec.Body.String())
		require.Empty(t, u.calls)
	})
	t.Run("allowlist", func(t *testing.T) {
		e, _, u := newInteractionEnv(t)
		e.group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-other"}}
		rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
		require.Equal(t, 403, rec.Code, rec.Body.String())
		require.Empty(t, u.calls)
	})
}

func TestGeminiInteractionsUpstreamErrorsAreSanitizedAndNotRetried(t *testing.T) {
	for _, status := range []int{400, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			u.status = status
			u.response = `{"error":"SECRET_UPSTREAM_KEY"}`
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, status, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "SECRET")
			require.Len(t, u.calls, 1)
			require.Len(t, repo.records, 1)
			require.Empty(t, e.usage.totals())
		})
	}
}

func TestGeminiInteractionsMalformedSuccessRemainsPending(t *testing.T) {
	for _, body := range []string{`{"status":"completed"}`, `{"id":"up1","status":"completed","usage":{"total_input_tokens":-1,"total_output_tokens":2}}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			u.response = body
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, 502, rec.Code, rec.Body.String())
			expectedCalls := 1
			if gjson.Get(body, "id").String() != "" {
				expectedCalls = 2
			}
			require.Len(t, u.calls, expectedCalls)
			require.Empty(t, e.usage.totals())
			for _, r := range repo.records {
				require.False(t, r.Settled)
			}
		})
	}
}

func TestGeminiInteractionsContinueBindsOriginalAccountAndMapsModel(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	e.accounts[900].Credentials["model_mapping"] = map[string]any{"gemini-2.5-pro": "gemini-upstream"}
	repo.records["previous"] = &service.GeminiInteraction{ID: 1, PublicID: "previous", UpstreamID: "previous_upstream", APIKeyID: e.apiKey.ID, UserID: e.apiKey.UserID, GroupID: e.group.ID, AccountID: 900, Model: "gemini-2.5-pro"}
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":[{"type":"text","text":"继续"}],"previous_interaction_id":"previous","tools":[{"type":"google_search"}]}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Len(t, u.calls, 1)
	require.Equal(t, "previous_upstream", gjson.Get(u.bodies[0], "previous_interaction_id").String())
	require.Equal(t, "gemini-upstream", gjson.Get(u.bodies[0], "model").String())
	require.Equal(t, "google_search", gjson.Get(u.bodies[0], "tools.0.type").String())
	require.Equal(t, "gemini-2.5-pro", gjson.Get(rec.Body.String(), "model").String())
}

func TestGeminiInteractionsCancelAndDeleteRequireOwnerAndSettlement(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	create := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, create.Code, create.Body.String())
	id := gjson.Get(create.Body.String(), "id").String()
	params := gin.Params{{Key: "interactionID", Value: id}}
	repo.records[id].Status = "in_progress"
	repo.records[id].Settled = false
	repo.records[id].Usage = nil
	u.getResponse = `{"id":"up1","status":"in_progress"}`
	rec := e.serve(http.MethodDelete, "/v1beta/interactions/"+id, "", params, e.h.GeminiInteractionsDelete)
	require.Equal(t, 409, rec.Code, rec.Body.String())
	require.Len(t, u.calls, 2)
	u.response = `{"id":"up1","status":"cancelled","usage":{"total_input_tokens":10,"total_output_tokens":3}}`
	rec = e.serve(http.MethodPost, "/v1beta/interactions/"+id+"/cancel", "", params, e.h.GeminiInteractionsCancel)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), `"id":"up1"`)
	require.True(t, repo.records[id].Settled)
	u.methodStatus = map[string]int{http.MethodDelete: 204}
	u.getResponse = u.response
	u.response = ""
	rec = e.serve(http.MethodDelete, "/v1beta/interactions/"+id, "", params, e.h.GeminiInteractionsDelete)
	require.Equal(t, 204, rec.Code, rec.Body.String())
	require.True(t, repo.records[id].Deleted)
	require.Equal(t, []string{"POST /v1beta/interactions", "GET /v1beta/interactions/up1", "POST /v1beta/interactions/up1/cancel", "GET /v1beta/interactions/up1", "DELETE /v1beta/interactions/up1"}, u.calls)
}

func TestGeminiInteractionsDeleteRefreshesAndSettlesBeforeDeleting(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	repo.records["pending"] = &service.GeminiInteraction{ID: 1, PublicID: "pending", UpstreamID: "up1", APIKeyID: e.apiKey.ID, UserID: e.apiKey.UserID, GroupID: e.group.ID, AccountID: 900, Model: "gemini-2.5-pro", BillingGroup: e.group, Status: "in_progress", PricingAt: time.Now(), Revision: "2026-05-20"}
	u.methodStatus = map[string]int{http.MethodDelete: 204}
	rec := e.serve(http.MethodDelete, "/v1beta/interactions/pending", "", gin.Params{{Key: "interactionID", Value: "pending"}}, e.h.GeminiInteractionsDelete)
	require.Equal(t, 204, rec.Code, rec.Body.String())
	require.Len(t, e.usage.totals(), 1)
	require.True(t, repo.records["pending"].Deleted)
	require.Equal(t, []string{"GET /v1beta/interactions/up1", "DELETE /v1beta/interactions/up1"}, u.calls)
}

func TestGeminiInteractionsDeleteWithoutUsageRemainsVisibleForReview(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	repo.records["pending"] = &service.GeminiInteraction{ID: 1, PublicID: "pending", UpstreamID: "up1", APIKeyID: e.apiKey.ID, UserID: e.apiKey.UserID, GroupID: e.group.ID, AccountID: 900, Model: "gemini-2.5-pro", BillingGroup: e.group, Status: "in_progress", BalanceHold: 1.5}
	u.response = `{"id":"up1","status":"cancelled"}`
	rec := e.serve(http.MethodDelete, "/v1beta/interactions/pending", "", gin.Params{{Key: "interactionID", Value: "pending"}}, e.h.GeminiInteractionsDelete)
	require.Equal(t, 409, rec.Code)
	require.Equal(t, "terminal_usage_missing", repo.records["pending"].ReviewReason)
	require.False(t, repo.records["pending"].Settled)
	require.Equal(t, 1.5, repo.records["pending"].BalanceHold)
}

func TestGeminiInteractionsDisconnectDoesNotLoseUsage(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	var clientCancel context.CancelFunc
	u.beforeReturn = func(req *http.Request) { clientCancel(); require.NoError(t, req.Context().Err()) }
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		clientCancel = cancel
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		e.h.GeminiInteractionsCreate(c)
	})
	require.Empty(t, rec.Body.String())
	require.Len(t, e.usage.totals(), 1)
	require.Len(t, repo.records, 1)
	for _, r := range repo.records {
		require.Equal(t, "up1", r.UpstreamID)
		require.True(t, r.Settled)
	}
}

func TestGeminiInteractionsResponseLimitAndQueryValidation(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		e, _, u := newInteractionEnv(t)
		u.response = `{"id":"up1","padding":"` + strings.Repeat("x", geminiInteractionMaxResponse) + `"}`
		rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
		require.Equal(t, 502, rec.Code, rec.Body.String())
		require.Len(t, u.calls, 1)
		require.Empty(t, e.usage.totals())
	})
	t.Run("query", func(t *testing.T) {
		e, repo, u := newInteractionEnv(t)
		rec := e.serve(http.MethodPost, "/v1beta/interactions?key=secret", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
		require.Equal(t, 400, rec.Code, rec.Body.String())
		require.Empty(t, repo.records)
		require.Empty(t, u.calls)
	})
}

func TestGeminiInteractionsPersistBeforePostAndBeforeReturningID(t *testing.T) {
	for _, tc := range []struct {
		name      string
		createErr error
		updateErr error
		want      int
		calls     int
	}{{"save", errors.New("db unavailable"), nil, 500, 0}, {"balance hold", service.ErrInsufficientBalance, nil, 403, 0}, {"pending concurrency", service.ErrGeminiInteractionConcurrency, nil, 429, 0}, {"dependency busy", service.ErrGeminiInteractionBusy, nil, 503, 0}, {"observe", nil, errors.New("db unavailable"), 502, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			repo.createErr = tc.createErr
			repo.updateErr = tc.updateErr
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test"}`, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			require.Len(t, u.calls, tc.calls)
			require.NotContains(t, rec.Body.String(), "up1")
			require.Empty(t, e.usage.totals())
		})
	}
}

func TestGeminiInteractionsBackgroundDurableBalanceHold(t *testing.T) {
	for _, simple := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "simple"}[simple], func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			if simple {
				e.h.cfg = &config.Config{RunMode: config.RunModeSimple}
			}
			u.response = `{"id":"up1","status":"in_progress"}`
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test","background":true}`, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			for _, r := range repo.records {
				if simple {
					require.Zero(t, r.BalanceHold)
				} else {
					require.Greater(t, r.BalanceHold, 0.0)
				}
			}
		})
	}
}

func TestGeminiInteractionsPreflightReleasesPendingButNetworkFailureDoesNot(t *testing.T) {
	for _, scenario := range []string{"empty key", "invalid base", "unknown network"} {
		t.Run(scenario, func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			switch scenario {
			case "empty key":
				e.accounts[900].Credentials["api_key"] = ""
			case "invalid base":
				e.accounts[900].Credentials["base_url"] = "https://example.com/path?unexpected=1"
			case "unknown network":
				u.transportErr = io.ErrUnexpectedEOF
			}
			rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test","background":true}`, nil, e.h.GeminiInteractionsCreate)
			require.Equal(t, 502, rec.Code, rec.Body.String())
			require.Len(t, repo.records, 1)
			for _, r := range repo.records {
				require.Greater(t, r.BalanceHold, 0.0)
				require.Equal(t, scenario != "unknown network", r.Settled)
			}
			if scenario == "unknown network" {
				require.Len(t, u.calls, 1)
			} else {
				require.Empty(t, u.calls)
			}
			require.Empty(t, e.usage.totals())
		})
	}
}

func TestGeminiInteractionsAllResourceActionsRequireOwnerAndOriginalModel(t *testing.T) {
	for _, action := range []string{"get", "cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			e, repo, u := newInteractionEnv(t)
			repo.records["foreign"] = &service.GeminiInteraction{ID: 1, PublicID: "foreign", UpstreamID: "up1", APIKeyID: e.apiKey.ID + 1, UserID: e.apiKey.UserID, GroupID: e.group.ID, AccountID: 900, Model: "gemini-2.5-pro"}
			method, call := http.MethodGet, e.h.GeminiInteractionsGet
			if action == "cancel" {
				method, call = http.MethodPost, e.h.GeminiInteractionsCancel
			}
			if action == "delete" {
				method, call = http.MethodDelete, e.h.GeminiInteractionsDelete
			}
			params := gin.Params{{Key: "interactionID", Value: "foreign"}}
			rec := e.serve(method, "/v1beta/interactions/foreign", "", params, call)
			require.Equal(t, 404, rec.Code, rec.Body.String())
			require.Empty(t, u.calls)
			repo.records["foreign"].APIKeyID = e.apiKey.ID
			e.group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-upstream"}}
			rec = e.serve(method, "/v1beta/interactions/foreign", "", params, call)
			require.Equal(t, 403, rec.Code, rec.Body.String())
			require.Empty(t, u.calls)
		})
	}
}

func TestGeminiInteractionsTruncatedTerminalSSEStaysPending(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	u.stream = true
	u.response = "event: interaction.start\ndata: {\"interaction\":{\"id\":\"up1\",\"status\":\"in_progress\"}}\n\nevent: interaction.completed\ndata: {\"interaction\":{\"id\":\"up1\",\"status\":\"completed\",\"usage\":{\"total_input_tokens\":10,\"total_output_tokens\":3}}}"
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test","stream":true}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), `"status":"completed"`)
	require.Empty(t, e.usage.totals())
	for _, r := range repo.records {
		require.Equal(t, "up1", r.UpstreamID)
		require.False(t, r.Settled)
	}
}

func TestGeminiInteractionsGetNativeSSEForwardsResumeQuery(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	repo.records["public"] = &service.GeminiInteraction{ID: 1, PublicID: "public", UpstreamID: "up1", APIKeyID: e.apiKey.ID, UserID: e.apiKey.UserID, GroupID: e.group.ID, AccountID: 900, Model: "gemini-2.5-pro", BillingGroup: e.group, Status: "in_progress", PricingAt: time.Now(), Revision: "2026-05-20"}
	u.stream = true
	u.response = "event: interaction.completed\nid: resume-1\ndata: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"up1\",\"status\":\"completed\",\"custom_field\":{\"keep\":true},\"usage\":{\"total_input_tokens\":10,\"total_output_tokens\":3}}}\n\n"
	rec := e.serve(http.MethodGet, "/v1beta/interactions/public?stream=true&include_input=false&last_event_id=resume-0", "", gin.Params{{Key: "interactionID", Value: "public"}}, e.h.GeminiInteractionsGet)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"id":"public"`)
	require.Contains(t, rec.Body.String(), `"custom_field":{"keep":true}`)
	require.Contains(t, rec.Body.String(), "id: resume-1")
	require.Equal(t, "resume-0", u.queries[0].Get("last_event_id"))
	require.Equal(t, "false", u.queries[0].Get("include_input"))
	require.Equal(t, "2026-05-20", u.revisions[0])
	require.Len(t, e.usage.totals(), 1)
}

func TestGeminiInteractionsAuditViewExtractsNativeInput(t *testing.T) {
	request := gjson.Parse(`{"model":"gemini-2.5-pro","system_instruction":"系统规则","input":[{"role":"user","content":[{"type":"text","text":"检查这张图片"},{"type":"image","mime_type":"image/png","data":"aGVsbG8="}]}]}`)
	body := geminiInteractionAuditBody(request)
	input := service.ExtractContentModerationInput(service.ContentModerationProtocolGemini, body)
	require.Contains(t, input.Text, "检查这张图片")
	require.Contains(t, string(body), "系统规则")
	require.Contains(t, string(body), "inlineData")
	require.NotContains(t, request.Raw, "contents")
	conversation := gjson.Parse(`{"input":[{"role":"user","content":[{"type":"text","text":"历史问题"}]},{"role":"model","content":[{"type":"text","text":"历史回答"}]},{"role":"user","content":[{"type":"text","text":"当前问题"}]}]}`)
	latest := service.ExtractContentModerationInput(service.ContentModerationProtocolGemini, geminiInteractionAuditBody(conversation))
	require.Equal(t, "当前问题", latest.Text)
}

func TestGeminiInteractionsMultilineSSEAndTruncatedUsage(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	u.stream = true
	u.response = "event: interaction.start\ndata: {\ndata: \"event_type\":\"interaction.start\",\"interaction\":{\"id\":\"up1\",\"status\":\"in_progress\"}}\n\nevent: content.delta\ndata: {\"event_type\":\"content.delta\",\"delta\":{\"type\":\"text\",\"text\":\"你好\"}}\n\n"
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test","stream":true}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "你好")
	require.NotContains(t, rec.Body.String(), `"id":"up1"`)
	require.Empty(t, e.usage.totals())
	for _, r := range repo.records {
		require.Equal(t, "up1", r.UpstreamID)
		require.False(t, r.Settled)
	}
}
func TestGeminiInteractionsCreateAndPollingChargesOnce(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"你是谁","generation_config":{"max_output_tokens":32}}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	id := gjson.Get(rec.Body.String(), "id").String()
	require.NotEqual(t, "up1", id)
	require.Contains(t, rec.Body.String(), "你好")
	require.Equal(t, []string{"POST /v1beta/interactions"}, u.calls)
	require.Equal(t, "你是谁", gjson.Get(u.bodies[0], "input").String())
	require.Len(t, e.usage.totals(), 1)
	require.True(t, repo.records[id].Settled)
	for i := 0; i < 2; i++ {
		get := e.serve(http.MethodGet, "/v1beta/interactions/"+id, "", gin.Params{{Key: "interactionID", Value: id}}, e.h.GeminiInteractionsGet)
		require.Equal(t, 200, get.Code, get.Body.String())
	}
	require.Len(t, e.usage.totals(), 1)
}
func TestGeminiInteractionsBackgroundRecoversWithoutClientPolling(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	u.getResponse = u.response
	u.response = `{"id":"up1","model":"gemini-2.5-pro","status":"in_progress"}`
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"你是谁","background":true}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	id := gjson.Get(rec.Body.String(), "id").String()
	require.Empty(t, e.usage.totals())
	n, err := e.h.geminiInteractionService.RecoverPending(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, e.usage.totals(), 1)
	require.True(t, repo.records[id].Settled)
	n, err = e.h.geminiInteractionService.RecoverPending(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Len(t, e.usage.totals(), 1)
}
func TestGeminiInteractionsForeignKeyCannotGetOrContinue(t *testing.T) {
	e, repo, u := newInteractionEnv(t)
	repo.records["foreign"] = &service.GeminiInteraction{ID: 1, PublicID: "foreign", APIKeyID: 999, UserID: e.apiKey.UserID, GroupID: e.group.ID, AccountID: 900, UpstreamID: "up1"}
	rec := e.serve(http.MethodGet, "/v1beta/interactions/foreign", "", gin.Params{{Key: "interactionID", Value: "foreign"}}, e.h.GeminiInteractionsGet)
	require.Equal(t, 404, rec.Code)
	rec = e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test","previous_interaction_id":"foreign"}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 404, rec.Code)
	require.Empty(t, u.calls)
}
func TestGeminiInteractionsNativeSSE(t *testing.T) {
	e, _, u := newInteractionEnv(t)
	u.stream = true
	u.response = "event: interaction.start\ndata: {\"event_type\":\"interaction.start\",\"interaction\":{\"id\":\"up1\",\"status\":\"in_progress\"}}\n\nevent: content.delta\ndata: {\"event_type\":\"content.delta\",\"delta\":{\"type\":\"text\",\"text\":\"你好\"}}\n\nevent: interaction.completed\ndata: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"up1\",\"status\":\"completed\",\"usage\":{\"total_input_tokens\":10,\"total_output_tokens\":3}}}\n\n"
	rec := e.serve(http.MethodPost, "/v1beta/interactions", `{"model":"gemini-2.5-pro","input":"test","stream":true}`, nil, e.h.GeminiInteractionsCreate)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, rec.Body.String(), "你好")
	require.NotContains(t, rec.Body.String(), `"id":"up1"`)
	require.Len(t, e.usage.totals(), 1)
}
