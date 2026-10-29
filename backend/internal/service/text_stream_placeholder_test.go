package service

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAITextSuccessDelta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\",\"output_index\":0,\"content_index\":0}\n\n"

func TestChatPlaceholderIsVisibleDataAndKeepsFailover(t *testing.T) {
	for _, headerWait := range []bool{false, true} {
		c, rec := newPassthroughKeepaliveTestContext(t)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		SetTextStreamRequestStart(c, time.Now().Add(-2350*time.Millisecond), "gpt-6-luna")
		ensureTextStreamTiming(c, time.Now().Add(-2*time.Second))
		if headerWait {
			k := &openAICompactSSEKeepalive{writer: c.Writer, stop: make(chan struct{}), timing: textStreamTimingFromContext(c)}
			require.True(t, k.beat())
		} else {
			require.True(t, writeTextStreamPlaceholder(c, c.Writer))
		}
		frame := strings.TrimPrefix(strings.TrimSpace(rec.Body.String()), "data: ")
		require.True(t, gjson.Valid(frame), "占位须是客户端可解析的 Chat Completions 事件")
		require.Equal(t, " ", gjson.Get(frame, "choices.0.delta.content").String())
		require.Equal(t, "chat.completion.chunk", gjson.Get(frame, "object").String())
		require.Equal(t, "gpt-6-luna", gjson.Get(frame, "model").String())
		require.False(t, textStreamHasRealOutput(c))
		require.False(t, writeTextStreamPlaceholder(c, c.Writer))
	}
}

func TestTextStreamPacketIncludesTimeBeforeForwarding(t *testing.T) {
	c, _ := newPassthroughKeepaliveTestContext(t)
	c.Set("text_stream_request_started", time.Now().Add(-2350*time.Millisecond))
	ensureTextStreamTiming(c, time.Now())
	require.True(t, writeTextStreamPlaceholder(c, c.Writer))
	require.GreaterOrEqual(t, *TextStreamFirstPacketMs(c), 2350)
}

func TestChatStreamErrorAfterFastOutputKeepsSSEProtocol(t *testing.T) {
	c, recorder := newPassthroughKeepaliveTestContext(t)
	ensureTextStreamTiming(c, time.Now())
	payload := openAITextSuccessDelta + "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	_, err := svc.handleChatStreamingResponse(&http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}, c,
		&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-6-luna", "gpt-6-luna", "gpt-6-luna", time.Now(), 0)
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "OK")
	require.NotContains(t, recorder.Body.String(), ": \n\n", "快正文路径不需要占位")
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		require.True(t, line == "" || strings.HasPrefix(line, "data:") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, ":"), "SSE 流不能拼入裸 JSON")
	}
}

type textStreamDeadlineCloseBody struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *textStreamDeadlineCloseBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

type textStreamDeadlineFlushWriter struct {
	gin.ResponseWriter
	closed <-chan struct{}
	once   sync.Once
}

func (w *textStreamDeadlineFlushWriter) Flush() {
	w.once.Do(func() { <-w.closed })
	w.ResponseWriter.Flush()
}

func TestTextStreamOutputFlushedAcrossDeadlineCannotReplay(t *testing.T) {
	for _, protocol := range []string{"raw_chat", "converted_chat", "passthrough"} {
		t.Run(protocol, func(t *testing.T) {
			c, rec := newPassthroughKeepaliveTestContext(t)
			ensureTextStreamTiming(c, time.Now())
			payload := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
			if protocol == "raw_chat" {
				payload = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
			}
			body := &textStreamDeadlineCloseBody{Reader: &openAIStreamReadThenErrorCloser{reader: strings.NewReader(payload), err: io.ErrUnexpectedEOF}, closed: make(chan struct{})}
			c.Writer = &textStreamDeadlineFlushWriter{ResponseWriter: c.Writer, closed: body.closed}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIFirstOutputTimeoutSeconds: 1}}}
			resp := &http.Response{Header: http.Header{}, Body: body}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			started := time.Now().Add(-900 * time.Millisecond)
			var err error
			switch protocol {
			case "raw_chat":
				_, err = svc.streamRawChatCompletions(c, resp, account, "model", "model", "model", nil, nil, started, 0)
			case "converted_chat":
				_, err = svc.handleChatStreamingResponse(resp, c, account, "model", "model", "model", started, 0)
			case "passthrough":
				_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, started, "model", "model")
			}
			require.Contains(t, rec.Body.String(), "hello")
			require.False(t, TextStreamAwaitingOutput(c))
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, err, &failover, "截止点已经交付的正文不能自动重放")
		})
	}
}

