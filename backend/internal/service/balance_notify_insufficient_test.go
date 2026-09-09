//go:build unit

package service

import (
	"context"
	"io"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type insufficientBalanceCooldownLock struct {
	mu      sync.Mutex
	owners  map[string]string
	lastTTL time.Duration
}

func (l *insufficientBalanceCooldownLock) TryAcquireLeaderLock(_ context.Context, key, owner string, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners == nil {
		l.owners = make(map[string]string)
	}
	l.lastTTL = ttl
	if _, exists := l.owners[key]; exists {
		return false, nil
	}
	l.owners[key] = owner
	return true, nil
}

func (l *insufficientBalanceCooldownLock) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners[key] == owner {
		delete(l.owners, key)
	}
	return nil
}

func TestInsufficientBalanceCooldownIsSharedAcrossInstances(t *testing.T) {
	lock := &insufficientBalanceCooldownLock{}
	first := NewBalanceNotifyService(nil, nil, nil)
	second := NewBalanceNotifyService(nil, nil, nil)
	first.leaderLockCache = lock
	second.leaderLockCache = lock

	releaseFirst, acquired := first.acquireInsufficientBalanceNotifyCooldown(42)
	require.True(t, acquired)
	_, acquired = second.acquireInsufficientBalanceNotifyCooldown(42)
	require.False(t, acquired)
	require.Equal(t, 3*time.Minute, lock.lastTTL)

	releaseFirst()
	_, acquired = second.acquireInsufficientBalanceNotifyCooldown(42)
	require.True(t, acquired)
}

func TestNotifyUserInsufficientBalanceUsesThreeMinuteCooldown(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))
	require.NoError(t, repo.SetMultiple(ctx, map[string]string{
		SettingKeyBalanceLowNotifyEnabled:     "true",
		SettingKeyBalanceLowNotifyThreshold:   "2",
		SettingKeyBalanceLowNotifyRechargeURL: "https://api.xnkaixin.eu.cc/redeem",
		SettingKeySiteName:                    "xn API中转站",
	}))

	emailService := NewEmailService(repo, nil)
	service := NewBalanceNotifyService(emailService, repo, nil)
	service.SetNotificationEmailService(NewNotificationEmailService(repo, emailService))
	initialNow := time.Date(2026, 7, 27, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	var nowUnixNano atomic.Int64
	nowUnixNano.Store(initialNow.UnixNano())
	service.now = func() time.Time { return time.Unix(0, nowUnixNano.Load()) }
	user := &User{
		ID:                   42,
		Username:             "测试用户",
		Email:                "user@example.com",
		Balance:              -0.01,
		BalanceNotifyEnabled: true,
	}

	service.NotifyUserInsufficientBalance(ctx, user, user.Balance)
	require.Eventually(t, func() bool { return smtpServer.messageCount() == 1 }, time.Second, 10*time.Millisecond)

	nowUnixNano.Add(int64(2*time.Minute + 59*time.Second))
	service.NotifyUserInsufficientBalance(ctx, user, user.Balance)
	require.Never(t, func() bool { return smtpServer.messageCount() > 1 }, 150*time.Millisecond, 10*time.Millisecond)

	nowUnixNano.Add(int64(2 * time.Second))
	service.NotifyUserInsufficientBalance(ctx, user, user.Balance)
	require.Eventually(t, func() bool { return smtpServer.messageCount() == 2 }, time.Second, 10*time.Millisecond)

	// 检查解码后的正文，不能把 MIME 传输编码当作中文明文。
	parsed, err := mail.ReadMessage(strings.NewReader(smtpServer.lastMessage()))
	require.NoError(t, err)
	decoded, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	require.NoError(t, err)
	body := strings.ToLower(string(decoded))
	require.Contains(t, body, "余额不足")
	require.Contains(t, body, "立即充值")
	require.NotContains(t, body, "<img")
	require.NotContains(t, body, "二维码")
}
