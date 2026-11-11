package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSpeedFenjueMessagesLifecycleAcrossAttempts(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	SetTextStreamRequestStart(c, time.Now(), "claude-test")
	ensureTextStreamTimingForRequest(c, time.Now())
	_, err := c.Writer.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"failed-attempt\",\"usage\":{}}}\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	require.Empty(t, rec.Body.String(), "正文前元数据不能绑定到失败账号")
	require.True(t, writeTextStreamPlaceholder(c, c.Writer))
	require.Contains(t, rec.Body.String(), `"text":" "`)
	require.True(t, TextStreamAwaitingOutput(c), "空格不能阻止切号")
	require.NotNil(t, TextStreamFirstPacketMs(c))
	packet := *TextStreamFirstPacketMs(c)
	for _, payload := range []string{
		`{"type":"message_start","message":{"id":"success-attempt","usage":{"input_tokens":7}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	} {
		_, err = c.Writer.WriteString("event: " + gjson.Get(payload, "type").String() + "\ndata: " + payload + "\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
	}
	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, "event: message_start\n"))
	require.Contains(t, body, `"index":1`)
	require.NotContains(t, body, "failed-attempt")
	require.Contains(t, body, `"input_tokens":7`)
	require.Contains(t, body, `"output_tokens":2`)
	require.False(t, TextStreamAwaitingOutput(c))
	require.Equal(t, packet, *TextStreamFirstPacketMs(c))
}

func TestSpeedFenjueClaudeConversionsPlaceholderThenError(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", path, nil)
			started := time.Now().Add(-TextStreamPlaceholderDelay - time.Second)
			SetTextStreamRequestStart(c, started, "claude-test")
			ensureTextStreamTimingForRequest(c, started)
			pr, pw := io.Pipe()
			defer pr.Close()
			go func() {
				time.Sleep(30 * time.Millisecond)
				_, _ = io.WriteString(pw, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n")
				_ = pw.Close()
			}()
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: pr}
			svc := &GatewayService{}
			var err error
			if strings.HasSuffix(path, "/responses") {
				_, err = svc.handleResponsesStreamingResponse(resp, c, "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			} else {
				_, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-test", "claude-test", nil, time.Now())
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Contains(t, rec.Body.String(), `" "`)
			require.NotNil(t, TextStreamFirstPacketMs(c))
			require.True(t, TextStreamAwaitingOutput(c))
			require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
		})
	}
}

func TestSpeedFenjueResponsesLifecycleAcrossAttempts(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	SetTextStreamRequestStart(c, time.Now(), "grok-test")
	ensureTextStreamTimingForRequest(c, time.Now())
	require.True(t, writeTextStreamPlaceholder(c, c.Writer))
	require.Contains(t, rec.Body.String(), "event: response.created\n")
	require.Contains(t, rec.Body.String(), `"delta":" "`)
	require.True(t, TextStreamAwaitingOutput(c))
	for _, payload := range []string{
		`{"type":"response.created","response":{"id":"resp-upstream","output":[]},"sequence_number":0}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-real","type":"message","role":"assistant","content":[]},"sequence_number":1}`,
		`{"type":"response.content_part.added","output_index":0,"item_id":"msg-real","content_index":0,"part":{"type":"output_text","text":""},"sequence_number":2}`,
		`{"type":"response.output_text.delta","output_index":0,"item_id":"msg-real","content_index":0,"delta":"hello","sequence_number":3}`,
		`{"type":"response.completed","response":{"id":"resp-upstream","output":[{"id":"msg-real","type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":7,"output_tokens":2}},"sequence_number":4}`,
	} {
		_, err := c.Writer.WriteString("event: " + gjson.Get(payload, "type").String() + "\ndata: " + payload + "\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
	}
	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, "event: response.created\n"))
	require.Contains(t, body, `"output_index":1`)
	require.NotContains(t, body, "resp-upstream")
	require.Contains(t, body, `"text":" "`)
	require.Contains(t, body, `"input_tokens":7`)
	var sequences []int
	for _, frame := range strings.Split(body, "\n\n") {
		_, payload := parseTextStreamSSEFrame(frame)
		if seq := gjson.Get(payload, "sequence_number"); seq.Exists() {
			sequences = append(sequences, int(seq.Int()))
		}
	}
	for i, sequence := range sequences {
		require.Equal(t, i, sequence, "placeholder and retry events must keep a monotonic sequence")
	}
	require.False(t, TextStreamAwaitingOutput(c))
}

func TestSpeedFenjueRecognizesMinimalResponsesTextDeltaAsRealOutput(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	SetTextStreamRequestStart(c, time.Now(), "gpt-test")
	ensureTextStreamTimingForRequest(c, time.Now())

	_, err := c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
	require.NoError(t, err)
	c.Writer.Flush()

	require.Contains(t, rec.Body.String(), `"delta":"hello"`)
	require.False(t, TextStreamAwaitingOutput(c))
}

func TestSpeedFenjuePlaceholderAccountingKeepsLaterOutputCommitted(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	SetTextStreamRequestStart(c, time.Now(), "grok-test")
	ensureTextStreamTimingForRequest(c, time.Now())
	require.True(t, writeTextStreamPlaceholder(c, c.Writer))
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c), "协议占位本身不算真实输出")

	payload := `{"type":"response.output_text.delta","delta":"x"}`
	_, err := c.Writer.WriteString("event: response.output_text.delta\ndata: " + payload + "\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	require.False(t, TextStreamAwaitingOutput(c))
	require.Greater(t, OpenAICompactKeepaliveAdjustedWrittenSize(c), -1, "短真实输出不能被占位字节抵消")
}

func TestSpeedFenjueGeminiPlaceholderThenError(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			path := "/v1/messages"
			if native {
				path = "/v1beta/models/gemini-test:streamGenerateContent?alt=sse"
			}
			c.Request = httptest.NewRequest("POST", path, nil)
			started := time.Now().Add(-TextStreamPlaceholderDelay - time.Second)
			SetTextStreamRequestStart(c, started, "gemini-test")
			ensureTextStreamTimingForRequest(c, started)
			pr, pw := io.Pipe()
			defer pr.Close()
			go func() {
				time.Sleep(30 * time.Millisecond)
				_, _ = io.WriteString(pw, "data: {\"error\":{\"code\":503,\"message\":\"busy\"}}\n\n")
				_ = pw.Close()
			}()
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: pr}
			svc := &GeminiMessagesCompatService{}
			var err error
			if native {
				_, err = svc.handleNativeStreamingResponse(c, resp, time.Now(), false, &Account{ID: 1}, "")
			} else {
				_, err = svc.handleStreamingResponse(c, resp, time.Now(), "gemini-test")
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Contains(t, rec.Body.String(), `"text":" "`)
			require.NotNil(t, TextStreamFirstPacketMs(c))
			require.True(t, TextStreamAwaitingOutput(c))
		})
	}
}
