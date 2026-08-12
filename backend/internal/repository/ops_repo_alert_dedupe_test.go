package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetLatestAlertEventByDedupeKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE rule_id = $1 AND dedupe_key = $2")).
		WithArgs(int64(18), "account:7:http_503:hash").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "rule_id", "severity", "status", "title", "description", "metric_value", "threshold_value",
			"dimensions", "dedupe_key", "fired_at", "resolved_at", "email_sent", "created_at",
		}).AddRow(int64(9), int64(18), "P1", "firing", "账号异常", "503：上游异常", nil, nil, []byte(`{}`), "account:7:http_503:hash", now, nil, false, now))

	repo := &opsRepository{db: db}
	event, err := repo.GetLatestAlertEventByDedupeKey(context.Background(), 18, "account:7:http_503:hash")
	require.NoError(t, err)
	require.Equal(t, "account:7:http_503:hash", event.DedupeKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAlertEventKeepsEmptyDedupeKeyNonNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	metricValue := 21.0
	thresholdValue := 20.0
	mock.ExpectQuery("INSERT INTO ops_alert_events").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"", now, sqlmock.AnyArg(), false,
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "rule_id", "severity", "status", "title", "description", "metric_value", "threshold_value",
			"dimensions", "dedupe_key", "fired_at", "resolved_at", "email_sent", "created_at",
		}).AddRow(int64(10), int64(8), "P0", "firing", "错误率极高", "错误率超过阈值", 21.0, 20.0, nil, "", now, nil, false, now))

	repo := &opsRepository{db: db}
	event, err := repo.CreateAlertEvent(context.Background(), &service.OpsAlertEvent{
		RuleID: 8, Severity: "P0", Status: "firing", Title: "错误率极高", Description: "错误率超过阈值",
		MetricValue: &metricValue, ThresholdValue: &thresholdValue, FiredAt: now,
	})
	require.NoError(t, err)
	require.Empty(t, event.DedupeKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAlertEventNormalizesNonEmptyDedupeKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery("INSERT INTO ops_alert_events").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"account:7:http_503:hash", now, sqlmock.AnyArg(), false,
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "rule_id", "severity", "status", "title", "description", "metric_value", "threshold_value",
			"dimensions", "dedupe_key", "fired_at", "resolved_at", "email_sent", "created_at",
		}).AddRow(int64(11), int64(18), "P1", "firing", "账号异常", "上游异常", nil, nil, nil, "account:7:http_503:hash", now, nil, false, now))

	repo := &opsRepository{db: db}
	event, err := repo.CreateAlertEvent(context.Background(), &service.OpsAlertEvent{
		RuleID: 18, Severity: "P1", Status: "firing", Title: "账号异常", Description: "上游异常",
		DedupeKey: "  account:7:http_503:hash  ", FiredAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, "account:7:http_503:hash", event.DedupeKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInsertAlertAccountDetailsIncludesRequestAttribution(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	userID := int64(11)
	apiKeyID := int64(22)
	groupID := int64(33)
	mock.ExpectBegin()
	expectedArgs := []driver.Value{
		int64(1), int64(2), "主账号", "openai", groupID, "Plus", "http_503", "upstream", 503, now,
		int64(44), userID, "user@example.com", apiKeyID, "生产 Key", "req-1", "client-1",
		"503：上游异常", "上游异常", "gpt-5.5", "gpt-5.5-2026-04-23",
	}
	mock.ExpectExec("INSERT INTO ops_alert_account_details").WithArgs(expectedArgs...).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := &opsRepository{db: db}
	err = repo.InsertAlertAccountDetails(context.Background(), []*service.OpsAlertAccountDetail{{
		AlertEventID: 1, AccountID: 2, AccountName: "主账号", Platform: "openai", GroupID: &groupID, GroupName: "Plus",
		Diagnosis: "http_503", ErrorPhase: "upstream", StatusCode: 503, OccurredAt: now,
		ErrorLogID: 44, UserID: &userID, UserEmail: "user@example.com", APIKeyID: &apiKeyID, APIKeyName: "生产 Key",
		RequestID: "req-1", ClientRequestID: "client-1", ErrorReason: "503：上游异常", ErrorMessage: "上游异常",
		RequestedModel: "gpt-5.5", UpstreamModel: "gpt-5.5-2026-04-23",
	}})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
