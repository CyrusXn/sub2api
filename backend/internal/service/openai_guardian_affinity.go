package service

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	codexAutoReviewModel             = "codex-auto-review"
	openAICodexSubagentFallbackModel = "gpt-5.6-sol"
	openAISubagentHeader             = "x-openai-subagent"
	codexParentThreadIDHeader        = "x-codex-parent-thread-id"
	codexTurnMetadataHeader          = "x-codex-turn-metadata"
)

type openAIGuardianParentAffinityContextKey struct{}

type openAIGuardianParentAffinity struct {
	currentSessionHash string
	legacySessionHash  string
}

// OpenAICodexThreadModelCache 是 Codex 父线程模型继承所需的最小缓存能力。
// 独立于 GatewayCache 主接口，避免让无关测试桩承担这一可选能力。
type OpenAICodexThreadModelCache interface {
	SetOpenAICodexThreadModel(ctx context.Context, apiKeyID int64, threadID, model string, ttl time.Duration) error
	GetOpenAICodexThreadModel(ctx context.Context, apiKeyID int64, threadID string) (string, error)
}

// ApplyOpenAICodexSubagentModelInheritance 让明确属于 collab_spawn、guardian
// 或 review 的 Codex 子请求继承同一 API Key 下父线程最近使用的模型。父模型
// 暂时不可用时，仅将 Luna/自动审核别名降级到本站默认 Sol；血缘来源冲突、
// 其他模型及其他内部任务均保持原请求。
func (s *OpenAIGatewayService) ApplyOpenAICodexSubagentModelInheritance(
	ctx context.Context,
	c *gin.Context,
	apiKeyID int64,
	body []byte,
	requestedModel string,
) ([]byte, string, string, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	if s == nil || ctx == nil || c == nil || apiKeyID <= 0 || requestedModel == "" || IsOpenAIResponsesCompactPath(c) {
		return body, requestedModel, "", false
	}
	var cache OpenAICodexThreadModelCache
	if s.cache != nil {
		cache, _ = s.cache.(OpenAICodexThreadModelCache)
	}

	payload := openAIRequestPayloadView(body)
	subagentKind, parentThreadID, lineageOK := openAICodexSubagentLineage(c, payload)
	if !lineageOK {
		return body, requestedModel, "", false
	}

	if subagentKind == "" && parentThreadID == "" {
		headerMetadata := c.GetHeader(codexTurnMetadataHeader)
		bodyMetadata := payload.Get("client_metadata.x-codex-turn-metadata").String()
		threadID, threadOK := unambiguousOpenAICodexValue(false,
			codexThreadIDFromMetadata(headerMetadata),
			codexThreadIDFromMetadata(bodyMetadata),
			payload.Get("client_metadata.thread_id").String(),
		)
		if cache != nil && threadOK && threadID != "" && !isUnsupportedOpenAICodexSubagentModel(requestedModel) {
			_ = cache.SetOpenAICodexThreadModel(ctx, apiKeyID, threadID, requestedModel, s.openAIWSSessionStickyTTL())
		}
		return body, requestedModel, "", false
	}

	if !isInheritedOpenAICodexSubagentKind(subagentKind) {
		return body, requestedModel, subagentKind, false
	}
	effectiveModel := ""
	if cache != nil && parentThreadID != "" {
		parentModel, err := cache.GetOpenAICodexThreadModel(ctx, apiKeyID, parentThreadID)
		if err == nil && !isUnsupportedOpenAICodexSubagentModel(parentModel) {
			effectiveModel = strings.TrimSpace(parentModel)
		}
	}
	if effectiveModel == "" && isUnsupportedOpenAICodexSubagentModel(requestedModel) {
		effectiveModel = openAICodexSubagentFallbackModel
	}
	if effectiveModel == "" || effectiveModel == requestedModel {
		return body, requestedModel, subagentKind, false
	}

	modelPath := "model"
	root := parseRawJSONView(body)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(root.Get("type").String())), "response.") && root.Get("response").IsObject() {
		modelPath = "response.model"
	}
	updated, err := sjson.SetBytes(body, modelPath, effectiveModel)
	if err != nil {
		return body, requestedModel, subagentKind, false
	}
	return updated, effectiveModel, subagentKind, true
}

// ShouldDeferOpenAICodexSubagentModelAllowlist 判断 Responses HTTP 请求是否需要
// 等模型继承/兜底完成后再校验分组白名单。只放行无歧义的可信子智能体标记，
// 且所有客户端 model 候选必须指向同一个 Luna/自动审核模型。
func ShouldDeferOpenAICodexSubagentModelAllowlist(c *gin.Context, body []byte, modelCandidates []string) bool {
	if c == nil || IsOpenAIResponsesCompactPath(c) {
		return false
	}
	kind, _, ok := openAICodexSubagentLineage(c, openAIRequestPayloadView(body))
	if !ok || !isInheritedOpenAICodexSubagentKind(kind) {
		return false
	}
	model := ""
	for _, candidate := range modelCandidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if model != "" && model != candidate {
			return false
		}
		model = candidate
	}
	return isUnsupportedOpenAICodexSubagentModel(model)
}

