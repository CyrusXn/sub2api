//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelQualityRepositoryPersistsProgressAndTerminalState(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, integrationEntClient, &service.User{})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM model_quality_runs WHERE user_id=$1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
		require.NoError(t, err)
	})
	repo := NewModelQualityRepository(integrationDB)
	for _, terminal := range []string{"completed", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			run := &service.ModelQualityRun{UserID: user.ID, Author: user.Email, GroupName: "测试分组", Model: "gpt-test", Theme: "candy", Prompt: "测试问题", Status: "running"}
			require.NoError(t, repo.Create(ctx, run))
			requestID := "quality-integration-request"
			// 必须由真实 PostgreSQL 解析参数；SQL mock 无法发现 varchar 与 text 的推断冲突。
			require.NoError(t, repo.Update(ctx, run.ID, user.ID, "running", "部分回答", requestID))
			var status, content, storedRequest, message string
			var finished sql.NullTime
			read := func() {
				t.Helper()
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,content,request_id,error,finished_at FROM model_quality_runs WHERE id=$1`, run.ID).Scan(&status, &content, &storedRequest, &message, &finished))
			}
			read()
			require.Equal(t, "running", status)
			require.Equal(t, "部分回答", content)
			require.Equal(t, requestID, storedRequest)
			require.False(t, finished.Valid)
			require.NoError(t, repo.Update(ctx, run.ID, user.ID, terminal, "最终回答", ""))
			read()
			require.Equal(t, terminal, status)
			require.Equal(t, "最终回答", content)
			require.Equal(t, requestID, storedRequest)
			require.True(t, finished.Valid)
			require.WithinDuration(t, time.Now(), finished.Time, 10*time.Second)
			require.Equal(t, terminal == "failed", message != "")
			require.ErrorIs(t, repo.Update(ctx, run.ID, user.ID, "running", "迟到的回答", requestID), service.ErrModelQualityNotRunning)
			read()
			require.Equal(t, "最终回答", content)
		})
	}
}
