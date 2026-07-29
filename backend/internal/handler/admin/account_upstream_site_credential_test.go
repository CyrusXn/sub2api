package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type upstreamSiteCredentialHandlerRepo struct {
	credential *service.UpstreamSiteCredential
}

func (r *upstreamSiteCredentialHandlerRepo) ListSites(context.Context) ([]service.UpstreamSiteCredentialSummary, error) {
	return []service.UpstreamSiteCredentialSummary{{
		Host:         "vovoapi.com",
		WebsiteURL:   "https://vovoapi.com",
		AccountIDs:   []int64{12},
		AccountNames: []string{"VoVo Plus"},
	}}, nil
}

func (r *upstreamSiteCredentialHandlerRepo) GetByHost(context.Context, string) (*service.UpstreamSiteCredential, error) {
	return r.credential, nil
}

func (r *upstreamSiteCredentialHandlerRepo) Upsert(_ context.Context, credential *service.UpstreamSiteCredential) error {
	copy := *credential
	r.credential = &copy
	return nil
}

func (r *upstreamSiteCredentialHandlerRepo) Delete(context.Context, string) error {
	r.credential = nil
	return nil
}

type upstreamSiteCredentialHandlerEncryptor struct{}

func (upstreamSiteCredentialHandlerEncryptor) Encrypt(value string) (string, error) {
	return "cipher:" + value, nil
}

func (upstreamSiteCredentialHandlerEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "cipher:"), nil
}

func TestAccountHandlerUpsertUpstreamSiteCredentialNeverReturnsPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &upstreamSiteCredentialHandlerRepo{}
	svc := service.NewUpstreamSiteCredentialService(repo, upstreamSiteCredentialHandlerEncryptor{})
	handler := &AccountHandler{}
	handler.SetUpstreamSiteCredentialService(svc)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{
		"base_url":"https://vovoapi.com/v1",
		"login_username":"admin@example.com",
		"login_password":"secret"
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.UpsertUpstreamSiteCredential(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret")
	var payload response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, 0, payload.Code)
	require.Equal(t, "cipher:secret", repo.credential.PasswordCiphertext)
}
