package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func interactionTransportTestAccount(base string) *Account {
	return &Account{ID: 801, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 2,
		Credentials: map[string]any{"api_key": "upstream-test-key", "base_url": base}}
}

func interactionTransportTestService() (*GeminiMessagesCompatService, *geminiCompatHTTPUpstreamStub) {
	upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"v1_opaque-id","steps":[]}`)),
	}}
	return &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: upstream}, upstream
}

func TestIsGeminiInteractionAccount(t *testing.T) {
	require.True(t, IsGeminiInteractionAccount(interactionTransportTestAccount("")))
	require.True(t, IsGeminiInteractionAccount(interactionTransportTestAccount("https://custom.example")))
	for _, account := range []*Account{nil, {Platform: PlatformGemini, Type: AccountTypeOAuth}, {Platform: PlatformGemini, Type: AccountTypeServiceAccount}, {Platform: PlatformOpenAI, Type: AccountTypeAPIKey}} {
		require.False(t, IsGeminiInteractionAccount(account))
	}
}

func TestIsGeminiInteractionAccount_RejectsExplicitVertexMode(t *testing.T) {
	for _, mode := range []string{"vertex", " Vertex "} {
		account := interactionTransportTestAccount("https://aiplatform.googleapis.com")
		account.Credentials["auth_mode"] = mode
		require.False(t, IsGeminiInteractionAccount(account))
	}
	// 调度快照可能不带密钥；不能据此误排兼容上游或官方 AI Studio。
	require.True(t, IsGeminiInteractionAccount(&Account{Platform: PlatformGemini, Type: AccountTypeAPIKey}))
}

func TestDoGeminiInteraction_RejectsExplicitVertexModeBeforeRequest(t *testing.T) {
	for _, mode := range []string{"vertex", " Vertex "} {
		svc, upstream := interactionTransportTestService()
		account := interactionTransportTestAccount("https://aiplatform.googleapis.com")
		account.Credentials["auth_mode"] = mode
		resp, err := svc.DoGeminiInteraction(context.Background(), account, http.MethodPost, "", "", nil, "", []byte(`{"model":"gemini-3.8-flash","input":"hi"}`))
		require.Error(t, err)
		require.Nil(t, resp)
		require.Zero(t, upstream.calls)
	}
}

func TestDoGeminiInteraction_NativeCreate(t *testing.T) {
	for _, base := range []string{"", "https://gateway.example/gemini", "https://gateway.example/gemini/v1beta/"} {
		t.Run(base, func(t *testing.T) {
			svc, upstream := interactionTransportTestService()
			body := []byte(`{"model":"gemini-3.8-flash","input":[{"type":"text","text":"hi"}],"background":true,"previous_interaction_id":"v1_previous","stream":true}`)
			ctx := context.WithValue(context.Background(), struct{}{}, "request-context")
			resp, err := svc.DoGeminiInteraction(ctx, interactionTransportTestAccount(base), http.MethodPost, "", "", url.Values{"alt": {"sse"}}, "", body)
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Equal(t, 1, upstream.calls)
			got := upstream.lastReq
			require.Equal(t, http.MethodPost, got.Method)
			expected := "https://generativelanguage.googleapis.com/v1beta/interactions?alt=sse"
			if base != "" {
				expected = "https://gateway.example/gemini/v1beta/interactions?alt=sse"
			}
			require.Equal(t, expected, got.URL.String())
			require.Equal(t, "upstream-test-key", got.Header.Get("x-goog-api-key"))
			require.Empty(t, got.Header.Get("Authorization"))
			require.Equal(t, "2026-05-20", got.Header.Get("Api-Revision"))
			require.Equal(t, "application/json", got.Header.Get("Content-Type"))
			require.Equal(t, ctx, got.Context())
			sent, err := io.ReadAll(got.Body)
			require.NoError(t, err)
			require.Equal(t, body, sent)
			// 传输层不得提前读取或关闭 JSON/SSE，响应生命周期由 handler 负责。
			payload, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Contains(t, string(payload), "steps")
		})
	}
}

func TestDoGeminiInteraction_ResourceMethodsAndResume(t *testing.T) {
	for _, tc := range []struct {
		name, method, action, path string
		query                      url.Values
	}{
		{"get", http.MethodGet, "", "/v1beta/interactions/v1_opaque-id", nil},
		{"resume", http.MethodGet, "", "/v1beta/interactions/v1_opaque-id", url.Values{"stream": {"true"}, "last_event_id": {"evt/1+2="}, "include_input": {"false"}}},
		{"cancel", http.MethodPost, "cancel", "/v1beta/interactions/v1_opaque-id/cancel", nil},
		{"delete", http.MethodDelete, "", "/v1beta/interactions/v1_opaque-id", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, upstream := interactionTransportTestService()
			resp, err := svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount("https://gateway.example/v1beta"), tc.method, "v1_opaque-id", tc.action, tc.query, "2025-12-11", nil)
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Equal(t, tc.path, upstream.lastReq.URL.Path)
			require.Equal(t, tc.query.Encode(), upstream.lastReq.URL.RawQuery)
			require.Equal(t, "2025-12-11", upstream.lastReq.Header.Get("Api-Revision"))
			require.Empty(t, upstream.lastReq.Header.Get("Content-Type"))
		})
	}
}

func TestDoGeminiInteraction_RejectsUnsafeRequestsBeforeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, method, id, action, revision, base string
		query                                    url.Values
	}{
		{name: "path traversal", method: http.MethodGet, id: "../secret"},
		{name: "encoded traversal", method: http.MethodGet, id: "%2e%2e"},
		{name: "encoded slash", method: http.MethodGet, id: "v1_a%2fb"},
		{name: "query injection", method: http.MethodGet, id: "v1_a?key=secret"},
		{name: "fragment injection", method: http.MethodGet, id: "v1_a#fragment"},
		{name: "resource prefix", method: http.MethodGet, id: "interactions/v1_a"},
		{name: "empty id", method: http.MethodGet},
		{name: "oversized id", method: http.MethodGet, id: strings.Repeat("a", 4097)},
		{name: "unsupported method", method: http.MethodPatch, id: "v1_a"},
		{name: "unknown action", method: http.MethodPost, id: "v1_a", action: "delete"},
		{name: "cancel without id", method: http.MethodPost, action: "cancel"},
		{name: "get action", method: http.MethodGet, id: "v1_a", action: "cancel"},
		{name: "client api key", method: http.MethodGet, id: "v1_a", query: url.Values{"key": {"client-secret"}}},
		{name: "unknown query", method: http.MethodGet, id: "v1_a", query: url.Values{"api_key": {"client-secret"}}},
		{name: "duplicate query", method: http.MethodGet, id: "v1_a", query: url.Values{"stream": {"true", "false"}}},
		{name: "nonboolean stream", method: http.MethodGet, id: "v1_a", query: url.Values{"stream": {"1"}}},
		{name: "nonboolean include input", method: http.MethodGet, id: "v1_a", query: url.Values{"include_input": {"yes"}}},
		{name: "resume without stream", method: http.MethodGet, id: "v1_a", query: url.Values{"last_event_id": {"evt1"}}},
		{name: "resume stream false", method: http.MethodGet, id: "v1_a", query: url.Values{"stream": {"false"}, "last_event_id": {"evt1"}}},
		{name: "create stream query", method: http.MethodPost, query: url.Values{"stream": {"true"}}},
		{name: "invalid alt", method: http.MethodGet, id: "v1_a", query: url.Values{"alt": {"json"}}},
		{name: "header injection", method: http.MethodGet, id: "v1_a", revision: "2026-05-20\r\nx-secret: secret"},
		{name: "bad revision date", method: http.MethodGet, id: "v1_a", revision: "2026-02-30"},
		{name: "base credential", method: http.MethodGet, id: "v1_a", base: "https://user:secret@gateway.example"},
		{name: "base query", method: http.MethodGet, id: "v1_a", base: "https://gateway.example?key=secret"},
		{name: "base fragment", method: http.MethodGet, id: "v1_a", base: "https://gateway.example#fragment"},
		{name: "insecure base", method: http.MethodGet, id: "v1_a", base: "http://gateway.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, upstream := interactionTransportTestService()
			resp, err := svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount(tc.base), tc.method, tc.id, tc.action, tc.query, tc.revision, nil)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
			require.Nil(t, resp)
			require.Zero(t, upstream.calls)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestDoGeminiInteraction_RejectsUnsupportedAccounts(t *testing.T) {
	for _, account := range []*Account{
		nil,
		{Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"oauth_type": "ai_studio", "access_token": "secret"}},
		{Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"project_id": "secret", "oauth_type": "code_assist"}},
		{Platform: PlatformGemini, Type: AccountTypeServiceAccount, Credentials: map[string]any{"auth_mode": "apikey", "api_key": "secret"}},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "secret"}},
		{Platform: PlatformGemini, Type: AccountTypeAPIKey},
	} {
		svc, upstream := interactionTransportTestService()
		_, err := svc.DoGeminiInteraction(context.Background(), account, http.MethodPost, "", "", nil, "", []byte(`{}`))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
		require.Zero(t, upstream.calls)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestDoGeminiInteraction_SSRFAndTransportErrorsAreSanitized(t *testing.T) {
	svc, upstream := interactionTransportTestService()
	svc.cfg.Security.URLAllowlist.Enabled = true
	svc.cfg.Security.URLAllowlist.UpstreamHosts = []string{"allowed.example"}
	_, err := svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount("https://secret.example"), http.MethodPost, "", "", nil, "", nil)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
	require.NotContains(t, err.Error(), "secret.example")
	require.Zero(t, upstream.calls)

	svc.cfg.Security.URLAllowlist.Enabled = false
	upstream.err = errors.New("GET https://secret.example?key=secret failed")
	_, err = svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount(""), http.MethodPost, "", "", nil, "", nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
	require.NotContains(t, err.Error(), "secret")
	require.Equal(t, 1, upstream.calls, "create 失败不得自动重试")
}

func TestDoGeminiInteraction_NonSuccessResponseIsNotConsumed(t *testing.T) {
	svc, upstream := interactionTransportTestService()
	upstream.response.StatusCode = http.StatusTooManyRequests
	upstream.response.Body = io.NopCloser(strings.NewReader(`{"error":{"message":"upstream-secret-detail"}}`))
	resp, err := svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount(""), http.MethodPost, "", "", nil, "", []byte(`{"input":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, 1, upstream.calls)
	payload, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(payload), "upstream-secret-detail")
}

