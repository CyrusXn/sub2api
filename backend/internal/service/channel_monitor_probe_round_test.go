//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// probeScript 按尝试次序描述 fake 上游的行为：sleep 超过单次上限即触发「切换下一个账号」。
type probeScript struct {
	sleep  time.Duration
	status int // 0 = 200
}

// scriptedUpstream 起一个按脚本逐次响应的 fake OpenAI，并把单次探针上限压到毫秒级。
func scriptedUpstream(t *testing.T, attemptCap time.Duration, maxAttempts int, script []probeScript) (string, *int64) {
	t.Helper()

	origTimeout := monitorProbeAttemptTimeout
	origMax := monitorProbeMaxAttemptsPerRound
	monitorProbeAttemptTimeout = attemptCap
	monitorProbeMaxAttemptsPerRound = maxAttempts
	t.Cleanup(func() {
		monitorProbeAttemptTimeout = origTimeout
		monitorProbeMaxAttemptsPerRound = origMax
	})

	var calls int64
	h := &openAICaptureHandler{}
	swapMonitorHTTPClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt64(&calls, 1)) - 1
		step := probeScript{}
		if idx < len(script) {
			step = script[idx]
		} else if len(script) > 0 {
			step = script[len(script)-1] // 脚本用尽后沿用最后一档行为
		}
		if step.sleep > 0 {
			select {
			case <-time.After(step.sleep):
			case <-r.Context().Done():
				return // 客户端已按上限中止
			}
		}
		if step.status != 0 && step.status != http.StatusOK {
			w.WriteHeader(step.status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &calls
}

func runRound(t *testing.T, endpoint string) probeRoundOutcome {
	t.Helper()
	return runProbeRound(context.Background(), MonitorProviderOpenAI, endpoint, "sk-test", "gpt-test", nil)
}

func TestRunProbeRound_GreenOnFirstAttemptStopsRound(t *testing.T) {
	endpoint, calls := scriptedUpstream(t, 300*time.Millisecond, 4, []probeScript{{}})

	out := runRound(t, endpoint)

	if out.result.Status != MonitorStatusOperational {
		t.Fatalf("首次即成功应记录绿色，实际 status=%s message=%q", out.result.Status, out.result.Message)
	}
	if out.attempts != 1 || out.degradedAttempts != 0 {
		t.Fatalf("绿色只应发起 1 次探针，实际 attempts=%d degraded=%d", out.attempts, out.degradedAttempts)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("命中绿色后本轮不应继续探针，实际请求 %d 次", got)
	}
}

func TestRunProbeRound_SlowAttemptsThenGreen(t *testing.T) {
	endpoint, calls := scriptedUpstream(t, 200*time.Millisecond, 5, []probeScript{
		{sleep: time.Second}, // 超上限 → 黄色，换下一个账号
		{sleep: time.Second}, // 超上限 → 黄色，换下一个账号
		{},                   // 秒回 → 绿色，本轮结束
	})

	out := runRound(t, endpoint)

	if out.result.Status != MonitorStatusOperational {
		t.Fatalf("最终拿到快响应应记录绿色，实际 status=%s message=%q", out.result.Status, out.result.Message)
	}
	if out.attempts != 3 {
		t.Fatalf("应在第 3 次命中绿色，实际 attempts=%d", out.attempts)
	}
	if out.degradedAttempts != 2 {
		t.Fatalf("前两次超时应计入降级次数，实际 degraded=%d", out.degradedAttempts)
	}
	if got := atomic.LoadInt64(calls); got != 3 {
		t.Fatalf("期望 3 次上游请求，实际 %d 次", got)
	}
}

func TestRunProbeRound_AllSlowRecordsDegraded(t *testing.T) {
	endpoint, calls := scriptedUpstream(t, 150*time.Millisecond, 4, []probeScript{{sleep: time.Second}})

	out := runRound(t, endpoint)

	if out.result.Status != MonitorStatusDegraded {
		t.Fatalf("全部超过单次上限应记录黄色，实际 status=%s message=%q", out.result.Status, out.result.Message)
	}
	if out.attempts != 4 || out.degradedAttempts != 4 {
		t.Fatalf("应把 4 个账号全部探完，实际 attempts=%d degraded=%d", out.attempts, out.degradedAttempts)
	}
	if got := atomic.LoadInt64(calls); got != 4 {
		t.Fatalf("期望 4 次上游请求，实际 %d 次", got)
	}
	if out.result.LatencyMs == nil {
		t.Fatal("黄色记录应带延迟，便于前端展示慢到什么程度")
	}
}

func TestRunProbeRound_AllHardFailuresRecordsRed(t *testing.T) {
	endpoint, calls := scriptedUpstream(t, 500*time.Millisecond, 3, []probeScript{
		{status: http.StatusInternalServerError},
	})

	out := runRound(t, endpoint)

	if out.result.Status != MonitorStatusError {
		t.Fatalf("全部硬失败应记录红色，实际 status=%s message=%q", out.result.Status, out.result.Message)
	}
	if out.attempts != 3 {
		t.Fatalf("红色也要把分组内账号探完，实际 attempts=%d", out.attempts)
	}
	if out.degradedAttempts != 0 {
		t.Fatalf("硬失败不计入降级（不产生扣费），实际 degraded=%d", out.degradedAttempts)
	}
	if got := atomic.LoadInt64(calls); got != 3 {
		t.Fatalf("期望 3 次上游请求，实际 %d 次", got)
	}
}

func TestRunProbeRound_DegradedWinsOverFailure(t *testing.T) {
	endpoint, _ := scriptedUpstream(t, 150*time.Millisecond, 3, []probeScript{
		{status: http.StatusInternalServerError},
		{sleep: time.Second},
		{status: http.StatusInternalServerError},
	})

	out := runRound(t, endpoint)

	// 黄色优先于红色：能慢着返回比完全不可用更接近可用，与可用率口径（绿+黄计可用）一致。
	if out.result.Status != MonitorStatusDegraded {
		t.Fatalf("出现过黄色时应优先记录黄色，实际 status=%s", out.result.Status)
	}
	if out.degradedAttempts != 1 {
		t.Fatalf("只有 1 次超时应记 1 次降级，实际 degraded=%d", out.degradedAttempts)
	}
}

func TestRunProbeRound_CancelledContextDoesNotProbe(t *testing.T) {
	endpoint, calls := scriptedUpstream(t, 500*time.Millisecond, 3, []probeScript{{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := runProbeRound(ctx, MonitorProviderOpenAI, endpoint, "sk-test", "gpt-test", nil)

	if out.attempts != 0 {
		t.Fatalf("上下文已取消不应发起探针，实际 attempts=%d", out.attempts)
	}
	if out.result == nil || out.result.Status != MonitorStatusError {
		t.Fatalf("零次尝试应返回兜底红色记录，实际 %+v", out.result)
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("上下文已取消不应请求上游，实际 %d 次", got)
	}
}

func TestNextDelayWithPenalty(t *testing.T) {
	task := &scheduledMonitor{interval: 60 * time.Second}

	// 绿色（1 次请求）与红色（不计费）都不退避。
	if got := task.nextDelayWithPenalty(0); got != 60*time.Second {
		t.Fatalf("无降级时应保持配置间隔，实际 %s", got)
	}
	if got := task.nextDelayWithPenalty(1); got != 60*time.Second {
		t.Fatalf("单次降级不放大间隔，实际 %s", got)
	}
	// 用户给的算例：一个分组内 5 个账号都黄，配置 60s → 下一轮等 5×60s。
	if got := task.nextDelayWithPenalty(5); got != 300*time.Second {
		t.Fatalf("5 次降级应等 300s，实际 %s", got)
	}
	// 上限兜底：大间隔 × 多次降级不能把监控推到不可用。
	big := &scheduledMonitor{interval: time.Hour}
	if got := big.nextDelayWithPenalty(5); got != monitorProbeBackoffMaxDelay {
		t.Fatalf("应被 %s 上限截断，实际 %s", monitorProbeBackoffMaxDelay, got)
	}
}
