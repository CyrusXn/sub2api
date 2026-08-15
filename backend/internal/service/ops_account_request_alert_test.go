package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountRequestAlertRepoMock struct {
	*opsRepoMock
	rule       *OpsAlertRule
	latest     map[string]*OpsAlertEvent
	created    []*OpsAlertEvent
	details    []*OpsAlertAccountDetail
	errorLogs  map[int64]*OpsErrorLogDetail
	deliveries []*OpsAlertEmailDeliveryInput
}

func (m *accountRequestAlertRepoMock) InsertAlertEmailDelivery(_ context.Context, input *OpsAlertEmailDeliveryInput) error {
	clone := *input
	m.deliveries = append(m.deliveries, &clone)
	return nil
}

func (m *accountRequestAlertRepoMock) ListAlertEmailDeliveries(context.Context, *OpsAlertEmailDeliveryFilter) (*OpsAlertEmailDeliveryList, error) {
	return &OpsAlertEmailDeliveryList{}, nil
}

func (m *accountRequestAlertRepoMock) ListPendingQuietAlertEmails(context.Context, time.Time) ([]*OpsAlertEmailDelivery, error) {
	return nil, nil
}

func (m *accountRequestAlertRepoMock) MarkQuietAlertEmailsDigested(context.Context, []int64, time.Time) error {
	return nil
}

func newAccountRequestAlertRepoMock() *accountRequestAlertRepoMock {
	return &accountRequestAlertRepoMock{
		opsRepoMock: &opsRepoMock{},
		rule: &OpsAlertRule{
			ID:              18,
			Name:            "账号请求异常",
			Enabled:         true,
			Severity:        "P1",
			MetricType:      OpsAlertMetricAccountRequestFailure,
			CooldownMinutes: 10,
		},
		latest:    map[string]*OpsAlertEvent{},
		errorLogs: map[int64]*OpsErrorLogDetail{},
	}
}

func (m *accountRequestAlertRepoMock) ListAlertRules(context.Context) ([]*OpsAlertRule, error) {
	return []*OpsAlertRule{m.rule}, nil
}

func (m *accountRequestAlertRepoMock) GetLatestAlertEventByDedupeKey(_ context.Context, _ int64, dedupeKey string) (*OpsAlertEvent, error) {
	return m.latest[dedupeKey], nil
}

func (m *accountRequestAlertRepoMock) CreateAlertEvent(_ context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error) {
	cloned := *event
	cloned.ID = int64(len(m.created) + 1)
	m.created = append(m.created, &cloned)
	m.latest[cloned.DedupeKey] = &cloned
	return &cloned, nil
}

func (m *accountRequestAlertRepoMock) InsertAlertAccountDetails(_ context.Context, details []*OpsAlertAccountDetail) error {
	m.details = append(m.details, details...)
	return nil
}

func (m *accountRequestAlertRepoMock) ListAlertAccountDetails(context.Context, int64) ([]*OpsAlertAccountDetail, error) {
	return m.details, nil
}

func (m *accountRequestAlertRepoMock) GetErrorLogByID(_ context.Context, id int64) (*OpsErrorLogDetail, error) {
	return m.errorLogs[id], nil
}

func TestBuildOpsAccountRequestReason(t *testing.T) {
	status503 := 503
	tests := []struct {
		name       string
		entry      *OpsInsertErrorLogInput
		wantReason string
		wantClass  string
	}{
		{
			name: "余额不足使用固定短原因",
			entry: &OpsInsertErrorLogInput{
				ErrorPhase: "upstream", ErrorOwner: "provider", StatusCode: 402,
				ErrorMessage: "insufficient balance for this request",
			},
			wantReason: "余额不足",
			wantClass:  "balance",
		},
		{
			name: "HTTP 状态码保留脱敏后的具体原因",
			entry: &OpsInsertErrorLogInput{
				ErrorPhase: "upstream", ErrorOwner: "provider", StatusCode: 500,
				UpstreamStatusCode: &status503,
				ErrorBody:          `{"error":{"message":"upstream temporarily unavailable"}}`,
			},
			wantReason: "503：upstream temporarily unavailable",
			wantClass:  "http_503",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signal := buildOpsAccountRequestAlertSignal(tt.entry)
			require.NotNil(t, signal)
			reason, class := buildOpsAccountRequestReason(signal)
			require.Equal(t, tt.wantReason, reason)
			require.Equal(t, tt.wantClass, class)
		})
	}
}

