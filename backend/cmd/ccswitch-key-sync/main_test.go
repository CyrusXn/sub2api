package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelectTargetKeyKeepsActiveCurrentCandidate(t *testing.T) {
	candidates := []candidate{
		{APIKey: "newest-key", APIKeyID: 2},
		{APIKey: "active-current-key", APIKeyID: 1},
	}

	selection := selectTargetKey("active-current-key", "admin-fallback-key", candidates)

	if selection.TargetKey != "active-current-key" {
		t.Fatalf("应保持仍有新请求的当前 Key，实际选择 %q", selection.TargetKey)
	}
	if selection.Candidate == nil || selection.Candidate.APIKeyID != 1 {
		t.Fatalf("应返回当前 Key 对应的候选记录，实际为 %#v", selection.Candidate)
	}
}

func TestSelectTargetKeySwitchesToNewestCandidateWhenCurrentIsIdle(t *testing.T) {
	candidates := []candidate{
		{APIKey: "newest-key", APIKeyID: 2},
		{APIKey: "older-key", APIKeyID: 1},
	}

	selection := selectTargetKey("idle-current-key", "admin-fallback-key", candidates)

	if selection.TargetKey != "newest-key" {
		t.Fatalf("当前 Key 空闲时应切换到最新候选，实际选择 %q", selection.TargetKey)
	}
	if selection.Candidate == nil || selection.Candidate.APIKeyID != 2 {
		t.Fatalf("应返回最新候选记录，实际为 %#v", selection.Candidate)
	}
}

func TestSelectTargetKeyUsesFallbackWithoutCandidates(t *testing.T) {
	selection := selectTargetKey("idle-current-key", "admin-fallback-key", nil)

	if selection.TargetKey != "admin-fallback-key" {
		t.Fatalf("无候选时应恢复管理员 Key，实际选择 %q", selection.TargetKey)
	}
	if selection.Candidate != nil {
		t.Fatalf("无候选时不应返回候选记录，实际为 %#v", selection.Candidate)
	}
}

func TestResolveFallbackKeyPinsStartupProviderKey(t *testing.T) {
	got, err := resolveFallbackKey("", "startup-admin-key")
	if err != nil {
		t.Fatalf("启动时固定管理员 Key 不应失败: %v", err)
	}
	if got != "startup-admin-key" {
		t.Fatalf("应固定启动时的 provider Key，实际为 %q", got)
	}
}

func TestCreateAttributionLeaseOnlySendsAPIKeyID(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("请求方法 = %s，期望 POST", r.Method)
		}
		if r.URL.Path != attributionLeaseEndpointPath {
			t.Fatalf("请求路径 = %s，期望 %s", r.URL.Path, attributionLeaseEndpointPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"api_key_id":42}}`))
	}))
	defer server.Close()

	err := createAttributionLease(context.Background(), config{
		BaseURL:     server.URL,
		AdminAPIKey: "admin-token",
	}, candidate{APIKeyID: 42, IPAddress: "203.0.113.9"})
	if err != nil {
		t.Fatalf("登记租约不应失败: %v", err)
	}
	if len(body) != 1 || body["api_key_id"] != float64(42) {
		t.Fatalf("请求体只能包含 api_key_id，实际为 %#v", body)
	}
}
