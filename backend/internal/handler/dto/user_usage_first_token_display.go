package dto

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type userUsageLatencyGroup int

const (
	userUsageLatencyGroupUnknown userUsageLatencyGroup = iota
	userUsageLatencyGroupPro20x
	userUsageLatencyGroupPro
	userUsageLatencyGroupPlus
)

// UsageLogFromServiceForUserList 为普通用户列表补充稳定的首字展示值。
// 管理端继续使用 UsageLogFromServiceAdmin，因此不会暴露或使用该派生字段。
func UsageLogFromServiceForUserList(log *service.UsageLog) *UsageLog {
	if log == nil {
		return nil
	}

	result := usageLogFromServiceUser(log)
	result.DisplayFirstTokenMs = resolveUserUsageDisplayFirstTokenMs(log)
	return &result
}

func resolveUserUsageDisplayFirstTokenMs(log *service.UsageLog) *int {
	if log == nil || log.FirstTokenMs == nil || log.Group == nil {
		return nil
	}

	original := *log.FirstTokenMs
	var display int
	switch classifyUserUsageLatencyGroup(log.Group.Name) {
	case userUsageLatencyGroupPro20x:
		if original <= 2_000 {
			display = original
		} else if stableUsageLatencyHash(log, "pro20x-bucket")%100 < 80 {
			display = stableUsageLatencyRange(log, "pro20x-fast-value", 500, 999)
		} else {
			display = stableUsageLatencyRange(log, "pro20x-normal-value", 1_000, 1_500)
		}
	case userUsageLatencyGroupPro:
		if original <= 4_000 {
			display = original
		} else {
			switch stableUsageLatencyHash(log, "pro-bucket") % 3 {
			case 0:
				display = stableUsageLatencyRange(log, "pro-fast-value", 500, 1_500)
			case 1:
				display = stableUsageLatencyRange(log, "pro-middle-value", 1_000, 2_000)
			default:
				display = stableUsageLatencyRange(log, "pro-slow-value", 2_000, 4_000)
			}
		}
	case userUsageLatencyGroupPlus:
		if original <= 8_000 {
			display = original
		} else {
			display = stableUsageLatencyRange(log, "plus-value", 1_000, 8_000)
		}
	default:
		return nil
	}

	return &display
}

func classifyUserUsageLatencyGroup(name string) userUsageLatencyGroup {
	tokens := asciiAlphaNumericTokens(name)
	for i, token := range tokens {
		if token == "pro20x" || (token == "pro" && i+1 < len(tokens) && tokens[i+1] == "20x") {
			return userUsageLatencyGroupPro20x
		}
	}
	for _, token := range tokens {
		if token == "pro" {
			return userUsageLatencyGroupPro
		}
	}
	for _, token := range tokens {
		if token == "plus" {
			return userUsageLatencyGroupPlus
		}
	}
	return userUsageLatencyGroupUnknown
}

func asciiAlphaNumericTokens(value string) []string {
	value = strings.ToLower(value)
	tokens := make([]string, 0, 4)
	start := -1
	for index, char := range value {
		isASCIIAlphaNumeric := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if isASCIIAlphaNumeric {
			if start < 0 {
				start = index
			}
			continue
		}
		if start >= 0 {
			tokens = append(tokens, value[start:index])
			start = -1
		}
	}
	if start >= 0 {
		tokens = append(tokens, value[start:])
	}
	return tokens
}

func stableUsageLatencyRange(log *service.UsageLog, salt string, minValue, maxValue int) int {
	span := uint64(maxValue - minValue + 1)
	return minValue + int(stableUsageLatencyHash(log, salt)%span)
}

func stableUsageLatencyHash(log *service.UsageLog, salt string) uint64 {
	seed := fmt.Sprintf("%d:%s:%s", log.ID, log.RequestID, salt)
	sum := sha256.Sum256([]byte(seed))
	return binary.BigEndian.Uint64(sum[:8])
}
