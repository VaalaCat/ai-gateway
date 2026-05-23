package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

// SleepReader 解耦 exec 包对 cache 包的依赖。
// 生产由 *cache.Store 实现:func (s *Store) FallbackSleepMs() int { return s.Settings().FallbackSleepMs }
// 测试可注入 stub。
type SleepReader interface {
	FallbackSleepMs() int
}

// Executor 串行遍历 Plan.Attempts 派发到 Dispatcher，处理 retry 和 forward 决策。
// 写到 rctx.State.Execution 和 rctx.State.Forwarded — 调用方接力做计费 / 落 trace。
//
// Dispatcher 字段是 state.Dispatcher 接口（声明于 state 叶子包），生产由
// backend.NewDispatcher 装配；测试可注入 stub。接口而非 *backend.Dispatcher
// 是为了避免 relay → backend → relay 的循环 import。
//
// Sleep 字段实现 SleepReader 接口，由生产 *cache.Store 满足；nil 时跳过 sleep（向后兼容老 test stub）。
type Executor struct {
	Dispatcher state.Dispatcher
	Sleep      SleepReader // 可为 nil,空时跳过 sleep 行为(向后兼容老 test stub)
}

// Run 主循环:每个 attempt 失败时按"3 类例外立即返回 / 否则 sleep + 推进"决策。
// 例外:
//
//	(1) Written=true        — SSE partial 已发出,继续 fallback 会产生双份数据
//	(2) context.Canceled    — 客户端 abort,继续 fallback 无意义且浪费 token
//	(3) HTTP 400 + provider error.type == "invalid_request_error" — prompt 本身违法,
//	                         fallback 下一 channel 也会被拒
//
// 默认:sleep cache.Settings().FallbackSleepMs ms,推进到下一 attempt。
// sleep 用 select + ctx.Done 监听,避免客户端 cancel 时被 sleep 卡住。
func (e *Executor) Run(rctx *state.RelayContext) {
	rec := rctx.State.Recorder
	out := &rctx.State.Execution
	attempts := rctx.State.Plan.Attempts
	for idx, a := range attempts {
		if maybeForward(rctx, idx, &rctx.State.Plan) {
			rctx.State.Forwarded = true
			return
		}
		rec.ResetAttempt()
		if rctx.Context != nil && rctx.Context.Request != nil {
			rctx.Context.Request.Body = io.NopCloser(bytes.NewReader(rctx.Input.Body))
		}
		res := e.Dispatcher.Dispatch(rctx, a)
		out.Used = a
		out.Outcome = res
		if res.Err == nil {
			return
		}
		logAttemptFailed(rctx, a, res.Err, len(attempts)-idx-1)

		// 例外 1: 已写出 partial response
		if res.Written {
			out.Err = res.Err
			return
		}
		// 例外 2: 客户端断连
		if errors.Is(res.Err, context.Canceled) {
			out.Err = res.Err
			return
		}
		// 例外 3: HTTP 400 + provider invalid_request_error
		var upErr *common.UpstreamError
		if errors.As(res.Err, &upErr) && upErr.Status == 400 &&
			upErr.ProviderErrorType == "invalid_request_error" {
			out.Err = res.Err
			return
		}

		// 默认: sleep + 推进
		// rctx.Context / Request nil guard 与 L54 body-reset 保持一致,
		// 测试场景注入 Sleep 但不构造完整 rctx 时不应 panic。
		if idx+1 < len(attempts) && e.Sleep != nil && rctx.Context != nil && rctx.Context.Request != nil {
			if ms := e.Sleep.FallbackSleepMs(); ms > 0 {
				select {
				case <-rctx.Context.Request.Context().Done():
					out.Err = rctx.Context.Request.Context().Err()
					return
				case <-time.After(time.Duration(ms) * time.Millisecond):
				}
			}
		}
	}
	// 至少 dispatch 过一次（Used.Channel 在第一次 dispatch 即赋值）→ promote Outcome.Err 到终态 Err。
	// 替代 len(History) > 0 的老判空：Used.Channel != nil 是"有过 attempt"的等价信号。
	if out.Used.Channel != nil {
		out.Err = out.Outcome.Err
	}
}