func TestDoGeminiInteraction_UnconfiguredServiceAndResourceBody(t *testing.T) {
	for _, svc := range []*GeminiMessagesCompatService{nil, {}, {cfg: &config.Config{}}} {
		_, err := svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount(""), http.MethodPost, "", "", nil, "", nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
	}
	svc, upstream := interactionTransportTestService()
	_, err := svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount(""), http.MethodDelete, "v1_opaque", "", nil, "", []byte(`{"input":"hi"}`))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
	require.Zero(t, upstream.calls)
}

func TestDoGeminiInteraction_DeterministicConstructionFailuresAreNotSent(t *testing.T) {
	for _, key := range []string{"", "bad\r\nkey", "bad\x00key", "bad\x7fkey"} {
		svc, upstream := interactionTransportTestService()
		account := interactionTransportTestAccount("")
		account.Credentials["api_key"] = key
		_, err := svc.DoGeminiInteraction(context.Background(), account, http.MethodPost, "", "", nil, "", nil)
		require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
		require.Zero(t, upstream.calls)
	}
	svc, upstream := interactionTransportTestService()
	_, err := svc.DoGeminiInteraction(nil, interactionTransportTestAccount(""), http.MethodPost, "", "", nil, "", nil)
	require.ErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
	require.Zero(t, upstream.calls)
}

