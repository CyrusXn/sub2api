package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type openAIIdentityRoutingContextKey struct{}

type openAIIdentityRoutingState struct {
	active  bool
	visited map[int64]struct{}
}

// WithOpenAIIdentityRouting 标记本次请求需要按身份账号链路调度。
func WithOpenAIIdentityRouting(ctx context.Context, enabled bool) context.Context {
	if !enabled {
		return ctx
	}
	return context.WithValue(ctx, openAIIdentityRoutingContextKey{}, &openAIIdentityRoutingState{visited: make(map[int64]struct{})})
}

// IsOpenAIIdentityRoutingActive 仅在实际遇到身份账号配置后关闭普通重试与粘性写入。
func IsOpenAIIdentityRoutingActive(ctx context.Context) bool {
	state, _ := ctx.Value(openAIIdentityRoutingContextKey{}).(*openAIIdentityRoutingState)
	return state != nil && state.active
}

const (
	accountIdentityReplyContentKey = "identity_reply_content"
	accountIdentityAccountIDKey    = "identity_account_id"
)
const defaultOpenAIIdentityReplyContent = "我是 ChatGPT，由 OpenAI训练的 AI 助手。我可以用中文和你交流，帮你解答问题、写作、翻译、编程等。"

// applyOpenAIPlainChatIdentity 按账号配置处理无工具、无调用方身份设定的 OpenAI APIKey 身份问答。
// 判断必须使用入站请求，不能使用已统一为编码客户端指纹的出站头。
func applyOpenAIPlainChatIdentity(c *gin.Context, account *Account, body []byte, testPrompt *string) ([]byte, error) {
	if account == nil || !account.IsOpenAIApiKey() {
		return body, nil
	}
	reply, _ := account.Extra[accountIdentityReplyContentKey].(string)
	reply = strings.TrimSpace(reply)
	// 留空直接透传，避免普通账号承担请求解析及分组查询开销。
	if reply == "" {
		return body, nil
	}
	_, ok := openAIPlainIdentityQuestionFromRequest(c, body, testPrompt)
	if !ok {
		return body, nil
	}
	instructions := "当前用户在进行普通对话，没有编程工具客户端身份。当用户询问你的身份时，即使历史回答包含其他身份，也只逐字回复下面这段介绍，不要补充模型版本、客户端、界面或运行环境等说明：\n" + reply
	if !gjson.GetBytes(body, "messages").Exists() {
		return sjson.SetBytes(body, "instructions", instructions)
	}
	identity, err := json.Marshal(map[string]string{"role": "system", "content": instructions})
	if err != nil {
		return nil, err
	}
	items := []json.RawMessage{identity}
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		items = append(items, json.RawMessage(message.Raw))
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "messages", encoded)
}

// IsOpenAIPlainIdentityRequest 复用身份回复的请求边界，避免工具任务被误切账号。
func IsOpenAIPlainIdentityRequest(c *gin.Context, body []byte) bool {
	_, ok := openAIPlainIdentityQuestionFromRequest(c, body, nil)
	return ok
}

func openAIPlainIdentityQuestionFromRequest(c *gin.Context, body []byte, testPrompt *string) (string, bool) {
	if !gjson.ValidBytes(body) {
		return "", false
	}
	if testPrompt == nil && c != nil && c.Request != nil {
		for _, name := range []string{"Originator", "X-Client-Name", "X-OpenCode-Session", "X-Codex-Window-ID"} {
			if strings.TrimSpace(c.GetHeader(name)) != "" {
				return "", false
			}
		}
		ua := strings.ToLower(c.Request.UserAgent())
		for _, client := range []string{"codex", "trae", "qoder", "deepseekharness", "cursor", "cline", "roo-code", "opencode", "claude-code", "kilo", "windsurf", "aider"} {
			if strings.Contains(ua, client) {
				return "", false
			}
		}
	}
	root := gjson.ParseBytes(body)
	for _, field := range []string{"instructions", "system", "prompt", "tools", "functions", "tool_choice", "function_call", "client_metadata", "metadata", "previous_response_id", "conversation", "response_format", "text.format"} {
		value := root.Get(field)
		if value.IsArray() && len(value.Array()) == 0 {
			continue
		}
		if value.Exists() && value.Type != gjson.Null && strings.TrimSpace(value.String()) != "" {
			return "", false
		}
	}
	messages := root.Get("messages")
	input := root.Get("input")
	if messages.Exists() {
		input = messages
	}
	question := ""
	if input.Type == gjson.String {
		question = input.String()
	} else if input.IsArray() {
		for _, message := range input.Array() {
			role := message.Get("role").String()
			kind := message.Get("type").String()
			if (role != "user" && role != "assistant") || (kind != "" && kind != "message") || message.Get("tool_calls").Exists() || message.Get("function_call").Exists() {
				return "", false
			}
			question = ""
			content := message.Get("content")
			if content.Type == gjson.String {
				if role == "user" {
					question = content.String()
				}
			} else if content.IsArray() {
				for _, part := range content.Array() {
					if kind := part.Get("type").String(); kind != "text" && kind != "input_text" && kind != "output_text" {
						return "", false
					}
					if role == "user" {
						question += part.Get("text").String()
					}
				}
			}
		}
	}
	if testPrompt != nil {
		// 站内测试使用 handler 提供的真实最后用户文本，不从正文中的角色标记猜测边界。
		question = *testPrompt
	}
	return question, isOpenAIPlainIdentityQuestion(question)
}

func isOpenAIPlainIdentityQuestion(question string) bool {
	question = strings.Trim(strings.ToLower(strings.TrimSpace(question)), " \t\r\n?？。.!！,，")
	for _, prefix := range []string{"请问一下", "请问"} {
		if strings.HasPrefix(question, prefix) {
			question = strings.TrimSpace(strings.TrimPrefix(question, prefix))
			break
		}
	}
	question = strings.TrimLeft(question, "，,：: ")
	// 只放宽问句尾部的语气词，仍要求整个短句匹配，避免任务正文的关键词误触发。
	for _, suffix := range []string{"啊", "呀", "呢", "吗"} {
		if strings.HasSuffix(question, suffix) {
			question = strings.TrimSpace(strings.TrimSuffix(question, suffix))
			break
		}
	}
	switch question {
	case "你是", "你是谁", "你是什么", "你是什么模型", "你是什么大模型", "你用的是什么模型", "你用的什么模型", "模型", "大模型", "什么模型", "什么大模型", "你是什么助手", "你是哪个模型", "你是哪个大模型", "你叫什么", "你叫什么名字", "介绍一下你自己", "介绍你自己", "请介绍自己", "请介绍一下你自己", "你是谁，你能做什么", "who are you", "what are you", "what model are you", "introduce yourself":
		return true
	default:
		return false
	}
}
