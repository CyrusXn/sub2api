//go:build unit

package handler

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gemini404SwitchUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *gemini404SwitchUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	attempt := len(u.accountIDs)
	u.mu.Unlock()

	status := http.StatusOK
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"second account succeeded"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	if attempt == 1 {
		status = http.StatusNotFound
		body = `{"error":{"code":404,"message":"Model gemini-4-argon-high is not found for this upstream account","status":"NOT_FOUND"}}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (u *gemini404SwitchUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

// 验证真实 Gemini handler 会排除返回 404 的账号、选择另一个账号，并返回后者的成功响应。
func TestGeminiV1BetaModels_Upstream404SwitchesAccountAndSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newCachedBillingEnv(t, 500, 900, 901)
	env.h.maxAccountSwitchesGemini = 3
	upstream := &gemini404SwitchUpstream{}
	env.h.geminiCompatService = service.NewGeminiMessagesCompatService(
		&cachedBillingAccountRepo{accounts: env.accounts}, nil, nil, nil, nil, nil, upstream, nil, &config.Config{},
	)

	rec := env.serve(http.MethodPost,
		"/v1beta/models/gemini-4-argon-high:generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		gin.Params{{Key: "modelAction", Value: "/gemini-4-argon-high:generateContent"}},
		env.h.GeminiV1BetaModels,
	)

	calls := upstream.calls()
	require.Len(t, calls, 2, "404 后必须重新选择账号")
	require.NotEqual(t, calls[0], calls[1], "第二次上游请求必须使用另一账号")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "second account succeeded")
}
