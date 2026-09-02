package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 用户反馈的真实场景：下游请求 gpt-5.6-sol，本站实际调用 gpt-5.5，
// 上游响应声明日期快照 gpt-5.5-2026-04-23。出站必须回显 gpt-5.6-sol，
// 否则下游 sub2api 会用同一套审计逻辑把它标成「模型不一致」。
func TestForceDownstreamModelEchoesClientModelForUpstreamVariant(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		expected string
	}{
		{
			name:     "Chat 顶层 model",
			payload:  `{"id":"chatcmpl-1","model":"gpt-5.5-2026-04-23","choices":[]}`,
			expected: `{"id":"chatcmpl-1","model":"gpt-5.6-sol","choices":[]}`,
		},
		{
			name:     "Responses 嵌套 response.model",
			payload:  `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.5-2026-04-23"}}`,
			expected: `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol"}}`,
		},
		{
			name:     "Anthropic 嵌套 message.model",
			payload:  `{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5-20260514"}}`,
			expected: `{"type":"message_start","message":{"id":"msg_1","model":"gpt-5.6-sol"}}`,
		},
		{
			name:     "顶层与嵌套同时存在时一并回显",
			payload:  `{"model":"gpt-5.5","response":{"model":"gpt-5.5-2026-04-23"}}`,
			expected: `{"model":"gpt-5.6-sol","response":{"model":"gpt-5.6-sol"}}`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := forceDownstreamModelInJSON(tt.payload, "gpt-5.6-sol")
			require.True(t, changed)
			require.Equal(t, tt.expected, got)
			require.Equal(t, tt.expected, string(forceDownstreamModelInJSONBytes([]byte(tt.payload), "gpt-5.6-sol")))
		})
	}
}

// 只覆盖已存在的字段：上游没声明模型时不得凭空补 model 键，
// 空模型名、非字符串 model 和已一致的报文都必须原样返回。
func TestForceDownstreamModelLeavesPayloadUntouched(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		clientModel string
	}{
		{name: "无 model 字段", payload: `{"type":"response.output_text.delta","delta":"hi"}`, clientModel: "gpt-5.6-sol"},
		{name: "model 为 null", payload: `{"model":null}`, clientModel: "gpt-5.6-sol"},
		{name: "model 非字符串", payload: `{"model":{"name":"x"}}`, clientModel: "gpt-5.6-sol"},
		{name: "下游模型为空", payload: `{"model":"gpt-5.5-2026-04-23"}`, clientModel: "   "},
		{name: "已经一致", payload: `{"model":"gpt-5.6-sol"}`, clientModel: "gpt-5.6-sol"},
		{name: "空报文", payload: ``, clientModel: "gpt-5.6-sol"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := forceDownstreamModelInJSON(tt.payload, tt.clientModel)
			require.False(t, changed)
			require.Equal(t, tt.payload, got)
			require.Equal(t, tt.payload, string(forceDownstreamModelInJSONBytes([]byte(tt.payload), tt.clientModel)))
		})
	}
}

// 快筛只放过含 "model" 字面量的报文，保证绝大多数流式增量事件不进 JSON 解析。
func TestDownstreamModelEchoCandidateSkipsDeltaEvents(t *testing.T) {
	require.False(t, downstreamModelEchoCandidate(`{"type":"response.output_text.delta","delta":"hello"}`))
	require.True(t, downstreamModelEchoCandidate(`{"type":"response.created","model":"gpt-5.5"}`))
	require.False(t, downstreamModelEchoCandidateBytes([]byte(`{"delta":"hello"}`)))
	require.True(t, downstreamModelEchoCandidateBytes([]byte(`{"response":{"model":"gpt-5.5"}}`)))
}

// 出站回显不改本站审计：观察器读的是改写前的原始上游报文，
// 因此本站使用记录仍然保留真实上游模型，并照旧判定为模型不一致。
func TestForceDownstreamModelKeepsLocalUpstreamAudit(t *testing.T) {
	raw := []byte(`{"type":"response.completed","response":{"model":"gpt-5.5-2026-04-23"}}`)
	observer := &upstreamResponseModelObserver{}
	observer.ObserveOpenAI(raw, "response.completed")

	echoed := forceDownstreamModelInJSONBytes(raw, "gpt-5.6-sol")
	require.Equal(t, `{"type":"response.completed","response":{"model":"gpt-5.6-sol"}}`, string(echoed))

	require.Equal(t, "gpt-5.5-2026-04-23", observer.Model())
	mismatch := upstreamModelMismatch("gpt-5.5", observer.Model())
	require.NotNil(t, mismatch)
	require.True(t, *mismatch)
}
