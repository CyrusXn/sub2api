package repository

import (
	"context"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGeminiInteractionPendingKeyDeleteReturnsConflict(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAPIKeyRepositoryWithSQL(client, db)
	m.ExpectExec(`UPDATE api_keys SET key = \$1, deleted_at = NOW\(\), updated_at = NOW\(\) WHERE id = \$2 AND deleted_at IS NULL`).WithArgs("deleted-placeholder", int64(1)).WillReturnError(&pq.Error{Code: "23503", Constraint: "gemini_interactions_api_key_pending"})
	err = repo.deleteWithTombstone(context.Background(), client, 1, "deleted-placeholder")
	require.ErrorIs(t, err, service.ErrGeminiInteractionKeyPending)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeminiInteractionPendingDependencyDeleteReturnsConflict(t *testing.T) {
	for _, entity := range []string{"account", "group", "subscription", "user"} {
		t.Run(entity, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			pgErr := &pq.Error{Code: "23503", Constraint: "gemini_interactions_" + entity + "_pending"}
			switch entity {
			case "account":
				m.ExpectQuery(`SELECT .* FROM "account_groups" WHERE "account_groups"."account_id" = \$1`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority", "created_at"}))
				m.ExpectBegin()
				m.ExpectExec(`DELETE FROM "account_groups"`).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectExec(`DELETE FROM scheduled_test_plans`).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectExec(`UPDATE "accounts"`).WillReturnError(pgErr)
				m.ExpectRollback()
				err = newAccountRepositoryWithSQL(client, db, nil).Delete(context.Background(), 1)
			case "group":
				m.ExpectExec(`UPDATE "groups"`).WillReturnError(pgErr)
				err = newGroupRepositoryWithSQL(client, db).Delete(context.Background(), 1)
			case "subscription":
				m.ExpectExec(`UPDATE "user_subscriptions"`).WillReturnError(pgErr)
				err = NewUserSubscriptionRepository(client).Delete(context.Background(), 1)
			case "user":
				m.ExpectBegin()
				m.ExpectQuery(`SELECT .* FROM "auth_identities"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
				m.ExpectExec(`UPDATE "users"`).WillReturnError(pgErr)
				m.ExpectRollback()
				err = NewUserRepository(client, db).Delete(context.Background(), 1)
			}
			require.ErrorIs(t, err, service.ErrGeminiInteractionDependencyPending)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
