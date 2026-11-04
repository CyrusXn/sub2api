package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qualityRepoStub struct {
	created        int
	runs           []service.ModelQualityRun
	createError    error
	createdRun     *service.ModelQualityRun
	updatedID      int64
	updatedUserID  int64
	updatedStatus  string
	updatedContent string
	listedGroup    string
	listedModel    string
	listedUser     string
	listedAdmin    bool
	deletedID      int64
	deleteError    error
}

func (r *qualityRepoStub) Delete(_ context.Context, id int64) error {
	r.deletedID = id
	return r.deleteError
}

func TestModelQualityDeleteRequiresAuthenticatedAdmin(t *testing.T) {
	for _, role := range []string{service.RoleUser, service.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			repo := &qualityRepoStub{}
			h := &ModelQualityHandler{service: service.NewModelQualityService(repo)}
			c, w := qualityContext("DELETE", "/model-quality/17?admin=true", "", role)
			c.Params = gin.Params{{Key: "id", Value: "17"}}
			h.Delete(c)
			if role == service.RoleAdmin {
				require.Equal(t, http.StatusOK, w.Code)
				require.EqualValues(t, 17, repo.deletedID)
			} else {
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Zero(t, repo.deletedID)
			}
		})
	}
	c, w := qualityContext("DELETE", "/model-quality/17", "", service.RoleAdmin)
	c.Set(string(middleware.ContextKeyUser), nil)
	(&ModelQualityHandler{}).Delete(c)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestModelQualityDeleteValidatesIDAndReportsFailures(t *testing.T) {
	for _, tc := range []struct {
		id   string
		err  error
		code int
	}{
		{"0", nil, http.StatusBadRequest},
		{"invalid", nil, http.StatusBadRequest},
		{"17", service.ErrModelQualityNotFound, http.StatusNotFound},
		{"17", errors.New("private database details"), http.StatusInternalServerError},
	} {
		repo := &qualityRepoStub{deleteError: tc.err}
		c, w := qualityContext("DELETE", "/model-quality/"+tc.id, "", service.RoleAdmin)
		c.Params = gin.Params{{Key: "id", Value: tc.id}}
		(&ModelQualityHandler{service: service.NewModelQualityService(repo)}).Delete(c)
		require.Equal(t, tc.code, w.Code)
		require.NotContains(t, w.Body.String(), "private database details")
		if tc.code == http.StatusBadRequest {
			require.Zero(t, repo.deletedID)
		}
	}
}

func (r *qualityRepoStub) Update(_ context.Context, id, userID int64, status, content, requestID string) error {
	r.updatedID, r.updatedUserID, r.updatedStatus, r.updatedContent = id, userID, status, content
	return nil
}

func TestModelQualityCreateStartsSharedRecordBeforeGeneration(t *testing.T) {
	groupID := int64(2)
	repo := &qualityRepoStub{}
	h := &ModelQualityHandler{service: service.NewModelQualityService(repo), keys: qualityKeyStub{&service.APIKey{ID: 2, UserID: 12, Status: service.StatusActive, GroupID: &groupID, Group: &service.Group{ID: 2, Name: "测试分组"}, User: &service.User{Email: "admin@example.com"}}}}
	c, w := qualityContext("POST", "/model-quality", `{"api_key_id":2,"model":"gpt-test","theme":"candy","content":"","status":"running"}`, service.RoleUser)
	h.Create(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "running", repo.createdRun.Status)
	require.Contains(t, w.Body.String(), "ad***@example.com")
	require.NotContains(t, w.Body.String(), "admin@example.com")
}