func TestTextStreamPlaceholderTracksActualPacketAcrossAttempts(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	started := time.Now().Add(-1500 * time.Millisecond)
	state := ensureTextStreamTiming(c, started)
	require.True(t, writeTextStreamPlaceholder(c, c.Writer))
	require.NotNil(t, TextStreamFirstPacketMs(c))
	require.GreaterOrEqual(t, *TextStreamFirstPacketMs(c), 1500)
	require.False(t, openAIStreamClientOutputStarted(c, false))
	require.Same(t, state, ensureTextStreamTiming(c, time.Now()))
	require.False(t, writeTextStreamPlaceholder(c, c.Writer))
}

func TestTextStreamSlowResponsesHasPacketBeforeRealOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newPassthroughKeepaliveTestContext(t)
	started := time.Now()
	ensureTextStreamTiming(c, started)
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		defer pw.Close()
		time.Sleep(1200 * time.Millisecond)
		_, _ = io.WriteString(pw, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}()
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	result, err := svc.handleStreamingResponse(context.Background(), &http.Response{Header: http.Header{}, Body: pr}, c,
		&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, started, "gpt-6-luna", "gpt-6-luna")
	require.NoError(t, err)
	require.NotNil(t, result.firstTokenMs)
	require.GreaterOrEqual(t, *result.firstTokenMs, 1200)
	require.NotNil(t, TextStreamFirstPacketMs(c))
	require.Less(t, *TextStreamFirstPacketMs(c), *result.firstTokenMs)
	require.Equal(t, 1, strings.Count(rec.Body.String(), ": \n\n"))
	require.Contains(t, rec.Body.String(), "hello")
}

func TestTextStreamWhitespaceDeduplicatesOnlyBeforeRealOutput(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ensureTextStreamTiming(c, time.Now())
	payload := `{"type":"response.output_text.delta","delta":" \n"}`
	require.True(t, forwardTextStreamWhitespace(c, payload, "response.output_text.delta"))
	require.Equal(t, ": \n\n", rec.Body.String())
	firstBody := rec.Body.String()
	require.True(t, forwardTextStreamWhitespace(c, payload, "response.output_text.delta"))
	require.Equal(t, firstBody, rec.Body.String())
	require.False(t, openAIStreamClientOutputStarted(c, false))
	markTextStreamRealOutput(c)
	require.False(t, forwardTextStreamWhitespace(c, payload, "response.output_text.delta"))
}

func TestTextStreamRealOutputCancelsPlaceholder(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ensureTextStreamTiming(c, time.Now())
	markTextStreamRealOutput(c)
	require.False(t, writeTextStreamPlaceholder(c, c.Writer))
	require.Nil(t, TextStreamFirstPacketMs(c), "尚未实际交付时不能提前记首包")
	recordTextStreamPacket(c)
	require.NotNil(t, TextStreamFirstPacketMs(c))
}

func TestTextStreamHTTPDeliveryAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{"responses", "passthrough", "raw_chat", "converted_chat"} {
		t.Run(protocol, func(t *testing.T) {
			type outcome struct {
				packet, token *int
				err           error
			}
			finished := make(chan outcome, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				c, _ := gin.CreateTestContext(w)
				c.Request = req
				started := time.Now()
				ensureTextStreamTiming(c, started)
				reader, writer := io.Pipe()
				defer reader.Close()
				go func() {
					defer writer.Close()
					if protocol == "raw_chat" {
						_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
					} else {
						_, _ = io.WriteString(writer, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-6-luna\"}}\n\n")
					}
					time.Sleep(1200 * time.Millisecond)
					if protocol == "raw_chat" {
						_, _ = io.WriteString(writer, "data: {\"id\":\"chat_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"output_index\":0,\"content_index\":0}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"model\":\"gpt-6-luna\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
					}
				}()
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				resp := &http.Response{Header: http.Header{}, Body: reader}
				var token *int
				var err error
				switch protocol {
				case "responses":
					var result *openaiStreamingResult
					result, err = svc.handleStreamingResponse(req.Context(), resp, c, account, started, "gpt-6-luna", "gpt-6-luna")
					if result != nil {
						token = result.firstTokenMs
					}
				case "passthrough":
					var result *openaiStreamingResultPassthrough
					result, err = svc.handleStreamingResponsePassthrough(req.Context(), resp, c, account, started, "gpt-6-luna", "gpt-6-luna")
					if result != nil {
						token = result.firstTokenMs
					}
				default:
					var result *OpenAIForwardResult
					if protocol == "raw_chat" {
						result, err = svc.streamRawChatCompletions(c, resp, account, "gpt-6-luna", "gpt-6-luna", "gpt-6-luna", nil, nil, started, 0)
					} else {
						result, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-6-luna", "gpt-6-luna", "gpt-6-luna", started, 0)
					}
					if result != nil {
						token = result.FirstTokenMs
					}
				}
				finished <- outcome{TextStreamFirstPacketMs(c), token, err}
			}))
			defer server.Close()
			started := time.Now()
			client := server.Client()
			client.Timeout = 5 * time.Second
			path := "/v1/responses"
			if protocol == "raw_chat" || protocol == "converted_chat" {
				path = "/v1/chat/completions"
			}
			resp, err := client.Get(server.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()
			reader := bufio.NewReader(resp.Body)
			firstLine, err := reader.ReadString('\n')
			require.NoError(t, err)
			if path == "/v1/chat/completions" {
				require.True(t, strings.HasPrefix(firstLine, "data: "))
				require.Equal(t, " ", gjson.Get(strings.TrimPrefix(firstLine, "data: "), "choices.0.delta.content").String())
			} else {
				require.Equal(t, ": \n", firstLine)
			}
			require.Less(t, time.Since(started), 1200*time.Millisecond, "占位应在正文之前通过真实HTTP交付")
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Contains(t, string(body), "hello")
			result := <-finished
			require.NoError(t, result.err)
			require.NotNil(t, result.packet)
			require.NotNil(t, result.token)
			require.Less(t, *result.packet, *result.token)
			require.GreaterOrEqual(t, *result.token, 1200)
		})
	}
}

func TestTextStreamWhitespaceFailureRemainsRetryable(t *testing.T) {
	for _, protocol := range []string{"responses", "passthrough", "raw_chat", "converted_chat"} {
		t.Run(protocol, func(t *testing.T) {
			c, rec := newPassthroughKeepaliveTestContext(t)
			started := time.Now()
			ensureTextStreamTiming(c, started)
			payload := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"upstream unavailable\"}}}\n\n"
			if protocol == "raw_chat" {
				payload = "data: {\"choices\":[{\"delta\":{\"content\":\" \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" \"}}]}\n\ndata: {\"error\":{\"code\":\"server_error\",\"message\":\"upstream unavailable\"}}\n\n"
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
			var err error
			switch protocol {
			case "responses":
				_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, started, "gpt-6-luna", "gpt-6-luna")
			case "passthrough":
				_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, started, "gpt-6-luna", "gpt-6-luna")
			case "raw_chat":
				_, err = svc.streamRawChatCompletions(c, resp, account, "gpt-6-luna", "gpt-6-luna", "gpt-6-luna", nil, nil, started, 0)
			case "converted_chat":
				_, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-6-luna", "gpt-6-luna", "gpt-6-luna", started, 0)
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.False(t, openAIStreamClientOutputStarted(c, false))
			require.NotNil(t, TextStreamFirstPacketMs(c))
			require.Equal(t, 1, strings.Count(rec.Body.String(), ": \n\n"))
			require.NotContains(t, rec.Body.String(), "response.failed")
			require.False(t, writeTextStreamPlaceholder(c, c.Writer), "切号沿用请求级占位")
		})
	}
}

func TestTextStreamUnflushedAttemptDoesNotCommitRealOutput(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		c, rec := newPassthroughKeepaliveTestContext(t)
		started := time.Now()
		ensureTextStreamTiming(c, started)
		// 没有 SSE 空行，第一账号只解析到 data 行，不能污染下一次尝试。
		resp := &http.Response{Header: http.Header{}, Body: &openAIStreamReadThenErrorCloser{reader: strings.NewReader(`data: {"type":"response.output_text.delta","delta":"hello"}` + "\n"), err: io.ErrUnexpectedEOF}}
		svc := &OpenAIGatewayService{}
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		var err error
		if passthrough {
			_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, started, "model", "model")
		} else {
			_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, started, "model", "model")
		}
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Empty(t, rec.Body.String())
		require.True(t, TextStreamAwaitingOutput(c))
		require.Nil(t, TextStreamFirstPacketMs(c))
		require.True(t, writeTextStreamPlaceholder(c, c.Writer))
	}
}

