package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestUpdateUpstreamBillingManualRateMultiplierUsesAtomicJSONBPathUpdate(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value *float64
		query string
		args  []driver.Value
	}{
		{name: "设置", value: repositoryFloat64Pointer(0.03), query: `jsonb_set\(COALESCE\(extra, '\{\}'::jsonb\), '\{upstream_billing_probe,manual_rate_multiplier\}'::text\[\], to_jsonb\(\$1::double precision\), true\)`, args: []driver.Value{0.03, int64(27), `{"api_key":"sk-test"}`, nil, `{"status":"failed"}`, `null`}},
		{name: "清除", query: `COALESCE\(extra, '\{\}'::jsonb\) #- '\{upstream_billing_probe,manual_rate_multiplier\}'::text\[\]`, args: []driver.Value{int64(27), `{"api_key":"sk-test"}`, nil, `{"status":"failed"}`, `null`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })

			mock.ExpectBegin()
			expectation := mock.ExpectExec(`(?s)UPDATE accounts SET extra = ` + tt.query + `.*platform = 'openai'.*type = 'apikey'.*#>> '\{upstream_billing_probe,status\}' = 'failed'`)
			expectation.WithArgs(tt.args...).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
				WithArgs(service.SchedulerOutboxEventAccountChanged, int64(27), nil, nil, sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			repo := newAccountRepositoryWithSQL(client, db, nil)
			err = repo.UpdateUpstreamBillingManualRateMultiplier(context.Background(), &service.Account{
				ID: 27, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{service.UpstreamBillingProbeExtraKey: map[string]any{"status": service.UpstreamBillingProbeStatusFailed}},
			}, tt.value)

			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpstreamBillingRateSortExpressionPrefersFailedManualRate(t *testing.T) {
	expression := upstreamBillingRateSortExpression("extra")
	manualPath := "extra #> '{upstream_billing_probe,manual_rate_multiplier}'"
	automaticPath := "extra #> '{upstream_billing_probe,data,resolved_rate_multiplier}'"

	require.Contains(t, expression, manualPath)
	require.Contains(t, expression, "= 'failed'")
	require.Less(t, indexOrFail(t, expression, manualPath), indexOrFail(t, expression, automaticPath))
}

func repositoryFloat64Pointer(value float64) *float64 {
	return &value
}

func indexOrFail(t *testing.T, value, needle string) int {
	t.Helper()
	index := regexp.MustCompile(regexp.QuoteMeta(needle)).FindStringIndex(value)
	require.NotNil(t, index)
	return index[0]
}
