package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// 正文前暂存生命周期，避免把下游绑定到随后需要切换的失败账号。
type textStreamProtocolWriter struct {
	gin.ResponseWriter
	mu            sync.Mutex
	c             *gin.Context
	state         *textStreamTiming
	pending       string
	metadata      []string
	metadataBytes int
	placeholder   bool
	real          bool
	err           error
	responseID    string
	itemID        string
	sequence      int
	inputUsage    map[string]any
	passthrough   bool
}

func installTextStreamProtocolWriter(c *gin.Context, state *textStreamTiming) {
	if c == nil || c.Writer == nil || state == nil || state.protocolWriter || c.Request == nil {
		return
	}
	path := c.Request.URL.Path
	if !strings.HasSuffix(path, "/messages") && !strings.HasSuffix(path, "/responses") {
		return
	}
	c.Writer = &textStreamProtocolWriter{ResponseWriter: c.Writer, c: c, state: state}
	state.protocolWriter = true
}

func (w *textStreamProtocolWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	if w.passthrough || w.Status() >= http.StatusBadRequest {
		w.passthrough = true
		if w.pending != "" {
			w.write(w.pending)
			w.pending = ""
			if w.err != nil {
				return 0, w.err
			}
		}
		return w.ResponseWriter.Write(p)
	}
	if len(w.pending)+len(p)+w.metadataBytes > 8*1024*1024 {
		w.err = fmt.Errorf("正文前 SSE 元数据超过限制")
		return 0, w.err
	}
	w.pending += string(p)
	return len(p), nil
}

func (w *textStreamProtocolWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *textStreamProtocolWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return
	}
	if !strings.Contains(w.ResponseWriter.Header().Get("Content-Type"), "text/event-stream") && w.Status() >= 400 {
		w.write(w.pending)
		w.pending = ""
		w.ResponseWriter.Flush()
		return
	}
	for {
		end := strings.Index(w.pending, "\n\n")
		if end < 0 {
			break
		}
		frame := w.pending[:end+2]
		w.pending = w.pending[end+2:]
		if frame == ": \n\n" {
			w.emitPlaceholder()
			continue
		}
		event, payload := parseTextStreamSSEFrame(frame)
		if payload == "" {
			w.write(frame)
			continue
		}
		kind := gjson.Get(payload, "type").String()
		if kind == "" {
			kind = event
		}
		if !w.real && (kind == "message_start" || kind == "response.created") {
			w.metadata = nil
			w.metadataBytes = 0
			if kind == "message_start" {
				w.inputUsage = nil
				if usage := gjson.Get(payload, "message.usage"); usage.IsObject() {
					w.inputUsage = map[string]any{}
					_ = json.Unmarshal([]byte(usage.Raw), &w.inputUsage)
				}
			}
		}
		semantic := textStreamProtocolSemantic(payload, kind)
		terminal := kind == "message_stop" || kind == "response.completed" || kind == "response.done" || kind == "response.failed" || kind == "error" || strings.TrimSpace(payload) == "[DONE]"
		if !w.real && !semantic && !terminal {
			w.metadata = append(w.metadata, frame)
			w.metadataBytes += len(frame)
			continue
		}
		for _, leading := range w.metadata {
			w.emitFrame(leading)
		}
		w.metadata = nil
		w.metadataBytes = 0
		w.emitFrame(frame)
		if semantic && w.err == nil {
			w.ResponseWriter.Flush()
			w.real = true
			markTextStreamRealOutput(w.c)
			w.state.recordPacket()
		}
	}
	if w.ResponseWriter.Written() {
		w.ResponseWriter.Flush()
	}
}

func textStreamProtocolSemantic(payload, kind string) bool {
	if gjson.Get(payload, "choices").Exists() {
		real, _ := chatStreamPayloadOutput(payload)
		return real
	}
	if strings.HasPrefix(kind, "response.") {
		return !openAIStreamEventIsMetadata(kind) && kind != "response.failed" && kind != "response.completed" && openAIStreamDataStartsClientOutput(payload, kind)
	}
	switch kind {
	case "content_block_start":
		block := gjson.Get(payload, "content_block")
		return block.Get("type").String() == "tool_use" || strings.TrimSpace(block.Get("text").String()) != "" || strings.TrimSpace(block.Get("thinking").String()) != ""
	case "content_block_delta":
		delta := gjson.Get(payload, "delta")
		return strings.TrimSpace(delta.Get("text").String()) != "" || strings.TrimSpace(delta.Get("thinking").String()) != "" || delta.Get("type").String() == "input_json_delta" || delta.Get("type").String() == "signature_delta"
	}
	return false
}

