package resilience

import (
	"errors"
	"fmt"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// ErrBreakerOpen 表示该 channel 熔断器处于 open，本次未真正 dispatch。
// Executor 外层据此跳到下一候选且不 sleep。
var ErrBreakerOpen = errors.New("channel circuit breaker open")

// SettingsReader 提供全局韧性默认值的实时快照。
// 生产由 agent 的 *cache.Store 满足(它有 Settings() settings.AgentSettings);
// 韧性参数走管理后台 Settings(非 config.yaml),admin 改后经同步即时生效。
type SettingsReader interface {
	Settings() settings.AgentSettings
}

// Runner 给单次 channel dispatch 套 failsafe(retry+breaker+timeout)。
// 全局默认每请求从 Settings 实时读取;每 channel 的 Resilience 覆盖在其上 Resolve。
type Runner struct {
	Settings SettingsReader
	Breakers *Registry
}

// globalDefaults 把后台 Settings 快照映射成韧性 Config(全局默认)。
func (r *Runner) globalDefaults() Config {
	s := r.Settings.Settings()
	return Config{
		MaxRetries:        s.MaxRetriesPerChannel,
		BackoffBaseMs:     s.RetryBackoffBaseMs,
		BackoffMaxMs:      s.RetryBackoffMaxMs,
		BreakerThreshold:  s.BreakerThreshold,
		BreakerCooldownMs: s.BreakerCooldownMs,
		BreakerEnabled:    s.BreakerEnabled != 0,
	}
}

// Run 解析该 channel 有效配置，组装 failsafe 策略栈，执行 dispatch 闭包。
//
// 策略顺序：retry 最外 → breaker 居中。等价组合：retry(breaker(dispatch))，
// retry 每次都经过 breaker 计账。
//
// 不套"单次超时"策略：dispatch 对流式响应要等整个流写完才返回，failsafe 的
// Timeout 到点不会取消底层 HTTP，只会丢下仍在写客户端的 dispatch 并误触发转移，
// 造成响应双写/错乱。超时保护交给 request context（客户端断连）、transport 的
// ResponseHeaderTimeout（上游迟迟不出响应头）和非流式的 RelayTimeout。
func (r *Runner) Run(_ *state.RelayContext, a state.Attempt, dispatch func() state.AttemptResult) state.AttemptResult {
	base := r.globalDefaults()
	cfg := base
	channelID := a.SourceID
	if a.Channel != nil {
		ov := a.Channel.Resilience.Data()
		cfg = Resolve(base, &ov)
		if channelID == 0 {
			channelID = a.Channel.ID
		}
	}

	retry := retrypolicy.NewBuilder[state.AttemptResult]().
		WithMaxRetries(cfg.MaxRetries).
		WithBackoff(
			time.Duration(cfg.BackoffBaseMs)*time.Millisecond,
			time.Duration(cfg.BackoffMaxMs)*time.Millisecond,
		).
		// 只读 res.Err，忽略第二个 err 参数。
		// dispatch 闭包返回 (res, res.Err)；真实失败时 res.Err 非 nil。
		// 熔断器 open 时 failsafe 合成 (零值 res, ErrOpen)，res.Err==nil →
		// Classify 判为成功 → retry 不对 ErrOpen 重试（正确）。
		// 若把 err 合并进 res.Err，Classify 会把 ErrOpen 当非结构化错误去重试（错误）。
		HandleIf(func(res state.AttemptResult, _ error) bool {
			return Classify(res).RetrySameChannel
		}).
		AbortIf(func(res state.AttemptResult, _ error) bool {
			return Classify(res).AbortAll
		}).
		// 返回最后一次失败结果（含 res.Err），而非 retrypolicy.ErrExceeded 包装器。
		ReturnLastFailure().
		Build()

	if !cfg.BreakerEnabled {
		res, _ := failsafe.With[state.AttemptResult](retry).
			Get(func() (state.AttemptResult, error) {
				out := dispatch()
				return out, out.Err
			})
		return res
	}

	cb := r.Breakers.Get(BreakerKey{Source: a.Source, ID: channelID}, cfg)
	res, err := failsafe.With[state.AttemptResult](retry, cb).
		Get(func() (state.AttemptResult, error) {
			out := dispatch()
			return out, out.Err
		})

	if errors.Is(err, circuitbreaker.ErrOpen) {
		return state.AttemptResult{Err: fmt.Errorf("%w (channel %d)", ErrBreakerOpen, channelID)}
	}
	return res
}
