package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestReportClientFirstPacketScopesAndPreservesMeasurements(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	// 客户端值只能更新本人、该 Key、该请求的独立字段，且只允许第一次上报。
	mock.ExpectExec(`UPDATE usage_logs SET client_first_packet_ms = COALESCE\(client_first_packet_ms, \$1\) WHERE user_id = \$2 AND api_key_id = \$3 AND request_id = \$4 AND stream = TRUE`).
		WithArgs(1560, int64(1), int64(2), "client:server-issued-id").WillReturnResult(sqlmock.NewResult(0, 1))
	err := repo.ReportClientFirstPacket(context.Background(), 1, 2, "server-issued-id", 1560)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
