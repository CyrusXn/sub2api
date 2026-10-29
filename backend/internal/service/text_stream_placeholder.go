package service

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// TextStreamPlaceholderDelay 只控制等待占位，不参与上游超时、健康或计费。
const TextStreamPlaceholderDelay = time.Second

const textStreamTimingKey = "text_stream_timing"
const textStreamTimingDisabledKey = "text_stream_timing_disabled"
const textStreamRequestStartedKey = "text_stream_request_started"
const textStreamRequestModelKey = "text_stream_request_model"

type textStreamTiming struct {
	mu                sync.Mutex
	started           time.Time
	generationStarted time.Time
	placeholderFrame  string
	placeholder       bool
	realOutput        bool
	firstPacketMs     *int
	bytes             int
}

// SetTextStreamRequestStart 只保存计时元数据；通过鉴权并进入生成时才启用占位。
func SetTextStreamRequestStart(c *gin.Context, started time.Time, model string) {
	c.Set(textStreamRequestStartedKey, started)
	c.Set(textStreamRequestModelKey, model)
}

func textStreamTimingFromContext(c *gin.Context) *textStreamTiming {
	if c == nil {
		return nil
	}
	value, _ := c.Get(textStreamTimingKey)
	state, _ := value.(*textStreamTiming)
	return state
}

func ensureTextStreamTiming(c *gin.Context, started time.Time) *textStreamTiming {
	if c == nil {
		return nil
	}
	if disabled, _ := c.Get(textStreamTimingDisabledKey); disabled == true {
		return nil
	}
	if state := textStreamTimingFromContext(c); state != nil {
		return state
	}
	state := &textStreamTiming{started: started, generationStarted: started, placeholderFrame: ": \n\n"}
	if value, ok := c.Get(textStreamRequestStartedKey); ok {
		if requestStarted, valid := value.(time.Time); valid && !requestStarted.IsZero() {
			state.started = requestStarted
		}
	}
	if c.Request != nil && strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
		model := c.GetString(textStreamRequestModelKey)
		// 使用请求级标识，避免把占位绑定到随后可能被排除的上游账号。
		payload, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-placeholder-" + uuid.NewString(), "object": "chat.completion.chunk",
			"created": state.started.Unix(), "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": " "}, "finish_reason": nil}},
		})
		state.placeholderFrame = "data: " + string(payload) + "\n\n"
	}
	c.Set(textStreamTimingKey, state)
	return state
}

func textStreamRequestEligible(endpoint, model string, body []byte) bool {
	if IsImageGenerationIntent(endpoint, model, body) || gjson.GetBytes(body, "audio").Exists() {
		return false
	}
	for _, modality := range gjson.GetBytes(body, "modalities").Array() {
		if modality.String() != "text" {
			return false
		}
	}
	return !strings.Contains(strings.ToLower(model), "audio")
}

// TextStreamAwaitingOutput 只用于已通过文本流鉴权的请求，不把占位算作正文交付。
func TextStreamAwaitingOutput(c *gin.Context) bool {
	return textStreamTimingFromContext(c) != nil && !textStreamHasRealOutput(c)
}

func (s *textStreamTiming) reservePlaceholder() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.placeholder || s.realOutput {
		return false
	}
	s.placeholder = true
	return true
}

func (s *textStreamTiming) recordPacket() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstPacketMs == nil {
		ms := int(time.Since(s.started).Milliseconds())
		s.firstPacketMs = &ms
	}
}

// TextStreamFirstPacketMs 返回实际交付耗时的副本，不泄露可变请求状态给异步入账。
func TextStreamFirstPacketMs(c *gin.Context) *int {
	state := textStreamTimingFromContext(c)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.firstPacketMs == nil {
		return nil
	}
	ms := *state.firstPacketMs
	return &ms
}

func markTextStreamRealOutput(c *gin.Context) {
	if state := textStreamTimingFromContext(c); state != nil {
		state.mu.Lock()
		state.realOutput = true
		state.mu.Unlock()
	}
}

func textStreamHasRealOutput(c *gin.Context) bool {
	state := textStreamTimingFromContext(c)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.realOutput
}

func textStreamPlaceholderTimer(c *gin.Context) (*time.Timer, <-chan time.Time) {
	state := textStreamTimingFromContext(c)
	if state == nil {
		return nil, nil
	}
	remaining := time.Until(state.generationStarted.Add(TextStreamPlaceholderDelay))
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	timer := time.NewTimer(remaining)
	return timer, timer.C
}

