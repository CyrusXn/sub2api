package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *balanceCenterRepository) ListBalanceCenterAssets(ctx context.Context) ([]service.BalanceCenterAsset, error) {
	// 资产视图与经营历史共享同一口径，接口不再自行复制余额聚合算法。
	rows, err := r.db.QueryContext(ctx, `SELECT s.id,COALESCE(NULLIF(s.display_name,''),s.name),s.normalized_domain,
 (SELECT MIN(a.id) FROM balance_center_account_bindings b JOIN accounts a ON a.id=b.account_id WHERE b.site_id=s.id AND a.deleted_at IS NULL),
 c.manual_balance,v.cash_balance,c.subscription_id,COALESCE(c.subscription_price,0),COALESCE(c.subscription_days,30),
 c.subscription_expires_at,COALESCE(c.subscription_auto_sync,false),c.subscription_synced_at,COALESCE(c.subscription_sync_error,''),
 v.subscription_balance,v.total_balance,v.balance_known,c.subscription_daily_remaining_usd
 FROM balance_center_sites s LEFT JOIN balance_center_asset_settings c ON c.site_id=s.id
 JOIN balance_center_asset_values v ON v.site_id=s.id ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.BalanceCenterAsset, 0)
	for rows.Next() {
		var a service.BalanceCenterAsset
		if err := rows.Scan(&a.SiteID, &a.SiteName, &a.Domain, &a.AccountID, &a.ManualBalance, &a.CashBalance, &a.SubscriptionID, &a.SubscriptionPrice, &a.SubscriptionDays, &a.SubscriptionExpiresAt, &a.SubscriptionAutoSync, &a.SubscriptionSyncedAt, &a.SubscriptionSyncError, &a.SubscriptionBalance, &a.TotalBalance, &a.BalanceKnown, &a.SubscriptionDailyRemainingUSD); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (r *balanceCenterRepository) SaveBalanceCenterAsset(ctx context.Context, a *service.BalanceCenterAsset) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO balance_center_asset_settings(site_id,manual_balance,subscription_id,subscription_price,subscription_days,subscription_expires_at,subscription_auto_sync)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(site_id) DO UPDATE SET manual_balance=EXCLUDED.manual_balance,
 subscription_id=EXCLUDED.subscription_id,subscription_price=EXCLUDED.subscription_price,subscription_days=EXCLUDED.subscription_days,
 subscription_expires_at=EXCLUDED.subscription_expires_at,subscription_auto_sync=EXCLUDED.subscription_auto_sync,
 subscription_synced_at=NULL,subscription_sync_error='',subscription_daily_remaining_usd=NULL,updated_at=NOW()`, a.SiteID, a.ManualBalance, a.SubscriptionID, a.SubscriptionPrice, a.SubscriptionDays, a.SubscriptionExpiresAt, a.SubscriptionAutoSync)
	return err
}

func (r *balanceCenterRepository) SaveBalanceCenterSubscriptionObservation(ctx context.Context, siteID, subscriptionID int64, o service.BalanceCenterSubscriptionObservation, observed time.Time, reason string, messages []service.AlertEmailOutboxInput) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// CAS 行锁与幂等入队同一事务；迟到结果不覆盖人工校正或新一轮同步。
	result, err := tx.ExecContext(ctx, `UPDATE balance_center_asset_settings SET
 subscription_expires_at=CASE WHEN $5='' THEN $3 ELSE subscription_expires_at END,
 subscription_synced_at=$4,subscription_sync_error=$5,updated_at=NOW(),
 subscription_daily_remaining_usd=CASE WHEN $5='' THEN $6::numeric ELSE NULL END
 WHERE site_id=$1 AND subscription_id=$2 AND subscription_auto_sync=true
 AND (subscription_synced_at IS NULL OR subscription_synced_at<=$4) AND updated_at<=$4`, siteID, subscriptionID, o.ExpiresAt, observed, reason, o.RemainingUSD)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("资产配置已变更，请重新同步")
	}
	for _, m := range messages {
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_email_outbox(source_type,source_id,source_key,alert_type,recipient_email,subject,body_html,aggregate_until,available_at,created_at)
  VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$8) ON CONFLICT(source_type,source_key,recipient_email) DO NOTHING`, m.SourceType, m.SourceID, m.SourceKey, m.AlertType, m.Recipient, m.Subject, m.BodyHTML, m.AvailableAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
