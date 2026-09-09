package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 所有用户余额提醒共享一次发送状态；先读取真实余额和注册邮箱，过滤充值后的迟到中继。
func (s *BalanceNotifyService) sendUserBalanceEmailOnce(ctx context.Context, userID int64, email, name string, balance, threshold float64, send func(string, string, float64) bool) bool {
	if s.leaderLockDB == nil {
		return send(email, name, balance)
	}
	token := uuid.NewString()
	err := s.leaderLockDB.QueryRowContext(ctx, `
WITH target AS (
 SELECT id,email,username,balance FROM users
 WHERE id=$1 AND deleted_at IS NULL AND status='active'
 AND (($2::numeric=0 AND balance<=0) OR ($2::numeric>0 AND balance<$2))
 FOR UPDATE
), claimed AS (
 INSERT INTO user_balance_email_states(user_id,claim_token,lease_until)
 SELECT id,$3,NOW()+INTERVAL '2 minutes' FROM target
 ON CONFLICT(user_id) DO UPDATE SET claim_token=EXCLUDED.claim_token,lease_until=EXCLUDED.lease_until
 WHERE user_balance_email_states.sent_at IS NULL AND user_balance_email_states.lease_until<=NOW()
 RETURNING user_id
)
SELECT t.email,t.username,t.balance FROM target t JOIN claimed c ON c.user_id=t.id`, userID, threshold, token).Scan(&email, &name, &balance)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		slog.Error("获取用户余额邮件发送状态失败", "user_id", userID, "error", err)
		return false
	}
	sent := strings.TrimSpace(email) != "" && send(email, name, balance)
	// SMTP 返回后使用独立超时保存状态，调用方超时不能丢失已发送标记。
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sent {
		_, err = s.leaderLockDB.ExecContext(saveCtx, `UPDATE user_balance_email_states SET sent_at=NOW() WHERE user_id=$1 AND claim_token=$2`, userID, token)
	} else {
		_, err = s.leaderLockDB.ExecContext(saveCtx, `DELETE FROM user_balance_email_states WHERE user_id=$1 AND claim_token=$2 AND sent_at IS NULL`, userID, token)
	}
	if err != nil {
		slog.Error("保存用户余额邮件发送状态失败", "user_id", userID, "error", err)
	}
	return sent && err == nil
}
