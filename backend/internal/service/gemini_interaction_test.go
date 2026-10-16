//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type interactionMemoryRepo struct {
	GeminiInteractionRepository
	records map[string]*GeminiInteraction
	updates int
}

type interactionReceiptRepo struct {
	GeminiInteractionRepository
	marks int
}

func (r *interactionReceiptRepo) HasUsageBilling(context.Context, string, int64) (bool, error) {
	return true, nil
}
func (r *interactionReceiptRepo) MarkSettled(context.Context, int64) error { r.marks++; return nil }

func TestGeminiInteractionSettlementRecoversCommittedReceiptWithoutRebilling(t *testing.T) {
	repo := &interactionReceiptRepo{}
	// 不配置可写计费依赖：已提交凭证必须先于当前账号/价格重新计算被识别。
	svc := NewGeminiInteractionService(repo, nil, &GatewayService{}, &APIKeyService{}, nil)
	r := &GeminiInteraction{ID: 1, PublicID: "local", APIKeyID: 2, UserID: 3, Status: "completed", Usage: &ClaudeUsage{InputTokens: 2, OutputTokens: 3}}
	require.NoError(t, svc.Settle(context.Background(), r))
	require.True(t, r.Settled)
	require.NoError(t, svc.Settle(context.Background(), r))
	require.Equal(t, 1, repo.marks)
}

func TestGeminiInteractionMissingTerminalUsageNeedsReviewWithoutRefund(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{ID: 1, PublicID: "cancelled", UpstreamID: "up", Status: "cancelled", BalanceHold: 1.5}
	require.NoError(t, svc.Settle(context.Background(), r))
	require.Equal(t, 1, repo.updates, "缺失用量终态必须持久化待核对，不能无限占执行并发")
	require.False(t, r.Settled)
	require.Equal(t, 1.5, r.BalanceHold)
}

type orphanInteractionRepo struct{ interactionMemoryRepo }

func (r *orphanInteractionRepo) Pending(context.Context, int) ([]*GeminiInteraction, error) {
	return []*GeminiInteraction{{ID: 1, PublicID: "orphan", Status: "creating", BalanceHold: 2, CreatedAt: time.Now().Add(-20 * time.Minute)}}, nil
}