func TestTextStreamConvertedChatRequestScopedIncompleteDoesNotFailover(t *testing.T) {
	for _, reason := range []string{"content_filter", "max_output_tokens"} {
		t.Run(reason, func(t *testing.T) {
			c, _ := newPassthroughKeepaliveTestContext(t)
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"" + reason + "\"}}}\n\n"))}
			_, err := (&OpenAIGatewayService{}).handleChatStreamingResponse(resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "model", "model", "model", time.Now(), 0)
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, err, &failover)
		})
	}
}

func TestTextStreamHighEffortPreservesLongerOutputDeadline(t *testing.T) {
	for _, protocol := range []string{"passthrough", "raw_chat", "converted_chat"} {
		t.Run(protocol, func(t *testing.T) {
			c, _ := newPassthroughKeepaliveTestContext(t)
			started := time.Now().Add(-1500 * time.Millisecond)
			ensureTextStreamTiming(c, started)
			reader, writer := io.Pipe()
			defer reader.Close()
			go func() {
				defer writer.Close()
				// 已超过普通预算，保留一个阻塞读间隔来验证高推理预算实际传到了截止器。
				time.Sleep(50 * time.Millisecond)
				payload := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
				if protocol == "raw_chat" {
					payload = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				}
				_, _ = io.WriteString(writer, payload)
			}()
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIFirstOutputTimeoutSeconds: 1, OpenAIHighEffortFirstOutputTimeoutSeconds: 3}}}
			resp := &http.Response{Header: http.Header{}, Body: reader}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			effort := "high"
			var err error
			switch protocol {
			case "passthrough":
				_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, started, "model", "model", effort)
			case "raw_chat":
				_, err = svc.streamRawChatCompletions(c, resp, account, "model", "model", "model", &effort, nil, started, 0)
			case "converted_chat":
				_, err = svc.handleChatStreamingResponse(resp, c, account, "model", "model", "model", started, 0, effort)
			}
			require.NoError(t, err)
		})
	}
}

func TestTextStreamChatRefusalIsDeliveredBeforeReadFailure(t *testing.T) {
	for _, raw := range []bool{false, true} {
		c, rec := newPassthroughKeepaliveTestContext(t)
		payload := "data: {\"type\":\"response.refusal.delta\",\"delta\":\"Cannot comply\"}\n\n"
		if raw {
			payload = "data: {\"choices\":[{\"delta\":{\"refusal\":\"Cannot comply\"}}]}\n\n"
		}
		resp := &http.Response{Header: http.Header{}, Body: &openAIStreamReadThenErrorCloser{reader: strings.NewReader(payload), err: io.ErrUnexpectedEOF}}
		svc := &OpenAIGatewayService{}
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		var err error
		if raw {
			_, err = svc.streamRawChatCompletions(c, resp, account, "model", "model", "model", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
		} else {
			_, err = svc.handleChatStreamingResponse(resp, c, account, "model", "model", "model", time.Now(), openAISilentRefusalMinRequestBodyBytes)
		}
		require.Error(t, err)
		var failover *UpstreamFailoverError
		require.NotErrorAs(t, err, &failover, "实际交付的拒绝文本不能因随后断流而自动重放")
		require.Contains(t, rec.Body.String(), "Cannot comply")
		require.False(t, TextStreamAwaitingOutput(c))
		require.NotNil(t, TextStreamFirstPacketMs(c))
	}
}

func TestTextStreamConvertedTerminalOnlyOutputCannotSucceedWithoutDelivery(t *testing.T) {
	for _, output := range []string{
		`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]`,
		`[{"type":"function_call","call_id":"call_1","name":"test_tool","arguments":"{}"}]`,
	} {
		c, rec := newPassthroughKeepaliveTestContext(t)
		payload := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":" + output + ",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
		_, err := (&OpenAIGatewayService{}).handleChatStreamingResponse(resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "model", "model", "model", time.Now(), 0)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover, "转换器未生成真实内容时不能仅凭原始 output 结算成功")
		require.True(t, TextStreamAwaitingOutput(c))
		require.Nil(t, TextStreamFirstPacketMs(c))
		require.Empty(t, rec.Body.String())
	}
}
