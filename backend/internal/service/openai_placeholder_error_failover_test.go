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
	errors []string
}

func (r *consecutiveFailureRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}
func (r *consecutiveFailureRepo) SetError(_ context.Context, _ int64, message string) error {
	r.errors = append(r.errors, message)
	return nil
}

func TestTransientServerFailuresDisableAccountOnThirdFailure(t *testing.T) {
	repo := &consecutiveFailureRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	account := &Account{ID: 95109, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	for range 2 {
		svc.handleOpenAIAccountUpstreamError(context.Background(), account, 502, nil, []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`), "gpt-5.6-luna")
	}
	require.Empty(t, repo.errors)
	svc.handleOpenAIAccountUpstreamError(context.Background(), account, 502, nil, []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`), "gpt-5.6-luna")
	require.Len(t, repo.errors, 1)
	require.Equal(t, StatusError, account.Status)
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
