package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *geminiInteractionRepository) ListReview(ctx context.Context, page, size int) ([]*service.GeminiInteraction, int, error) {
	var total int
	const filter = ` FROM gemini_interactions WHERE settled = FALSE AND deleted_at IS NULL AND review_reason <> ''`
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+filter).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+interactionColumns+filter+` ORDER BY id LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]*service.GeminiInteraction, 0)
	for rows.Next() {
		v, err := scanInteraction(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}

func (r *geminiInteractionRepository) ResolveReview(ctx context.Context, id, actorID int64, input service.GeminiInteractionResolution) (*service.GeminiInteraction, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	v, err := scanInteraction(tx.QueryRowContext(ctx, `SELECT `+interactionColumns+` FROM gemini_interactions WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGeminiInteractionReviewConflict
	}
	if err != nil {
		return nil, err
	}
	if v.Settled || v.ReviewReason == "" || v.Usage != nil {
		return nil, service.ErrGeminiInteractionReviewConflict
	}
	var billed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2 UNION ALL SELECT 1 FROM usage_billing_dedup_archive WHERE request_id = $1 AND api_key_id = $2)`, service.GeminiInteractionUsageRequestIDPrefix+v.PublicID, v.APIKeyID).Scan(&billed); err != nil {
		return nil, err
	}
	if billed {
		return nil, service.ErrGeminiInteractionReviewConflict
	}
	resolution, err := json.Marshal(struct {
		ActorID int64 `json:"actor_id"`
		service.GeminiInteractionResolution
	}{actorID, input})
	if err != nil {
		return nil, err
	}
	if input.Action == "release" {
		if v.BalanceHold > 0 {
			result, err := tx.ExecContext(ctx, `UPDATE users SET balance = balance + $1, frozen_balance = COALESCE(frozen_balance, 0) - $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1`, v.BalanceHold, v.UserID)
			if err != nil {
				return nil, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return nil, err
			}
			if n != 1 {
				return nil, errors.New("interaction balance hold cannot be released")
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE gemini_interactions SET settled = TRUE, resolution = $2::jsonb, updated_at = NOW() WHERE id = $1`, id, resolution)
		v.Settled = true
	} else {
		usage, e := json.Marshal(input.Usage)
		if e != nil {
			return nil, e
		}
		_, err = tx.ExecContext(ctx, `UPDATE gemini_interactions SET usage = $2::jsonb, status = CASE WHEN status IN ('completed','failed','cancelled','requires_action') THEN status ELSE 'completed' END, review_reason = 'manual_usage_pending', resolution = $3::jsonb, next_poll_at = NOW(), updated_at = NOW() WHERE id = $1`, id, usage, resolution)
		v.Usage = input.Usage
		if !service.GeminiInteractionTerminal(v.Status) {
			v.Status = "completed"
		}
		v.ReviewReason = "manual_usage_pending"
	}
	if err != nil {
		return nil, err
	}
	// 与账务处置同事务同步写审计；异步HTTP审计失败也不会留下无依据的资金变更。
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs (actor_user_id, actor_role, action, method, path, status_code, extra) VALUES ($1,'admin',$2,'POST',$3,200,$4::jsonb)`, actorID, "admin.gemini_interactions."+input.Action, fmt.Sprintf("/api/v1/admin/balance-center/interactions/%d/resolve", id), resolution)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}
