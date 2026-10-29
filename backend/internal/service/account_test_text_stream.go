package service

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const accountTextPlaceholderFrame = "data: {\"type\":\"content\",\"text\":\" \"}\n\n"

// 账号测试使用自己的 content 协议；定时占位与业务事件共用同一把锁，避免阻塞
// 等响应头/响应体时并发操作 Gin writer，也不改变图片、音频和 compact 探针。
type accountTextProbeWriter struct {
	gin.ResponseWriter
	mu      sync.Mutex
	stopped bool
}

func (w *accountTextProbeWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Write(data)
}

func (w *accountTextProbeWriter) WriteString(data string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.WriteString(data)
}

func (w *accountTextProbeWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.Flush()
}

func startAccountTextProbePlaceholder(c *gin.Context) func() {
	if _, active := c.Writer.(*accountTextProbeWriter); active {
		return func() {}
	}
	state := ensureTextStreamTiming(c, time.Now())
	original := c.Writer
	w := &accountTextProbeWriter{ResponseWriter: original}
	c.Writer = w
	requestContext := c.Request.Context()
	timer := time.AfterFunc(time.Until(state.started.Add(TextStreamPlaceholderDelay)), func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.stopped && requestContext.Err() == nil {
			writeTextStreamPlaceholderFrame(c, original, accountTextPlaceholderFrame)
		}
	})
	return func() {
		w.mu.Lock()
		w.stopped = true
		timer.Stop()
		w.mu.Unlock()
		c.Writer = original
	}
}

func (s *AccountTestService) sendAccountTestText(c *gin.Context, text string) {
	if text == "" {
		return
	}
	if strings.TrimSpace(text) == "" && !textStreamHasRealOutput(c) {
		writeTextStreamPlaceholderFrame(c, c.Writer, accountTextPlaceholderFrame)
		return
	}
	markTextStreamRealOutput(c)
	s.sendEvent(c, TestEvent{Type: "content", Text: text})
}
