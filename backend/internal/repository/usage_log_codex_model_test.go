package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRecentCodexModel(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		queryError  bool
	}{
		{name: "recent user model", model: "gpt-6-astra"},
		{name: "no history"},
		{name: "query error", queryError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := &usageLogRepository{sql: db}
			reader, ok := any(repo).(interface {
				GetRecentOpenAICodexModel(context.Context, int64) (string, error)
			})
			require.True(t, ok, "用量仓库必须支持读取用户最近业务模型")
			// 查询必须在数据库中隔离用户、排除后台改写记录和失败/非文本请求，且只取一行。
			expect := mock.ExpectQuery(`(?s)SELECT .* FROM usage_logs.*user_id = \$1.*output_tokens > 0.*request_type.*native_compaction_v2.*image_count.*video_count.*model_mapping_chain.*ORDER BY created_at DESC, id DESC.*LIMIT 1`).WithArgs(int64(7))
			if tc.queryError {
				expect.WillReturnError(context.DeadlineExceeded)
			} else {
				rows := sqlmock.NewRows([]string{"model"})
				if tc.model != "" {
					rows.AddRow(tc.model)
				}
				expect.WillReturnRows(rows)
			}
			model, err := reader.GetRecentOpenAICodexModel(context.Background(), 7)
			if tc.queryError {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.model, model)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
