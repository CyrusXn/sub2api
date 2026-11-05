package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// invalid_encrypted_content 失效密文 lineage。
//
// 上游拒绝 reasoning/compaction 密文或代理、工具结果密文后，同一失效密文会随
// 客户端维护的会话历史在后续每一轮重新出现。这里按会话记录已被上游拒绝过的
// encrypted_content 摘要（OpenAIWSStateStore，带 TTL 与容量自保护）；后续请求
// 进场时仅处理摘要命中的项，新生成的密文摘要不同，不会被误删。

const openAIWSFallbackReasonInvalidEncryptedContent = "invalid_encrypted_content"

// 只识别上游的密文校验拒绝，普通参数错误不能触发历史改写。
func isOpenAIInvalidEncryptedContentError(code, message string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	message = strings.ToLower(strings.TrimSpace(message))
	return code == "invalid_encrypted_content" ||
		(code == "thinking_signature_invalid" && strings.Contains(message, "encrypted content") &&
			strings.Contains(message, "could not be verified") && strings.Contains(message, "could not be decrypted or parsed")) ||
		strings.HasPrefix(message, "encrypted function output content could not be decrypted or decoded")
}

type openAIEncryptedContentRecoverySignal struct {
	payload []byte
	message string
}

func (e *openAIEncryptedContentRecoverySignal) Error() string { return e.message }

func newOpenAIEncryptedContentRecoverySignal(payload []byte, message string) error {
	// response.failed 把错误码放在 response.error 中，不能只按 HTTP 错误包读取。
	code := openAIStreamFailedEventErrorCode(payload)
	if code == "" {
		code = extractUpstreamErrorCode(payload)
	}
	if !isOpenAIInvalidEncryptedContentError(code, message) {
		return nil
	}
	return &openAIEncryptedContentRecoverySignal{payload: append([]byte(nil), payload...), message: sanitizeUpstreamErrorMessage(message)}
}

// 仅上游明确拒绝后调用；与 HTTP 恢复共用一次预算，不能在重试循环中无限剥离。
func (s *OpenAIGatewayService) recoverOpenAIEncryptedContentStreamError(c *gin.Context, account *Account, err error, body, entryBody []byte, tried *bool) ([]byte, bool, error) {
	var signal *openAIEncryptedContentRecoverySignal
	if !errors.As(err, &signal) {
		return body, false, err
	}
	if !*tried {
		var decoded map[string]any
		if decodeErr := decodeOpenAIJSONUseNumber(body, &decoded); decodeErr != nil {
			return body, false, decodeErr
		}
		invalidDigests := collectOpenAIEncryptedContentDigestsRaw(entryBody)
		removedReasoning := trimOpenAIEncryptedReasoningItems(decoded)
		replacedToolOutput := replaceOpenAIEncryptedToolOutputContents(decoded, openAIEncryptedContentDigestSet(invalidDigests))
		if removedReasoning || replacedToolOutput > 0 {
			retryBody, marshalErr := marshalOpenAIUpstreamJSON(decoded)
			if marshalErr != nil {
				return body, false, marshalErr
			}
			s.markOpenAIWSInvalidEncryptedContentLineage(getOpenAIGroupIDFromContext(c), s.openAIWSLineageSessionHashFromContext(c, entryBody), invalidDigests)
			*tried = true
			return retryBody, true, nil
		}
	}
	// 不可恢复时保留请求错误语义，不能把同一份坏历史在整个账号池中重放。
	s.recordOpenAIStreamUpstreamError(c, account, account.IsOpenAIPassthroughEnabled(), "", "http_error", signal.payload, signal.message)
	writeOpenAIUpstreamClientError(c, 400, openAIStreamFailedEventPassthroughBody(signal.payload, signal.message), signal.message)
	return body, false, err
}

// openAIWSIngressSessionHashContextKey 在 gin context 中携带 ingress 会话哈希，
// 供 HTTP bridge turn 内的 lineage 记录复用同一会话键。
const openAIWSIngressSessionHashContextKey = "openai_ws_ingress_session_hash"

func openAIEncryptedContentDigest(encrypted string) string {
	sum := sha256.Sum256([]byte(encrypted))
	return hex.EncodeToString(sum[:])
}

// openAIEncryptedLineageItemType 限定 lineage 覆盖的项类型，与剥离端
// sanitizeEncryptedReasoningInputItem 能处理的类型对称；其他类型即使携带
// encrypted_content 也不收摘要，避免记入永远剥不掉的摘要后每轮空转解码。
func openAIEncryptedLineageItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "reasoning", "compaction", "compaction_summary":
		return true
	default:
		return false
	}
}

func openAIEncryptedToolOutputItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "agent_message", "function_call_output", "custom_tool_call_output":
		return true
	default:
		return false
	}
}

func openAIEncryptedToolOutputField(itemType string) string {
	if strings.TrimSpace(itemType) == "agent_message" {
		return "content"
	}
	return "output"
}

func openAIEncryptedContentUnavailablePlaceholder() map[string]any {
	return map[string]any{
		"type": "input_text",
		"text": "[结果不可用，需重新核查：上游无法解密此前的加密工具或代理结果。请重新读取相关数据后继续。]",
	}
}

func appendOpenAIEncryptedContentDigest(digests *[]string, encrypted gjson.Result) {
	if encrypted.Type == gjson.String && encrypted.String() != "" {
		*digests = append(*digests, openAIEncryptedContentDigest(encrypted.String()))
	}
}

func appendOpenAIEncryptedContentDigestsFromItem(digests *[]string, item gjson.Result) {
	itemType := item.Get("type").String()
	if openAIEncryptedLineageItemType(itemType) {
		appendOpenAIEncryptedContentDigest(digests, item.Get("encrypted_content"))
		return
	}
	if !openAIEncryptedToolOutputItemType(itemType) {
		return
	}
	item.Get(openAIEncryptedToolOutputField(itemType)).ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").String() == "encrypted_content" {
			appendOpenAIEncryptedContentDigest(digests, part.Get("encrypted_content"))
		}
		return true
	})
}

func openAIEncryptedContentDigestSet(digests []string) map[string]struct{} {
	if len(digests) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		set[digest] = struct{}{}
	}
	return set
}

func slicesContainOpenAIInvalidDigest(digests []string, invalid map[string]struct{}) bool {
	for _, digest := range digests {
		if _, ok := invalid[digest]; ok {
			return true
		}
	}
	return false
}

// collectOpenAIEncryptedContentDigestsRaw 收集 input 中可恢复的加密历史摘要。
// payload 须遵守 replay 所有权不变式（零拷贝视图解析）。
func collectOpenAIEncryptedContentDigestsRaw(payload []byte) []string {
	if len(payload) == 0 {
		return nil
	}
	input := gjson.Get(openAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return nil
	}
	var digests []string
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			appendOpenAIEncryptedContentDigestsFromItem(&digests, item)
			return true
		})
		return digests
	}
	if input.IsObject() {
		appendOpenAIEncryptedContentDigestsFromItem(&digests, input)
	}
	return digests
}

// 只替换被上游拒绝的摘要，保留工具调用标识、可读结果和新生成的密文。
func replaceOpenAIEncryptedToolOutputItem(item map[string]any, invalid map[string]struct{}) int {
	itemType := firstNonEmptyString(item["type"])
	if !openAIEncryptedToolOutputItemType(itemType) || len(invalid) == 0 {
		return 0
	}
	parts, ok := item[openAIEncryptedToolOutputField(itemType)].([]any)
	if !ok {
		return 0
	}
	replaced := 0
	for i, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok || firstNonEmptyString(part["type"]) != "encrypted_content" {
			continue
		}
		encrypted, ok := part["encrypted_content"].(string)
		if !ok || encrypted == "" {
			continue
		}
		if _, hit := invalid[openAIEncryptedContentDigest(encrypted)]; !hit {
			continue
		}
		parts[i] = openAIEncryptedContentUnavailablePlaceholder()
		replaced++
	}
	return replaced
}

func replaceOpenAIEncryptedToolOutputContents(reqBody map[string]any, invalid map[string]struct{}) int {
	replaced := 0
	switch input := reqBody["input"].(type) {
	case []any:
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok {
				replaced += replaceOpenAIEncryptedToolOutputItem(item, invalid)
			}
		}
	case map[string]any:
		replaced = replaceOpenAIEncryptedToolOutputItem(input, invalid)
	}
	return replaced
}

