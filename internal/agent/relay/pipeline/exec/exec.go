package exec

import (
	"bytes"
	"io"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

// Executor 串行遍历 Plan.Attempts 派发到 Dispatcher，处理 retry 和 forward 决策。
// 写到 rctx.State.Execution 和 rctx.State.Forwarded — 调用方接力做计费 / 落 trace。
//
// Dispatcher 字段是 state.Dispatcher 接口（声明于 state 叶子包），生产由
// backend.NewDispatcher 装配；测试可注入 stub。接口而非 *backend.Dispatcher
// 是为了避免 relay → backend → relay 的循环 import。
type Executor struct {
	Dispatcher state.Dispatcher
}

// Run 主循环：每个 attempt 先做 forward 决策（命中则结束），否则 Dispatcher 派发。
// 成功（Err=nil）→ 结束；失败 + Written=true（流已部分写出）→ 不可重试，结束；
// 失败 + Written=false → 继续下一个 attempt。所有产物都写到 rctx.State.Execution。
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
		// 每次 attempt 前重置 Request.Body —— backend 内部会消费它（io.ReadAll 或代理转发）。
		// 老主循环仅 native 路径在 attempt 内重置 Request.Body；新流程不知道 backend 实现，统一兜底。
		if rctx.Context != nil && rctx.Context.Request != nil {
			rctx.Context.Request.Body = io.NopCloser(bytes.NewReader(rctx.Input.Body))
		}
		res := e.Dispatcher.Dispatch(rctx, a)
		out.Used = a
		out.Outcome = res
		if res.Err == nil {
			return
		}
		// "relay attempt failed" 诊断日志：main:handler.go 老主循环 attempt 失败分支 老行为。
		// 字段对齐 main：channel_id / attempts_left / path / error。
		logAttemptFailed(rctx, a, res.Err, len(attempts)-idx-1)
		if res.Written {
			out.Err = res.Err
			return
		}
	}
	// 至少 dispatch 过一次（Used.Channel 在第一次 dispatch 即赋值）→ promote Outcome.Err 到终态 Err。
	// 替代 len(History) > 0 的老判空：Used.Channel != nil 是"有过 attempt"的等价信号。
	if out.Used.Channel != nil {
		out.Err = out.Outcome.Err
	}
}
