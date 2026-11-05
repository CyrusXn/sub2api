//go:build unit

package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAuthService_Register_BlacklistedEmailAliasRejected(t *testing.T) {
	repo := &userRepoStub{nextID: 9}
	svc := newAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled:        "true",
		SettingKeyRegistrationEmailBlacklist: `["spam.user@qq.com"]`,
	}, nil, nil)

	_, _, err := svc.Register(context.Background(), " Spam.User+1@QQ.COM ", "password")
	require.ErrorIs(t, err, ErrEmailBlocked)
	require.Equal(t, "EMAIL_BLOCKED", infraerrors.FromError(err).Reason)
	require.Empty(t, repo.created)
}

func TestAuthService_Register_BlacklistOverridesSuffixWhitelist(t *testing.T) {
	repo := &userRepoStub{nextID: 9}
	svc := newAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled:              "true",
		SettingKeyRegistrationEmailSuffixWhitelist: `["@qq.com"]`,
		SettingKeyRegistrationEmailBlacklist:       `["blocked@qq.com"]`,
	}, nil, nil)

	_, _, err := svc.Register(context.Background(), "blocked@qq.com", "password")
	require.ErrorIs(t, err, ErrEmailBlocked)
	require.Empty(t, repo.created)

	_, user, err := svc.Register(context.Background(), "allowed@qq.com", "password")
	require.NoError(t, err)
	require.Equal(t, "allowed@qq.com", user.Email)
}

func TestAuthService_Registration_BlacklistCheckedBeforeInvitation(t *testing.T) {
	for _, source := range []string{"email", "github"} {
		t.Run(source, func(t *testing.T) {
			repo := &userRepoStub{}
			svc := newAuthService(repo, map[string]string{
				SettingKeyRegistrationEnabled:        "true",
				SettingKeyInvitationCodeEnabled:      "true",
				SettingKeyRegistrationEmailBlacklist: `["blocked@163.com"]`,
			}, nil, nil)
			var err error
			if source == "email" {
				_, _, err = svc.RegisterWithOptions(context.Background(), "blocked@163.com", "password", "", "", "")
			} else {
				_, _, err = svc.RegisterOAuthAccount(context.Background(), "blocked@163.com", "password", "", source)
			}
			require.ErrorIs(t, err, ErrEmailBlocked)
			require.Empty(t, repo.created)
		})
	}
}

func TestAuthService_ValidateRegistrationEmailPolicy_BlacklistedEmailRejected(t *testing.T) {
	svc := newAuthService(&userRepoStub{}, map[string]string{
		SettingKeyRegistrationEmailBlacklist: `["bind.target@gmail.com"]`,
	}, nil, nil)

	require.ErrorIs(t, svc.validateRegistrationEmailPolicy(context.Background(), "bindtarget@gmail.com"), ErrEmailBlocked)
	require.NoError(t, svc.validateRegistrationEmailPolicy(context.Background(), "someone@gmail.com"))
}
