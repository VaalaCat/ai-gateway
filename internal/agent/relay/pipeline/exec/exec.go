package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// SleepReader 解耦 exec 包对 cache 包的依赖。
// 生产由 *cache.Store 实现:func (s *Store) FallbackSleepMs() int { return s.Settings().FallbackSleepMs }
// 测试可注入 stub。
type SleepReader interface {
	FallbackSleepMs() int
}

// ResilientRunner 给单次 channel dispatch 套重试/熔断/超时。
// 生产由 *resilience.Runner 实现；nil 时 Executor 退化为裸 dispatch（向后兼容）。
type ResilientRunner interface {
	Run(rctx *state.RelayContext, a state.Attempt, dispatch func() state.AttemptResult) state.AttemptResult
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
	Sleep      SleepReader      // 可为 nil,空时跳过 sleep 行为(向后兼容老 test stub)
	Affinity   *affinity.Engine // 可为 nil：nil 时不剔除粘性
	Resilience ResilientRunner  // 可为 nil：nil 时裸 dispatch（向后兼容老 test stub）
	Gate       state.RateGate   // 可为 nil：nil 时不限流（向后兼容老 test stub）
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
	if e.Gate != nil {
		reqLease, err := e.Gate.AcquireRequest(rctx)
		if err != nil {
			out.Err = err // handler 会把 Execution.Err 升成 State.Err → 429
			return
		}
		defer reqLease.Release()
	}
	for idx, a := range attempts {
		if maybeForward(rctx, idx, &rctx.State.Plan) {
			rctx.State.Forwarded = true
			return
		}
		rec.ResetAttempt()
		started := time.Now()
		res, dispatches := e.runAttempt(rctx, a)
		durMs := int(time.Since(started).Milliseconds())
		out.Used = a
		out.Outcome = res
		ar := buildAttemptRecord(idx+1, a, res, dispatches, durMs)
		rec.SnapshotAttempt()
		ar.HasTrace = rec.LastSnapshotVerbose()
		out.History = append(out.History, ar)
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

		// 粘性 channel 发生可重试硬失败 → 剔除，避免下次继续优先打坏账号。
		if a.ByAffinity && e.Affinity != nil && rctx.Input.UserInfo != nil && rctx.Input.UserInfo.UserID != 0 {
			e.Affinity.Forget(affinity.Key{UserID: rctx.Input.UserInfo.UserID, RealModel: a.RealModel})
		}

		// 熔断 open 已跳过 dispatch，直接试下一候选，不必再 sleep。
		if errors.Is(res.Err, resilience.ErrBreakerOpen) {
			continue
		}

		// 默认: sleep + 推进
		// rctx.Context / Request nil guard 与 runAttempt 内 body-reset 保持一致,
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

// runAttempt 单次候选的派发：有 Resilience 则套 failsafe（可能同 channel 重试多次），
// 否则裸 dispatch。body reset 在每次实际 dispatch 前做（同 channel 重试要重发）。
// 返回结果与该候选实际 dispatch 次数（含内层重试，最少 1）。
func (e *Executor) runAttempt(rctx *state.RelayContext, a state.Attempt) (state.AttemptResult, int) {
	dispatches := 0
	if e.Gate != nil {
		attLease, err := e.Gate.AcquireAttempt(rctx, a)
		if err != nil {
			// 尝试级拒绝：当作该渠道一次失败，让主循环 fallback 下一渠道。
			return state.AttemptResult{Err: err}, dispatches
		}
		defer attLease.Release() // 该 attempt(含内层 resilience 重试)结束即还
	}
	dispatch := func() state.AttemptResult {
		rctx.State.Recorder.ResetAttempt() // 每次内层 dispatch 前重置 attempt 级 trace 状态,避免失败→重试成功时旧失败泄漏
		dispatches++
		if rctx.Context != nil && rctx.Context.Request != nil {
			rctx.Context.Request.Body = io.NopCloser(bytes.NewReader(rctx.Input.Body))
		}
		return e.Dispatcher.Dispatch(rctx, a)
	}
	if e.Resilience == nil {
		return dispatch(), dispatches
	}
	return e.Resilience.Run(rctx, a, dispatch), dispatches
}

// buildAttemptRecord 把一次候选结果拼成链路条目（不含密钥，error 转 string 截断）。
func buildAttemptRecord(seq int, a state.Attempt, res state.AttemptResult, dispatches, durMs int) models.AttemptRecord {
	rec := models.AttemptRecord{
		Seq:        seq,
		RealModel:  a.RealModel,
		Source:     string(a.Source),
		ByAffinity: a.ByAffinity,
		DurationMs: durMs,
		Status:     "ok",
	}
	if a.Channel != nil {
		rec.ChannelName = a.Channel.Name
		if a.SourceID != 0 {
			rec.ChannelID = a.SourceID
		} else {
			rec.ChannelID = a.Channel.ID
		}
	}
	if dispatches > 1 {
		rec.Retries = dispatches - 1
	}
	if res.Err != nil {
		rec.Status = "fail"
		rec.ErrorMessage = truncateErr(res.Err.Error())
		if errors.Is(res.Err, resilience.ErrBreakerOpen) {
			rec.BreakerOpen = true
		}
		var upErr *common.UpstreamError
		if errors.As(res.Err, &upErr) {
			rec.HTTPStatus = upErr.Status
			rec.ErrorType = upErr.ProviderErrorType
		}
	}
	return rec
}

func truncateErr(s string) string {
	if len(s) > 256 {
		return s[:256] + "..."
	}
	return s
}
