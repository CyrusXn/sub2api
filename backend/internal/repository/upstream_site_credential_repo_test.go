package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteCredentialRepositoryListSitesGroupsAccountsByHost(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamSiteCredentialRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteAccountsQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "base_url"}).
			AddRow(12, "VoVo Plus", "https://vovoapi.com/v1").
			AddRow(14, "VoVo Image", "https://VOVOAPI.com").
			AddRow(4, "AIGC", "https://api.aigclink.xyz/v1"),
	)
	mock.ExpectQuery(regexp.QuoteMeta(upstreamSiteCredentialsQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"host", "display_name", "login_username", "password_encrypted"}).
			AddRow("vovoapi.com", "VoVo", "admin@example.com", "ciphertext"),
	)

	sites, err := repo.ListSites(context.Background())

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
