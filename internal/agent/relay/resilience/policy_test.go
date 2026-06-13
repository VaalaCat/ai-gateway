package resilience

import (
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"gorm.io/datatypes"
)

func chAttempt(id uint) state.Attempt {
	c := &models.Channel{}
	c.ID = id
	return state.Attempt{Channel: c, SourceID: id}
}

func okRes() state.AttemptResult { return state.AttemptResult{} }
func errRes() state.AttemptResult {
	return state.AttemptResult{Err: &common.UpstreamError{Status: 503}}
}

// stubSettings 满足 SettingsReader,供测试注入全局韧性默认。
type stubSettings struct{ s settings.AgentSettings }

func (s stubSettings) Settings() settings.AgentSettings { return s.s }

// settingsFromCfg 把 Config 转成等值的 AgentSettings 快照(测试用)。
func settingsFromCfg(c Config) stubSettings {
	breakerEnabled := 0
	if c.BreakerEnabled {
		breakerEnabled = 1
	}
	return stubSettings{settings.AgentSettings{
		MaxRetriesPerChannel: c.MaxRetries,
		RetryBackoffBaseMs:   c.BackoffBaseMs,
		RetryBackoffMaxMs:    c.BackoffMaxMs,
		BreakerThreshold:     c.BreakerThreshold,
		BreakerCooldownMs:    c.BreakerCooldownMs,
		BreakerEnabled:       breakerEnabled,
	}}
}

func TestRunner_SuccessFirstTry(t *testing.T) {
	r := &Runner{Settings: settingsFromCfg(testCfg()), Breakers: NewRegistry()}
	n := 0
	res := r.Run(nil, chAttempt(1), func() state.AttemptResult { n++; return okRes() })
	if n != 1 || res.Err != nil {
		t.Fatalf("want 1 dispatch ok, got n=%d err=%v", n, res.Err)
	}
}

func TestRunner_RetryThenSuccess(t *testing.T) {
	r := &Runner{Settings: settingsFromCfg(testCfg()), Breakers: NewRegistry()} // MaxRetries=2
	n := 0
	res := r.Run(nil, chAttempt(2), func() state.AttemptResult {
		n++
		if n == 1 {
			return errRes()
		}
		return okRes()
	})
	if n != 2 || res.Err != nil {
		t.Fatalf("want retry-then-success n=2 err=nil, got n=%d err=%v", n, res.Err)
	}
}

func TestRunner_ExhaustsRetries(t *testing.T) {
	// BreakerThreshold=10 确保 3 次失败不会让熔断器提前 open 截断重试。
	cfg := Config{MaxRetries: 2, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 10, BreakerCooldownMs: 50, BreakerEnabled: true}
	r := &Runner{Settings: settingsFromCfg(cfg), Breakers: NewRegistry()} // MaxRetries=2 → 共 3 次
	n := 0
	res := r.Run(nil, chAttempt(3), func() state.AttemptResult { n++; return errRes() })
	if n != 3 || res.Err == nil {
		t.Fatalf("want 3 dispatches and final err, got n=%d err=%v", n, res.Err)
	}
}

func TestRunner_BreakerOpenSkipsDispatch(t *testing.T) {
	r := &Runner{Settings: settingsFromCfg(testCfg()), Breakers: NewRegistry()} // BreakerThreshold=2
	// 先打到 open：一次 Run 跑 3 次失败 dispatch(>=2)即触发 open。
	r.Run(nil, chAttempt(4), func() state.AttemptResult { return errRes() })
	n := 0
	res := r.Run(nil, chAttempt(4), func() state.AttemptResult { n++; return errRes() })
	if n != 0 {
		t.Fatalf("breaker open should skip dispatch, got n=%d", n)
	}
	if !errors.Is(res.Err, ErrBreakerOpen) {
		t.Fatalf("want ErrBreakerOpen, got %v", res.Err)
	}
}

func TestRunner_GlobalBreakerDisabled_DoesNotSkipDispatch(t *testing.T) {
	cfg := Config{MaxRetries: 0, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 1, BreakerCooldownMs: 50, BreakerEnabled: false}
	reg := NewRegistry()
	r := &Runner{Settings: settingsFromCfg(cfg), Breakers: reg}

	for i := 0; i < 3; i++ {
		n := 0
		res := r.Run(nil, chAttempt(10), func() state.AttemptResult {
			n++
			return errRes()
		})
		if n != 1 {
			t.Fatalf("disabled breaker should dispatch on run %d, got n=%d", i+1, n)
		}
		if errors.Is(res.Err, ErrBreakerOpen) {
			t.Fatalf("disabled breaker must not return ErrBreakerOpen, got %v", res.Err)
		}
	}
	if reg.Len() != 0 {
		t.Fatalf("disabled breaker should not create registry entries, got %d", reg.Len())
	}
}

