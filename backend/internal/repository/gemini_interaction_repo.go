package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type geminiInteractionRepository struct{ db *sql.DB }

func NewGeminiInteractionRepository(db *sql.DB) service.GeminiInteractionRepository {
	return &geminiInteractionRepository{db: db}
}

// metadata 与动态状态列分离，响应更新不能用旧快照回退 settled 标记。
type interactionMetadata struct {
	Model, UpstreamModel, PayloadHash, Revision string
	PreviousPublicID, PreviousUpstreamID        string
	PricingAt                                   time.Time
	BillingGroup                                *service.Group
	SubscriptionID                              int64
	ChannelUsage                                service.ChannelUsageFields
	Stream, Background                          bool
}

func (r *geminiInteractionRepository) Create(ctx context.Context, v *service.GeminiInteraction) error {
	if v.BalanceHold < 0 || math.IsNaN(v.BalanceHold) || math.IsInf(v.BalanceHold, 0) {
		return errors.New("invalid interaction hold")
	}
	metadata, err := json.Marshal(interactionMetadata{Model: v.Model, UpstreamModel: v.UpstreamModel, PayloadHash: v.PayloadHash, Revision: v.Revision, PreviousPublicID: v.PreviousPublicID, PreviousUpstreamID: v.PreviousUpstreamID, PricingAt: v.PricingAt, BillingGroup: v.BillingGroup, SubscriptionID: v.SubscriptionID, ChannelUsage: v.ChannelUsage, Stream: v.Stream, Background: v.Background})
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 与现有计费一致先锁用户再锁 Key，避免并发创建/结算相互反锁。
	var limit, pending int
	if err = tx.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, v.UserID).Scan(&limit); err != nil {
		return err
	}
	// 分组级联按分组→订阅加锁；后续依赖均不等待，防止与旧删除/计费链形成跨事务环。
	var dependencyID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE id = $1 AND deleted_at IS NULL FOR SHARE NOWAIT`, v.GroupID).Scan(&dependencyID); err != nil {
		return geminiInteractionLockError(err)
	}
	if v.SubscriptionID > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM user_subscriptions WHERE id = $1 AND user_id = $2 AND group_id = $3 AND deleted_at IS NULL FOR SHARE NOWAIT`, v.SubscriptionID, v.UserID, v.GroupID).Scan(&dependencyID); err != nil {
			return geminiInteractionLockError(err)
		}
	}
	// 与 Key 删除触发器共用行锁；NOWAIT 避免旧删用户流程持 Key 等用户时形成反锁。
	var keyUserID int64
	var keyGroupID sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT user_id, group_id FROM api_keys WHERE id = $1 AND deleted_at IS NULL FOR UPDATE NOWAIT`, v.APIKeyID).Scan(&keyUserID, &keyGroupID); err != nil {
		return geminiInteractionLockError(err)
	}
	if keyUserID != v.UserID || !keyGroupID.Valid || keyGroupID.Int64 != v.GroupID {
		return service.ErrGeminiInteractionNotFound
	}
	// HTTP 返回后后台任务仍占用新协议并发额度；行锁避免多节点同时超额创建。
	if limit > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gemini_interactions WHERE user_id = $1 AND settled = FALSE AND deleted_at IS NULL AND review_reason = '' AND status NOT IN ('completed','failed','cancelled','requires_action')`, v.UserID).Scan(&pending); err != nil {
			return err
		}
		if pending >= limit {
			return service.ErrGeminiInteractionConcurrency
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT concurrency FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE NOWAIT`, v.AccountID).Scan(&limit); err != nil {
		return geminiInteractionLockError(err)
	}
	if limit > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gemini_interactions WHERE account_id = $1 AND settled = FALSE AND deleted_at IS NULL AND review_reason = '' AND status NOT IN ('completed','failed','cancelled','requires_action')`, v.AccountID).Scan(&pending); err != nil {
			return err
		}
		if pending >= limit {
			return service.ErrGeminiInteractionConcurrency
		}
	}
	if v.BalanceHold > 0 {
		result, e := tx.ExecContext(ctx, `UPDATE users SET balance = balance - $1, frozen_balance = COALESCE(frozen_balance, 0) + $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL AND balance >= $1`, v.BalanceHold, v.UserID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return service.ErrInsufficientBalance
		}
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO gemini_interactions (public_id,api_key_id,user_id,group_id,account_id,metadata,hold_amount) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id,created_at`, v.PublicID, v.APIKeyID, v.UserID, v.GroupID, v.AccountID, metadata, v.BalanceHold).Scan(&v.ID, &v.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func geminiInteractionLockError(err error) error {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return service.ErrGeminiInteractionBusy
	}
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrGeminiInteractionNotFound
	}
	return err
}

func geminiInteractionDeleteError(err error) error {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		switch pgErr.Constraint {
		case "gemini_interactions_api_key_pending":
			return service.ErrGeminiInteractionKeyPending
		case "gemini_interactions_account_pending", "gemini_interactions_group_pending", "gemini_interactions_subscription_pending", "gemini_interactions_user_pending":
			return service.ErrGeminiInteractionDependencyPending
		}
	}
	return err
}

const interactionColumns = `id, public_id, upstream_id, api_key_id, user_id, group_id, account_id, metadata, status, usage, settled, created_at, hold_amount, review_reason`

func (r *geminiInteractionRepository) GetOwned(ctx context.Context, k, u, g int64, id string) (*service.GeminiInteraction, error) {
	v, err := scanInteraction(r.db.QueryRowContext(ctx, `SELECT `+interactionColumns+` FROM gemini_interactions WHERE public_id = $1 AND api_key_id = $2 AND user_id = $3 AND group_id = $4 AND deleted_at IS NULL`, id, k, u, g))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGeminiInteractionNotFound
	}
	return v, err
}
func (r *geminiInteractionRepository) Update(ctx context.Context, v *service.GeminiInteraction) error {
	var usage any
	if v.Usage != nil {
		b, e := json.Marshal(v.Usage)
		if e != nil {
			return e
		}
		usage = b
	}
	var authoritativeUsage []byte
	err := r.db.QueryRowContext(ctx, `UPDATE gemini_interactions SET upstream_id = $2,
status = CASE WHEN status IN ('completed','failed','cancelled','requires_action') THEN status ELSE $3 END,
usage = CASE WHEN status IN ('completed','failed','cancelled','requires_action') AND usage IS NOT NULL THEN usage ELSE COALESCE($4::jsonb, usage) END,
review_reason = CASE WHEN settled THEN review_reason WHEN resolution IS NOT NULL THEN review_reason WHEN $4::jsonb IS NOT NULL THEN '' WHEN $5 <> '' THEN $5 ELSE review_reason END,
updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL AND (upstream_id = '' OR upstream_id = $2)
RETURNING upstream_id, status, usage, settled, review_reason`, v.ID, v.UpstreamID, v.Status, usage, v.ReviewReason).Scan(&v.UpstreamID, &v.Status, &authoritativeUsage, &v.Settled, &v.ReviewReason)
	if err != nil {
		return err
	}
	v.Usage = nil
	if len(authoritativeUsage) > 0 && string(authoritativeUsage) != "null" {
		return json.Unmarshal(authoritativeUsage, &v.Usage)
	}
	return nil
}

func (r *geminiInteractionRepository) HasUsageBilling(ctx context.Context, requestID string, keyID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2 UNION ALL SELECT 1 FROM usage_billing_dedup_archive WHERE request_id = $1 AND api_key_id = $2)`, requestID, keyID).Scan(&exists)
	return exists, err
}
func (r *geminiInteractionRepository) MarkSettled(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID int64
	var hold float64
	var settled bool
	if err = tx.QueryRowContext(ctx, `SELECT user_id, hold_amount, settled FROM gemini_interactions WHERE id = $1 FOR UPDATE`, id).Scan(&userID, &hold, &settled); err != nil {
		return err
	}
	if settled {
		return tx.Commit()
	}
	if hold > 0 {
		result, e := tx.ExecContext(ctx, `UPDATE users SET balance = balance + $1, frozen_balance = COALESCE(frozen_balance, 0) - $1, updated_at = NOW() WHERE id = $2 AND COALESCE(frozen_balance, 0) >= $1`, hold, userID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("interaction balance hold cannot be released")
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE gemini_interactions SET settled = TRUE, updated_at = NOW() WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *geminiInteractionRepository) SoftDelete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE gemini_interactions SET deleted_at = NOW() WHERE id = $1 AND settled = TRUE`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return service.ErrGeminiInteractionNotFound
	}
	return err
}
func (r *geminiInteractionRepository) Pending(ctx context.Context, limit int) ([]*service.GeminiInteraction, error) {
	// 无 ID 的创建任务须超过完整 POST 超时窗口，避免误判仍在执行的请求。
	rows, err := r.db.QueryContext(ctx, `WITH due AS (SELECT id FROM gemini_interactions WHERE settled = FALSE AND deleted_at IS NULL AND (upstream_id <> '' OR usage IS NOT NULL OR (review_reason = '' AND created_at < NOW() - INTERVAL '12 minutes')) AND next_poll_at <= NOW() ORDER BY next_poll_at,id LIMIT $1 FOR UPDATE SKIP LOCKED) UPDATE gemini_interactions gi SET next_poll_at = NOW() + INTERVAL '60 seconds' FROM due WHERE gi.id = due.id RETURNING gi.`+`id, gi.public_id, gi.upstream_id, gi.api_key_id, gi.user_id, gi.group_id, gi.account_id, gi.metadata, gi.status, gi.usage, gi.settled, gi.created_at, gi.hold_amount, gi.review_reason`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*service.GeminiInteraction
	for rows.Next() {
		v, e := scanInteraction(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func scanInteraction(row interface{ Scan(...any) error }) (*service.GeminiInteraction, error) {
	v := &service.GeminiInteraction{}
	var metadata, usage []byte
	if err := row.Scan(&v.ID, &v.PublicID, &v.UpstreamID, &v.APIKeyID, &v.UserID, &v.GroupID, &v.AccountID, &metadata, &v.Status, &usage, &v.Settled, &v.CreatedAt, &v.BalanceHold, &v.ReviewReason); err != nil {
		return nil, err
	}
	var m interactionMetadata
	if err := json.Unmarshal(metadata, &m); err != nil {
		return nil, err
	}
	v.Model = m.Model
	v.UpstreamModel = m.UpstreamModel
	v.PayloadHash = m.PayloadHash
	v.Revision = m.Revision
	v.PreviousPublicID, v.PreviousUpstreamID = m.PreviousPublicID, m.PreviousUpstreamID
	v.PricingAt = m.PricingAt
	v.BillingGroup = m.BillingGroup
	v.SubscriptionID = m.SubscriptionID
	v.ChannelUsage = m.ChannelUsage
	v.Stream = m.Stream
	v.Background = m.Background
	if len(usage) > 0 && string(usage) != "null" {
		if err := json.Unmarshal(usage, &v.Usage); err != nil {
			return nil, err
		}
	}
	return v, nil
}