// stripOpenAIInvalidEncryptedContentItems 剥离 input 中摘要命中 invalid 的密文
// 项，剥离语义与 trimOpenAIEncryptedReasoningItems 对齐：reasoning 仅剥
// encrypted_content（连带 content:null 清理与空骨架删除），compaction/
// compaction_summary 整项删除。返回被改写的项数。
func stripOpenAIInvalidEncryptedContentItems(reqBody map[string]any, invalid map[string]struct{}) int {
	if len(reqBody) == 0 || len(invalid) == 0 {
		return 0
	}
	inputValue, has := reqBody["input"]
	if !has {
		return 0
	}
	stripped := 0
	stripItem := func(item any) (next any, changed bool, keep bool) {
		inputItem, ok := item.(map[string]any)
		if !ok {
			return item, false, true
		}
		if openAIEncryptedToolOutputItemType(firstNonEmptyString(inputItem["type"])) {
			return inputItem, replaceOpenAIEncryptedToolOutputItem(inputItem, invalid) > 0, true
		}
		encrypted, ok := inputItem["encrypted_content"].(string)
		if !ok || encrypted == "" {
			return item, false, true
		}
		if _, hit := invalid[openAIEncryptedContentDigest(encrypted)]; !hit {
			return item, false, true
		}
		return sanitizeEncryptedReasoningInputItem(item)
	}
	switch input := inputValue.(type) {
	case []any:
		filtered := input[:0]
		for _, item := range input {
			nextItem, changed, keep := stripItem(item)
			if changed {
				stripped++
			}
			if !keep {
				continue
			}
			filtered = append(filtered, nextItem)
		}
		if stripped == 0 {
			return 0
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return stripped
		}
		reqBody["input"] = filtered
		return stripped
	case map[string]any:
		nextItem, changed, keep := stripItem(input)
		if !changed {
			return 0
		}
		if !keep {
			delete(reqBody, "input")
			return 1
		}
		nextMap, ok := nextItem.(map[string]any)
		if !ok {
			return 0
		}
		reqBody["input"] = nextMap
		return 1
	default:
		return 0
	}
}

// openAIRawPayloadHasInvalidEncryptedContent 探测 raw payload 是否携带命中
// invalid 摘要的密文项；零命中的常态路径不做任何改写。payload 须遵守 replay
// 所有权不变式（零拷贝视图解析）。
func openAIRawPayloadHasInvalidEncryptedContent(payload []byte, invalid map[string]struct{}) bool {
	if len(payload) == 0 || len(invalid) == 0 {
		return false
	}
	input := gjson.Get(openAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return false
	}
	hit := false
	checkItem := func(item gjson.Result) bool {
		matched := false
		var digests []string
		appendOpenAIEncryptedContentDigestsFromItem(&digests, item)
		for _, digest := range digests {
			if _, ok := invalid[digest]; ok {
				matched = true
				break
			}
		}
		return matched
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if checkItem(item) {
				hit = true
				return false
			}
			return true
		})
		return hit
	}
	if input.IsObject() {
		return checkItem(input)
	}
	return false
}

// stripOpenAIInvalidEncryptedContentFromReplayItems 对 replay 历史序列做同款
// 剥离。有改动时返回新头数组，命中项以重写后的新正文整体替换（遵守 replay
// 所有权不变式，原正文不被修改）；未命中时原样返回。
func stripOpenAIInvalidEncryptedContentFromReplayItems(items []json.RawMessage, invalid map[string]struct{}) ([]json.RawMessage, int) {
	if len(items) == 0 || len(invalid) == 0 {
		return items, 0
	}
	hit := false
	for _, item := range items {
		var digests []string
		appendOpenAIEncryptedContentDigestsFromItem(&digests, gjson.ParseBytes(item))
		if slicesContainOpenAIInvalidDigest(digests, invalid) {
			hit = true
			break
		}
	}
	if !hit {
		return items, 0
	}
	stripped := 0
	next := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var decoded map[string]any
		if err := decodeOpenAIJSONUseNumber(item, &decoded); err != nil {
			next = append(next, item)
			continue
		}
		if replaceOpenAIEncryptedToolOutputItem(decoded, invalid) > 0 {
			rebuilt, err := marshalOpenAIUpstreamJSON(decoded)
			if err != nil {
				next = append(next, item)
				continue
			}
			next = append(next, json.RawMessage(rebuilt))
			stripped++
			continue
		}
		encrypted, ok := decoded["encrypted_content"].(string)
		if !ok || encrypted == "" {
			next = append(next, item)
			continue
		}
		if _, matched := invalid[openAIEncryptedContentDigest(encrypted)]; !matched {
			next = append(next, item)
			continue
		}
		nextItem, changed, keep := sanitizeEncryptedReasoningInputItem(decoded)
		if !changed {
			next = append(next, item)
			continue
		}
		stripped++
		if !keep {
			continue
		}
		rebuilt, err := marshalOpenAIUpstreamJSON(nextItem)
		if err != nil {
			next = append(next, item)
			stripped--
			continue
		}
		next = append(next, json.RawMessage(rebuilt))
	}
	if stripped == 0 {
		return items, 0
	}
	return next, stripped
}

