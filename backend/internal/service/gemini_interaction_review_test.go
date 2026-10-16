//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type reviewServiceRepo struct {
	GeminiInteractionRepository
	calls  int
	record *GeminiInteraction
}

func (r *reviewServiceRepo) ResolveReview(_ context.Context, _, _ int64, _ GeminiInteractionResolution) (*GeminiInteraction, error) {
	r.calls++
	return r.record, nil
}
func (r *reviewServiceRepo) ListReview(context.Context, int, int) ([]*GeminiInteraction, int, error) {
	return nil, 0, nil
}

func TestGeminiInteractionReviewRejectsInvalidResolutionBeforeStorage(t *testing.T) {
	for _, input := range []GeminiInteractionResolution{
		{Action: "release", Reason: "短"},
		{Action: "settle", Reason: "已核对上游记录"},
		{Action: "settle", Reason: "已核对上游记录", Usage: &ClaudeUsage{}},
		{Action: "settle", Reason: "已核对上游记录", Usage: &ClaudeUsage{InputTokens: -1, OutputTokens: 2}},
		{Action: "settle", Reason: "已核对上游记录", Usage: &ClaudeUsage{OutputTokens: 2, ImageOutputTokens: 3}},
		{Action: "settle", Reason: "已核对上游记录", Usage: &ClaudeUsage{OutputTokens: 2, ImageOutputTokens: -1}},
		{Action: "release", Reason: "已核对上游记录", Usage: &ClaudeUsage{InputTokens: 1}},
		{Action: "unknown", Reason: "已核对上游记录"},
	} {
		r := &reviewServiceRepo{}
		svc := NewGeminiInteractionService(r, nil, nil, nil, nil)
		_, err := svc.ResolveReview(context.Background(), 1, 2, input)
		require.Error(t, err)
		require.Zero(t, r.calls)
	}
}

func TestGeminiInteractionReviewAcceptsImageOutputSubset(t *testing.T) {
	r := &reviewServiceRepo{record: &GeminiInteraction{Settled: true}}
	s := NewGeminiInteractionService(r, nil, nil, nil, nil)
	settled, err := s.ResolveReview(context.Background(), 1, 2, GeminiInteractionResolution{Action: "settle", Reason: "已核对图片输出用量", Usage: &ClaudeUsage{InputTokens: 5, OutputTokens: 100, ImageOutputTokens: 80}})
	require.NoError(t, err)
	require.True(t, settled)
	require.Equal(t, 1, r.calls)
}

func TestGeminiInteractionReviewReleaseDoesNotCallAutomaticBilling(t *testing.T) {
	r := &reviewServiceRepo{record: &GeminiInteraction{Settled: true, BalanceHold: 1.5}}
	svc := NewGeminiInteractionService(r, nil, nil, nil, nil)
	settled, err := svc.ResolveReview(context.Background(), 1, 2, GeminiInteractionResolution{Action: "release", Reason: "核实任务未创建，无费用"})
	require.NoError(t, err)
	require.True(t, settled)
	require.Equal(t, 1, r.calls)
}

type manualPendingInteractionRepo struct{ interactionReceiptRepo }

func (r *manualPendingInteractionRepo) Pending(context.Context, int) ([]*GeminiInteraction, error) {
	return []*GeminiInteraction{{ID: 1, PublicID: "orphan", Status: "completed", ReviewReason: "manual_usage_pending", Usage: &ClaudeUsage{InputTokens: 5}, BalanceHold: 2}}, nil
}

func TestGeminiInteractionReviewManualUsageWithoutUpstreamIDResumesBilling(t *testing.T) {
	r := &manualPendingInteractionRepo{}
	s := NewGeminiInteractionService(r, nil, &GatewayService{}, &APIKeyService{}, nil)
	n, err := s.RecoverPending(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, r.marks)
}
