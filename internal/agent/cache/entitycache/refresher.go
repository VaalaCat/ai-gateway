package entitycache

import (
	"context"
	"errors"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"

	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// Refresher 在后台韧性地把 key 拉回来,不持有任何缓存引用——只通过 handler 回调结果。
// 同 key 同时只有一次刷新在跑(去重)。失败带 failsafe 退避重试;ErrNotFound 不重试。
type Refresher[K comparable, V any] struct {
	load     Loader[K, V]
	cfg      func() RefreshConfig
	inflight utils.SyncMap[K, struct{}]
}

// NewRefresher 构造。load 与 cfg 必须非 nil。
func NewRefresher[K comparable, V any](load Loader[K, V], cfg func() RefreshConfig) *Refresher[K, V] {
	return &Refresher[K, V]{load: load, cfg: cfg}
}

// TriggerRefresh 异步触发一次刷新;同 key 已在刷新则直接返回(去重),不重复打远端。
// 完成后回调 (outcome, value):RefreshOK 时 value 为新值,否则为零值。
// in-flight 标记一直持有到 handler 回调返回:同 key 的并发触发在整个
// load + handler 期间被丢弃(去重窗口覆盖全程,设计如此)。
func (r *Refresher[K, V]) TriggerRefresh(key K, handler func(RefreshOutcome, V)) {
	if _, busy := r.inflight.LoadOrStore(key, struct{}{}); busy {
		return
	}
	go func() {
		defer r.inflight.Delete(key)
		defer func() {
			// 后台尽力刷新:handler/loader 万一 panic 不应拖垮进程;
			// 本轮放弃,下次访问陈旧条目时会再次触发。
			_ = recover()
		}()
		o, v := r.runOnce(key)
		handler(o, v)
	}()
}

// runOnce 同步执行一次带重试的刷新,返回三态结果。
func (r *Refresher[K, V]) runOnce(key K) (RefreshOutcome, V) {
	cfg := r.cfg()
	b := retrypolicy.NewBuilder[V]().
		WithMaxRetries(cfg.RefreshMaxRetries).
		AbortIf(func(_ V, err error) bool { return errors.Is(err, ErrNotFound) }). // 定论不重试
		ReturnLastFailure()
	if cfg.RefreshBackoffBase > 0 && cfg.RefreshBackoffMax > 0 {
		b = b.WithBackoff(cfg.RefreshBackoffBase, cfg.RefreshBackoffMax)
	}
	retry := b.Build()

	v, err := failsafe.With[V](retry).Get(func() (V, error) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.RefreshTimeout)
		defer cancel()
		return r.load.Load(ctx, key)
	})

	switch {
	case err == nil:
		return RefreshOK, v
	case errors.Is(err, ErrNotFound):
		var zero V
		return RefreshGone, zero
	default:
		var zero V
		return RefreshUnavailable, zero
	}
}