func TestDoGeminiInteraction_AfterTransportCallIsUncertain(t *testing.T) {
	svc, upstream := interactionTransportTestService()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	upstream.err = context.Canceled
	_, err := svc.DoGeminiInteraction(ctx, interactionTransportTestAccount(""), http.MethodPost, "", "", nil, "", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
	require.Equal(t, 1, upstream.calls)
	upstream.err, upstream.response.Body = nil, nil
	_, err = svc.DoGeminiInteraction(context.Background(), interactionTransportTestAccount(""), http.MethodPost, "", "", nil, "", nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrGeminiInteractionRequestNotSent)
}

func TestParseGeminiInteractionUsage(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          ClaudeUsage
	}{
		{"normal", `{"usage":{"total_input_tokens":100,"total_cached_tokens":20,"total_output_tokens":30,"total_thought_tokens":15}}`, ClaudeUsage{InputTokens: 80, CacheReadInputTokens: 20, OutputTokens: 45}},
		{"official example separates thoughts", `{"usage":{"total_input_tokens":7,"total_output_tokens":20,"total_thought_tokens":22,"total_tokens":49}}`, ClaudeUsage{InputTokens: 7, OutputTokens: 42}},
		{"valid zero", `{"usage":{"total_input_tokens":0,"total_output_tokens":0}}`, ClaudeUsage{}},
		{"completed event", `{"event_type":"interaction.completed","interaction":{"usage":{"total_input_tokens":50,"total_cached_tokens":50,"total_output_tokens":10,"total_thought_tokens":0}}}`, ClaudeUsage{CacheReadInputTokens: 50, OutputTokens: 10}},
		{"image and thought", `{"usage":{"total_input_tokens":7,"total_output_tokens":100,"total_thought_tokens":10,"output_tokens_by_modality":[{"modality":"text","tokens":20},{"modality":"image","tokens":80}]}}`, ClaudeUsage{InputTokens: 7, OutputTokens: 110, ImageOutputTokens: 80}},
		{"image SSE", `{"event_type":"interaction.completed","interaction":{"usage":{"total_input_tokens":7,"total_output_tokens":100,"total_thought_tokens":10,"output_tokens_by_modality":[{"modality":"image","tokens":80},{"modality":"text","tokens":20}]}}}`, ClaudeUsage{InputTokens: 7, OutputTokens: 110, ImageOutputTokens: 80}},
		{"zero image", `{"usage":{"total_input_tokens":0,"total_output_tokens":0,"output_tokens_by_modality":[{"modality":"image","tokens":0}]}}`, ClaudeUsage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseGeminiInteractionUsage([]byte(tc.payload))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseGeminiInteractionUsage_ImageBillingDoesNotDoubleCount(t *testing.T) {
	usage, err := ParseGeminiInteractionUsage([]byte(`{"usage":{"total_input_tokens":7,"total_output_tokens":100,"total_thought_tokens":10,"output_tokens_by_modality":[{"modality":"image","tokens":80},{"modality":"text","tokens":20}]}}`))
	require.NoError(t, err)
	svc := &BillingService{}
	cost := svc.computeTokenBreakdown(&ModelPricing{InputPricePerToken: 0.01, OutputPricePerToken: 0.02, ImageOutputPricePerToken: 0.10}, UsageTokens{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ImageOutputTokens: usage.ImageOutputTokens,
	}, 1, "", false)
	require.InDelta(t, 0.07, cost.InputCost, 1e-12)
	require.InDelta(t, 0.60, cost.OutputCost, 1e-12, "20 text + 10 thought，图片仅在计费层扣一次")
	require.InDelta(t, 8.0, cost.ImageOutputCost, 1e-12)
	require.InDelta(t, 8.67, cost.TotalCost, 1e-12)
}

func TestParseGeminiInteractionUsage_RejectsInvalidImageBreakdown(t *testing.T) {
	maxInt := strconv.Itoa(int(^uint(0) >> 1))
	for _, detail := range []string{
		`null`, `{}`, `"image"`, `[null]`,
		`[{"modality":"image","tokens":-1}]`,
		`[{"modality":"image","tokens":1.5}]`,
		`[{"modality":"image","tokens":"secret"}]`,
		`[{"modality":"image","tokens":null}]`,
		`[{"modality":"image"}]`,
		`[{"tokens":1}]`,
		`[{"modality":1,"tokens":1}]`,
		`[{"modality":"unknown","tokens":1}]`,
		`[{"modality":"image","tokens":101}]`,
		`[{"modality":"image","tokens":80},{"modality":"text","tokens":21}]`,
		`[{"modality":"image","tokens":1},{"modality":"image","tokens":2}]`,
		`[{"modality":"image","tokens":` + maxInt + `},{"modality":"text","tokens":1}]`,
		`[{"modality":"image","tokens":9999999999999999999999999999999}]`,
	} {
		for _, wrap := range []string{`{"usage":%s}`, `{"event_type":"interaction.completed","interaction":{"usage":%s}}`} {
			payload := fmt.Sprintf(wrap, `{"total_input_tokens":7,"total_output_tokens":100,"total_thought_tokens":10,"output_tokens_by_modality":`+detail+`}`)
			_, err := ParseGeminiInteractionUsage([]byte(payload))
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrGeminiInteractionUsageMissing)
			require.NotContains(t, err.Error(), "secret")
		}
	}
}

func TestParseGeminiInteractionUsage_MissingAndInvalid(t *testing.T) {
	for _, payload := range []string{`{}`, `{"usage":null}`, `{"interaction":{}}`, `{"event_type":"step.delta","delta":{"text":"hello"}}`} {
		_, err := ParseGeminiInteractionUsage([]byte(payload))
		require.ErrorIs(t, err, ErrGeminiInteractionUsageMissing)
	}
	maxInt := strconv.Itoa(int(^uint(0) >> 1))
	for _, payload := range []string{
		`not-json-secret`, `null`, `[]`,
		`{"usage":{}}`,
		`{"usage":{"total_input_tokens":10}}`,
		`{"usage":{"total_input_tokens":-1,"total_output_tokens":0}}`,
		`{"usage":{"total_input_tokens":1.5,"total_output_tokens":0}}`,
		`{"usage":{"total_input_tokens":"secret","total_output_tokens":0}}`,
		`{"usage":{"total_input_tokens":10,"total_cached_tokens":11,"total_output_tokens":0}}`,
		`{"usage":{"total_input_tokens":10,"total_output_tokens":null}}`,
		`{"usage":{"total_input_tokens":10,"total_output_tokens":0,"total_thought_tokens":-1}}`,
		`{"usage":{"total_input_tokens":10,"total_output_tokens":` + maxInt + `,"total_thought_tokens":1}}`,
	} {
		_, err := ParseGeminiInteractionUsage([]byte(payload))
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrGeminiInteractionUsageMissing)
		require.NotContains(t, err.Error(), "secret")
	}
}
