package service

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

type clientFirstPacketReporter interface {
	ReportClientFirstPacket(context.Context, int64, int64, string, int) error
}

func (s *UsageService) ReportClientFirstPacket(ctx context.Context, userID, apiKeyID int64, requestID string, ms int) error {
	repo, ok := s.usageRepo.(clientFirstPacketReporter)
	if !ok {
		return fmt.Errorf("client timing repository unavailable")
	}
	return repo.ReportClientFirstPacket(ctx, userID, apiKeyID, requestID, ms)
}

func ValidClientFirstPacket(requestID string, ms int) bool {
	return requestID != "" && len(requestID) <= 64 && strings.IndexFunc(requestID, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
	}) == -1 && ms >= 0 && ms <= 24*60*60*1000
}