func TestEvaluateAccountRequestAlertsCreatesPerAccountAndDeduplicatesByReason(t *testing.T) {
	repo := newAccountRequestAlertRepoMock()
	now := time.Now().UTC()
	userID := int64(1001)
	apiKeyID := int64(2002)
	accountID := int64(3003)
	groupID := int64(4004)
	status503 := 503

	repo.errorLogs[9001] = &OpsErrorLogDetail{OpsErrorLog: OpsErrorLog{
		ID: 9001, CreatedAt: now, Phase: "upstream", Type: "api_error", Owner: "provider", Source: "upstream_http",
		StatusCode: 503, Platform: "openai", Model: "gpt-5.5", RequestedModel: "gpt-5.5", UpstreamModel: "gpt-5.5-2026-04-23",
		UserID: &userID, UserEmail: "user@example.com", APIKeyID: &apiKeyID, APIKeyName: "生产 Key",
		AccountID: &accountID, AccountName: "OpenAI 主账号", GroupID: &groupID, GroupName: "Plus 分组",
		RequestID: "req-internal", ClientRequestID: "req-client", Message: "upstream temporarily unavailable",
	}, UpstreamStatusCode: &status503, UpstreamErrorMessage: "upstream temporarily unavailable"}

	entry := &OpsInsertErrorLogInput{
		ErrorLogID: 9001, CreatedAt: now, ErrorPhase: "upstream", ErrorOwner: "provider", ErrorSource: "upstream_http",
		StatusCode: 500, UpstreamStatusCode: &status503, ErrorMessage: "upstream temporarily unavailable",
		Platform: "openai", Model: "gpt-5.5", RequestedModel: "gpt-5.5", UpstreamModel: "gpt-5.5-2026-04-23",
		UserID: &userID, APIKeyID: &apiKeyID, AccountID: &accountID, GroupID: &groupID,
		RequestID: "req-internal", ClientRequestID: "req-client",
		UpstreamErrors: []*OpsUpstreamErrorEvent{{
			AccountID: accountID, AccountName: "OpenAI 主账号", Platform: "openai",
			UpstreamStatusCode: 503, Message: "upstream temporarily unavailable",
		}},
	}

	opsService := &OpsService{opsRepo: repo}
	evaluator := NewOpsAlertEvaluatorService(opsService, repo, nil, nil, nil, nil)
	signals := buildOpsAccountRequestAlertSignals(entry)
	require.Len(t, signals, 1)

	evaluator.evaluateAccountRequestAlerts(signals)
	require.Len(t, repo.created, 1)
	require.Equal(t, "OpenAI 主账号异常", repo.created[0].Title)
	require.Equal(t, "503：upstream temporarily unavailable", repo.created[0].Description)
	require.NotEmpty(t, repo.created[0].DedupeKey)
	require.NotContains(t, repo.created[0].Title, "P0")
	require.NotContains(t, repo.created[0].Title, "P1")

	require.Len(t, repo.details, 1)
	detail := repo.details[0]
	require.Equal(t, int64(9001), detail.ErrorLogID)
	require.Equal(t, &userID, detail.UserID)
	require.Equal(t, "user@example.com", detail.UserEmail)
	require.Equal(t, &apiKeyID, detail.APIKeyID)
	require.Equal(t, "生产 Key", detail.APIKeyName)
	require.Equal(t, "req-internal", detail.RequestID)
	require.Equal(t, "req-client", detail.ClientRequestID)
	require.Equal(t, "Plus 分组", detail.GroupName)
	require.Equal(t, "openai", detail.Platform)
	require.Equal(t, "gpt-5.5", detail.RequestedModel)
	require.Equal(t, "gpt-5.5-2026-04-23", detail.UpstreamModel)
	require.Equal(t, "503：upstream temporarily unavailable", detail.ErrorReason)

	// 同一账号、同一原因在冷却窗口内不重复创建。
	evaluator.evaluateAccountRequestAlerts(signals)
	require.Len(t, repo.created, 1)

	// 同一账号的新原因必须创建独立事件。
	entry.ErrorLogID = 9002
	entry.ErrorMessage = "gateway timeout"
	entry.UpstreamErrors[0].Message = "gateway timeout"
	repo.errorLogs[9002] = &OpsErrorLogDetail{OpsErrorLog: OpsErrorLog{
		ID: 9002, CreatedAt: now, Phase: "upstream", Owner: "provider", Source: "upstream_http",
		StatusCode: 503, AccountID: &accountID, AccountName: "OpenAI 主账号", Message: "gateway timeout",
	}, UpstreamStatusCode: &status503, UpstreamErrorMessage: "gateway timeout"}
	evaluator.evaluateAccountRequestAlerts(buildOpsAccountRequestAlertSignals(entry))
	require.Len(t, repo.created, 2)
	require.NotEqual(t, repo.created[0].DedupeKey, repo.created[1].DedupeKey)
}

