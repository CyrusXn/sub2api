package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingHandlerAdminQuickActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	request := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(method, "/api/v1/admin/settings", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		if method == http.MethodGet {
			handler.GetSettings(c)
		} else {
			handler.UpdateSettings(c)
		}
		return rec
	}
	rec := request(http.MethodGet, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var payload response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.(map[string]any)["admin_quick_actions"], 5)

	rec = request(http.MethodPut, `{"admin_quick_actions":[{"name":" 账号 ","url":" /admin/accounts "}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `[{"name":"账号","url":"/admin/accounts"}]`, repo.values[service.SettingKeyAdminQuickActions])
	rec = request(http.MethodPut, `{}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, service.ParseAdminQuickActions(repo.values[service.SettingKeyAdminQuickActions]), 1)
	rec = request(http.MethodPut, `{"admin_quick_actions":[{"name":"危险","url":"javascript:alert(1)"}]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Len(t, service.ParseAdminQuickActions(repo.values[service.SettingKeyAdminQuickActions]), 1)
	rec = request(http.MethodPut, `{"admin_quick_actions":[]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "[]", repo.values[service.SettingKeyAdminQuickActions])
}
