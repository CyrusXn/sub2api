package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteCredentialRepositoryRechargeConversionScale(t *testing.T) {
	for _, tt := range []struct {
		name     string
		first    float64
		fallback bool
		want     float64
	}{
		{name: "使用首个有效钱包探测账号的系数", first: 1, want: 0.1},
		{name: "没有有效钱包探测账号时沿用首账号", first: 0.2, fallback: true, want: 0.2},
		{name: "非法系数沿用账号默认口径", first: 0, fallback: true, want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteAccountsQuery)).WithArgs(false).WillReturnRows(
				sqlmock.NewRows([]string{"id", "name", "base_url", "upstream_recharge_scale", "status", "type", "has_api_key"}).
					AddRow(1, "首账号", "https://hby.example/v1", tt.first, "inactive", "apikey", true).
					AddRow(2, "无密钥账号", "https://HBY.EXAMPLE", 0.3, "active", "apikey", false).
					AddRow(3, "钱包探测账号", "https://hby.example/v1", 0.1, "active", "apikey", !tt.fallback).
					AddRow(4, "后续账号", "https://hby.example/v1", 0.4, "active", "apikey", !tt.fallback),
			)
			mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteCredentialsQuery)).WillReturnRows(
				sqlmock.NewRows([]string{"host", "display_name", "login_username", "password_encrypted"}),
			)
			sites, err := NewUpstreamSiteCredentialRepository(db).ListSites(context.Background(), false)
			require.NoError(t, err)
			require.Len(t, sites, 1)
			require.Equal(t, []int64{1, 2, 3, 4}, sites[0].AccountIDs)
			encoded, err := json.Marshal(sites[0])
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(encoded, &payload))
			require.Equal(t, tt.want, payload["recharge_conversion_scale"])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpstreamSiteCredentialRepositoryListSitesGroupsAccountsByHost(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamSiteCredentialRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteAccountsQuery)).WithArgs(false).WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "base_url", "upstream_recharge_scale", "status", "type", "has_api_key"}).
			AddRow(12, "VoVo Plus", "https://vovoapi.com/v1", 1, "active", "apikey", true).
			AddRow(14, "VoVo Image", "https://VOVOAPI.com", 1, "active", "apikey", true).
			AddRow(4, "AIGC", "https://api.aigclink.xyz/v1", 1, "active", "apikey", true),
	)
	mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteCredentialsQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"host", "display_name", "login_username", "password_encrypted"}).
			AddRow("vovoapi.com", "VoVo", "admin@example.com", "ciphertext"),
	)

	sites, err := repo.ListSites(context.Background(), false)

	require.NoError(t, err)
	require.Len(t, sites, 2)
	require.Equal(t, "api.aigclink.xyz", sites[0].Host)
	require.Equal(t, "newapi", sites[0].Protocol)
	require.False(t, sites[0].HasPassword)
	require.Equal(t, "vovoapi.com", sites[1].Host)
	require.Equal(t, []int64{12, 14}, sites[1].AccountIDs)
	require.Equal(t, []string{"VoVo Plus", "VoVo Image"}, sites[1].AccountNames)
	require.Equal(t, "admin@example.com", sites[1].LoginUsername)
	require.Equal(t, "VoVo", sites[1].DisplayName)
	require.True(t, sites[1].HasPassword)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteAccountsQueryDoesNotRestrictPlatformOrModel(t *testing.T) {
	// 站点归属来自域名，不能按协议、模型或账号类型丢掉同站点请求。
	require.NotContains(t, upstreamSiteAccountsQuery, "platform =")
	require.NotContains(t, upstreamSiteAccountsQuery, "type =")
	require.NotContains(t, upstreamSiteAccountsQuery, "model =")
	require.Contains(t, upstreamSiteAccountsQuery, "WHERE ($1 OR deleted_at IS NULL)")
}

func TestUpstreamSiteCredentialRepositoryHistoryIncludesAllAccountIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamSiteCredentialRepository(db)
	mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteAccountsQuery)).WithArgs(true).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "base_url", "upstream_recharge_scale", "status", "type", "has_api_key"}).
			AddRow(1, "Claude", "https://relay.example/v1", 1, "active", "apikey", true).
			AddRow(2, "Gemini（历史账号）", "https://relay.example/v1beta", 1, "active", "apikey", true).
			AddRow(3, "Grok", "https://RELAY.example", 1, "active", "apikey", true).
			AddRow(4, "国模", "https://relay.example/api", 1, "active", "apikey", true))
	mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteCredentialsQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"host", "display_name", "login_username", "password_encrypted"}))
	sites, err := repo.ListSites(context.Background(), true)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	require.Equal(t, []int64{1, 2, 3, 4}, sites[0].AccountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteCredentialRepositoryUpsertPersistsCiphertext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamSiteCredentialRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(upstreamSiteCredentialUpsertQuery)).
		WithArgs("vovoapi.com", "VoVo", "admin@example.com", "ciphertext").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE balance_center_sites SET display_name=$1, updated_at=NOW() WHERE normalized_domain=$2")).
		WithArgs("VoVo", "vovoapi.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = repo.Upsert(context.Background(), &service.UpstreamSiteCredential{
		Host:               "vovoapi.com",
		DisplayName:        "VoVo",
		LoginUsername:      "admin@example.com",
		PasswordCiphertext: "ciphertext",
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
