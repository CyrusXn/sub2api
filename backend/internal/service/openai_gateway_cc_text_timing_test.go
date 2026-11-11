//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCCTextTimingWhitespaceAndErrorRemainRetryable(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			started := time.Now()
			SetTextStreamRequestStart(c, started, "model")
			ensureTextStreamTimingForRequest(c, started)
			require.True(t, writeTextStreamPlaceholder(c, c.Writer))
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" \"}}]}\n\nevent: error\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"temporarily unavailable\"}}\n\n"))}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			var err error
			var result *OpenAIForwardResult
			if path == "/v1/messages" {
				result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "model", "model", "model", nil, nil, started)
			} else {
				result, err = svc.streamChatCompletionsAsResponses(c, resp, "model", nil, nil, false, nil, "model", "model", nil, nil, started)
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, TextStreamAwaitingOutput(c))
			if result != nil {
				require.Nil(t, result.FirstTokenMs)
			}
			require.NotContains(t, rec.Body.String(), "event: response.completed")
			require.NotContains(t, rec.Body.String(), "event: message_stop")
		})
	}
}

func TestCCTextTimingEmptyEOFIsRetryable(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetTextStreamRequestStart(c, time.Now(), "model")
	ensureTextStreamTimingForRequest(c, time.Now())
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	st := svc.scanCCStream(c, &http.Response{Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, "test", "", time.Now(), func(*apicompat.ChatCompletionsChunk) {})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, st.Err, &failover)
}

func TestCCTextTimingBodyWaitSendsPlaceholder(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	started := time.Now().Add(-TextStreamPlaceholderDelay + 40*time.Millisecond)
	SetTextStreamRequestStart(c, started, "model")
	ensureTextStreamTimingForRequest(c, started)
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		_ = writer.Close()
	}()
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	result, err := svc.streamChatCompletionsAsAnthropic(c, &http.Response{Header: http.Header{}, Body: reader}, "model", "model", "model", nil, nil, started)
	require.NoError(t, err)
	require.NotNil(t, result.FirstTokenMs)
	require.NotNil(t, TextStreamFirstPacketMs(c))
	require.Less(t, *TextStreamFirstPacketMs(c), *result.FirstTokenMs)
	require.False(t, TextStreamAwaitingOutput(c))
}

type ccTextTimingHeaderUpstream struct {
	httpUpstreamRecorder
}

func (u *ccTextTimingHeaderUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	time.Sleep(TextStreamPlaceholderDelay + 150*time.Millisecond)
	return u.httpUpstreamRecorder.Do(req, proxyURL, accountID, concurrency)
}

func (u *ccTextTimingHeaderUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestCCTextTimingHeaderWaitSendsPlaceholder(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages", "grok"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			body := []byte(`{"model":"model","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":8,"stream":true}`)
			endpoint := path
			if path == "grok" {
				endpoint = "/v1/responses"
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
			started := time.Now()
			SetTextStreamRequestStart(c, started, "model")
			upstreamBody := "data: {\"id\":\"chatcmpl_headers\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			account := forceChatResponsesFallbackAccount()
			if path == "grok" {
				account = &Account{ID: 53, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.x.ai/v1"}}
				upstreamBody = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_headers\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			}
			upstream := &ccTextTimingHeaderUpstream{httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			var result *OpenAIForwardResult
			var err error
			switch path {
			case "/v1/messages":
				result, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, body, "")
			case "grok":
				result, err = svc.forwardGrokResponses(context.Background(), c, account, body, "grok", true, started)
			default:
				result, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body, started)
			}
			require.NoError(t, err)
			require.NotNil(t, result.FirstTokenMs)
			require.NotNil(t, TextStreamFirstPacketMs(c))
			require.Less(t, *TextStreamFirstPacketMs(c), *result.FirstTokenMs)
			require.False(t, TextStreamAwaitingOutput(c))
		})
	}
}
