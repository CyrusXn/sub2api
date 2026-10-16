package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"golang.org/x/net/http/httpguts"
)

const geminiInteractionDefaultRevision = "2026-05-20"

// ErrGeminiInteractionUsageMissing 区分尚未返回用量与合法的零 token，避免后台轮询提前结算。
var ErrGeminiInteractionUsageMissing = errors.New("Gemini Interactions 用量缺失")

// ErrGeminiInteractionRequestNotSent 表示本地校验失败，上游请求尚未发出。
var ErrGeminiInteractionRequestNotSent = errors.New("Gemini Interactions 请求未发送")

var geminiInteractionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// IsGeminiInteractionAccount 只按通道及显式认证模式判断，不依赖调度快照中的密钥内容。
func IsGeminiInteractionAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformGemini && account.Type == AccountTypeAPIKey &&
		!strings.EqualFold(strings.TrimSpace(account.GetCredential("auth_mode")), "vertex")
}

// DoGeminiInteraction 保留 Interactions 原生协议；不转换 OAuth/Vertex，不重试有副作用的 create。
// 返回体由调用方读取和关闭，后台、steps 和 SSE 不经过现有 Models/OpenAI 兼容转换。
func (s *GeminiMessagesCompatService) DoGeminiInteraction(ctx context.Context, account *Account, method, upstreamID, action string, query url.Values, revision string, body []byte) (response *http.Response, err error) {
	upstreamCalled := false
	defer func() {
		// 本地确定失败可以释放预留；一旦交给传输层，错误也可能已产生上游任务。
		if err != nil && !upstreamCalled {
			err = fmt.Errorf("%w: %v", ErrGeminiInteractionRequestNotSent, err)
		}
	}()
	if !IsGeminiInteractionAccount(account) {
		return nil, errors.New("Gemini Interactions 仅支持 Gemini API Key 账号")
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") || !httpguts.ValidHeaderFieldValue(apiKey) {
		return nil, errors.New("Gemini Interactions 上游密钥无效")
	}
	if s == nil || s.cfg == nil || s.httpUpstream == nil {
		return nil, errors.New("Gemini Interactions 传输服务未配置")
	}
	create := method == http.MethodPost && upstreamID == "" && action == ""
	if !create {
		if len(upstreamID) > 4096 || !geminiInteractionIDPattern.MatchString(upstreamID) {
			return nil, errors.New("Gemini Interactions 资源标识无效")
		}
		if !((method == http.MethodGet || method == http.MethodDelete) && action == "" || method == http.MethodPost && action == "cancel") {
			return nil, errors.New("Gemini Interactions 操作无效")
		}
		if len(body) != 0 {
			return nil, errors.New("Gemini Interactions 资源操作不接受请求体")
		}
	}
	filteredQuery, err := geminiInteractionQuery(method, query)
	if err != nil {
		return nil, err
	}
	if revision == "" {
		revision = geminiInteractionDefaultRevision
	}
	if _, err := time.Parse("2006-01-02", revision); err != nil {
		return nil, errors.New("Gemini Interactions API 修订版本无效")
	}
	base, err := s.validateUpstreamBaseURL(account.GetGeminiBaseURL(geminicli.AIStudioBaseURL))
	if err != nil {
		return nil, errors.New("Gemini Interactions 上游地址不符合安全策略")
	}
	target, err := url.Parse(base)
	if err != nil || target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return nil, errors.New("Gemini Interactions 上游地址无效")
	}
	path := strings.TrimRight(target.Path, "/")
	if !strings.HasSuffix(path, "/v1beta") {
		path += "/v1beta"
	}
	path += "/interactions"
	if !create {
		path += "/" + upstreamID
		if action == "cancel" {
			path += "/cancel"
		}
	}
	target.Path, target.RawPath, target.RawQuery = path, "", filteredQuery.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("Gemini Interactions 请求构造失败")
	}
	req.Header.Set("x-goog-api-key", apiKey)
	req.Header.Set("Api-Revision", revision)
	if create {
		req.Header.Set("Content-Type", "application/json")
	}
	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamCalled = true
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Gemini Interactions 上游请求失败")
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("Gemini Interactions 上游响应无效")
	}
	return resp, nil
}

