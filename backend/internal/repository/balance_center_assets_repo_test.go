package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 额度观察和提醒必须同时提交，迟到的同步不能留下误报邮件。
func TestSubscriptionObservationAtomicQueue(t *testing.T) {
	for _, rows := range []int64{0, 1} {
		t.Run(string(rune('0'+rows)), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := &balanceCenterRepository{db: db}
			now := time.Now()
			remaining := 0.5
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE balance_center_asset_settings").WithArgs(int64(9), int64(944), now, now, "", remaining).WillReturnResult(sqlmock.NewResult(0, rows))
			if rows == 1 {
				mock.ExpectExec("INSERT INTO alert_email_outbox").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = repo.SaveBalanceCenterSubscriptionObservation(context.Background(), 9, 944, service.BalanceCenterSubscriptionObservation{ExpiresAt: now, RemainingUSD: &remaining}, now, "", []service.AlertEmailOutboxInput{{SourceType: "balance_center_subscription", SourceKey: "window-1", AvailableAt: now}})
			if rows == 1 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
