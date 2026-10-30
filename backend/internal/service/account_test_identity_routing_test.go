//go:build unit

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountChatIdentityRoutingSkipsOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, prompt string
		route        bool
		unconfigured bool
		wantID       int64
	}{
		{name: "身份对话直接使用目标", prompt: "你是谁", route: true, wantID: 38102},
		{name: "普通对话仍使用原账号", prompt: "你把xxx修改一下", route: true, wantID: 38101},
		{name: "未配置保持原账号", prompt: "你是谁", route: true, unconfigured: true, wantID: 38101},
		{name: "探针仍检查原账号", prompt: "你是谁", wantID: 38101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(9)
			accounts := newLegacySchedulerDecisionTestAccounts(groupID, true)
			for i := range accounts {
				accounts[i].Credentials = map[string]any{"api_key": "test-only-origin"}
			}
			accounts[0].Extra[accountIdentityAccountIDKey] = accounts[1].ID
			if tc.unconfigured {
				delete(accounts[0].Extra, accountIdentityAccountIDKey)
			}
			accounts[1].Credentials["api_key"] = "test-only-target"
			gateway := newLegacySchedulerDecisionTestService(accounts, false, schedulerTestConcurrencyCache{})
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"我是测试助手\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))}}
			accounts[0].Extra["openai_responses_supported"] = true
			accounts[1].Extra["openai_responses_supported"] = true
			svc := &AccountTestService{cfg: gateway.cfg, accountRepo: gateway.accountRepo, httpUpstream: upstream, openaiGatewayService: gateway}
			c, rec := newTestContext()
			testedID := accounts[0].ID
			err := svc.TestAccountConnection(c, accounts[0].ID, "gpt-test", tc.prompt, AccountTestModeDefault,
				AccountTestOptions{LastUserPrompt: &tc.prompt, RouteIdentity: tc.route, TestedAccountID: &testedID})

			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			wantKey := "test-only-origin"
			if tc.wantID == accounts[1].ID {
				wantKey = "test-only-target"
			}
			require.Equal(t, "Bearer "+wantKey, upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, tc.wantID, testedID)
			require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
			require.NotContains(t, rec.Body.String(), `"type":"error"`)
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"test_start"`))
		})
	}
}
