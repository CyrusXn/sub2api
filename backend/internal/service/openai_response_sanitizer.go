package service

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/tidwall/gjson"
)

var openAIErrorModelPattern = regexp.MustCompile(`(?i)\b(?:gpt|o[1-9]|claude|gemini|grok|deepseek|qwen|kimi|glm|ernie|llama|mistral)[a-z0-9._:-]*\b`)

var openAISensitiveJSONKeys = map[string]struct{}{
	"provider":               {},
	"vendor":                 {},
	"model_id":               {},
	"model_version":          {},
	"provider_model":         {},
	"resolved_model":         {},
	"upstream_model":         {},
	"system_fingerprint":     {},
	"provider_metadata":      {},
	"vendor_metadata":        {},
	"upstream_metadata":      {},
	"provider_response_meta": {},
}

var openAIStandardTopLevelKeys = map[string]struct{}{
	// Responses/Chat response objects.
	"id": {}, "object": {}, "created": {}, "created_at": {}, "status": {},
	"background": {}, "error": {}, "incomplete_details": {}, "instructions": {},
	"max_output_tokens": {}, "max_tool_calls": {}, "model": {}, "output": {},
	"parallel_tool_calls": {}, "previous_response_id": {}, "prompt": {},
	"prompt_cache_key": {}, "prompt_cache_retention": {}, "reasoning": {},
	"safety_identifier": {}, "service_tier": {}, "store": {}, "temperature": {},
	"text": {}, "tool_choice": {}, "tools": {}, "top_logprobs": {}, "top_p": {},
	"truncation": {}, "usage": {}, "user": {}, "metadata": {}, "choices": {},
	// Responses/Chat stream event envelopes.
	"type": {}, "sequence_number": {}, "response": {}, "item": {}, "output_index": {},
	"content_index": {}, "delta": {}, "logprobs": {}, "annotation": {},
	"annotation_index": {}, "part": {}, "arguments": {}, "name": {}, "item_id": {},
	"summary_index": {}, "refusal": {}, "code": {}, "message": {}, "param": {},
	// Other standard OpenAI JSON response containers used by compatible routes.
	"data": {}, "has_more": {}, "first_id": {}, "last_id": {}, "deleted": {},
}

// sanitizeOpenAIResponseJSON 只净化 Responses/Chat 的协议元数据；output 内的
// 工具参数和 assistant 正文保持原样，避免改变下游业务内容。
func sanitizeOpenAIResponseJSON(body []byte, requestedModel, upstreamModel string) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	if strings.TrimSpace(requestedModel) == "" {
		requestedModel = "unknown"
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if !sanitizeOpenAIJSONValue(payload, requestedModel, upstreamModel, nil) {
		// 没有需要净化的字段时保留原始字节，避免破坏透传 JSON 的键顺序和格式。
		return body
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updated
}

func sanitizeOpenAIJSONValue(value any, requestedModel, upstreamModel string, path []string) bool {
	changed := false
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if shouldDropOpenAIJSONKey(lowerKey) {
				delete(current, key)
				changed = true
				continue
			}
			if lowerKey == "model" {
				if model, ok := child.(string); !ok || model != requestedModel {
					current[key] = requestedModel
					changed = true
				}
				continue
			}
			if lowerKey == "message" && openAIJSONPathContains(path, "error") {
				if message, ok := child.(string); ok {
					sanitized := sanitizeOpenAIErrorMessage(message, requestedModel, upstreamModel)
					if sanitized != message {
						current[key] = sanitized
						changed = true
					}
				}
				continue
			}
			if (lowerKey == "code" || lowerKey == "param") && openAIJSONPathContains(path, "error") {
				if text, ok := child.(string); ok {
					sanitized := sanitizeOpenAIErrorMessage(text, requestedModel, upstreamModel)
					if sanitized != text {
						current[key] = sanitized
						changed = true
					}
				}
				continue
			}
			// 工具参数、用户 metadata、输入和 assistant 正文可能是任意 JSON，属于业务正文，不能净化。
			if lowerKey == "arguments" || lowerKey == "metadata" || lowerKey == "input" ||
				lowerKey == "content" || lowerKey == "text" || lowerKey == "refusal" ||
				lowerKey == "tools" || lowerKey == "parameters" || lowerKey == "properties" {
				continue
			}
			changed = sanitizeOpenAIJSONValue(child, requestedModel, upstreamModel, append(path, lowerKey)) || changed
		}
	case []any:
		for _, child := range current {
			changed = sanitizeOpenAIJSONValue(child, requestedModel, upstreamModel, path) || changed
		}
	}
	return changed
}

func isOpenAIStandardTopLevelKey(key string) bool {
	_, ok := openAIStandardTopLevelKeys[key]
	return ok
}

