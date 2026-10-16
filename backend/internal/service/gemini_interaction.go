package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var ErrGeminiInteractionNotFound = errors.New("interaction not found")
var ErrGeminiInteractionConcurrency = errors.New("interaction concurrency exceeded")
var ErrGeminiInteractionKeyPending = infraerrors.Conflict("GEMINI_INTERACTION_KEY_PENDING", "该 Key 有未结算的 Interactions 任务，请完成或取消并等待结算后再删除")
var ErrGeminiInteractionDependencyPending = infraerrors.Conflict("GEMINI_INTERACTION_DEPENDENCY_PENDING", "该资源有未结算的 Interactions 任务，请完成或取消并等待结算后再删除或撤销")
var ErrGeminiInteractionBusy = infraerrors.ServiceUnavailable("GEMINI_INTERACTION_BUSY", "Interactions 依赖正在更新，请稍后重试")

const GeminiInteractionUsageRequestIDPrefix = "ginteraction:"

// GeminiInteraction 只保存路由与结算元数据，不持久化输入、输出或认证凭据。
type GeminiInteraction struct {
	ID                                          int64
	PublicID, UpstreamID                        string
	PreviousPublicID, PreviousUpstreamID        string
	UserConcurrency, AccountConcurrency         int
	UserID, APIKeyID, GroupID, AccountID        int64
	Model, UpstreamModel, PayloadHash, Revision string
	PricingAt, CreatedAt                        time.Time
	BillingGroup                                *Group
	SubscriptionID                              int64
	ChannelUsage                                ChannelUsageFields
	Usage                                       *ClaudeUsage
	BalanceHold                                 float64
	Status                                      string
	Stream, Background, Settled, Deleted        bool
}

type GeminiInteractionRepository interface {
	Create(context.Context, *GeminiInteraction) error
	GetOwned(context.Context, int64, int64, int64, string) (*GeminiInteraction, error)
	Update(context.Context, *GeminiInteraction) error
	MarkSettled(context.Context, int64) error
	HasUsageBilling(context.Context, string, int64) (bool, error)
	SoftDelete(context.Context, int64) error
	// Pending 原子抢占到期的结算任务，租约过期后其他进程可恢复。
	Pending(context.Context, int) ([]*GeminiInteraction, error)
}

type GeminiInteractionService struct {
	repo                GeminiInteractionRepository
	compat              *GeminiMessagesCompatService
	gateway             *GatewayService
	apiKeys             *APIKeyService
	cfg                 *config.Config
	stop                chan struct{}
	done                chan struct{}
	startOnce, stopOnce sync.Once
}

func NewGeminiInteractionService(repo GeminiInteractionRepository, compat *GeminiMessagesCompatService, gateway *GatewayService, apiKeys *APIKeyService, cfg *config.Config) *GeminiInteractionService {
	return &GeminiInteractionService{repo: repo, compat: compat, gateway: gateway, apiKeys: apiKeys, cfg: cfg, stop: make(chan struct{}), done: make(chan struct{})}
}
func (s *GeminiInteractionService) Save(ctx context.Context, r *GeminiInteraction) error {
	if err := s.repo.Create(ctx, r); err != nil {
		return err
	}
	s.invalidateBalance(ctx, r)
	return nil
}

func (s *GeminiInteractionService) invalidateBalance(ctx context.Context, r *GeminiInteraction) {
	if r.BalanceHold > 0 && s.gateway != nil && s.gateway.billingCacheService != nil {
		_ = s.gateway.billingCacheService.InvalidateUserBalance(ctx, r.UserID)
	}
}

func (s *GeminiInteractionService) markSettled(ctx context.Context, r *GeminiInteraction) error {
	if err := s.repo.MarkSettled(ctx, r.ID); err != nil {
		return err
	}
	r.Settled = true
	s.invalidateBalance(ctx, r)
	return nil
}
func (s *GeminiInteractionService) GetOwned(ctx context.Context, k, u, g int64, id string) (*GeminiInteraction, error) {
	if id == "" || len(id) > 128 || strings.ContainsAny(id, "/\\?#") {
		return nil, ErrGeminiInteractionNotFound
	}
	return s.repo.GetOwned(ctx, k, u, g, id)
}
func (s *GeminiInteractionService) Account(ctx context.Context, id int64) (*Account, error) {
	return s.compat.accountRepo.GetByID(ctx, id)
}
func (s *GeminiInteractionService) Forward(ctx context.Context, a *Account, method, id, action string, q url.Values, revision string, body []byte) (*http.Response, error) {
	return s.compat.DoGeminiInteraction(ctx, a, method, id, action, q, revision, body)
}

