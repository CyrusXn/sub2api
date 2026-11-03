package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type modelQualityRepository struct{ db *sql.DB }

func NewModelQualityRepository(db *sql.DB) service.ModelQualityRepository {
	return &modelQualityRepository{db: db}
}

func (r *modelQualityRepository) Create(ctx context.Context, run *service.ModelQualityRun) error {
	err := r.db.QueryRowContext(ctx, `INSERT INTO model_quality_runs (user_id, api_key_id, author, group_name, model, theme, prompt, status, content, error, account_id, created_at, request_id)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE(u.account_id,e.account_id),COALESCE(u.created_at,NOW()),NULLIF($11,'')
		FROM (SELECT 1) seed LEFT JOIN LATERAL (
			SELECT account_id,created_at FROM usage_logs WHERE api_key_id=$2 AND user_id=$1 AND request_id='client:' || NULLIF($11,'') ORDER BY id DESC LIMIT 1
		) u ON TRUE
		LEFT JOIN LATERAL (
			SELECT account_id FROM ops_error_logs WHERE api_key_id=$2 AND user_id=$1 AND client_request_id=NULLIF($11,'') AND account_id IS NOT NULL ORDER BY id DESC LIMIT 1
		) e ON u.account_id IS NULL
		RETURNING id, created_at, COALESCE(account_id,0)`, run.UserID, run.APIKeyID, run.Author, run.GroupName, run.Model, run.Theme, run.Prompt, run.Status, run.Content, run.Error, run.RequestID).Scan(&run.ID, &run.CreatedAt, &run.AccountID)
	if err != nil {
		return err
	}
	return nil
}

const modelQualityStatusSQL = `CASE WHEN q.status='running' AND q.created_at < NOW() - INTERVAL '9 minutes' THEN 'failed' ELSE q.status END`

func (r *modelQualityRepository) List(ctx context.Context, page int, theme, status string) ([]service.ModelQualityRun, int64, error) {
	where := ` WHERE ($1='' OR q.theme=$1) AND ($2='' OR (` + modelQualityStatusSQL + `)=$2)`
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_quality_runs q`+where, theme, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	// 用量与错误日志异步写入；读取时补查真实账号，不以创建时的查询结果作为终态。
	rows, err := r.db.QueryContext(ctx, `SELECT q.id,q.author,q.group_name,q.model,q.theme,q.prompt,`+modelQualityStatusSQL+`,q.content,CASE WHEN q.status='running' AND q.created_at < NOW()-INTERVAL '9 minutes' THEN '检测已中断或超时，请重新发起' ELSE q.error END,COALESCE(q.account_id,u.account_id,e.account_id,0),COALESCE(a.name,''),q.created_at
		FROM (SELECT q.* FROM model_quality_runs q`+where+` ORDER BY q.created_at DESC,q.id DESC LIMIT 12 OFFSET $3) q
		LEFT JOIN LATERAL (
			SELECT u.account_id FROM usage_logs u WHERE u.api_key_id=q.api_key_id AND u.request_id='client:' || q.request_id AND u.user_id=q.user_id ORDER BY u.id DESC LIMIT 1
		) u ON q.account_id IS NULL
		LEFT JOIN LATERAL (
			SELECT e.account_id FROM ops_error_logs e WHERE e.api_key_id=q.api_key_id AND e.client_request_id=q.request_id AND e.user_id=q.user_id AND e.account_id IS NOT NULL ORDER BY e.id DESC LIMIT 1
		) e ON q.account_id IS NULL AND u.account_id IS NULL
		LEFT JOIN accounts a ON a.id=COALESCE(q.account_id,u.account_id,e.account_id)
		ORDER BY q.created_at DESC,q.id DESC`, theme, status, (page-1)*12)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]service.ModelQualityRun, 0)
	for rows.Next() {
		var run service.ModelQualityRun
		if err := rows.Scan(&run.ID, &run.Author, &run.GroupName, &run.Model, &run.Theme, &run.Prompt, &run.Status, &run.Content, &run.Error, &run.AccountID, &run.AccountName, &run.CreatedAt); err != nil {
			return nil, 0, err
		}
		result = append(result, run)
	}
	return result, total, rows.Err()
}