func TestRunner_ChannelOverrideDisablesBreaker(t *testing.T) {
	cfg := Config{MaxRetries: 0, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 1, BreakerCooldownMs: 50, BreakerEnabled: true}
	reg := NewRegistry()
	r := &Runner{Settings: settingsFromCfg(cfg), Breakers: reg}
	off := false
	attempt := chAttempt(11)
	attempt.Channel.Resilience = datatypes.NewJSONType(models.ChannelResilience{BreakerEnabled: &off})

	for i := 0; i < 2; i++ {
		n := 0
		res := r.Run(nil, attempt, func() state.AttemptResult {
			n++
			return errRes()
		})
		if n != 1 {
			t.Fatalf("channel disabled breaker should dispatch on run %d, got n=%d", i+1, n)
		}
		if errors.Is(res.Err, ErrBreakerOpen) {
			t.Fatalf("channel disabled breaker must not return ErrBreakerOpen, got %v", res.Err)
		}
	}
	if reg.Len() != 0 {
		t.Fatalf("channel disabled breaker should not create registry entries, got %d", reg.Len())
	}
}

func TestRunner_ChannelOverrideEnablesBreaker(t *testing.T) {
	cfg := Config{MaxRetries: 0, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 1, BreakerCooldownMs: 50, BreakerEnabled: false}
	r := &Runner{Settings: settingsFromCfg(cfg), Breakers: NewRegistry()}
	on := true
	attempt := chAttempt(12)
	attempt.Channel.Resilience = datatypes.NewJSONType(models.ChannelResilience{BreakerEnabled: &on})

	r.Run(nil, attempt, func() state.AttemptResult { return errRes() })
	n := 0
	res := r.Run(nil, attempt, func() state.AttemptResult {
		n++
		return errRes()
	})
	if n != 0 {
		t.Fatalf("channel enabled breaker should skip open channel, got n=%d", n)
	}
	if !errors.Is(res.Err, ErrBreakerOpen) {
		t.Fatalf("want ErrBreakerOpen, got %v", res.Err)
	}
}

func TestRunner_BreakerDisabledStillRetries(t *testing.T) {
	cfg := Config{MaxRetries: 2, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 1, BreakerCooldownMs: 50, BreakerEnabled: false}
	r := &Runner{Settings: settingsFromCfg(cfg), Breakers: NewRegistry()}
	n := 0
	res := r.Run(nil, chAttempt(13), func() state.AttemptResult {
		n++
		return errRes()
	})
	if n != 3 || res.Err == nil {
		t.Fatalf("disabled breaker must preserve retries, got n=%d err=%v", n, res.Err)
	}
}

func TestRunner_WrittenNoRetry(t *testing.T) {
	r := &Runner{Settings: settingsFromCfg(testCfg()), Breakers: NewRegistry()}
	n := 0
	res := r.Run(nil, chAttempt(5), func() state.AttemptResult {
		n++
		return state.AttemptResult{Written: true, Err: errors.New("mid-stream")}
	})
	if n != 1 || res.Err == nil {
		t.Fatalf("written must not retry, got n=%d err=%v", n, res.Err)
	}
}

// TestRunner_WaitsForSlowDispatch_NoPerAttemptTimeout 回归:Runner 绝不能给
// dispatch 套"单次超时"。流式 LLM 响应里 dispatch 要等整个流写完才返回(可能很久),
// 若被超时丢下,Runner 会返回零值结果(Written 丢失)并让 Executor 误转移 → 响应双写。
// 本用例钉住:Run 必须【完整等待】dispatch 跑完、原样返回其 Written 结果、只 dispatch 一次。
func TestRunner_WaitsForSlowDispatch_NoPerAttemptTimeout(t *testing.T) {
	r := &Runner{Settings: settingsFromCfg(testCfg()), Breakers: NewRegistry()}
	n := 0
	finished := false
	res := r.Run(nil, chAttempt(9), func() state.AttemptResult {
		n++
		time.Sleep(60 * time.Millisecond) // 模拟"长流",远超任何会被重新引入的短超时
		finished = true
		return state.AttemptResult{Written: true}
	})
	if !finished {
		t.Fatal("Run 必须等 dispatch 完整跑完(不得有超时丢下正在写客户端的 dispatch)")
	}
	if n != 1 {
		t.Fatalf("慢 dispatch 必须恰好执行一次,got n=%d", n)
	}
	if !res.Written || res.Err != nil {
		t.Fatalf("已完成 dispatch 的结果(含 Written)必须原样返回,got %+v", res)
	}
}