func TestModelQualityProgressUsesAuthenticatedOwner(t *testing.T) {
	repo := &qualityRepoStub{}
	h := &ModelQualityHandler{service: service.NewModelQualityService(repo)}
	c, w := qualityContext("PATCH", "/model-quality/17", `{"status":"running","content":"回答第一段","request_id":"req-1","user_id":99}`, service.RoleAdmin)
	c.Params = gin.Params{{Key: "id", Value: "17"}}
	h.Update(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.EqualValues(t, 17, repo.updatedID)
	require.EqualValues(t, 12, repo.updatedUserID)
	require.Equal(t, "回答第一段", repo.updatedContent)
}

func TestModelQualityOfficialConfigurationRequiresAdmin(t *testing.T) {
	h := &ModelQualityHandler{}
	c, w := qualityContext("PUT", "/model-quality/official", `{"enabled":true,"api_key_id":3,"model":"gpt-test"}`, service.RoleUser)
	h.ConfigureOfficial(c)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func (r *qualityRepoStub) Create(_ context.Context, run *service.ModelQualityRun) error {
	r.created++
	run.ID = 17
	r.createdRun = run
	return r.createError
}
func (r *qualityRepoStub) List(_ context.Context, _ int, _, _, group, model string, user service.ModelQualityUserFilter) ([]service.ModelQualityRun, int64, error) {
	r.listedGroup, r.listedModel = group, model
	r.listedUser, r.listedAdmin = user.Search, user.Admin
	return append([]service.ModelQualityRun(nil), r.runs...), int64(len(r.runs)), nil
}
func (r *qualityRepoStub) FilterOptions(context.Context, string) (service.ModelQualityFilterOptions, error) {
	return service.ModelQualityFilterOptions{Groups: []string{"共享分组"}, Models: []string{"shared-model"}}, nil
}

type qualityKeyStub struct{ key *service.APIKey }

func (s qualityKeyStub) GetByID(context.Context, int64) (*service.APIKey, error) { return s.key, nil }
func qualityContext(method, path, body, role string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 12})
	c.Set(string(middleware.ContextKeyUserRole), role)
	return c, recorder
}
func TestModelQualityHandlerSharedListPrivacy(t *testing.T) {
	repo := &qualityRepoStub{runs: []service.ModelQualityRun{{UserID: 99, Author: "private@example.com", AccountID: 7, AccountName: "私有账号", Model: "gpt-test"}}}
	h := &ModelQualityHandler{service: service.NewModelQualityService(repo)}
	for _, admin := range []bool{false, true} {
		role := service.RoleUser
		if admin {
			role = service.RoleAdmin
		}
		c, w := qualityContext("GET", "/model-quality?admin=true", "", role)
		h.List(c)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), "gpt-test")
		require.NotContains(t, w.Body.String(), "private@example.com")
		require.NotContains(t, w.Body.String(), "user_id")
		if admin {
			require.Contains(t, w.Body.String(), "私有账号")
		} else {
			require.NotContains(t, w.Body.String(), "account")
		}
	}
}
func TestModelQualityHandlerRejectsOtherUsersKeys(t *testing.T) {
	repo := &qualityRepoStub{}
	h := &ModelQualityHandler{service: service.NewModelQualityService(repo), keys: qualityKeyStub{&service.APIKey{UserID: 99}}}
	c, w := qualityContext("POST", "/model-quality", `{"api_key_id":2,"model":"gpt-test","theme":"candy","content":"answer","status":"completed"}`, service.RoleAdmin)
	h.Create(c)
	require.Equal(t, 403, w.Code)
	require.Zero(t, repo.created)
}
func TestModelQualityListRejectsInvalidFilters(t *testing.T) {
	h := &ModelQualityHandler{service: service.NewModelQualityService(&qualityRepoStub{})}
	for _, query := range []string{"page=-1", "theme=other", "status=other", "user=" + strings.Repeat("x", 201)} {
		c, w := qualityContext("GET", "/?"+query, "", service.RoleUser)
		h.List(c)
		require.Equal(t, 400, w.Code)
	}
}

func TestModelQualityListUserSearchUsesAuthenticatedRole(t *testing.T) {
	for _, role := range []string{service.RoleUser, service.RoleAdmin} {
		repo := &qualityRepoStub{}
		h := &ModelQualityHandler{service: service.NewModelQualityService(repo)}
		c, w := qualityContext("GET", "/model-quality?user=%20Ad%2A%2A%2A%40example.com%20&admin=true", "", role)
		h.List(c)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "Ad***@example.com", repo.listedUser)
		require.Equal(t, role == service.RoleAdmin, repo.listedAdmin)
	}
}

func TestModelQualityListForwardsGroupAndModelAndReturnsSharedOptions(t *testing.T) {
	repo := &qualityRepoStub{}
	h := &ModelQualityHandler{service: service.NewModelQualityService(repo)}
	c, w := qualityContext("GET", "/model-quality?theme=candy&group=shared&model=gpt-test", "", service.RoleUser)
	h.List(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "shared", repo.listedGroup)
	require.Equal(t, "gpt-test", repo.listedModel)
	require.Contains(t, w.Body.String(), "shared-model")
	require.Contains(t, w.Body.String(), "共享分组")
}

func TestModelQualityCreatePersistsCompletedChatAnswer(t *testing.T) {
	groupID := int64(2)
	repo := &qualityRepoStub{}
	h := &ModelQualityHandler{service: service.NewModelQualityService(repo), keys: qualityKeyStub{&service.APIKey{ID: 2, UserID: 12, Status: service.StatusActive, Key: "test-only", GroupID: &groupID, Group: &service.Group{ID: 2, Name: "测试分组"}}}}
	c, w := qualityContext("POST", "/model-quality", `{"api_key_id":2,"model":"gpt-test","theme":"candy","content":"模型回答 28 个","status":"completed","request_id":"req-1"}`, service.RoleUser)
	h.Create(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, service.ModelQualityCandyPrompt, repo.createdRun.Prompt)
	require.Equal(t, "模型回答 28 个", repo.createdRun.Content)
	require.Equal(t, "req-1", repo.createdRun.RequestID)
	require.Equal(t, "completed", repo.createdRun.Status)
}
