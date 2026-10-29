package service

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func identityAccountID(account *Account) int64 {
	if account == nil || !account.IsOpenAIApiKey() {
		return 0
	}
	var id int64
	switch value := account.Extra[accountIdentityAccountIDKey].(type) {
	case int64:
		id = value
	case int:
		id = int64(value)
	case float64:
		if value > 0 && value < float64(math.MaxInt64) && math.Trunc(value) == value {
			id = int64(value)
		}
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			id = parsed
		}
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil {
			id = parsed
		}
	}
	if id <= 0 {
		return 0
	}
	return id
}

// selectOpenAIIdentityAccount 在整次请求中共享 visited；配置链和上游失败重选均不得回访。
// 只复用原调度器的资格校验与抢槽，不写入普通会话的粘性绑定。
func (s *OpenAIGatewayService) selectOpenAIIdentityAccount(
	ctx context.Context, state *openAIIdentityRoutingState, initial *Account,
	groupID *int64, model string, excluded map[int64]struct{},
	transport OpenAIUpstreamTransport, capability OpenAIEndpointCapability, requireCompact bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	decision := OpenAIAccountScheduleDecision{Layer: "identity_account"}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	if err := ctx.Err(); err != nil {
		return nil, decision, err
	}
	if s.checkChannelPricingRestriction(ctx, groupID, model) {
		return nil, decision, ErrNoAvailableAccounts
	}
	for id := range excluded {
		state.visited[id] = struct{}{}
	}
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil {
		return nil, decision, err
	}
	candidates := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.IsOpenAIApiKey() && openAIStickyAccountMatchesGroup(account, groupID) {
			candidates = append(candidates, account)
		}
	}
	decision.CandidateCount = len(candidates)
	rateOrder := newOpenAILegacyUpstreamRateOrder(candidates, time.Now(), s.openAIOAuthSchedulingRateMultiplier(ctx))
	sort.SliceStable(candidates, func(i, j int) bool {
		if cmp := rateOrder.compare(candidates[i], candidates[j]); cmp != 0 {
			return cmp < 0
		}
		return s.isBetterAccount(candidates[i], candidates[j])
	})
	scheduler := &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
	req := OpenAIAccountScheduleRequest{GroupID: groupID, Platform: PlatformOpenAI, RequestedModel: model,
		RequiredTransport: transport, RequiredCapability: capability, RequireCompact: requireCompact,
		RequirePrivacySet: s.openAIGroupRequiresPrivacySet(ctx, groupID), PreserveStickyBinding: true}
	budget := newOpenAISelectionProbeBudget()
	index := 0
	current := initial
	for {
		if err := ctx.Err(); err != nil {
			return nil, decision, err
		}
		if current == nil {
			if index >= len(candidates) {
				return nil, decision, ErrNoAvailableAccounts
			}
			current = candidates[index]
			index++
		}
		id := current.ID
		if _, seen := state.visited[id]; seen {
			current = nil
			continue
		}
		// 先标记，再查询/跟随配置：不存在、禁用或成环的目标本轮也只检查一次。
		state.visited[id] = struct{}{}
		current, err = s.getSchedulableAccount(ctx, id)
		if err != nil || current == nil || !current.IsOpenAIApiKey() || !openAIStickyAccountMatchesGroup(current, groupID) {
			current = nil
			continue
		}
		current = s.resolveFreshSchedulableOpenAIAccount(ctx, current, PlatformOpenAI, model, requireCompact, capability)
		if current == nil || !scheduler.isAccountRequestCompatible(ctx, current, req) || !scheduler.isAccountTransportCompatible(current, transport) {
			current = nil
			continue
		}
		if target := identityAccountID(current); target > 0 {
			current = &Account{ID: target}
			continue
		}
		selection, _, acquireErr := scheduler.tryAcquireOpenAISelectionOrderWithBudget(ctx, req, []openAIAccountCandidateScore{{account: current}}, budget)
		current = nil
		if acquireErr != nil || selection == nil || selection.Account == nil {
			continue
		}
		fresh := selection.Account
		if !fresh.IsOpenAIApiKey() || !openAIStickyAccountMatchesGroup(fresh, groupID) || identityAccountID(fresh) > 0 {
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			if target := identityAccountID(fresh); target > 0 {
				current = &Account{ID: target}
			}
			continue
		}
		decision.SelectedAccountID = fresh.ID
		decision.SelectedAccountType = fresh.Type
		return selection, decision, nil
	}
}
