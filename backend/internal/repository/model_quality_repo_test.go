package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"regexp"
	"testing"
	"time"
)

func TestModelQualityRepositoryStoresCompletedAnswerAndResolvesActualAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("INSERT INTO model_quality_runs .*request_id.*RETURNING id, created_at, COALESCE\\(account_id,0\\)").WithArgs(int64(12), int64(3), "author", "group", "gpt-test", "candy", "prompt", "completed", "answer", "", "req-1").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "account_id"}).AddRow(9, time.Now(), 7))
	run := &service.ModelQualityRun{UserID: 12, APIKeyID: 3, Author: "author", GroupName: "group", Model: "gpt-test", Theme: "candy", Prompt: "prompt", Status: "completed", Content: "answer", RequestID: "req-1"}
	require.NoError(t, NewModelQualityRepository(db).Create(context.Background(), run))
	require.EqualValues(t, 9, run.ID)
	require.EqualValues(t, 7, run.AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelQualityRepositoryResolvesAccountAfterUsageIsWritten(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT.*FROM model_quality_runs q").WithArgs("", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	// 列表必须用持久化请求标识再次关联，不能依赖创建时已写入用量。
	query := `SELECT q.id.*LIMIT 12 OFFSET.*` + regexp.QuoteMeta("u.api_key_id=q.api_key_id AND u.request_id='client:' || q.request_id") + `.*` + regexp.QuoteMeta("e.api_key_id=q.api_key_id AND e.client_request_id=q.request_id")
	mock.ExpectQuery(query).WithArgs("", "", 0).WillReturnRows(sqlmock.NewRows([]string{"id", "author", "group_name", "model", "theme", "prompt", "status", "content", "error", "account_id", "account_name", "created_at"}).AddRow(9, "author", "group", "gpt-test", "candy", "question", "completed", "21", "", 7, "迟到用量对应的账号", time.Now()))
	runs, _, err := NewModelQualityRepository(db).List(context.Background(), 1, "", "")
	require.NoError(t, err)
	require.EqualValues(t, 7, runs[0].AccountID)
	require.Equal(t, "迟到用量对应的账号", runs[0].AccountName)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestModelQualityRepositorySharedPagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT.*FROM model_quality_runs q").WithArgs("candy", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(13))
	mock.ExpectQuery("SELECT q.id.*LIMIT 12 OFFSET").WithArgs("candy", "", 12).WillReturnRows(sqlmock.NewRows([]string{"id", "author", "group_name", "model", "theme", "prompt", "status", "content", "error", "account_id", "account_name", "created_at"}).AddRow(2, "author", "group", "gpt-test", "candy", "question", "completed", "21", "", 7, "account", time.Now()))
	runs, total, err := NewModelQualityRepository(db).List(context.Background(), 2, "candy", "")
	require.NoError(t, err)
	require.EqualValues(t, 13, total)
	require.Len(t, runs, 1)
	require.EqualValues(t, 7, runs[0].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}
