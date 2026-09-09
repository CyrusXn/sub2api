//go:build unit

package service

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// 低余额的多个入口必须共用持久记录；SMTP 失败则保留重试机会。
func TestUserBalanceEmailOnce(t *testing.T) {
	for _, tc := range []struct {
		name               string
		duplicate, success bool
	}{{"首次成功", false, true}, {"已经提醒", true, true}, {"投递失败", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			s := NewBalanceNotifyService(nil, nil, nil)
			s.leaderLockDB = db
			query := mock.ExpectQuery("WITH target AS").WithArgs(int64(7), float64(0), sqlmock.AnyArg())
			if tc.duplicate {
				query.WillReturnError(sql.ErrNoRows)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"email", "username", "balance"}).AddRow("owner@example.com", "用户", -0.1))
				if tc.success {
					mock.ExpectExec("UPDATE user_balance_email_states").WillReturnResult(sqlmock.NewResult(0, 1))
				} else {
					mock.ExpectExec("DELETE FROM user_balance_email_states").WillReturnResult(sqlmock.NewResult(0, 1))
				}
			}
			calls := 0
			ok := s.sendUserBalanceEmailOnce(context.Background(), 7, "stale@example.com", "旧名称", -2, 0, func(email, name string, balance float64) bool {
				calls++
				require.Equal(t, "owner@example.com", email)
				require.Equal(t, -0.1, balance)
				return tc.success
			})
			require.Equal(t, tc.duplicate || tc.success, ok)
			if tc.duplicate {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestEmailFingerprintPreservesDifferentTransactions(t *testing.T) {
	a := emailContentFingerprint("User <USER@example.com>", "余额告警", "检测时间：2026-09-09T12:00:00Z 账号 1 余额 2")
	require.Equal(t, a, emailContentFingerprint("user@example.com", "余额告警", "检测时间：2026-09-09T12:01:00Z 账号 1 余额 2"))
	require.NotEqual(t, a, emailContentFingerprint("other@example.com", "余额告警", "检测时间：2026-09-09T12:00:00Z 账号 1 余额 2"))
	require.NotEqual(t, emailContentFingerprint("user@example.com", "验证码", "123456"), emailContentFingerprint("user@example.com", "验证码", "654321"))
	require.Equal(t, 5*time.Minute, emailContentDedupWindow)
}

// 真实 SMTP 测试确认不同服务实例共享内容去重，同时用户余额信只发给本人。
type testEmailDeliveryGuard struct {
	EmailCache
	mu     sync.Mutex
	owners map[string]string
}

func (g *testEmailDeliveryGuard) AcquireEmailDelivery(_ context.Context, k, o string, _ time.Duration) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owners == nil {
		g.owners = map[string]string{}
	}
	if _, ok := g.owners[k]; ok {
		return false, nil
	}
	g.owners[k] = o
	return true, nil
}
func (g *testEmailDeliveryGuard) ReleaseEmailDelivery(_ context.Context, k, o string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owners[k] == o {
		delete(g.owners, k)
	}
	return nil
}
func TestEmailDedupSMTPAndOwnerRecipient(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))
	guard := &testEmailDeliveryGuard{}
	first := NewEmailService(repo, guard)
	second := NewEmailService(repo, guard)
	require.NoError(t, first.SendEmail(ctx, "user@example.com", "相同告警", "检测时间：2026-09-09T12:00:00Z"))
	require.NoError(t, second.SendEmail(ctx, "user@example.com", "相同告警", "检测时间：2026-09-09T12:01:00Z"))
	require.EqualValues(t, 1, smtpServer.messageCount())
	require.NoError(t, second.SendEmail(ctx, "other@example.com", "相同告警", "检测时间：2026-09-09T12:01:00Z"))
	require.EqualValues(t, 2, smtpServer.messageCount())
	service := NewBalanceNotifyService(first, repo, nil)
	require.True(t, service.sendBalanceLowEmails([]string{"admin@example.com"}, 7, "用户", "owner@example.com", 1, 2, "站点", ""))
	message := strings.ToLower(smtpServer.lastMessage())
	require.Contains(t, message, "to: <owner@example.com>")
	require.NotContains(t, message, "admin@example.com")
}

// 明确失败不能占住去重窗口，修复 SMTP 配置后仍可重发。
func TestEmailDedupSMTPFailureReleasesReservation(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))
	svc := NewEmailService(repo, &testEmailDeliveryGuard{})
	config, err := svc.GetSMTPConfig(ctx)
	require.NoError(t, err)
	bad := *config
	bad.Port = 1
	require.Error(t, svc.SendEmailWithConfig(&bad, "user@example.com", "失败后重试", "body"))
	require.NoError(t, svc.SendEmailWithConfig(config, "user@example.com", "失败后重试", "body"))
	require.EqualValues(t, 1, smtpServer.messageCount())
}
