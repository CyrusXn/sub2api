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

func TestModelQualityDeleteClearsContentAndRejectsMissingRecords(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE model_quality_runs SET deleted_at=NOW(),content='',prompt='',error='' WHERE id=$1 AND deleted_at IS NULL")).WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, affected))
		err = NewModelQualityRepository(db).Delete(context.Background(), 17)
		if affected == 0 {
			require.ErrorIs(t, err, service.ErrModelQualityNotFound)
		} else {
			require.NoError(t, err)
		}
		require.NoError(t, mock.ExpectationsWereMet())
		db.Close()
	}
}

func TestModelQualityProgressCannotOverwriteFinalOrAnotherUsersRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("UPDATE model_quality_runs.*WHERE id=\\$1 AND user_id=\\$2 AND status='running'").WithArgs(int64(9), int64(12), "running", "partial", "req-1").WillReturnResult(sqlmock.NewResult(0, 0))
	err = NewModelQualityRepository(db).Update(context.Background(), 9, 12, "running", "partial", "req-1")
	require.ErrorIs(t, err, service.ErrModelQualityNotRunning)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelQualityOfficialSlotIsClaimedAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	slot := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("INSERT INTO model_quality_runs .*official_slot.*ON CONFLICT DO NOTHING RETURNING id,created_at").WithArgs(int64(12), int64(3), "admin@example.com", "group", "gpt-test", "你是谁", slot).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}))
	err = NewModelQualityRepository(db).Create(context.Background(), &service.ModelQualityRun{UserID: 12, APIKeyID: 3, Author: "admin@example.com", GroupName: "group", Model: "gpt-test", OfficialSlot: &slot, Prompt: "你是谁"})
	require.ErrorIs(t, err, service.ErrModelQualityAlreadyScheduled)
	require.NoError(t, mock.ExpectationsWereMet())
}

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
	mock.ExpectQuery("SELECT COUNT.*FROM model_quality_runs q").WithArgs("", "", "", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	// 列表必须用持久化请求标识再次关联，不能依赖创建时已写入用量。
	query := `SELECT q.id.*LIMIT 12 OFFSET.*` + regexp.QuoteMeta("u.api_key_id=q.api_key_id AND u.request_id='client:' || q.request_id") + `.*` + regexp.QuoteMeta("e.api_key_id=q.api_key_id AND e.client_request_id=q.request_id")
	mock.ExpectQuery(query).WithArgs("", "", "", "", 0).WillReturnRows(sqlmock.NewRows([]string{"id", "author", "group_name", "model", "theme", "prompt", "status", "content", "error", "account_id", "account_name", "created_at"}).AddRow(9, "author", "group", "gpt-test", "candy", "question", "completed", "21", "", 7, "迟到用量对应的账号", time.Now()))
	runs, _, err := NewModelQualityRepository(db).List(context.Background(), 1, "", "", "", "")
	require.NoError(t, err)
	require.EqualValues(t, 7, runs[0].AccountID)
	require.Equal(t, "迟到用量对应的账号", runs[0].AccountName)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestModelQualityRepositorySharedPagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT.*FROM model_quality_runs q").WithArgs("candy", "", "", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(13))
	mock.ExpectQuery("SELECT q.id.*LIMIT 12 OFFSET").WithArgs("candy", "", "", "", 12).WillReturnRows(sqlmock.NewRows([]string{"id", "author", "group_name", "model", "theme", "prompt", "status", "content", "error", "account_id", "account_name", "created_at"}).AddRow(2, "author", "group", "gpt-test", "candy", "question", "completed", "21", "", 7, "account", time.Now()))
	runs, total, err := NewModelQualityRepository(db).List(context.Background(), 2, "candy", "", "", "")
	require.NoError(t, err)
	require.EqualValues(t, 13, total)
	require.Len(t, runs, 1)
	require.EqualValues(t, 7, runs[0].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelQualityRepositoryFiltersBeforeCountingAndPagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	where := regexp.QuoteMeta("AND ($3='' OR q.group_name=$3) AND ($4='' OR q.model=$4)")
	mock.ExpectQuery("SELECT COUNT.*"+where).WithArgs("candy", "completed", "shared", "gpt-test").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT q.id.*"+where+".*LIMIT 12 OFFSET "+regexp.QuoteMeta("$5")).WithArgs("candy", "completed", "shared", "gpt-test", 0).WillReturnRows(sqlmock.NewRows([]string{"id", "author", "group_name", "model", "theme", "prompt", "status", "content", "error", "account_id", "account_name", "created_at"}))
	_, total, err := NewModelQualityRepository(db).List(context.Background(), 1, "candy", "completed", "shared", "gpt-test")
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelQualityRepositoryOptionsUseAllRecordsInSelectedCategory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT DISTINCT group_name,model FROM model_quality_runs WHERE.*" + regexp.QuoteMeta("theme=$1")).WithArgs("candy").WillReturnRows(sqlmock.NewRows([]string{"group_name", "model"}).AddRow("共享分组", "model-b").AddRow("另一分组", "model-a").AddRow("共享分组", "model-a"))
	options, err := NewModelQualityRepository(db).FilterOptions(context.Background(), "candy")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"共享分组", "另一分组"}, options.Groups)
	require.Equal(t, []string{"model-a", "model-b"}, options.Models)
	require.NoError(t, mock.ExpectationsWereMet())
}
