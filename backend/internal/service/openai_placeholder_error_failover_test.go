//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChatPlaceholderThenGenericUpstreamErrorAllowsFailover(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	start := time.Now()
	ensureTextStreamTiming(c, start)
	writeTextStreamPlaceholder(c, c.Writer)
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
		": keepalive\n\nevent: error\ndata: {\"error\":{\"type\":\"upstream_error\",\"message\":\"Upstream request failed\"}}\n\n"))}
	_, err := svc.handleChatStreamingResponse(resp, c, rawChatCompletionsTestAccount(), "gpt-5.6-luna", "gpt-5.6-luna", "gpt-5.6-luna", start, 32)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, TextStreamAwaitingOutput(c))
	require.NotContains(t, rec.Body.String(), "Upstream request failed")
}

type consecutiveFailureRepo struct {
	AccountRepository
	errors        []string
	writeDelay    time.Duration
	modelCooldown struct {
		accountID int64
		model     string
		until     time.Time
		reason    string
		calls     int
	}
}

func (r *consecutiveFailureRepo) SetModelRateLimit(_ context.Context, accountID int64, model string, until time.Time, reason ...string) error {
	r.modelCooldown.accountID = accountID
	r.modelCooldown.model = model
	r.modelCooldown.until = until
	r.modelCooldown.reason = firstNonEmpty(reason...)
	r.modelCooldown.calls++
	if r.writeDelay > 0 {
		time.Sleep(r.writeDelay)
	}
	return nil
}
func (r *consecutiveFailureRepo) SetError(_ context.Context, _ int64, message string) error {
	r.errors = append(r.errors, message)
	return nil
}

func TestTransientServerFailuresNeverDisableAccount(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			repo := &consecutiveFailureRepo{}
			svc := &OpenAIGatewayService{accountRepo: repo}
			account := &Account{ID: 95109, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
			for attempt := 1; attempt <= 10; attempt++ {
				started := time.Now()
				disabled := svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`), "gpt-5.6-luna")
				require.False(t, disabled, "第 %d 次瞬时故障不能停用账号", attempt)
				require.Empty(t, repo.errors)
				require.Equal(t, StatusActive, account.Status)
				require.True(t, account.Schedulable)
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
				require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.5"), "不能影响其他模型")
				require.Equal(t, attempt, repo.modelCooldown.calls)
				require.Equal(t, account.ID, repo.modelCooldown.accountID)
				require.Equal(t, "gpt-5.6-luna", repo.modelCooldown.model)
				require.Equal(t, "openai_model_transient", repo.modelCooldown.reason)
				cooldown := time.Duration(min(attempt, 5)) * time.Second
				require.False(t, repo.modelCooldown.until.Before(started.Add(cooldown)))
				require.False(t, repo.modelCooldown.until.After(time.Now().Add(cooldown)))
			}
		})
	}
}

func TestStreamGenericServerErrorDoesNotOverrideRequestFailures(t *testing.T) {
	for _, payload := range []string{
		`{"error":{"type":"invalid_request_error","message":"bad tools parameter"}}`,
		`{"error":{"type":"server_error","message":"content_policy violation"}}`,
		`{"error":{"message":"upstream rejected request"}}`,
	} {
		require.False(t, openAIStreamErrorEventShouldFailover([]byte(payload), extractUpstreamErrorMessage([]byte(payload))), payload)
	}
}

func TestTransientServerFailureSuccessResetsStreak(t *testing.T) {
	repo := &consecutiveFailureRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 95110, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	for range 2 {
		svc.coolOpenAIAccountModelAfterFailure(context.Background(), account, "gpt-test")
	}
	svc.clearOpenAIAccountModelTransientState(account.ID, "gpt-test")
	svc.coolOpenAIAccountModelAfterFailure(context.Background(), account, "gpt-test")
	require.Empty(t, repo.errors)
	for range 4 {
		svc.coolOpenAIAccountModelAfterFailure(context.Background(), account, "gpt-test", false)
	}
	require.Empty(t, repo.errors)
}

func TestTransientServerFailureSlowStateWriteIsNotCountedAgainOnReport(t *testing.T) {
	repo := &consecutiveFailureRepo{writeDelay: 1100 * time.Millisecond}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 95111, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}
	body := []byte(`{"error":{"type":"upstream_error","message":"temporary failure"}}`)
	err := svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, account, resp, body, "temporary failure", "gpt-test")
	require.NotNil(t, err)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"), "写入耗时已超过首档冷却")
	repo.writeDelay = 0
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-test", false, nil, err)
	require.Equal(t, 1, repo.modelCooldown.calls, "同一故障只能记账一次，不依赖冷却仍有效")
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"))
}

func TestTransientServerFailureStreamStillCoolsOnReport(t *testing.T) {
	repo := &consecutiveFailureRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 95112, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"server_error","status_code":502,"message":"temporary failure"}}}`)
	err := svc.newOpenAIStreamFailoverErrorWithModel(nil, account, false, "", payload, "temporary failure", "gpt-test")
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-test", false, nil, err)
	require.Equal(t, 1, repo.modelCooldown.calls, "流内普通 5xx 仍须执行模型冷却")
	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"))
}

func TestTransientServerFailureReportHandlesUnknownModelAndCountsOnce(t *testing.T) {
	repo := &consecutiveFailureRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 95113, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	err := svc.newOpenAIAccountFailoverError(account, 502, nil, nil, "empty upstream response", false, false)
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-test", false, nil, err)
	require.Equal(t, 1, repo.modelCooldown.calls, "入口没有模型时仍由回报兜底")
	svc.clearOpenAIAccountModelTransientState(account.ID, "gpt-test")
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-test", false, nil, err)
	require.Equal(t, 1, repo.modelCooldown.calls, "冷却结束后重复回报同一错误也不能重新累计")
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-test"))
}