// 沿用已有等待配置，纯空白和心跳不能重新开始真实输出的等待预算。
func (s *OpenAIGatewayService) textStreamFirstOutputTimeout(reasoningEffort string) time.Duration {
	if timeout := s.openAIFirstOutputTimeout(reasoningEffort); timeout > 0 {
		return timeout
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		return time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	return 0
}

// 阻塞读取路径只在定时器里关闭本次上游 body，不并发操作 Gin 或下游 writer。
func startTextStreamReadDeadline(c *gin.Context, body io.Closer, started time.Time, timeout time.Duration) (func() bool, func()) {
	state := textStreamTimingFromContext(c)
	var expired atomic.Bool
	if state == nil || timeout <= 0 {
		return expired.Load, func() {}
	}
	timer := time.AfterFunc(time.Until(started.Add(timeout)), func() {
		state.mu.Lock()
		real := state.realOutput
		if !real {
			expired.Store(true)
		}
		state.mu.Unlock()
		if !real {
			_ = body.Close()
		}
	})
	return expired.Load, func() { timer.Stop() }
}

func recordTextStreamPacket(c *gin.Context) {
	if state := textStreamTimingFromContext(c); state != nil {
		state.recordPacket()
	}
}

func stampTextStreamTiming(c *gin.Context, result *OpenAIForwardResult) {
	if result == nil || !result.Stream || result.OpenAIWSMode {
		return
	}
	result.FirstPacketMs = TextStreamFirstPacketMs(c)
}

func writeTextStreamPlaceholder(c *gin.Context, w gin.ResponseWriter) bool {
	state := textStreamTimingFromContext(c)
	if state == nil {
		return false
	}
	return writeTextStreamPlaceholderFrame(c, w, state.placeholderFrame)
}

func writeTextStreamPlaceholderFrame(c *gin.Context, w gin.ResponseWriter, frame string) bool {
	state := textStreamTimingFromContext(c)
	if state == nil || !state.reservePlaceholder() {
		return false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	n, err := w.WriteString(frame)
	state.mu.Lock()
	state.bytes += n
	state.mu.Unlock()
	if err != nil {
		return false
	}
	w.Flush()
	state.recordPacket()
	return true
}

// 只去重正文开始前的纯文本空白；结构事件、工具参数与正文缩进不参与此规则。
func forwardTextStreamWhitespace(c *gin.Context, payload, eventType string) bool {
	state := textStreamTimingFromContext(c)
	if state == nil {
		return false
	}
	state.mu.Lock()
	real := state.realOutput
	state.mu.Unlock()
	if real {
		return false
	}
	var text gjson.Result
	switch eventType {
	case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		text = gjson.Get(payload, "delta")
	default:
		return false
	}
	if text.Type != gjson.String || text.String() == "" || strings.TrimSpace(text.String()) != "" {
		return false
	}
	// 占位不能携带某次尝试的 response/item ID，否则随后切号会混合两套生命周期。
	writeTextStreamPlaceholder(c, c.Writer)
	return true
}

func textStreamTerminalHasOutput(payload string) bool {
	for _, item := range gjson.Get(payload, "response.output").Array() {
		frame := `{"item":` + item.Raw + `}`
		if openAIStreamAddedEventStartsClientOutput([]byte(frame), "response.output_item.done") {
			return true
		}
	}
	return false
}

func textStreamPlaceholderBytes(c *gin.Context) int {
	if state := textStreamTimingFromContext(c); state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.bytes
	}
	return 0
}

func textStreamPlaceholderCommitted(c *gin.Context) bool {
	if state := textStreamTimingFromContext(c); state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.firstPacketMs != nil && state.placeholder
	}
	return false
}

// CC 的 role、finish 和 usage 都不是有效正文；工具交付则必须禁止重放。
func chatStreamPayloadOutput(payload string) (real, whitespace bool) {
	for _, choice := range gjson.Get(payload, "choices").Array() {
		for _, field := range []string{"delta", "message"} {
			part := choice.Get(field)
			if len(part.Get("tool_calls").Array()) > 0 || part.Get("function_call").Exists() {
				real = true
			}
			for _, key := range []string{"content", "reasoning_content", "reasoning", "refusal"} {
				value := part.Get(key)
				if value.Type == gjson.String && value.String() != "" {
					if strings.TrimSpace(value.String()) != "" {
						real = true
					} else {
						whitespace = true
					}
				}
			}
		}
	}
	return real, whitespace && !real
}