// stripOpenAIInvalidEncryptedContentRaw 是 raw JSON 版剥离：先零成本探测，命中
// 才解码改写重编码。返回改写后的 payload 与剥离项数；未命中时原样返回。
func stripOpenAIInvalidEncryptedContentRaw(payload []byte, invalid map[string]struct{}) ([]byte, int, error) {
	if !openAIRawPayloadHasInvalidEncryptedContent(payload, invalid) {
		return payload, 0, nil
	}
	var decoded map[string]any
	if err := decodeOpenAIJSONUseNumber(payload, &decoded); err != nil {
		return payload, 0, err
	}
	stripped := stripOpenAIInvalidEncryptedContentItems(decoded, invalid)
	if stripped == 0 {
		return payload, 0, nil
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(decoded)
	if err != nil {
		return payload, 0, err
	}
	return rebuilt, stripped, nil
}

// markOpenAIWSInvalidEncryptedContentLineage 把本次被上游拒绝的密文摘要写入
// 会话 lineage。digests 须在剥离前收集。
func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineage(groupID int64, sessionHash string, digests []string) {
	if s == nil || len(digests) == 0 || strings.TrimSpace(sessionHash) == "" {
		return
	}
	stateStore := s.getOpenAIWSStateStore()
	if stateStore == nil {
		return
	}
	stateStore.MarkSessionInvalidEncryptedContent(groupID, sessionHash, digests, s.openAIWSSessionStickyTTL())
}

// sessionInvalidEncryptedContentDigests 返回会话已知失效密文摘要；全局无记录
// 时（常态）零成本返回 nil。
func (s *OpenAIGatewayService) sessionInvalidEncryptedContentDigests(groupID int64, sessionHash string) map[string]struct{} {
	if s == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	stateStore := s.getOpenAIWSStateStore()
	if stateStore == nil || !stateStore.HasAnySessionInvalidEncryptedContent() {
		return nil
	}
	return stateStore.GetSessionInvalidEncryptedContentDigests(groupID, sessionHash)
}

// openAIWSLineageSessionHashFromContext 取 lineage 会话键：优先 ingress 循环
// 写入的会话哈希（与读取侧同键），否则按请求体派生。
func (s *OpenAIGatewayService) openAIWSLineageSessionHashFromContext(c *gin.Context, body []byte) string {
	if c != nil {
		if fromCtx := strings.TrimSpace(c.GetString(openAIWSIngressSessionHashContextKey)); fromCtx != "" {
			return fromCtx
		}
	}
	return s.GenerateSessionHash(c, body)
}

// markOpenAIWSInvalidEncryptedContentLineageFromPayload 在上游以
// invalid_encrypted_content 拒绝 payload 时记录其密文摘要并输出观测日志。
func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineageFromPayload(
	c *gin.Context,
	payload []byte,
	logKey string,
	accountID int64,
	turn int,
) {
	digests := collectOpenAIEncryptedContentDigestsRaw(payload)
	if len(digests) == 0 {
		return
	}
	s.markOpenAIWSInvalidEncryptedContentLineage(
		getOpenAIGroupIDFromContext(c),
		s.openAIWSLineageSessionHashFromContext(c, payload),
		digests,
	)
	logOpenAIWSModeInfo("%s account_id=%d turn=%d digests=%d", logKey, accountID, turn, len(digests))
}

// stripSessionInvalidEncryptedContentLogged 对 payload 执行会话失效密文剥离并
// 输出观测日志（logKey / logKey+"_skip"），返回（可能已替换的）payload 与剥离
// 项数；未命中或剥离失败时原样返回。
func (s *OpenAIGatewayService) stripSessionInvalidEncryptedContentLogged(
	payload []byte,
	invalid map[string]struct{},
	logKey string,
	accountID int64,
	turn int,
) ([]byte, int) {
	strippedPayload, strippedCount, stripErr := stripOpenAIInvalidEncryptedContentRaw(payload, invalid)
	if stripErr != nil {
		logOpenAIWSModeInfo(
			"%s_skip account_id=%d turn=%d reason=strip_error cause=%s",
			logKey,
			accountID,
			turn,
			truncateOpenAIWSLogValue(stripErr.Error(), openAIWSLogValueMaxLen),
		)
		return payload, 0
	}
	if strippedCount > 0 {
		logOpenAIWSModeInfo(
			"%s account_id=%d turn=%d stripped_items=%d",
			logKey,
			accountID,
			turn,
			strippedCount,
		)
	}
	return strippedPayload, strippedCount
}
