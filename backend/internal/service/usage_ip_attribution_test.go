package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type usageIPAttributionStoreStub struct {
	apiKeyID int64
	sourceIP string
	targetIP string
	ttl      time.Duration
	found    bool
	err      error
}

func (s *usageIPAttributionStoreStub) SetUsageIPAttributionLease(_ context.Context, apiKeyID int64, sourceIP string, targetIP string, ttl time.Duration) error {
	s.apiKeyID = apiKeyID
	s.sourceIP = sourceIP
	s.targetIP = targetIP
	s.ttl = ttl
	return s.err
}

func (s *usageIPAttributionStoreStub) GetUsageIPAttributionLease(_ context.Context, apiKeyID int64, sourceIP string) (string, bool, error) {
	s.apiKeyID = apiKeyID
	s.sourceIP = sourceIP
	return s.targetIP, s.found, s.err
}

func TestUsageIPAttributionServiceCreateLeaseUsesFixedTTL(t *testing.T) {
	store := &usageIPAttributionStoreStub{}
	svc := NewUsageIPAttributionService(store)

	if err := svc.CreateLease(context.Background(), 42, "198.51.100.8", "203.0.113.9"); err != nil {
		t.Fatalf("创建租约不应失败: %v", err)
	}
	if store.apiKeyID != 42 || store.sourceIP != "198.51.100.8" || store.targetIP != "203.0.113.9" {
		t.Fatalf("租约参数不匹配: %#v", store)
	}
	if store.ttl != CCSwitchIPAttributionLeaseTTL {
		t.Fatalf("租约 TTL = %v，期望 %v", store.ttl, CCSwitchIPAttributionLeaseTTL)
	}
}

func TestResolveUsageLogIPAddressUsesAdminFallback(t *testing.T) {
	got := ResolveUsageLogIPAddress(context.Background(), "198.51.100.8", &APIKey{ID: 7}, &User{Email: AdminUsageAttributionEmail}, nil)
	if got != AdminUsageFallbackIP {
		t.Fatalf("管理员请求 IP = %q，期望 %q", got, AdminUsageFallbackIP)
	}
}

func TestResolveUsageLogIPAddressUsesMatchingLease(t *testing.T) {
	store := &usageIPAttributionStoreStub{targetIP: "203.0.113.9", found: true}
	svc := NewUsageIPAttributionService(store)

	got := ResolveUsageLogIPAddress(context.Background(), "198.51.100.8", &APIKey{ID: 42}, &User{Email: "user@example.com"}, svc)

	if got != "203.0.113.9" {
		t.Fatalf("命中租约后的 IP = %q，期望候选 IP", got)
	}
	if store.apiKeyID != 42 || store.sourceIP != "198.51.100.8" {
		t.Fatalf("租约查询参数不匹配: %#v", store)
	}
}

func TestResolveUsageLogIPAddressKeepsRealIPWithoutLease(t *testing.T) {
	store := &usageIPAttributionStoreStub{found: false}
	svc := NewUsageIPAttributionService(store)

	got := ResolveUsageLogIPAddress(context.Background(), "198.51.100.8", &APIKey{ID: 42}, &User{Email: "user@example.com"}, svc)

	if got != "198.51.100.8" {
		t.Fatalf("未命中租约时 IP = %q，期望真实 IP", got)
	}
}

func TestResolveUsageLogIPAddressKeepsRealIPOnStoreError(t *testing.T) {
	store := &usageIPAttributionStoreStub{err: errors.New("redis unavailable")}
	svc := NewUsageIPAttributionService(store)

	got := ResolveUsageLogIPAddress(context.Background(), "198.51.100.8", &APIKey{ID: 42}, &User{Email: "user@example.com"}, svc)

	if got != "198.51.100.8" {
		t.Fatalf("缓存故障时 IP = %q，期望真实 IP", got)
	}
}
