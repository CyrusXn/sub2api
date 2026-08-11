package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSanitizeOpenAIResponseJSONNormalizesModelAndDropsVendorSignals(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5-2026-04-23","response":{"model":"gpt-5.5-2026-04-23","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"upstream model gpt-5.5-2026-04-23"}]}]},"usage":{"input_tokens":2},"system_fingerprint":"fp_upstream","provider":"upstream-provider","model_version":"2026-04-23"}`)

	got := sanitizeOpenAIResponseJSON(body, "gpt-5.5", "gpt-5.5-2026-04-23")

	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "model").String())
	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "response.model").String())
	require.Equal(t, "completed", gjson.GetBytes(got, "response.status").String())
	require.Equal(t, 2, int(gjson.GetBytes(got, "usage.input_tokens").Int()))
	require.Contains(t, gjson.GetBytes(got, "response.output.0.content.0.text").String(), "gpt-5.5-2026-04-23")
	require.False(t, gjson.GetBytes(got, "system_fingerprint").Exists())
	require.False(t, gjson.GetBytes(got, "provider").Exists())
	require.False(t, gjson.GetBytes(got, "model_version").Exists())
}

func TestSanitizeOpenAIResponseSSEDataNormalizesNestedModel(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"model":"gpt-5.5-2026-04-23","output":[],"provider":"upstream","vendor_metadata":{"model":"gpt-5.5-2026-04-23"}},"x-vendor-meta":{"model":"gpt-5.5-2026-04-23"}}`)

	got := sanitizeOpenAIResponseSSEData(data, "gpt-5.5", "gpt-5.5-2026-04-23")

	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "response.model").String())
	require.False(t, gjson.GetBytes(got, "response.provider").Exists())
	require.False(t, gjson.GetBytes(got, "response.vendor_metadata").Exists())
	require.False(t, gjson.GetBytes(got, "x-vendor-meta").Exists())
}

func TestSanitizeOpenAIResponseJSONPreservesOpaqueToolArguments(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5-2026-04-23","output":[{"type":"function_call","name":"run","arguments":"{\"model\":\"gpt-5.5-2026-04-23\",\"provider\":\"user-value\"}"}],"metadata":{"model":"user-value"}}`)

	got := sanitizeOpenAIResponseJSON(body, "gpt-5.5", "gpt-5.5-2026-04-23")

	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "model").String())
	require.Equal(t, `{"model":"gpt-5.5-2026-04-23","provider":"user-value"}`, gjson.GetBytes(got, "output.0.arguments").String())
	require.Equal(t, "user-value", gjson.GetBytes(got, "metadata.model").String())
}

func TestSanitizeOpenAIResponseJSONRewritesVersionWhenMappedModelMatches(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5-2026-04-23","response":{"model":"gpt-5.5-2026-04-23","usage":{"input_tokens":1}}}`)

	got := sanitizeOpenAIResponseJSON(body, "gpt-5.5", "gpt-5.5")

	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "model").String())
	require.Equal(t, "gpt-5.5", gjson.GetBytes(got, "response.model").String())
}

func TestSanitizeOpenAIErrorBodyDoesNotExposeUpstreamModel(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","code":"model_not_found","message":"model gpt-5.5-2026-04-23 is unavailable","provider":"upstream"}}`)

	got := sanitizeOpenAIErrorBody(body, "gpt-5.5", "gpt-5.5-2026-04-23")

	require.Equal(t, "invalid_request_error", gjson.GetBytes(got, "error.type").String())
	require.Equal(t, "model_not_found", gjson.GetBytes(got, "error.code").String())
	require.NotContains(t, string(got), "gpt-5.5-2026-04-23")
	require.NotContains(t, string(got), "upstream")
}

func TestSanitizeOpenAIResponseHeadersDropsUpstreamIdentityHeaders(t *testing.T) {
	src := http.Header{
		"Content-Type":                 []string{"application/json"},
		"Retry-After":                  []string{"3"},
		"X-Request-Id":                 []string{"upstream-request"},
		"X-Codex-Primary-Used-Percent": []string{"42"},
		"X-Model":                      []string{"gpt-5.5-2026-04-23"},
		"X-Upstream-Provider":          []string{"provider"},
	}

	got := sanitizeOpenAIResponseHeaders(src, nil)

	require.Equal(t, "application/json", got.Get("Content-Type"))
	require.Equal(t, "3", got.Get("Retry-After"))
	require.Empty(t, got.Get("X-Request-Id"))
	require.Empty(t, got.Get("X-Codex-Primary-Used-Percent"))
	require.Empty(t, got.Get("X-Model"))
	require.Empty(t, got.Get("X-Upstream-Provider"))
}

func TestSanitizeOpenAIResponseHeadersCannotReallowIdentityHeaders(t *testing.T) {
	src := http.Header{"X-Upstream-Model": []string{"gpt-5.5-2026-04-23"}, "X-Codex-Debug": []string{"1"}}
	filter := responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled:           true,
		AdditionalAllowed: []string{"x-upstream-model", "x-codex-debug"},
	})

	got := sanitizeOpenAIResponseHeaders(src, filter)

	require.Empty(t, got.Get("X-Upstream-Model"))
	require.Empty(t, got.Get("X-Codex-Debug"))
}
