package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type qualitySettingsFake struct{ value string }

func (s *qualitySettingsFake) GetValue(context.Context, string) (string, error) { return s.value, nil }
func (s *qualitySettingsFake) Set(_ context.Context, _ string, value string) error {
	s.value = value
	return nil
}

type qualityKeysFake struct{ key *APIKey }

func (s qualityKeysFake) GetByID(context.Context, int64) (*APIKey, error) { return s.key, nil }

type officialRepoFake struct {
	runs    []*ModelQualityRun
	slots   map[time.Time]bool
	updates []string
}

func (r *officialRepoFake) Create(_ context.Context, run *ModelQualityRun) error {
	if r.slots == nil {
		r.slots = map[time.Time]bool{}
	}
	if r.slots[*run.OfficialSlot] {
		return ErrModelQualityAlreadyScheduled
	}
	r.slots[*run.OfficialSlot] = true
	run.ID = int64(len(r.runs) + 1)
	r.runs = append(r.runs, run)
	return nil
}
func (r *officialRepoFake) Update(_ context.Context, _, _ int64, status, content, _ string) error {
	r.updates = append(r.updates, status+":"+content)
	return nil
}
func (r *officialRepoFake) List(context.Context, int, string, string, string, string) ([]ModelQualityRun, int64, error) {
	return nil, 0, nil
}
func (r *officialRepoFake) FilterOptions(context.Context, string) (ModelQualityFilterOptions, error) {
	return ModelQualityFilterOptions{}, nil
}
func officialTestKey() *APIKey {
	gid := int64(2)
	return &APIKey{ID: 3, UserID: 12, Name: "官key", Key: "test-only-secret", Status: StatusActive, GroupID: &gid, Group: &Group{Name: "官key分组"}, User: &User{Email: "admin@example.com", Role: RoleAdmin, Status: StatusActive}}
}

func TestModelQualityOfficialDisabledAndOwnership(t *testing.T) {
	settings, repo := &qualitySettingsFake{}, &officialRepoFake{}
	s := NewModelQualityOfficialService(repo, settings, qualityKeysFake{officialTestKey()}, "http://127.0.0.1:1")
	s.runOnce(context.Background(), time.Now())
	require.Empty(t, repo.runs)
	err := s.Configure(context.Background(), 99, 3, "gpt-test", true)
	require.Error(t, err)
	require.Empty(t, settings.value)
	require.NoError(t, s.Configure(context.Background(), 12, 3, "gpt-test", true))
	require.NotContains(t, settings.value, "test-only-secret")
	public, err := s.Status(context.Background(), false)
	require.NoError(t, err)
	require.Zero(t, public.APIKeyID)
	require.True(t, public.Enabled)
}

func TestModelQualityOfficialHourlyClaimAndStream(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer test-only-secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Client-Request-ID", "test-request-id")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"我是\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"测试模型\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	settings, repo := &qualitySettingsFake{}, &officialRepoFake{}
	s := NewModelQualityOfficialService(repo, settings, qualityKeysFake{officialTestKey()}, server.URL)
	require.NoError(t, s.Configure(context.Background(), 12, 3, "gpt-test", true))
	now := time.Date(2026, 11, 4, 10, 2, 0, 0, time.UTC)
	s.runOnce(context.Background(), now)
	s.runOnce(context.Background(), now.Add(20*time.Minute))
	require.Equal(t, 1, calls)
	require.Equal(t, "你是谁", repo.runs[0].Prompt)
	require.Equal(t, "official", repo.runs[0].Theme)
	require.Contains(t, repo.updates, "completed:我是测试模型")
	s.runOnce(context.Background(), now.Add(time.Hour))
	require.Equal(t, 2, calls)
}

func TestModelQualityOfficialRejectsIncompleteAndDoesNotLeakUpstream(t *testing.T) {
	err := readModelQualityStream(context.Background(), strings.NewReader("data: {\"error\":{\"message\":\"private secret\"}}\n\n"), func(string) error { return nil })
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
	err = readModelQualityStream(context.Background(), strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), func(string) error { return nil })
	require.Error(t, err)
	require.False(t, errors.Is(err, context.Canceled))
}

func TestModelQualityOfficialFailureIsFinalAndNotRetriedWithinHour(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"HTTP错误", http.StatusUnauthorized, "private upstream secret"},
		{"回答截断", http.StatusOK, "data: {\"choices\":[{\"delta\":{\"content\":\"部分回答\"}}]}\n\n"},
		{"空白回答", http.StatusOK, "data: {\"choices\":[{\"delta\":{\"content\":\" \"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			settings, repo := &qualitySettingsFake{}, &officialRepoFake{}
			s := NewModelQualityOfficialService(repo, settings, qualityKeysFake{officialTestKey()}, server.URL)
			require.NoError(t, s.Configure(context.Background(), 12, 3, "gpt-test", true))
			now := time.Date(2026, 11, 4, 10, 2, 0, 0, time.UTC)
			s.runOnce(context.Background(), now)
			s.runOnce(context.Background(), now.Add(time.Minute))
			require.Equal(t, 1, calls)
			require.Len(t, repo.runs, 1)
			require.True(t, strings.HasPrefix(repo.updates[len(repo.updates)-1], "failed:"))
			require.NotContains(t, strings.Join(repo.updates, "|"), "secret")
		})
	}
}

func TestModelQualityOfficialRevokedKeyDoesNotSendRequest(t *testing.T) {
	key := officialTestKey()
	settings, repo := &qualitySettingsFake{}, &officialRepoFake{}
	s := NewModelQualityOfficialService(repo, settings, qualityKeysFake{key}, "http://127.0.0.1:1")
	require.NoError(t, s.Configure(context.Background(), 12, 3, "gpt-test", true))
	key.Status = "disabled"
	s.runOnce(context.Background(), time.Now())
	require.Len(t, repo.runs, 1)
	require.Equal(t, []string{"failed:"}, repo.updates)
}

func TestModelQualityStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := readModelQualityStream(ctx, strings.NewReader("data: [DONE]\n\n"), func(string) error { t.Fatal("已取消的请求不能追加正文"); return nil })
	require.ErrorIs(t, err, context.Canceled)
}
