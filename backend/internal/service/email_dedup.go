package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

const emailContentDedupWindow = 5 * time.Minute

// 复用邮件缓存的可选能力，生产使用 Redis 保证所有实例共享去重状态。
type emailDeliveryGuard interface {
	AcquireEmailDelivery(context.Context, string, string, time.Duration) (bool, error)
	ReleaseEmailDelivery(context.Context, string, string) error
}

// 仅忽略完整检测时间和排版空白，不抹去账号编号、金额、验证码或重置链接。
var emailDetectionTimestamp = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?\b`)

func emailContentFingerprint(to, subject, body string) string {
	if address, err := mail.ParseAddress(to); err == nil {
		to = address.Address
	}
	body = emailDetectionTimestamp.ReplaceAllString(body, "[检测时间]")
	canonical := strings.ToLower(strings.TrimSpace(to)) + "\x00" + sanitizeEmailHeader(subject) + "\x00" + strings.Join(strings.Fields(body), " ")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
}
