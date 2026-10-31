//go:build unit

package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type identityRoutingHTTPUpstream struct {
	service.HTTPUpstream
	accountIDs []int64
	headers    []string
	succeed    bool
}

func (u *identityRoutingHTTPUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, id)
	u.headers = append(u.headers, req.Header.Get("Authorization"))
	status, body := 520, "temporary test failure"
	if u.succeed {
		status = http.StatusOK
		body = `{"id":"resp_identity_test","model":"gpt-5.1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"我是测试助手"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(body))}, nil
}

// 使用内存账号与隔离上游替身驱动真实 HTTP handler、调度器和转发器，不连接数据库。
func TestOpenAIIdentityRoutingHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, identity := range []bool{true, false} {
			name := endpoint + map[bool]string{true: "/身份失败遍历且不重复", false: "/普通问题保持原调度"}[identity]
			t.Run(name, func(t *testing.T) {
				accounts := make([]service.Account, 5)
				for i := range accounts {
					rate := []float64{0.1, 0.9, 0.5, 0.5, 0.7}[i]
					accounts[i] = service.Account{ID: int64(i + 1), Name: "隔离测试账号", Platform: service.PlatformOpenAI,
						Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
						Priority: []int{0, 1, 8, 2, 1}[i], RateMultiplier: &rate, GroupIDs: []int64{3131},
						Credentials: map[string]any{"api_key": fmt.Sprintf("test-only-%d", i+1), "pool_mode": true, "pool_mode_retry_count": 3},
						Extra:       map[string]any{}}
				}
				accounts[0].Extra["identity_account_id"] = 2
				accounts[3].Extra["identity_account_id"] = 2
				upstream := &identityRoutingHTTPUpstream{}
				h := newOpenAIResponsesFailoverTestHandler(t, upstream, accounts)
				h.maxAccountSwitches = 0
				c, _ := newOpenAIResponsesFailoverTestContext(t, nil)
				apiKey, _ := middleware2.GetAPIKeyFromContext(c)
				apiKey.Group.AllowMessagesDispatch = true
				question := "你把xxx修改一下"
				if identity {
					question = "你是什么大模型？"
				}
				field := `"messages":[{"role":"user","content":"` + question + `"}]`
				if endpoint == "/v1/responses" {
					field = `"input":"` + question + `"`
				}
				body := []byte(`{"model":"gpt-5.1","stream":false,` + field + `}`)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				switch endpoint {
				case "/v1/responses":
					h.Responses(c)
				case "/v1/chat/completions":
					h.ChatCompletions(c)
				case "/v1/messages":
					h.Messages(c)
				}
				if identity {
					require.Equal(t, []int64{2, 3, 5}, upstream.accountIDs, "跳过原账号与重复目标，且不受普通换号次数截断")
					require.Equal(t, []string{"Bearer test-only-2", "Bearer test-only-3", "Bearer test-only-5"}, upstream.headers)
				} else {
					require.Equal(t, []int64{1, 4, 3, 5, 2}, upstream.accountIDs, "普通问题不使用身份映射，故障仍按倍率和优先级去重切号")
					require.False(t, service.IsOpenAIIdentityRoutingActive(c.Request.Context()))
				}
				require.Equal(t, http.StatusBadGateway, c.Writer.Status())
			})
		}
	}
}

func TestOpenAIIdentityRoutingHTTPSuccess(t *testing.T) {
	upstream := &identityRoutingHTTPUpstream{succeed: true}
	accounts := []service.Account{
		{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{3131},
			Extra: map[string]any{"identity_account_id": 2}, Credentials: map[string]any{"api_key": "test-only-1"}},
		{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Priority: 1,
			Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{3131}, Credentials: map[string]any{"api_key": "test-only-2"}},
	}
	h := newOpenAIResponsesFailoverTestHandler(t, upstream, accounts)
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.1","input":"你是谁？"}`))
	h.Responses(c)
	require.Equal(t, []int64{2}, upstream.accountIDs)
	require.Equal(t, []string{"Bearer test-only-2"}, upstream.headers)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "我是测试助手")
}
