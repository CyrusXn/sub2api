package repository

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// 显式指定隔离验收配置才运行；拒绝任何非本机或非专用数据库，绝不连线上。
func TestBalanceCenterRechargeLocalPostgresAndMailpit(t *testing.T) {
	path := os.Getenv("SUB2API_RECHARGE_LOCAL_ENV")
	if path == "" {
		t.Skip("仅手动启用隔离数据库验收")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var env map[string]string
	require.NoError(t, json.Unmarshal(data, &env))
	require.Equal(t, "127.0.0.1", env["DATABASE_HOST"])
	require.Equal(t, "15432", env["DATABASE_PORT"])
	require.Equal(t, "sub2api_review", env["DATABASE_DBNAME"])
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:15432", Path: "/sub2api_review", User: url.UserPassword(env["DATABASE_USER"], env["DATABASE_PASSWORD"]), RawQuery: "sslmode=disable"}
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	domain := fmt.Sprintf("recharge-local-%d.example", now.UnixNano())
	var account1, account2, siteID int64
	for _, account := range []*int64{&account1, &account2} {
		require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials) VALUES('本地充值验收','openai','apikey','{"api_key":"local-demo-key"}') RETURNING id`).Scan(account))
	}
	t.Cleanup(func() {
		// 清理必须先于关闭数据库；充值流水的外键只置空，需显式删除以免污染演示统计。
		_, err := db.Exec(`DELETE FROM alert_email_outbox WHERE source_type='balance_center_recharge' AND source_id=$1`, fmt.Sprint(siteID))
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM balance_center_recharge_events WHERE site_id=$1`, siteID)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM balance_center_sites WHERE normalized_domain=$1`, domain)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM accounts WHERE id IN ($1,$2)`, account1, account2)
		require.NoError(t, err)
	})
	repo := NewBalanceCenterRepository(db)
	keyFingerprint := fmt.Sprintf("%x", md5.Sum([]byte("local-demo-key")))
	probe := func(account int64, amount float64, stamp time.Time) *service.BalanceCenterSnapshot {
		s, err := repo.PersistSnapshot(ctx, &service.BalanceCenterSnapshot{Source: "sub2api_probe", SourceKey: fmt.Sprintf("local:%d:%d", account, stamp.UnixNano()), SiteName: "本地验收站点", NormalizedDomain: domain, BaseURL: "https://" + domain, Status: "ok", AccountID: &account, ConvertedBalance: &amount, Balance: &amount, Currency: "CNY", ConversionScale: 1, ProbedAt: stamp, RechargeRecipients: []string{"recharge-check@example.test"}, RechargeKeyFingerprint: keyFingerprint})
		require.NoError(t, err)
		siteID = s.SiteID
		return s
	}
	probe(account1, 9, now)
	probe(account2, 9, now.Add(time.Second))
	probe(account1, 11, now.Add(2*time.Second))
	probe(account1, 11, now.Add(2*time.Second))
	probe(account2, 11, now.Add(3*time.Second))
	probe(account1, 8, now.Add(time.Second)) // 乱序快照不能改变充值基线
	var count int
	var amount float64
	require.NoError(t, db.QueryRow(`SELECT count(*),COALESCE(sum(amount),0) FROM balance_center_recharge_events WHERE site_id=$1`, siteID).Scan(&count, &amount))
	require.Equal(t, 1, count)
	require.Equal(t, 2.0, amount)
	var subject, body string
	require.NoError(t, db.QueryRow(`SELECT subject,body_html FROM alert_email_outbox WHERE source_type='balance_center_recharge' AND source_id=$1`, fmt.Sprint(siteID)).Scan(&subject, &body))
	require.Equal(t, "本地验收站点-充值成功2.00元", subject)
	require.Equal(t, subject, body)
	// 实际 EmailService 只投递至本机 Mailpit；不启动应用后台或外部邮件发送。
	sender := service.NewEmailService(nil, nil)
	require.NoError(t, sender.SendEmailWithConfig(&service.SMTPConfig{Host: "127.0.0.1", Port: 11025, Username: "local-review", Password: "local-review", From: "review@example.test", FromName: "本地验收"}, "recharge-check@example.test", subject, body))
	resp, err := http.Get("http://127.0.0.1:18025/api/v1/messages")
	require.NoError(t, err)
	defer resp.Body.Close()
	var messages struct {
		Messages []struct {
			ID      string `json:"ID"`
			Subject string `json:"Subject"`
		} `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&messages))
	found := false
	for _, m := range messages.Messages {
		if m.Subject == subject {
			response, err := http.Get("http://127.0.0.1:18025/api/v1/message/" + m.ID)
			require.NoError(t, err)
			var received struct {
				HTML string `json:"HTML"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&received))
			response.Body.Close()
			require.Contains(t, received.HTML, body)
			found = true
			break
		}
	}
	require.True(t, found, "Mailpit 必须实际收到正确主题邮件")
	// 换 Key 后第一笔仅重建基线，不能把另一个钱包余额记充值。
	_, err = db.Exec(`UPDATE accounts SET credentials='{"api_key":"replacement-key"}' WHERE id=$1`, account1)
	require.NoError(t, err)
	keyFingerprint = fmt.Sprintf("%x", md5.Sum([]byte("replacement-key")))
	probe(account1, 20, now.Add(4*time.Second))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM balance_center_recharge_events WHERE site_id=$1`, siteID).Scan(&count))
	require.Equal(t, 1, count)
	t.Log("真实 PostgreSQL：9→11 仅一笔 2 元；重复、第二 Key、乱序和换 Key 均不重复；Mailpit 收到充值成功邮件")
}
