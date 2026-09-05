package service

import (
	"context"
	"fmt"
	"time"
)

// probeRoundOutcome 单个模型「一轮」探针的最终结果与计费统计。
//
// result 是唯一会落库的那条记录；attempts / degradedAttempts 只在内存里流转，
// 供调度器计算下一轮等待时长，不写入历史表。
type probeRoundOutcome struct {
	result           *CheckResult
	attempts         int
	degradedAttempts int
}

// runProbeRound 对单个模型执行一轮探针，最多 monitorProbeMaxAttemptsPerRound 次尝试。
//
// 每次尝试都套一层 monitorProbeAttemptTimeout（45s）硬上限：10s 内成功为绿色，
// 10s 至 45s 内成功为黄色，45s 超时或请求失败为红色；失败后继续切换分组内账号重试。
//
// 记录规则：
//   - 命中绿色（operational）→ 立刻记录并结束本轮，剩余账号不再探针；
//   - 所有尝试都没绿色但出现过黄色（10s 至 45s 内成功）→ 记录最快的那条黄色；
//   - 连黄色都没有（HTTP/网络/challenge 失败或超过 45s 超时）→ 记录第一条红色。
func runProbeRound(ctx context.Context, provider, endpoint, apiKey, model string, opts *CheckOptions) probeRoundOutcome {
	out := probeRoundOutcome{}
	var bestDegraded, firstFailure *CheckResult

	for attempt := 1; attempt <= monitorProbeMaxAttemptsPerRound; attempt++ {
		if ctx.Err() != nil {
			break
		}
		res := runProbeAttempt(ctx, provider, endpoint, apiKey, model, opts, attempt)
		out.attempts++

		switch res.Status {
		case MonitorStatusOperational:
			// 绿色即止：本轮不再触发后续账号的探针，直接等下一轮。
			out.result = res
			return out
		case MonitorStatusDegraded:
			out.degradedAttempts++
			if bestDegraded == nil || latencyMsOrMax(res) < latencyMsOrMax(bestDegraded) {
				bestDegraded = res
			}
		default:
			if firstFailure == nil {
				firstFailure = res
			}
		}
	}

	out.result = summarizeProbeRound(model, out.attempts, out.degradedAttempts, bestDegraded, firstFailure)
	return out
}

// runProbeAttempt 执行一次带 45s 硬上限的探针尝试。
//
// 超过 45s 仍未返回时保留 error（红色），因为这已经超过单次探针允许的最大等待时间。
func runProbeAttempt(ctx context.Context, provider, endpoint, apiKey, model string, opts *CheckOptions, attempt int) *CheckResult {
	attemptCtx, cancel := context.WithTimeout(ctx, monitorProbeAttemptTimeout)
	defer cancel()

	return runCheckForModel(attemptCtx, provider, endpoint, apiKey, model, opts)
}

// summarizeProbeRound 在本轮没拿到绿色时挑选最终落库的记录，并补上中文汇总说明。
// 黄色优先于红色：能慢着返回也比完全不可用更接近可用状态，与既有可用率口径一致
// （operational + degraded 均计入可用）。
func summarizeProbeRound(model string, attempts, degradedAttempts int, bestDegraded, firstFailure *CheckResult) *CheckResult {
	switch {
	case bestDegraded != nil:
		bestDegraded.Message = truncateMessage(fmt.Sprintf(
			"分组内探针 %d 次均未拿到 %dms 内的正常响应（其中 %d 次降级）",
			attempts, int(monitorDegradedThreshold/time.Millisecond), degradedAttempts))
		return bestDegraded
	case firstFailure != nil:
		return firstFailure
	default:
		// attempts == 0：父 ctx 在第一次尝试前就被取消（进程退出 / 轮次预算耗尽）。
		return &CheckResult{
			Model:     model,
			Status:    MonitorStatusError,
			Message:   truncateMessage("本轮探针未执行：调度上下文已取消"),
			CheckedAt: time.Now(),
		}
	}
}

// latencyMsOrMax 取延迟毫秒数，缺失时返回极大值，保证「挑最快的黄色」时排在最后。
func latencyMsOrMax(res *CheckResult) int {
	if res == nil || res.LatencyMs == nil {
		return int(^uint(0) >> 1)
	}
	return *res.LatencyMs
}
