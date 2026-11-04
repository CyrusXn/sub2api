package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type modelQualityRepository struct{ db *sql.DB }

func NewModelQualityRepository(db *sql.DB) service.ModelQualityRepository {
	return &modelQualityRepository{db: db}
}

func (r *modelQualityRepository) Create(ctx context.Context, run *service.ModelQualityRun) error {
	if run.OfficialSlot != nil {
		err := r.db.QueryRowContext(ctx, `INSERT INTO model_quality_runs (user_id,api_key_id,author,group_name,model,theme,prompt,status,official_slot)
			VALUES ($1,$2,$3,$4,$5,'official',$6,'running',$7) ON CONFLICT DO NOTHING RETURNING id,created_at`, run.UserID, run.APIKeyID, run.Author, run.GroupName, run.Model, run.Prompt, run.OfficialSlot).Scan(&run.ID, &run.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrModelQualityAlreadyScheduled
		}
		return err
	}
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

func (r *modelQualityRepository) Update(ctx context.Context, id, userID int64, status, content, requestID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE model_quality_runs SET status=$3,content=$4,request_id=COALESCE(NULLIF($5,''),request_id),
		finished_at=CASE WHEN $3='running' THEN NULL ELSE NOW() END,
		error=CASE WHEN $3='failed' THEN '模型请求失败或已中断，请检查密钥、额度或上游账号状态' ELSE '' END
		WHERE id=$1 AND user_id=$2 AND status='running'`, id, userID, status, content, requestID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return service.ErrModelQualityNotRunning
	}
	return nil
}

func (r *modelQualityRepository) List(ctx context.Context, page int, theme, status, group, model string) ([]service.ModelQualityRun, int64, error) {
	where := ` WHERE ($1='' OR q.theme=$1) AND ($2='' OR (` + modelQualityStatusSQL + `)=$2) AND ($3='' OR q.group_name=$3) AND ($4='' OR q.model=$4)`
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_quality_runs q`+where, theme, status, group, model).Scan(&total); err != nil {
		return nil, 0, err
	}
	// 用量与错误日志异步写入；读取时补查真实账号，不以创建时的查询结果作为终态。
	rows, err := r.db.QueryContext(ctx, `SELECT q.id,COALESCE(NULLIF(usr.email,''),''),q.group_name,q.model,q.theme,q.prompt,`+modelQualityStatusSQL+`,q.content,CASE WHEN q.status='running' AND q.created_at < NOW()-INTERVAL '9 minutes' THEN '检测已中断或超时，请重新发起' ELSE q.error END,COALESCE(q.account_id,u.account_id,e.account_id,0),COALESCE(a.name,''),q.created_at
		FROM (SELECT q.* FROM model_quality_runs q`+where+` ORDER BY q.created_at DESC,q.id DESC LIMIT 12 OFFSET $5) q
		LEFT JOIN users usr ON usr.id=q.user_id AND usr.deleted_at IS NULL
		LEFT JOIN LATERAL (
			SELECT u.account_id FROM usage_logs u WHERE u.api_key_id=q.api_key_id AND u.request_id='client:' || q.request_id AND u.user_id=q.user_id ORDER BY u.id DESC LIMIT 1
		) u ON q.account_id IS NULL
		LEFT JOIN LATERAL (
			SELECT e.account_id FROM ops_error_logs e WHERE e.api_key_id=q.api_key_id AND e.client_request_id=q.request_id AND e.user_id=q.user_id AND e.account_id IS NOT NULL ORDER BY e.id DESC LIMIT 1
		) e ON q.account_id IS NULL AND u.account_id IS NULL
		LEFT JOIN accounts a ON a.id=COALESCE(q.account_id,u.account_id,e.account_id)
		ORDER BY q.created_at DESC,q.id DESC`, theme, status, group, model, (page-1)*12)
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

func (r *modelQualityRepository) FilterOptions(ctx context.Context, theme string) (service.ModelQualityFilterOptions, error) {
	options := service.ModelQualityFilterOptions{Groups: []string{}, Models: []string{}}
	// 筛选项来自整个分类，避免分页或当前筛选将可选分组、模型隐藏。
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT group_name,model FROM model_quality_runs WHERE ($1='' OR theme=$1)`, theme)
	if err != nil {
		return options, err
	}
	defer rows.Close()
	groups, models := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var group, model string
		if err := rows.Scan(&group, &model); err != nil {
			return options, err
		}
		if group != "" && !groups[group] {
			groups[group] = true
			options.Groups = append(options.Groups, group)
		}
		if model != "" && !models[model] {
			models[model] = true
			options.Models = append(options.Models, model)
		}
	}
	sort.Strings(options.Groups)
	sort.Strings(options.Models)
	return options, rows.Err()
}
