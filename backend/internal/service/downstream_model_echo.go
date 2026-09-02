package service

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 下游模型回显。
//
// 上游经常回带日期快照或运行时变体模型名（例如下游请求 gpt-5.6-sol，本站实际调用
// gpt-5.5，上游响应声明 gpt-5.5-2026-04-23）。这些模型名一旦透传给下游，下游
// sub2api 会用与本站相同的审计逻辑把它判定为「模型不一致」。
//
// 这里统一把出站响应里已存在的 model 字段改写成下游本次请求的模型：
//   - 只改出站内容，不触碰 upstreamResponseModelObserver 的上游审计与用量记录，
//     本站自己的使用记录仍然保留真实上游模型；
//   - 只覆盖已存在的字段，不新建 model 键，值相同时原样返回以保持字节稳定。
//
// 三条路径分别覆盖 Chat/Responses 顶层 model、Responses 事件的 response.model 和
// Anthropic 的 message.model；下游审计优先读 response.model，所以必须一起改写，
// 只改顶层仍会被下游判定为不一致。
var downstreamModelEchoJSONPaths = []string{"model", "response.model", "message.model"}

// downstreamModelEchoJSONKey 是 model 字段的 JSON 键字面量，用于解析前的廉价快筛。
const downstreamModelEchoJSONKey = `"model"`

// downstreamModelEchoCandidate 判断文本是否可能包含 model 字段，
// 避免在流式增量事件上无谓地调用 gjson。
func downstreamModelEchoCandidate(payload string) bool {
	return strings.Contains(payload, downstreamModelEchoJSONKey)
}

// downstreamModelEchoCandidateBytes 是 downstreamModelEchoCandidate 的字节版本。
func downstreamModelEchoCandidateBytes(payload []byte) bool {
	return bytes.Contains(payload, []byte(downstreamModelEchoJSONKey))
}

// forceDownstreamModelInJSON 把 JSON 文本里已存在的 model 字段改写成 clientModel。
// 第二个返回值表示是否真的改动过，便于调用方决定是否重新拼装报文。
func forceDownstreamModelInJSON(data, clientModel string) (string, bool) {
	clientModel = strings.TrimSpace(clientModel)
	if data == "" || clientModel == "" || !downstreamModelEchoCandidate(data) {
		return data, false
	}
	updated := data
	changed := false
	for _, path := range downstreamModelEchoJSONPaths {
		// 非字符串（含字段缺失、null）不处理，避免给上游没声明模型的报文凭空补字段。
		if current := gjson.Get(updated, path); current.Type != gjson.String || current.Str == clientModel {
			continue
		}
		next, err := sjson.Set(updated, path, clientModel)
		if err != nil {
			continue
		}
		updated = next
		changed = true
	}
	return updated, changed
}

// forceDownstreamModelInJSONBytes 是 forceDownstreamModelInJSON 的字节版本。
func forceDownstreamModelInJSONBytes(body []byte, clientModel string) []byte {
	clientModel = strings.TrimSpace(clientModel)
	if len(body) == 0 || clientModel == "" || !downstreamModelEchoCandidateBytes(body) {
		return body
	}
	updated := body
	for _, path := range downstreamModelEchoJSONPaths {
		if current := gjson.GetBytes(updated, path); current.Type != gjson.String || current.Str == clientModel {
			continue
		}
		next, err := sjson.SetBytes(updated, path, clientModel)
		if err != nil {
			continue
		}
		updated = next
	}
	return updated
}
