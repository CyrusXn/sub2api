package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type upstreamSiteCredentialRepoStub struct {
	sites       []UpstreamSiteCredentialSummary
	credentials map[string]*UpstreamSiteCredential
}

func (r *upstreamSiteCredentialRepoStub) ListSites(context.Context) ([]UpstreamSiteCredentialSummary, error) {
	return append([]UpstreamSiteCredentialSummary(nil), r.sites...), nil
}

func (r *upstreamSiteCredentialRepoStub) GetByHost(_ context.Context, host string) (*UpstreamSiteCredential, error) {
	credential := r.credentials[host]
	if credential == nil {
		return nil, nil
	}
	copy := *credential
	return &copy, nil
}

func (r *upstreamSiteCredentialRepoStub) Upsert(_ context.Context, credential *UpstreamSiteCredential) error {
	if r.credentials == nil {
		r.credentials = make(map[string]*UpstreamSiteCredential)
	}
	copy := *credential
	r.credentials[credential.Host] = &copy
	return nil
}

func (r *upstreamSiteCredentialRepoStub) Delete(_ context.Context, host string) error {
	delete(r.credentials, host)
	return nil
}

type upstreamSiteCredentialEncryptorStub struct{}

func (upstreamSiteCredentialEncryptorStub) Encrypt(value string) (string, error) {
	return "cipher:" + value, nil
}

func (upstreamSiteCredentialEncryptorStub) Decrypt(value string) (string, error) {
	if len(value) < len("cipher:") || value[:len("cipher:")] != "cipher:" {
		return "", errors.New("invalid ciphertext")
	}
	return value[len("cipher:"):], nil
}

func TestUpstreamSiteCredentialServiceUpsertNormalizesHostAndPreservesPassword(t *testing.T) {
	repo := &upstreamSiteCredentialRepoStub{}
	svc := NewUpstreamSiteCredentialService(repo, upstreamSiteCredentialEncryptorStub{})

	result, err := svc.Upsert(context.Background(), UpstreamSiteCredentialInput{
		BaseURL:  "https://VOVOAPI.com/v1",
		Username: " admin@example.com ",
		Password: " secret ",
	})

	require.NoError(t, err)
	require.Equal(t, "vovoapi.com", result.Host)
	require.Equal(t, "https://vovoapi.com", result.WebsiteURL)
	require.Equal(t, "admin@example.com", result.LoginUsername)
	require.True(t, result.HasPassword)
	require.Equal(t, "cipher: secret ", repo.credentials["vovoapi.com"].PasswordCiphertext)
}

func TestUpstreamSiteCredentialServiceBlankPasswordPreservesExistingCiphertext(t *testing.T) {
	repo := &upstreamSiteCredentialRepoStub{credentials: map[string]*UpstreamSiteCredential{
		"vovoapi.com": {
			Host:               "vovoapi.com",
			LoginUsername:      "old@example.com",
			PasswordCiphertext: "cipher:old-password",
		},
	}}
	svc := NewUpstreamSiteCredentialService(repo, upstreamSiteCredentialEncryptorStub{})

	_, err := svc.Upsert(context.Background(), UpstreamSiteCredentialInput{
		BaseURL:  "https://vovoapi.com",
		Username: "new@example.com",
	})

	require.NoError(t, err)
	require.Equal(t, "new@example.com", repo.credentials["vovoapi.com"].LoginUsername)
	require.Equal(t, "cipher:old-password", repo.credentials["vovoapi.com"].PasswordCiphertext)
}

func TestUpstreamSiteCredentialServiceResolveReturnsDecryptedCredential(t *testing.T) {
	repo := &upstreamSiteCredentialRepoStub{credentials: map[string]*UpstreamSiteCredential{
		"vovoapi.com": {
			Host:               "vovoapi.com",
			LoginUsername:      "admin@example.com",
			PasswordCiphertext: "cipher:secret",
		},
	}}
	svc := NewUpstreamSiteCredentialService(repo, upstreamSiteCredentialEncryptorStub{})

	credential, err := svc.Resolve(context.Background(), "https://vovoapi.com/v1")

	require.NoError(t, err)
	require.Equal(t, "admin@example.com", credential.Username)
	require.Equal(t, "secret", credential.Password)
}

func TestUpstreamSiteCredentialServiceDeleteUsesNormalizedHost(t *testing.T) {
	repo := &upstreamSiteCredentialRepoStub{credentials: map[string]*UpstreamSiteCredential{
		"vovoapi.com": {Host: "vovoapi.com"},
	}}
	svc := NewUpstreamSiteCredentialService(repo, upstreamSiteCredentialEncryptorStub{})

	err := svc.Delete(context.Background(), "HTTPS://VOVOAPI.COM/v1")

	require.NoError(t, err)
	require.Empty(t, repo.credentials)
}
