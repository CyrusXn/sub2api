package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestInsertSystemMetricsPersistsDailyNetworkTrafficAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	repo := &opsRepository{db: db}
	receiveBytes := int64(1_500_000)
	transmitBytes := int64(2_500_000)

	mock.ExpectExec(`(?s)WITH inserted AS \(.*INSERT INTO ops_system_metrics.*network_receive_bytes.*RETURNING.*\).*INSERT INTO ops_network_traffic_daily.*ON CONFLICT \(bucket_date\) DO UPDATE`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.InsertSystemMetrics(context.Background(), &service.OpsInsertSystemMetricsInput{
		CreatedAt:            time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC),
		WindowMinutes:        1,
		NetworkReceiveBytes:  &receiveBytes,
		NetworkTransmitBytes: &transmitBytes,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
