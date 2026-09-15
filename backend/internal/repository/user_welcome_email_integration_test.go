//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserWelcomeEmailFollowsUserTransaction(t *testing.T) {
	ctx := context.Background()
	repo := NewUserRepository(integrationEntClient, integrationDB)
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%t", commit), func(t *testing.T) {
			tx, err := integrationEntClient.Tx(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tx.Rollback() })
			u := &service.User{Email: fmt.Sprintf("welcome-tx-%t@example.com", commit), Role: service.RoleUser, Status: service.StatusActive, PasswordHash: "test-hash", Concurrency: 1}
			txCtx := dbent.NewTxContext(ctx, tx)
			require.NoError(t, repo.CreateWithEmailAliasGuard(txCtx, u))
			var count int
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(u.ID)).Scan(&count))
			require.Zero(t, count, "提交前其他连接不能投递欢迎邮件")
			if commit {
				require.NoError(t, tx.Commit())
				t.Cleanup(func() {
					_, _ = integrationDB.ExecContext(ctx, "DELETE FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(u.ID))
					_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", u.ID)
				})
				var subject, body string
				require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT subject, body_html FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(u.ID)).Scan(&subject, &body))
				require.Contains(t, subject, "领取试用额度")
				for _, content := range []string{"ncwqwert", "Astra", "不降智、高速、稳定", "醍醐测试", "用户交流群"} {
					require.Contains(t, body, content)
				}
			} else {
				require.NoError(t, tx.Rollback())
				require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(u.ID)).Scan(&count))
				require.Zero(t, count, "注册回滚必须同时撤销邮件")
			}
		})
	}
}

func TestUserWelcomeEmailWaitsForRealEmailAndDoesNotResend(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, "INSERT INTO users (email, password_hash) VALUES ('linuxdo-welcome@linuxdo-connect.invalid', 'test-hash') RETURNING id").Scan(&userID))
	var ready bool
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT available_at <= NOW() FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(userID)).Scan(&ready))
	require.False(t, ready)
	_, err = tx.ExecContext(ctx, "UPDATE users SET email = 'welcome-bound@example.com' WHERE id = $1", userID)
	require.NoError(t, err)
	var recipient string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT recipient_email, available_at <= NOW() FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(userID)).Scan(&recipient, &ready))
	require.True(t, ready)
	require.Equal(t, "welcome-bound@example.com", recipient)
	_, err = tx.ExecContext(ctx, "UPDATE alert_email_outbox SET status = 'sent' WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(userID))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE users SET email = 'welcome-changed@example.com', username = '更新资料' WHERE id = $1", userID)
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1 AND status = 'pending'", fmt.Sprint(userID)).Scan(&count))
	require.Zero(t, count, "修改已发信用户不能再次入队")
	// 模拟功能启用前的用户：没有欢迎邮件记录，修改邮箱也不补发。
	_, err = tx.ExecContext(ctx, "DELETE FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(userID))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE users SET email = 'welcome-legacy@example.com' WHERE id = $1", userID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM alert_email_outbox WHERE source_type = 'user_welcome' AND source_id = $1", fmt.Sprint(userID)).Scan(&count))
	require.Zero(t, count)
}

func TestUserWelcomeEmailClaimSeparatesWelcomeFromAlerts(t *testing.T) {
	ctx := context.Background()
	repo := NewAlertEmailOutboxRepository(integrationDB)
	// 使用已到期的独立收件人窗口，不依赖其他测试的队列内容。
	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, welcomeFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("welcome_first=%t", welcomeFirst), func(t *testing.T) {
			email := fmt.Sprintf("welcome-claim-%t@example.com", welcomeFirst)
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM alert_email_outbox WHERE recipient_email = $1", email)
			})
			sources := []string{"user_welcome", "ops_alert", "balance_center", "user_welcome"}
			if !welcomeFirst {
				sources[0], sources[1] = sources[1], sources[0]
			}
			for i, source := range sources {
				require.NoError(t, repo.EnqueueAlertEmail(ctx, &service.AlertEmailOutboxInput{
					SourceType: source, SourceKey: fmt.Sprintf("welcome-test:%t:%d", welcomeFirst, i), AlertType: "test",
					Recipient: email, Subject: "测试邮件", BodyHTML: "<p>测试正文</p>", CreatedAt: now.Add(-time.Minute), AvailableAt: now,
				}))
			}
			items, err := repo.ClaimAlertEmailBatch(ctx, now, time.Minute, 100)
			require.NoError(t, err)
			if welcomeFirst {
				require.Len(t, items, 1, "每位用户的欢迎邮件必须单独投递")
				require.Equal(t, "user_welcome", items[0].SourceType)
			} else {
				require.Len(t, items, 2, "原有不同来源的告警仍可聚合")
				for _, item := range items {
					require.NotEqual(t, "user_welcome", item.SourceType)
				}
			}
		})
	}
}
