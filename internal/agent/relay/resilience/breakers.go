package resilience

import (
	"sync/atomic"
	"time"

	"github.com/failsafe-go/failsafe-go/circuitbreaker"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// idleTTL 是 breaker 空闲多久后可被 sweep 淘汰。channel 数量本身有界,
// 这里只防"删除 channel 后 breaker 泄漏"。
const idleTTL = time.Hour

// maxBreakers 软上限:超过即在 Get 时按 idleTTL sweep 一次(对齐 affinity.maxEntries 思路)。
const maxBreakers = 10000

type breakerEntry struct {
	cb  circuitbreaker.CircuitBreaker[state.AttemptResult]
	exp atomic.Int64 // unix nano,访问时续期
}

// BreakerKey 唯一标识一个熔断器。必须带 Source:admin 的 Channel.ID 与 BYOK
// private 的 PrivateChannel.ID 是两套独立自增空间,只用 ID 会让同号的 admin /
// private 渠道串用同一个熔断器(跨租户互相熔断)。
type BreakerKey struct {
	Source state.ChannelSource
	ID     uint
}

// Registry 是进程内、每 channel 一个熔断器的注册表(跨请求复用同实例)。
type Registry struct {
	m utils.SyncMap[BreakerKey, *breakerEntry]
}

func NewRegistry() *Registry { return &Registry{} }

// Get 取(或按 cfg 首次创建)key 对应的 breaker,并续期。
func (r *Registry) Get(key BreakerKey, cfg Config) circuitbreaker.CircuitBreaker[state.AttemptResult] {
	if r.Len() >= maxBreakers {
		r.sweep(time.Now())
	}
	now := time.Now().UnixNano()
	if e, ok := r.m.Load(key); ok {
		e.exp.Store(now + int64(idleTTL))
		return e.cb
	}
	e := &breakerEntry{cb: buildBreaker(cfg)}
	e.exp.Store(now + int64(idleTTL))
	// LoadOrStore 处理并发首访:输者丢弃自己 build 的(无副作用),用赢者的。
	actual, _ := r.m.LoadOrStore(key, e)
	return actual.cb
}

func (r *Registry) Len() int { return r.m.Len() }

// sweep 删除已过 idleTTL 的 entry。Range 回调内重新 Load 防误删刚续期的。
func (r *Registry) sweep(now time.Time) {
	cutoff := now.UnixNano()
	r.m.Range(func(k BreakerKey, _ *breakerEntry) bool {
		if cur, ok := r.m.Load(k); ok && cur.exp.Load() <= cutoff {
			r.m.Delete(k)
		}
		return true
	})
}

func buildBreaker(cfg Config) circuitbreaker.CircuitBreaker[state.AttemptResult] {
	return circuitbreaker.NewBuilder[state.AttemptResult]().
		WithFailureThreshold(uint(cfg.BreakerThreshold)).
		WithDelay(time.Duration(cfg.BreakerCooldownMs) * time.Millisecond).
		HandleIf(func(r state.AttemptResult, _ error) bool {
			return Classify(r).CountToBreaker
		}).
		Build()
}