// ExcludedGeminiInteractionAccounts 限制新协议候选，不改变已有协议调度策略。
func (s *GatewayService) ExcludedGeminiInteractionAccounts(ctx context.Context, groupID *int64, boundID int64) (map[int64]struct{}, error) {
	accounts, _, err := s.listSchedulableAccounts(ctx, groupID, PlatformGemini, false)
	if err != nil {
		return nil, err
	}
	excluded := make(map[int64]struct{})
	for _, a := range accounts {
		if !IsGeminiInteractionAccount(&a) || (boundID > 0 && a.ID != boundID) {
			excluded[a.ID] = struct{}{}
		}
	}
	return excluded, nil
}

func GeminiInteractionTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "requires_action"
}

// Observe 同时处理 JSON 与 SSE 中的 interaction 对象，先保存绑定再向客户端暴露 ID。
func (s *GeminiInteractionService) Observe(ctx context.Context, r *GeminiInteraction, payload []byte) ([]byte, error) {
	if !gjson.ValidBytes(payload) {
		return nil, errors.New("invalid interaction response")
	}
	root := gjson.ParseBytes(payload)
	prefix := ""
	interaction := root
	if nested := root.Get("interaction"); nested.IsObject() {
		interaction = nested
		prefix = "interaction."
	}
	id := interaction.Get("id").String()
	if id == "" {
		id = root.Get("interaction_id").String()
	}
	if r.UpstreamID != "" && r.UpstreamID != id {
		if id != "" {
			return nil, errors.New("interaction response ID changed")
		}
	}
	if len(id) > 2048 || (id != "" && !geminiInteractionIDPattern.MatchString(id)) {
		return nil, errors.New("interaction ID too long")
	}
	if id != "" {
		r.UpstreamID = id
	}
	terminal := GeminiInteractionTerminal(r.Status)
	if status := interaction.Get("status").String(); status != "" && !terminal {
		r.Status = status
	}
	usage, err := ParseGeminiInteractionUsage(payload)
	// 进度用量不是最终账单；终态缺失用量时必须等待查询恢复，不能拿旧进度结算。
	if err == nil && GeminiInteractionTerminal(interaction.Get("status").String()) && !(terminal && r.Usage != nil) {
		r.Usage = &usage
	}
	// 先固化资源 ID，坏用量不能使已经创建的任务失去恢复入口。
	if r.UpstreamID != "" && (id != "" || interaction.Get("status").Exists() || err == nil) {
		if updateErr := s.repo.Update(ctx, r); updateErr != nil {
			return nil, updateErr
		}
	}
	if err != nil && !errors.Is(err, ErrGeminiInteractionUsageMissing) {
		return nil, err
	}
	err = nil
	out := payload
	for _, field := range []string{prefix + "id", "interaction_id", prefix + "interaction_id"} {
		if gjson.GetBytes(out, field).String() == r.UpstreamID && r.UpstreamID != "" {
			out, err = sjson.SetBytes(out, field, r.PublicID)
			if err != nil {
				return nil, err
			}
		}
	}
	for _, field := range []string{"previous_interaction_id", "interaction.previous_interaction_id"} {
		if previous := gjson.GetBytes(out, field).String(); previous != "" {
			if r.PreviousUpstreamID == "" || previous != r.PreviousUpstreamID {
				return nil, errors.New("interaction previous ID changed")
			}
			out, err = sjson.SetBytes(out, field, r.PreviousPublicID)
			if err != nil {
				return nil, err
			}
		}
	}
	if interaction.Get("model").Exists() {
		out, err = sjson.SetBytes(out, prefix+"model", r.Model)
	}
	return out, err
}