func TestGeminiInteractionRecoveryClassifiesOrphanWithoutReposting(t *testing.T) {
	repo := &orphanInteractionRepo{interactionMemoryRepo{records: map[string]*GeminiInteraction{}}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	n, err := svc.RecoverPending(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, repo.updates, "无上游ID的过期创建任务也必须收敛到待核对")
	require.Equal(t, 2.0, repo.records["orphan"].BalanceHold)
	require.False(t, repo.records["orphan"].Settled)
}

func TestGeminiInteractionInvalidUsageStillPersistsBinding(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{PublicID: "local", Status: "pending"}
	_, err := svc.Observe(context.Background(), r, []byte(`{"id":"up","status":"completed","usage":{"total_input_tokens":-1,"total_output_tokens":3}}`))
	require.Error(t, err)
	require.Equal(t, "up", repo.records["local"].UpstreamID)
	require.Nil(t, repo.records["local"].Usage)
	require.Equal(t, "terminal_usage_missing", repo.records["local"].ReviewReason)
}

func TestGeminiInteractionProgressUsageCannotSettleAsFinalUsage(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{PublicID: "local"}
	_, err := svc.Observe(context.Background(), r, []byte(`{"id":"up","status":"in_progress","usage":{"total_input_tokens":2,"total_output_tokens":1}}`))
	require.NoError(t, err)
	require.Nil(t, r.Usage)
	_, err = svc.Observe(context.Background(), r, []byte(`{"id":"up","status":"completed"}`))
	require.NoError(t, err)
	require.Nil(t, r.Usage)
	require.NoError(t, svc.Settle(context.Background(), r))
	require.False(t, r.Settled)
}

func (m *interactionMemoryRepo) Create(_ context.Context, r *GeminiInteraction) error {
	r.ID = int64(len(m.records) + 1)
	c := *r
	m.records[r.PublicID] = &c
	return nil
}
func (m *interactionMemoryRepo) GetOwned(_ context.Context, k, u, g int64, id string) (*GeminiInteraction, error) {
	r := m.records[id]
	if r == nil || r.APIKeyID != k || r.UserID != u || r.GroupID != g || r.Deleted {
		return nil, ErrGeminiInteractionNotFound
	}
	c := *r
	return &c, nil
}
func (m *interactionMemoryRepo) Update(_ context.Context, r *GeminiInteraction) error {
	m.updates++
	c := *r
	m.records[r.PublicID] = &c
	return nil
}

func TestGeminiInteractionObservePersistsNativeResponse(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{PublicID: "local", APIKeyID: 1, UserID: 2, GroupID: 3}
	require.NoError(t, repo.Create(context.Background(), r))
	out, err := svc.Observe(context.Background(), r, []byte(`{"id":"upstream","model":"gemini-4-argon-high","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"你好"}]}],"usage":{"total_input_tokens":10,"total_cached_tokens":2,"total_output_tokens":4,"total_thought_tokens":3}}`))
	require.NoError(t, err)
	require.Contains(t, string(out), `"id":"local"`)
	require.Contains(t, string(out), `"text":"你好"`)
	require.Equal(t, "upstream", r.UpstreamID)
	require.Equal(t, "completed", r.Status)
	require.NotNil(t, r.Usage)
	require.Equal(t, 8, r.Usage.InputTokens)
	require.Equal(t, 2, r.Usage.CacheReadInputTokens)
	require.Equal(t, 1, repo.updates)
}
func TestGeminiInteractionOwnerIsolation(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	require.NoError(t, repo.Create(context.Background(), &GeminiInteraction{PublicID: "owned", APIKeyID: 1, UserID: 2, GroupID: 3}))
	_, err := svc.GetOwned(context.Background(), 1, 2, 3, "owned")
	require.NoError(t, err)
	for _, ids := range [][3]int64{{9, 2, 3}, {1, 9, 3}, {1, 2, 9}} {
		_, err = svc.GetOwned(context.Background(), ids[0], ids[1], ids[2], "owned")
		require.ErrorIs(t, err, ErrGeminiInteractionNotFound)
	}
}
func TestGeminiInteractionObserveSSEEnvelope(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{PublicID: "local"}
	require.NoError(t, repo.Create(context.Background(), r))
	out, err := svc.Observe(context.Background(), r, []byte(`{"event_type":"interaction.start","interaction":{"id":"upstream","status":"in_progress"}}`))
	require.NoError(t, err)
	require.Contains(t, string(out), `"id":"local"`)
	require.Equal(t, "upstream", r.UpstreamID)
	out, err = svc.Observe(context.Background(), r, []byte(`{"event_type":"content.delta","delta":{"type":"text","text":"你好"}}`))
	require.NoError(t, err)
	require.Contains(t, string(out), `"text":"你好"`)
}
func TestGeminiInteractionBillingRequestIDOverridesClientID(t *testing.T) {
	require.True(t, isForcedUsageBillingRequestID("ginteraction:abc"))
}

func TestGeminiInteractionObserveKeepsTerminalUsageAndRewritesReferences(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{PublicID: "local", UpstreamID: "upstream", Status: "completed", Usage: &ClaudeUsage{InputTokens: 10, OutputTokens: 4}, PreviousPublicID: "previous", PreviousUpstreamID: "up_previous"}
	out, err := svc.Observe(context.Background(), r, []byte(`{"id":"upstream","status":"in_progress","previous_interaction_id":"up_previous","usage":{"total_input_tokens":1,"total_output_tokens":0}}`))
	require.NoError(t, err)
	require.Equal(t, "completed", r.Status)
	require.Equal(t, 10, r.Usage.InputTokens)
	require.Contains(t, string(out), `"previous_interaction_id":"previous"`)
	out, err = svc.Observe(context.Background(), r, []byte(`{"event_type":"content.delta","interaction_id":"upstream","delta":{"text":"test"}}`))
	require.NoError(t, err)
	require.Contains(t, string(out), `"interaction_id":"local"`)
}

func TestGeminiInteractionObserveTerminalEnvelopeWithoutID(t *testing.T) {
	repo := &interactionMemoryRepo{records: map[string]*GeminiInteraction{}}
	svc := NewGeminiInteractionService(repo, nil, nil, nil, nil)
	r := &GeminiInteraction{PublicID: "local", UpstreamID: "upstream", Status: "in_progress"}
	_, err := svc.Observe(context.Background(), r, []byte(`{"event_type":"interaction.completed","interaction":{"status":"completed","usage":{"total_input_tokens":2,"total_output_tokens":3}}}`))
	require.NoError(t, err)
	require.Equal(t, "completed", r.Status)
	require.Equal(t, 3, r.Usage.OutputTokens)
}

type interactionScheduleRepo struct {
	AccountRepository
	accounts []Account
}

func (r *interactionScheduleRepo) ListSchedulableByGroupIDAndPlatforms(context.Context, int64, []string) ([]Account, error) {
	return r.accounts, nil
}
func TestGeminiInteractionExclusionsBindAccountAndRejectOtherProtocols(t *testing.T) {
	svc := &GatewayService{accountRepo: &interactionScheduleRepo{accounts: []Account{{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey}, {ID: 2, Platform: PlatformGemini, Type: AccountTypeOAuth}, {ID: 3, Platform: PlatformAntigravity, Type: AccountTypeOAuth}, {ID: 4, Platform: PlatformGemini, Type: AccountTypeAPIKey}}}}
	id := int64(5)
	got, err := svc.ExcludedGeminiInteractionAccounts(context.Background(), &id, 4)
	require.NoError(t, err)
	require.Contains(t, got, int64(1))
	require.Contains(t, got, int64(2))
	require.NotContains(t, got, int64(4))
}
