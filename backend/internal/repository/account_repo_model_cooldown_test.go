package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSetModelRateLimit_TransientCooldownAtomicallyPreservesLongerBlock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)
	// 比较必须在 UPDATE 内完成，不能用入站快照或先读后写代替，否则并发会缩短冷却。
	mock.ExpectExec(`(?s)UPDATE accounts SET.*CASE.*openai_model_transient.*timestamptz.*THEN extra->'model_rate_limits'->\$1.*ELSE \$2::jsonb.*WHERE id = \$3`).
		WithArgs("gpt-test", sqlmock.AnyArg(), int64(27)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(27), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.SetModelRateLimit(context.Background(), 27, "gpt-test", time.Now().Add(10*time.Second), "openai_model_transient"))
	require.NoError(t, mock.ExpectationsWereMet())
}