func geminiInteractionQuery(method string, query url.Values) (url.Values, error) {
	filtered := make(url.Values, len(query))
	for key, values := range query {
		if len(values) != 1 {
			return nil, errors.New("Gemini Interactions 查询参数无效")
		}
		value := values[0]
		valid := false
		switch key {
		case "alt":
			valid = value == "sse"
		case "stream", "include_input":
			valid = method == http.MethodGet && (value == "true" || value == "false")
		case "last_event_id":
			valid = method == http.MethodGet && query.Get("stream") == "true" && value != "" && len(value) <= 4096 && !strings.ContainsAny(value, "\r\n\x00")
		}
		if !valid {
			return nil, errors.New("Gemini Interactions 查询参数无效")
		}
		filtered.Set(key, value)
	}
	return filtered, nil
}

// ParseGeminiInteractionUsage 接受完整 Interaction 或 SSE 的 interaction.* 事件载荷。
// 官方示例 input=7、output=20、thought=22、total=49，thought 不包含在 output 中，只相加一次。
// 参考：https://ai.google.dev/api/interactions#Interaction
func ParseGeminiInteractionUsage(payload []byte) (ClaudeUsage, error) {
	invalid := errors.New("Gemini Interactions 用量格式无效")
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope == nil {
		return ClaudeUsage{}, invalid
	}
	raw := envelope["usage"]
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nested := envelope["interaction"]; len(nested) > 0 {
			if err := json.Unmarshal(nested, &envelope); err != nil {
				return ClaudeUsage{}, invalid
			}
			raw = envelope["usage"]
		}
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ClaudeUsage{}, ErrGeminiInteractionUsageMissing
	}
	var usage struct {
		Input      *int            `json:"total_input_tokens"`
		Cached     int             `json:"total_cached_tokens"`
		Output     *int            `json:"total_output_tokens"`
		Thought    int             `json:"total_thought_tokens"`
		Modalities json.RawMessage `json:"output_tokens_by_modality"`
	}
	if err := json.Unmarshal(raw, &usage); err != nil || usage.Input == nil || usage.Output == nil {
		return ClaudeUsage{}, invalid
	}
	if *usage.Input < 0 || *usage.Output < 0 || usage.Cached < 0 || usage.Thought < 0 || usage.Cached > *usage.Input || usage.Thought > int(^uint(0)>>1)-*usage.Output {
		return ClaudeUsage{}, invalid
	}
	imageTokens := 0
	if len(usage.Modalities) > 0 {
		var details []struct {
			Modality string `json:"modality"`
			Tokens   *int   `json:"tokens"`
		}
		if bytes.Equal(bytes.TrimSpace(usage.Modalities), []byte("null")) || json.Unmarshal(usage.Modalities, &details) != nil {
			return ClaudeUsage{}, invalid
		}
		seen := make(map[string]bool, len(details))
		total := 0
		for _, detail := range details {
			switch detail.Modality {
			case "text", "image", "audio", "video", "document":
			default:
				return ClaudeUsage{}, invalid
			}
			// 模态拆分不得超过总输出，也不能借思考 token 掩盖超量或整型溢出。
			if detail.Tokens == nil || *detail.Tokens < 0 || seen[detail.Modality] || *detail.Tokens > *usage.Output-total {
				return ClaudeUsage{}, invalid
			}
			seen[detail.Modality] = true
			total += *detail.Tokens
			if detail.Modality == "image" {
				imageTokens = *detail.Tokens
			}
		}
	}
	return ClaudeUsage{
		InputTokens:          *usage.Input - usage.Cached,
		CacheReadInputTokens: usage.Cached,
		OutputTokens:         *usage.Output + usage.Thought,
		// 计费层会从 OutputTokens 扣除图片部分；这里保留聚合值，避免重复扣减。
		ImageOutputTokens: imageTokens,
	}, nil
}