func openAICodexSubagentLineage(c *gin.Context, payload gjson.Result) (string, string, bool) {
	if c == nil {
		return "", "", false
	}
	headerMetadata := c.GetHeader(codexTurnMetadataHeader)
	bodyMetadata := payload.Get("client_metadata.x-codex-turn-metadata").String()
	subagentKind, kindOK := unambiguousOpenAICodexValue(true,
		c.GetHeader(openAISubagentHeader),
		codexSubagentKindFromMetadata(headerMetadata),
		codexSubagentKindFromMetadata(bodyMetadata),
		payload.Get("client_metadata.subagent_kind").String(),
	)
	parentThreadID, parentOK := unambiguousOpenAICodexValue(false,
		c.GetHeader(codexParentThreadIDHeader),
		codexParentThreadIDFromMetadata(headerMetadata),
		codexParentThreadIDFromMetadata(bodyMetadata),
		payload.Get("client_metadata.parent_thread_id").String(),
	)
	return subagentKind, parentThreadID, kindOK && parentOK
}

func unambiguousOpenAICodexValue(lower bool, candidates ...string) (string, bool) {
	value := ""
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if lower {
			candidate = strings.ToLower(candidate)
		}
		if candidate == "" {
			continue
		}
		if value != "" && value != candidate {
			return "", false
		}
		value = candidate
	}
	return value, true
}

func isInheritedOpenAICodexSubagentKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "collab_spawn", "guardian", "review":
		return true
	default:
		return false
	}
}

func isUnsupportedOpenAICodexSubagentModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case codexAutoReviewModel, "gpt-5.6-luna":
		return true
	default:
		return false
	}
}

// WithOpenAIGuardianParentAffinity records a Codex review request's parent
// thread as a routing hint. The hint is resolved against the current group's
// sticky-session namespace later; client headers never carry an account ID.
func WithOpenAIGuardianParentAffinity(ctx context.Context, c *gin.Context, body []byte, requestedModel string) context.Context {
	if ctx == nil || c == nil || !strings.EqualFold(strings.TrimSpace(requestedModel), codexAutoReviewModel) {
		return ctx
	}

	headerMetadata := c.GetHeader(codexTurnMetadataHeader)
	bodyMetadata := openAIRequestPayloadView(body).Get("client_metadata.x-codex-turn-metadata").String()
	if !hasUnambiguousOpenAICodexReviewSubagent(
		c.GetHeader(openAISubagentHeader),
		codexSubagentKindFromMetadata(headerMetadata),
		codexSubagentKindFromMetadata(bodyMetadata),
	) {
		return ctx
	}

	parentID := ""
	for _, candidate := range []string{
		strings.TrimSpace(c.GetHeader(codexParentThreadIDHeader)),
		codexParentThreadIDFromMetadata(headerMetadata),
		codexParentThreadIDFromMetadata(bodyMetadata),
	} {
		if candidate == "" {
			continue
		}
		if parentID != "" && parentID != candidate {
			return ctx
		}
		parentID = candidate
	}
	if parentID == "" {
		return ctx
	}

	currentHash, legacyHash := deriveOpenAISessionHashes(parentID)
	if currentHash == "" {
		return ctx
	}
	return context.WithValue(ctx, openAIGuardianParentAffinityContextKey{}, openAIGuardianParentAffinity{
		currentSessionHash: currentHash,
		legacySessionHash:  legacyHash,
	})
}

func codexParentThreadIDFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return ""
	}
	return strings.TrimSpace(gjson.Get(raw, "parent_thread_id").String())
}

func codexThreadIDFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return ""
	}
	return strings.TrimSpace(gjson.Get(raw, "thread_id").String())
}

func codexSubagentKindFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return ""
	}
	return strings.TrimSpace(gjson.Get(raw, "subagent_kind").String())
}

func hasUnambiguousOpenAICodexReviewSubagent(candidates ...string) bool {
	subagent := ""
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if subagent != "" && subagent != candidate {
			return false
		}
		subagent = candidate
	}
	return subagent == "guardian" || subagent == "review"
}

func openAIGuardianParentAffinityFromContext(ctx context.Context) (openAIGuardianParentAffinity, bool) {
	if ctx == nil {
		return openAIGuardianParentAffinity{}, false
	}
	affinity, ok := ctx.Value(openAIGuardianParentAffinityContextKey{}).(openAIGuardianParentAffinity)
	return affinity, ok && affinity.currentSessionHash != ""
}

func preserveOpenAIGuardianParentBinding(ctx context.Context, sessionHash string) bool {
	affinity, ok := openAIGuardianParentAffinityFromContext(ctx)
	if !ok {
		return false
	}
	sessionHash = strings.TrimSpace(sessionHash)
	return sessionHash != "" && (sessionHash == affinity.currentSessionHash || sessionHash == affinity.legacySessionHash)
}

func (s *OpenAIGatewayService) resolveOpenAIGuardianParentAccountID(ctx context.Context, groupID *int64) int64 {
	if s == nil || s.cache == nil {
		return 0
	}
	affinity, ok := openAIGuardianParentAffinityFromContext(ctx)
	if !ok {
		return 0
	}
	lookupCtx := withOpenAILegacySessionHash(ctx, affinity.legacySessionHash)
	accountID, err := s.getStickySessionAccountID(lookupCtx, groupID, affinity.currentSessionHash)
	if err != nil || accountID <= 0 {
		return 0
	}
	return accountID
}