// Settle 与后台恢复及客户端轮询共享同一个金额事件 ID，生产原子计费仓储负责去重。
func (s *GeminiInteractionService) Settle(ctx context.Context, r *GeminiInteraction) error {
	if r.Settled || !GeminiInteractionTerminal(r.Status) || r.Usage == nil {
		return nil
	}
	if s.gateway == nil || s.apiKeys == nil {
		return errors.New("interaction billing unavailable")
	}
	// 计费事务成功后若进程在标记之前退出，优先核对已提交凭证，避免重算配置漂移。
	billed, err := s.repo.HasUsageBilling(ctx, GeminiInteractionUsageRequestIDPrefix+r.PublicID, r.APIKeyID)
	if err != nil {
		return err
	}
	if billed {
		return s.markSettled(ctx, r)
	}
	key, err := s.apiKeys.apiKeyRepo.GetByID(ctx, r.APIKeyID)
	if err != nil {
		return err
	}
	if key == nil || key.UserID != r.UserID {
		return ErrGeminiInteractionNotFound
	}
	cp := *key
	key = &cp
	key.GroupID = &r.GroupID
	key.Group = r.BillingGroup
	if key.User == nil {
		key.User, err = s.gateway.userRepo.GetByID(ctx, r.UserID)
		if err != nil {
			return err
		}
	}
	account, err := s.Account(ctx, r.AccountID)
	if err != nil {
		return err
	}
	var sub *UserSubscription
	if r.SubscriptionID > 0 {
		sub, err = s.gateway.userSubRepo.GetByID(ctx, r.SubscriptionID)
		if err != nil {
			return err
		}
	}
	err = s.gateway.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{RequestID: GeminiInteractionUsageRequestIDPrefix + r.PublicID, Model: r.Model, UpstreamModel: r.UpstreamModel, Usage: *r.Usage, Stream: r.Stream, Duration: time.Since(r.PricingAt)},
		APIKey: key, User: key.User, Account: account, Subscription: sub, APIKeyService: s.apiKeys, PricingAt: r.PricingAt, QuotaPlatform: PlatformGemini,
		InboundEndpoint: "/v1beta/interactions", UpstreamEndpoint: "/v1beta/interactions", RequestPayloadHash: r.PayloadHash, ChannelUsageFields: r.ChannelUsage,
	})
	if err != nil {
		return err
	}
	return s.markSettled(ctx, r)
}

func (s *GeminiInteractionService) Failed(ctx context.Context, r *GeminiInteraction) error {
	r.Status = "failed"
	if err := s.repo.Update(ctx, r); err != nil {
		return err
	}
	return s.markSettled(ctx, r)
}
func (s *GeminiInteractionService) Delete(ctx context.Context, r *GeminiInteraction) error {
	return s.repo.SoftDelete(ctx, r.ID)
}

// RecoverPending 由主节点执行；绑定账号不可调度时仍可读取已存在的上游资源，禁止改号。
func (s *GeminiInteractionService) RecoverPending(ctx context.Context) (int, error) {
	records, err := s.repo.Pending(ctx, 20)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, r := range records {
		if err = ctx.Err(); err != nil {
			return settled, err
		}
		if r.UpstreamID == "" {
			continue
		}
		if !GeminiInteractionTerminal(r.Status) || r.Usage == nil {
			a, e := s.Account(ctx, r.AccountID)
			if e != nil {
				continue
			}
			resp, e := s.Forward(ctx, a, http.MethodGet, r.UpstreamID, "", nil, r.Revision, nil)
			if e != nil {
				continue
			}
			body, e := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
			resp.Body.Close()
			if e != nil || len(body) > 16<<20 || resp.StatusCode != http.StatusOK {
				continue
			}
			if _, e = s.Observe(ctx, r, body); e != nil {
				continue
			}
		}
		if e := s.Settle(ctx, r); e != nil {
			slog.Warn("Gemini Interactions 结算暂未成功，将恢复重试", "interaction_id", r.PublicID)
			continue
		}
		if r.Settled {
			settled++
		}
	}
	return settled, nil
}

func (s *GeminiInteractionService) Start() {
	s.startOnce.Do(func() {
		go func() {
			defer close(s.done)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.stop:
					return
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					_, err := s.RecoverPending(ctx)
					cancel()
					if err != nil {
						slog.Warn("Gemini Interactions 后台恢复暂未成功")
					}
				}
			}
		}()
	})
}
func (s *GeminiInteractionService) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.startOnce.Do(func() { close(s.done) })
	<-s.done
}

func GeminiInteractionPublicID() (string, error) {
	id, err := NewGeminiCachedContentPublicID()
	if err != nil {
		return "", fmt.Errorf("generate interaction ID: %w", err)
	}
	return "i_" + id, nil
}
