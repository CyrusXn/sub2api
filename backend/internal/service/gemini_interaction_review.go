package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

var ErrGeminiInteractionReviewConflict = infraerrors.Conflict("GEMINI_INTERACTION_REVIEW_CONFLICT", "任务已结算、已有权威用量或不在待核对状态，请刷新后重试")

type GeminiInteractionResolution struct {
	Action string       `json:"action"`
	Reason string       `json:"reason"`
	Usage  *ClaudeUsage `json:"usage,omitempty"`
}

type GeminiInteractionReviewRepository interface {
	ListReview(context.Context, int, int) ([]*GeminiInteraction, int, error)
	ResolveReview(context.Context, int64, int64, GeminiInteractionResolution) (*GeminiInteraction, error)
}

// GeminiInteractionReviewItem 只公开核对必需字段，不暴露上游 ID、定价快照或请求信息。
type GeminiInteractionReviewItem struct {
	ID           int64        `json:"id"`
	PublicID     string       `json:"public_id"`
	UserID       int64        `json:"user_id"`
	APIKeyID     int64        `json:"api_key_id"`
	GroupID      int64        `json:"group_id"`
	AccountID    int64        `json:"account_id"`
	Model        string       `json:"model"`
	Status       string       `json:"status"`
	HoldAmount   float64      `json:"hold_amount"`
	ReviewReason string       `json:"review_reason"`
	Usage        *ClaudeUsage `json:"usage,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
}

func (s *GeminiInteractionService) ListReview(ctx context.Context, page, size int) ([]GeminiInteractionReviewItem, int, error) {
	r, ok := s.repo.(GeminiInteractionReviewRepository)
	if !ok {
		return nil, 0, ErrGeminiInteractionBusy
	}
	rows, total, err := r.ListReview(ctx, page, size)
	if err != nil {
		return nil, 0, err
	}
	items := make([]GeminiInteractionReviewItem, 0, len(rows))
	for _, v := range rows {
		items = append(items, GeminiInteractionReviewItem{ID: v.ID, PublicID: v.PublicID, UserID: v.UserID, APIKeyID: v.APIKeyID, GroupID: v.GroupID, AccountID: v.AccountID, Model: v.Model, Status: v.Status, HoldAmount: v.BalanceHold, ReviewReason: v.ReviewReason, Usage: v.Usage, CreatedAt: v.CreatedAt})
	}
	return items, total, nil
}

func (s *GeminiInteractionService) ResolveReview(ctx context.Context, id, actorID int64, input GeminiInteractionResolution) (bool, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	valid := id > 0 && actorID > 0 && utf8.RuneCountInString(input.Reason) >= 5 && utf8.RuneCountInString(input.Reason) <= 500
	if input.Action == "release" {
		valid = valid && input.Usage == nil
	} else if input.Action == "settle" && input.Usage != nil {
		u := input.Usage
		valid = valid && u.InputTokens >= 0 && u.OutputTokens >= 0 && u.CacheReadInputTokens >= 0 && (u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadInputTokens > 0)
		// 图片输出是总输出的子集，不能另加到总量；Gemini 不提供缓存创建用量。
		valid = valid && u.ImageOutputTokens >= 0 && u.ImageOutputTokens <= u.OutputTokens && u.CacheCreationInputTokens == 0 && u.CacheCreation5mTokens == 0 && u.CacheCreation1hTokens == 0
	} else {
		valid = false
	}
	if !valid {
		return false, infraerrors.BadRequest("INVALID_INTERACTION_RESOLUTION", "请填写5至500字核对依据；结算必须提供非负且非全零的核实用量，解冻不能携带用量")
	}
	input.Reason = logredact.RedactText(input.Reason)
	r, ok := s.repo.(GeminiInteractionReviewRepository)
	if !ok {
		return false, ErrGeminiInteractionBusy
	}
	v, err := r.ResolveReview(ctx, id, actorID, input)
	if err != nil {
		return false, err
	}
	s.invalidateBalance(ctx, v)
	if !v.Settled {
		// 人工核实已原子落库，自动恢复继续使用同一计费事件，不重复处置冻结款。
		if err := s.Settle(ctx, v); err != nil {
			return false, nil
		}
	}
	return v.Settled, nil
}
