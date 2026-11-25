package admin

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mobileReaderStub struct {
	balanceCenterAdminService
	cursor int64
	mirror bool
}

type mobileStreamReader struct {
	balanceCenterAdminService
	wake chan struct{}
	rows chan service.MobileAlert
}

func (s *mobileStreamReader) WatchMobileAlerts(context.Context) (<-chan struct{}, error) {
	return s.wake, nil
}

func (s *mobileStreamReader) ListMobileAlerts(_ context.Context, after int64, mirror bool) ([]service.MobileAlert, error) {
	if mirror {
		return nil, fmt.Errorf("安卓推送不应读取邮件镜像")
	}
	select {
	case row := <-s.rows:
		if row.ID > after {
			return []service.MobileAlert{row}, nil
		}
	default:
	}
	return nil, nil
}

func TestMobileAlertStreamPushesNewRowsAndHonorsCursor(t *testing.T) {
	const token = "test-only-0123456789012345678901234567890123456789"
	t.Setenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256", fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
	reader := &mobileStreamReader{wake: make(chan struct{}, 1), rows: make(chan service.MobileAlert, 2)}
	h := &BalanceCenterHandler{service: reader}
	r := gin.New()
	r.GET("/stream", h.MobileAlertStream)
	srv := httptest.NewServer(r)
	defer srv.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	denied, err := client.Get(srv.URL + "/stream")
	require.NoError(t, err)
	defer denied.Body.Close()
	require.Equal(t, http.StatusUnauthorized, denied.StatusCode)
	reader.rows <- service.MobileAlert{ID: 42}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/stream?after=42", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	lines := bufio.NewScanner(res.Body)
	require.True(t, lines.Scan())
	require.Equal(t, ": connected", lines.Text())
	reader.rows <- service.MobileAlert{ID: 43, Title: "『派大星』余额不足，当前余额8.88"}
	reader.wake <- struct{}{}
	require.True(t, lines.Scan())
	require.Empty(t, lines.Text())
	require.True(t, lines.Scan())
	require.Contains(t, lines.Text(), `"id":43`)
	require.Contains(t, lines.Text(), "『派大星』余额不足，当前余额8.88")
}

func (s *mobileReaderStub) ListMobileAlerts(_ context.Context, cursor int64, mirror bool) ([]service.MobileAlert, error) {
	s.cursor = cursor
	s.mirror = mirror
	return []service.MobileAlert{{ID: 43, Kind: "low_balance", Title: "余额不足"}}, nil
}

func TestMobileAlertRouteRequiresDedicatedTokenAndValidCursor(t *testing.T) {
	const token = "test-only-0123456789012345678901234567890123456789"
	t.Setenv("SUB2API_MOBILE_ALERT_TOKEN_SHA256", fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
	reader := &mobileReaderStub{}
	h := &BalanceCenterHandler{service: reader}
	r := gin.New()
	r.GET("/mobile-alerts", h.MobileAlerts)
	for _, tc := range []struct {
		method, query, authorization string
		status                       int
		mirror                       bool
	}{
		{"GET", "?after=42&kind=email_mirror", "", http.StatusUnauthorized, false},
		{"GET", "?after=-1", "Bearer " + token, http.StatusBadRequest, false},
		{"POST", "", "Bearer " + token, http.StatusNotFound, false},
		{"GET", "?after=42", "Bearer " + token, http.StatusOK, false},
		{"GET", "?after=42&kind=email_mirror", "Bearer " + token, http.StatusOK, true},
	} {
		req := httptest.NewRequest(tc.method, "/mobile-alerts"+tc.query, nil)
		req.Header.Set("Authorization", tc.authorization)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		require.Equal(t, tc.status, res.Code)
		if tc.status == http.StatusOK {
			require.Equal(t, "no-store", res.Header().Get("Cache-Control"))
			require.Contains(t, res.Body.String(), `"id":43`)
			require.Equal(t, int64(42), reader.cursor)
			require.Equal(t, tc.mirror, reader.mirror)
		} else {
			require.Zero(t, reader.cursor)
		}
	}
}
