package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type MobileAlert struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type mobileAlertRepository interface {
	CaptureMobileBalanceAlerts(context.Context, *BalanceCenterSnapshot, float64) error
	ListMobileAlerts(context.Context, int64) ([]MobileAlert, error)
}

func mobileAlertDigest() ([sha256.Size]byte, bool) {
	var digest [sha256.Size]byte
	value := strings.TrimSpace(os.Getenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256"))
	if len(value) != hex.EncodedLen(len(digest)) {
		return digest, false
	}
	_, err := hex.Decode(digest[:], []byte(value))
	return digest, err == nil
}

// AuthorizeMobileAlerts 只接受专用只读口令，服务端配置仅保存其摘要。
func AuthorizeMobileAlerts(header string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(token) < 32 || len(token) > 256 {
		return false
	}
	expected, enabled := mobileAlertDigest()
	actual := sha256.Sum256([]byte(token))
	return enabled && subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

func (s *BalanceCenterService) ListMobileAlerts(ctx context.Context, after int64) ([]MobileAlert, error) {
	repo, ok := s.repository.(mobileAlertRepository)
	if !ok {
		return nil, errors.New("手机告警仓储不可用")
	}
	return repo.ListMobileAlerts(ctx, after)
}

func NewMobileBalanceAlert(snapshot *BalanceCenterSnapshot, decision BalanceCenterAlertDecision) MobileAlert {
	label := strings.TrimSpace(balanceCenterAlertAccountLabel(snapshot))
	site := strings.TrimSpace(snapshot.SiteName)
	if site != "" && label != "" && site != label {
		label = site + " · " + label
	}
	if label == "" {
		label = site
	}
	label = redactOpsAlertEmailText(label)
	alert := MobileAlert{Kind: decision.Type, Title: balanceCenterAlertTypeName(decision.Type)}
	if decision.Type == BalanceCenterAlertLowBalance {
		alert.Body = fmt.Sprintf("%s，余额 %g 元，提醒阈值 %g 元", label, *decision.NewValue, *decision.Threshold)
	} else {
		alert.Body = fmt.Sprintf("%s，%g → %g", label, *decision.OldValue, *decision.NewValue)
	}
	return alert
}
