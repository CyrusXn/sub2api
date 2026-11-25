package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
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
	ListMobileAlerts(context.Context, int64, bool) ([]MobileAlert, error)
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

func (s *BalanceCenterService) ListMobileAlerts(ctx context.Context, after int64, emailMirror bool) ([]MobileAlert, error) {
	repo, ok := s.repository.(mobileAlertRepository)
	if !ok {
		return nil, errors.New("手机告警仓储不可用")
	}
	return repo.ListMobileAlerts(ctx, after, emailMirror)
}

func (s *BalanceCenterService) WatchMobileAlerts(ctx context.Context) (<-chan struct{}, error) {
	repo, ok := s.repository.(interface {
		WatchMobileAlerts(context.Context) (<-chan struct{}, error)
	})
	if !ok {
		return nil, errors.New("手机告警推送仓储不可用")
	}
	return repo.WatchMobileAlerts(ctx)
}

// CaptureMobileSiteWallet 使用上游站点钱包的实测余额，并沿用余额中心充值换算与阈值。
func (s *BalanceCenterService) CaptureMobileSiteWallet(ctx context.Context, item UpstreamSiteBalanceObservation) error {
	if _, enabled := mobileAlertDigest(); !enabled {
		return nil
	}
	repo, ok := s.repository.(interface {
		CaptureMobileSiteWallet(context.Context, UpstreamSiteBalanceObservation, float64) error
	})
	if !ok {
		return errors.New("手机告警仓储不可用")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	return repo.CaptureMobileSiteWallet(ctx, item, settings.LowBalanceThreshold)
}
