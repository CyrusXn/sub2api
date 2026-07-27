package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type lowBalanceReminderRepository struct {
	db *sql.DB
}

// NewLowBalanceReminderRepository 创建每日低余额提醒的只读查询仓储。
func NewLowBalanceReminderRepository(db *sql.DB) service.LowBalanceReminderRepository {
	return &lowBalanceReminderRepository{db: db}
}

func (r *lowBalanceReminderRepository) ListLowBalanceReminderUsers(ctx context.Context, threshold float64) ([]*service.User, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("低余额提醒仓储未初始化")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT u.id, u.email, u.username, u.balance
FROM users u
WHERE u.deleted_at IS NULL
  AND u.status = $1
  AND u.role = $2
  AND u.balance < $3
  AND u.balance_notify_enabled = TRUE
  AND BTRIM(u.email) <> ''
  AND EXISTS (
    SELECT 1
    FROM usage_logs ul
    WHERE ul.user_id = u.id
      AND ul.actual_cost > 0
  )
ORDER BY u.id ASC`, service.StatusActive, service.RoleUser, threshold)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	users := make([]*service.User, 0)
	for rows.Next() {
		user := &service.User{BalanceNotifyEnabled: true, Status: service.StatusActive}
		if err := rows.Scan(&user.ID, &user.Email, &user.Username, &user.Balance); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}