func TestShouldSuppressAggregateRateAlertEmail(t *testing.T) {
	require.True(t, shouldSuppressAggregateRateAlertEmail("success_rate"))
	require.True(t, shouldSuppressAggregateRateAlertEmail("error_rate"))
	require.True(t, shouldSuppressAggregateRateAlertEmail("upstream_error_rate"))
	require.False(t, shouldSuppressAggregateRateAlertEmail(OpsAlertMetricAccountRequestFailure))
	require.False(t, shouldSuppressAggregateRateAlertEmail("account_balance"))
}

func TestAccountRequestAlertEmailUsesDirectSubjectAndReasonFirst(t *testing.T) {
	rule := &OpsAlertRule{MetricType: OpsAlertMetricAccountRequestFailure, Severity: "P1"}
	event := &OpsAlertEvent{
		Severity: "P1", Title: "OpenAI 主账号异常", Description: "503：上游服务异常",
		FiredAt: time.Now().UTC(), Status: OpsAlertStatusFiring,
	}

	require.Equal(t, "[运维告警]OpenAI 主账号异常", buildOpsAlertEmailSubject(rule, event))
	body := buildOpsAlertEmailBody(rule, event, "<div>DETAIL</div>")
	require.NotContains(t, body, "P1")
	require.Less(t, strings.Index(body, "503：上游服务异常"), strings.Index(body, "OpenAI 主账号异常"))
}

func TestAccountRequestAlertEmailEnqueuesPersistentAggregationInsteadOfSendingDirectly(t *testing.T) {
	repo := newAccountRequestAlertRepoMock()
	settings := &balanceCenterSettingRepoStub{values: map[string]string{
		SettingKeyOpsEmailNotificationConfig: `{"alert":{"enabled":true,"recipients":["ops@example.com"],"min_severity":"P2","rate_limit_per_hour":100}}`,
	}}
	opsService := &OpsService{opsRepo: repo, settingRepo: settings}
	evaluator := NewOpsAlertEvaluatorService(opsService, repo, &EmailService{}, nil, nil, nil)
	outbox := &balanceCenterAlertOutboxStub{}
	evaluator.SetAlertEmailOutbox(outbox)
	rule := &OpsAlertRule{ID: 18, Name: "账号请求异常", MetricType: OpsAlertMetricAccountRequestFailure, Severity: "P1", NotifyEmail: true}
	event := &OpsAlertEvent{ID: 27, RuleID: 18, Severity: "P1", Title: "OpenAI 主账号异常", Description: "503：上游服务异常", FiredAt: time.Now().UTC()}

	queued := evaluator.maybeSendAlertEmail(context.Background(), nil, rule, event, nil, nil)

	require.True(t, queued)
	require.Len(t, outbox.inputs, 1)
	require.Equal(t, AlertEmailSourceOpsAlert, outbox.inputs[0].SourceType)
	require.Equal(t, "27", outbox.inputs[0].SourceID)
	require.Equal(t, OpsAlertMetricAccountRequestFailure, outbox.inputs[0].AlertType)
	require.Len(t, repo.deliveries, 1)
	require.Equal(t, OpsAlertEmailStatusQueued, repo.deliveries[0].Status)
	require.False(t, event.EmailSent, "成功入队不等于 SMTP 已发送")
}
