package service

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIPlainChatIdentityInstructions = "当前用户在进行普通对话，没有编程工具客户端身份。当用户询问你的身份时，即使历史回答包含其他身份，也只逐字回复下面这段介绍，不要补充模型版本、客户端、界面或运行环境等说明：\n我是 ChatGPT，由 OpenAI训练的 AI 助手。我可以用中文和你交流，帮你解答问题、写作、翻译、编程等。"

// applyOpenAIPlainChatIdentity 仅修正无工具、无调用方身份设定的 OpenAI APIKey 身份问答。
// 判断必须使用入站请求，不能使用已统一为编码客户端指纹的出站头。
func applyOpenAIPlainChatIdentity(c *gin.Context, account *Account, body []byte, testPrompt *string) ([]byte, error) {
	if account == nil || !account.IsOpenAIApiKey() || !gjson.ValidBytes(body) {
		return body, nil
	}
	if testPrompt == nil && c != nil && c.Request != nil {
		for _, name := range []string{"Originator", "X-Client-Name", "X-OpenCode-Session", "X-Codex-Window-ID"} {
			if strings.TrimSpace(c.GetHeader(name)) != "" {
				return body, nil
			}
		}
		ua := strings.ToLower(c.Request.UserAgent())
		for _, client := range []string{"codex", "trae", "qoder", "deepseekharness", "cursor", "cline", "roo-code", "opencode", "claude-code", "kilo", "windsurf", "aider"} {
			if strings.Contains(ua, client) {
				return body, nil
			}
		}
	}
	root := gjson.ParseBytes(body)
	model := root.Get("model").String()
	if testPrompt == nil {
		model = account.GetMappedModel(model)
	}
	// OpenAI 兼容账号也可能转发其他厂商的模型，不能给它们设置 ChatGPT 身份。
	if !isOpenAICodexGPTModel(model) {
		return body, nil
	}
	for _, field := range []string{"instructions", "system", "prompt", "tools", "functions", "tool_choice", "function_call", "client_metadata", "metadata", "previous_response_id", "conversation", "response_format", "text.format"} {
		value := root.Get(field)
		if value.IsArray() && len(value.Array()) == 0 {
			continue
		}
		if value.Exists() && value.Type != gjson.Null && strings.TrimSpace(value.String()) != "" {
			return body, nil
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
				return body, nil
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
						return body, nil
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
	if !isOpenAIPlainIdentityQuestion(question) {
		return body, nil
	}
	if !messages.Exists() {
		return sjson.SetBytes(body, "instructions", openAIPlainChatIdentityInstructions)
	}
	identity, err := json.Marshal(map[string]string{"role": "system", "content": openAIPlainChatIdentityInstructions})
	if err != nil {
		return nil, err
	}
	items := []json.RawMessage{identity}
	for _, message := range messages.Array() {
		items = append(items, json.RawMessage(message.Raw))
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "messages", encoded)
}

func isOpenAIPlainIdentityQuestion(question string) bool {
	question = strings.Trim(strings.ToLower(strings.TrimSpace(question)), " \t\r\n?？。.!！")
	question = strings.TrimSpace(strings.TrimPrefix(question, "请问"))
	question = strings.TrimLeft(question, "，,：: ")
	switch question {
	case "你是", "你是谁", "你是谁啊", "你是谁呀", "你是什么", "你是什么模型", "你是什么助手", "你是哪个模型", "你叫什么", "你叫什么名字", "介绍一下你自己", "介绍你自己", "请介绍自己", "请介绍一下你自己", "你是谁，你能做什么", "who are you", "what are you", "introduce yourself":
		return true
	default:
		return false
	}
}