func shouldDropOpenAIJSONKey(key string) bool {
	if _, sensitive := openAISensitiveJSONKeys[key]; sensitive {
		return true
	}
	if isOpenAIStandardTopLevelKey(key) {
		return false
	}
	for _, prefix := range []string{
		"x_", "x-", "vendor_", "provider_", "upstream_", "openai_", "anthropic_", "xai_", "azure_", "grok_", "gemini_",
	} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return strings.HasPrefix(key, "_") || strings.Contains(key, ":")
}

func openAIJSONPathContains(path []string, want string) bool {
	for _, part := range path {
		if part == want {
			return true
		}
	}
	return false
}

func sanitizeOpenAIResponseSSEData(data []byte, requestedModel, upstreamModel string) []byte {
	if string(strings.TrimSpace(string(data))) == "[DONE]" {
		return data
	}
	return sanitizeOpenAIResponseJSON(data, requestedModel, upstreamModel)
}

func sanitizeOpenAIResponseSSEBody(body, requestedModel, upstreamModel string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		data, ok := extractOpenAISSEDataLine(line)
		if !ok || strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		lines[i] = "data: " + string(sanitizeOpenAIResponseSSEData([]byte(data), requestedModel, upstreamModel))
	}
	return strings.Join(lines, "\n")
}

// sanitizeOpenAIResponseHeaders 使用安全基础白名单，并强制移除上游身份/模型头；
// 即使管理员配置 additional_allowed，也不能让这些头重新暴露给下游。
func sanitizeOpenAIResponseHeaders(src http.Header, filter *responseheaders.CompiledHeaderFilter) http.Header {
	filtered := responseheaders.FilterHeaders(src, filter)
	for key := range filtered {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "x-request-id" || lower == "request-id" || strings.HasPrefix(lower, "x-") && !strings.HasPrefix(lower, "x-ratelimit-") ||
			strings.HasPrefix(lower, "openai-") || strings.Contains(lower, "model") ||
			strings.Contains(lower, "provider") || strings.Contains(lower, "vendor") ||
			strings.Contains(lower, "upstream") || strings.Contains(lower, "debug") ||
			strings.Contains(lower, "trace") || strings.HasPrefix(lower, "anthropic-") ||
			strings.HasPrefix(lower, "xai-") {
			delete(filtered, key)
		}
	}
	return filtered
}

func copySanitizedOpenAIResponseHeaders(dst, src http.Header, filter *responseheaders.CompiledHeaderFilter) {
	if dst == nil {
		return
	}
	for key, values := range sanitizeOpenAIResponseHeaders(src, filter) {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func sanitizeOpenAIErrorMessage(message, requestedModel, upstreamModel string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "Upstream request failed"
	}
	for _, model := range []string{strings.TrimSpace(upstreamModel), strings.TrimSpace(requestedModel)} {
		if model != "" {
			message = strings.ReplaceAll(message, model, requestedModel)
		}
	}
	return openAIErrorModelPattern.ReplaceAllString(message, requestedModel)
}

// SanitizeOpenAIErrorMessage 为跨 handler 的 OpenAI failover 错误出口提供统一净化入口。
func SanitizeOpenAIErrorMessage(message, requestedModel, upstreamModel string) string {
	return sanitizeOpenAIErrorMessage(message, requestedModel, upstreamModel)
}

func openAIErrorModelPair(models []string) (requestedModel, upstreamModel string) {
	if len(models) == 0 {
		return "", ""
	}
	requestedModel = strings.TrimSpace(models[0])
	if len(models) > 1 {
		upstreamModel = strings.TrimSpace(models[1])
	} else {
		upstreamModel = requestedModel
	}
	return requestedModel, upstreamModel
}

func sanitizeOpenAIErrorBody(body []byte, requestedModel, upstreamModel string) []byte {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return marshalSanitizedOpenAIError("upstream_error", "Upstream request failed", requestedModel, upstreamModel)
	}
	errorValue, ok := root["error"].(map[string]any)
	if !ok {
		return marshalSanitizedOpenAIError("upstream_error", "Upstream request failed", requestedModel, upstreamModel)
	}

	clean := make(map[string]any, 4)
	for _, key := range []string{"type", "code", "param"} {
		if value, exists := errorValue[key]; exists {
			if text, ok := value.(string); ok {
				clean[key] = sanitizeOpenAIErrorMessage(text, requestedModel, upstreamModel)
			} else {
				clean[key] = value
			}
		}
	}
	message, _ := errorValue["message"].(string)
	clean["message"] = sanitizeOpenAIErrorMessage(message, requestedModel, upstreamModel)
	result, err := json.Marshal(map[string]any{"error": clean})
	if err != nil {
		return marshalSanitizedOpenAIError("upstream_error", "Upstream request failed", requestedModel, upstreamModel)
	}
	return result
}

func marshalSanitizedOpenAIError(errType, message, requestedModel, upstreamModel string) []byte {
	result, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": sanitizeOpenAIErrorMessage(message, requestedModel, upstreamModel),
		},
	})
	if err != nil {
		return []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	}
	return result
}
