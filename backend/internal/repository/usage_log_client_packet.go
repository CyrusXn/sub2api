package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) ReportClientFirstPacket(ctx context.Context, userID, apiKeyID int64, requestID string, ms int) error {
	result, err := r.sql.ExecContext(ctx, `UPDATE usage_logs SET client_first_packet_ms = COALESCE(client_first_packet_ms, $1) WHERE user_id = $2 AND api_key_id = $3 AND request_id = $4 AND stream = TRUE`, ms, userID, apiKeyID, "client:"+requestID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// 异步用量尚未落库时，由客户端有限重试；不创建虚假用量记录。
		return service.ErrUsageLogNotFound
	}
	return nil
}
