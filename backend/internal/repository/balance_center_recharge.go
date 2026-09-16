package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 调用方已经持有站点行锁；基线、充值和邮件一起提交，重复/乱序探测不会重复记账。
func recordBalanceCenterCashIncrease(ctx context.Context, tx *sql.Tx, siteID, snapshotID int64, scale float64, snapshot *service.BalanceCenterSnapshot) error {
	if snapshot.RechargeSkip || snapshot.Source != "sub2api_probe" || snapshot.AccountID == nil || snapshot.ConvertedBalance == nil ||
		math.IsNaN(*snapshot.ConvertedBalance) || math.IsInf(*snapshot.ConvertedBalance, 0) {
		return nil
	}
	// 与站点资产使用同一绑定维度，只选一个仍在使用的账号，避免同钱包多个 Key 重复记账。
	var representative sql.NullInt64
	var fingerprint string
	if err := tx.QueryRowContext(ctx, `SELECT a.id,md5(COALESCE(a.credentials->>'api_key','')) FROM balance_center_account_bindings b
JOIN accounts a ON a.id=b.account_id
WHERE b.site_id=$1 AND b.enabled AND a.deleted_at IS NULL AND a.status='active'
AND EXISTS (SELECT 1 FROM balance_center_current_states c WHERE c.account_id=a.id AND c.converted_balance IS NOT NULL)
ORDER BY a.id LIMIT 1`, siteID).Scan(&representative, &fingerprint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if !representative.Valid || representative.Int64 != *snapshot.AccountID {
		return nil
	}
	// 探测过程中换 Key 时丢弃旧钱包结果；指纹只用于相等比较，不暴露原始凭据。
	if snapshot.RechargeKeyFingerprint != fingerprint {
		return nil
	}
	var previousAccount int64
	var previousBalance, previousScale float64
	var previousCurrency string
	var previousFingerprint string
	var previousTime sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT account_id,balance,conversion_scale,currency,probed_at,key_fingerprint
FROM balance_center_recharge_baselines WHERE site_id=$1 FOR UPDATE`, siteID).
		Scan(&previousAccount, &previousBalance, &previousScale, &previousCurrency, &previousTime, &previousFingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previousTime.Valid && !snapshot.ProbedAt.After(previousTime.Time) {
		return nil
	}
	amount := 0.0
	if err == nil && previousFingerprint == fingerprint && previousAccount == *snapshot.AccountID && previousCurrency == snapshot.Currency && math.Abs(previousScale-scale) < 1e-9 {
		// 按用户选择以人民币现金净增量记充值；赠送/退款也计入，期间消费会抵减此值。
		amount = math.Round((*snapshot.ConvertedBalance-previousBalance)*1e8) / 1e8
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO balance_center_recharge_baselines(site_id,account_id,balance,conversion_scale,currency,probed_at,key_fingerprint)
VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(site_id) DO UPDATE SET account_id=EXCLUDED.account_id,
balance=EXCLUDED.balance,conversion_scale=EXCLUDED.conversion_scale,currency=EXCLUDED.currency,probed_at=EXCLUDED.probed_at,key_fingerprint=EXCLUDED.key_fingerprint`,
		siteID, *snapshot.AccountID, *snapshot.ConvertedBalance, scale, snapshot.Currency, snapshot.ProbedAt, fingerprint); err != nil {
		return err
	}
	if amount <= 0 {
		return nil
	}
	key := fmt.Sprintf("site:%d:snapshot:%d", siteID, snapshotID)
	result, err := tx.ExecContext(ctx, `INSERT INTO balance_center_recharge_events
(source,source_key,site_id,site_label,account_id,amount,currency,occurred_at,note,record_type)
SELECT 'balance_increase',$1,id,COALESCE(NULLIF(display_name,''),name),$3,$4,'CNY',$5,
'自动余额增量：包含赠送或退款，金额为相邻现金余额净增量，非支付流水','recharge'
FROM balance_center_sites WHERE id=$2 ON CONFLICT(source,source_key) DO NOTHING`,
		key, siteID, *snapshot.AccountID, amount, snapshot.ProbedAt)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}
	var siteName string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(display_name,''),name) FROM balance_center_sites WHERE id=$1`, siteID).Scan(&siteName); err != nil {
		return err
	}
	subject := fmt.Sprintf("%s-充值成功%.2f元", siteName, amount)
	for _, recipient := range snapshot.RechargeRecipients {
		_, err := tx.ExecContext(ctx, `INSERT INTO alert_email_outbox
(source_type,source_id,source_key,alert_type,recipient_email,subject,body_html,aggregate_until,available_at,created_at)
VALUES('balance_center_recharge',$1,$2,'recharge_success',$3,$4,$5,$6,$6,$6)
ON CONFLICT(source_type,source_key,recipient_email) DO NOTHING`,
			fmt.Sprint(siteID), key, recipient, subject, html.EscapeString(subject), snapshot.ProbedAt)
		if err != nil {
			return err
		}
	}
	return nil
}