func (w *textStreamProtocolWriter) emitPlaceholder() {
	if w.placeholder || w.real {
		return
	}
	before := w.ResponseWriter.Size()
	if before < 0 {
		before = 0
	}
	w.placeholder = true
	// 丢弃当前尝试的生命周期元数据，避免切号时透传失败账号的消息 ID。
	w.metadata = nil
	w.metadataBytes = 0
	w.responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	w.itemID = "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if strings.HasSuffix(w.c.Request.URL.Path, "/messages") {
		w.frame("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": w.itemID, "type": "message", "role": "assistant", "model": w.c.GetString(textStreamRequestModelKey), "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}})
		w.frame("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		w.frame("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": " "}})
		w.frame("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	} else {
		for _, kind := range []string{"response.created", "response.in_progress"} {
			w.frame(kind, map[string]any{"type": kind, "response": map[string]any{"id": w.responseID, "object": "response", "created_at": w.state.started.Unix(), "model": w.c.GetString(textStreamRequestModelKey), "status": "in_progress", "output": []any{}}})
		}
		w.frame("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": w.itemID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		w.frame("response.content_part.added", map[string]any{"type": "response.content_part.added", "output_index": 0, "item_id": w.itemID, "content_index": 0, "part": part})
		w.frame("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "item_id": w.itemID, "content_index": 0, "delta": " "})
		w.frame("response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": 0, "item_id": w.itemID, "content_index": 0, "text": " "})
		part["text"] = " "
		w.frame("response.content_part.done", map[string]any{"type": "response.content_part.done", "output_index": 0, "item_id": w.itemID, "content_index": 0, "part": part})
		w.frame("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": w.placeholderItem()})
	}
	w.adjustPlaceholderBytes(w.ResponseWriter.Size() - before)
	if w.err == nil {
		w.ResponseWriter.Flush()
		w.state.recordPacket()
	}
}

func (w *textStreamProtocolWriter) adjustPlaceholderBytes(n int) {
	w.state.mu.Lock()
	w.state.bytes += n
	w.state.mu.Unlock()
}

func (w *textStreamProtocolWriter) placeholderItem() map[string]any {
	return map[string]any{"id": w.itemID, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": " ", "annotations": []any{}}}}
}

func (w *textStreamProtocolWriter) emitFrame(frame string) {
	if !w.placeholder {
		w.write(frame)
		return
	}
	event, payload := parseTextStreamSSEFrame(frame)
	var data map[string]any
	if json.Unmarshal([]byte(payload), &data) != nil {
		w.write(frame)
		return
	}
	kind, _ := data["type"].(string)
	if kind == "" {
		kind = event
	}
	if kind == "message_start" {
		message, _ := data["message"].(map[string]any)
		w.inputUsage, _ = message["usage"].(map[string]any)
		return
	}
	if kind == "message_delta" && len(w.inputUsage) > 0 {
		usage, _ := data["usage"].(map[string]any)
		if usage == nil {
			usage = map[string]any{}
		}
		for key, value := range w.inputUsage {
			if key != "output_tokens" {
				if _, ok := usage[key]; !ok {
					usage[key] = value
				}
			}
		}
		data["usage"] = usage
	}
	if idx, ok := data["index"].(float64); ok {
		data["index"] = idx + 1
	}
	if strings.HasPrefix(kind, "response.") {
		if kind == "response.created" || kind == "response.in_progress" {
			return
		}
		if idx, ok := data["output_index"].(float64); ok {
			data["output_index"] = idx + 1
		}
		if _, ok := data["response_id"]; ok {
			data["response_id"] = w.responseID
		}
		if response, ok := data["response"].(map[string]any); ok {
			response["id"] = w.responseID
			if kind == "response.completed" || kind == "response.done" || kind == "response.incomplete" {
				output, _ := response["output"].([]any)
				response["output"] = append([]any{w.placeholderItem()}, output...)
			}
		}
		// 占位已占用前序序号，不能沿用上游本次尝试的 sequence_number。
		data["sequence_number"] = w.sequence
		w.sequence++
	}
	w.writeFrame(kind, data)
}

func (w *textStreamProtocolWriter) frame(event string, data map[string]any) {
	if strings.HasPrefix(event, "response.") {
		data["sequence_number"] = w.sequence
		w.sequence++
	}
	w.writeFrame(event, data)
}

func (w *textStreamProtocolWriter) writeFrame(event string, data map[string]any) {
	payload, err := json.Marshal(data)
	if err != nil {
		w.err = err
		return
	}
	w.write(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))
}

func (w *textStreamProtocolWriter) write(frame string) {
	if w.err != nil {
		return
	}
	n, err := io.WriteString(w.ResponseWriter, frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	w.err = err
}

func parseTextStreamSSEFrame(frame string) (string, string) {
	var event string
	var data []string
	for _, line := range strings.Split(frame, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return event, strings.Join(data, "\n")
}
