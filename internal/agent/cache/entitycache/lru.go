package entitycache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Config 是 LRUCache 的构造参数。
type Config[K comparable, V any] struct {
	// Capacity 是最大条目数（必须 > 0）。
	Capacity int

	// MaxConcurrentLoads 限制同时冷加载的不同 key 数。0 使用有界默认值；负值非法。
	MaxConcurrentLoads int

	// Loader 在 Get miss 时被调用。可为 nil（仅本地缓存）。
	Loader Loader[K, V]

	// NegativeTTL 是负缓存（"key 不存在"）的过期时间。0 表示禁用负缓存。
	NegativeTTL time.Duration

	// Now 用于测试控制时间。零值时使用 time.Now。
	Now func() time.Time

	// OnEvict 在 LRU 因容量淘汰一条**正常**条目时被调用（负缓存条目不触发）。
	// 用于派生反向索引同步清理（如 tokenStore.byID）。可为 nil。
	OnEvict func(key K, value V)

	// Refresh 提供缓存韧性参数(动态读取,支持运行时改值)。
	// 非 nil 且 Loader 非 nil 时启用 stale-while-revalidate + detached 冷 miss。
	// nil 时退化为原行为(冷 miss 沿用调用方 ctx,不做后台刷新)。
	Refresh func() RefreshConfig

	Lifecycle *Lifecycle
}

// entry 是 LRU 内部存储单元，区分正常值和负缓存条目。
type entry[V any] struct {
	value    V
	missing  bool  // true 表示负缓存条目
	expireAt int64 // 仅在 missing=true 时有效（unix nano）
	loadedAt int64 // unix nano:最近一次成功写入(load/Set/Apply)时间
}

type loadOutcome[V any] struct {
	value V
	found bool
	err   error
}

type loadFlight[V any] struct {
	done    chan struct{}
	outcome loadOutcome[V]
	closed  bool
}

// LRUCache 基于 hashicorp/golang-lru，带 Loader、负缓存、metrics。
type LRUCache[K comparable, V any] struct {
	cap         int
	cache       *lru.Cache[K, entry[V]]
	loader      Loader[K, V]
	negativeTTL time.Duration
	now         func() time.Time

	hits          atomic.Int64
	misses        atomic.Int64
	evictions     atomic.Int64
	negativeHits  atomic.Int64
	loadErrors    atomic.Int64
	invalidations atomic.Int64

	loadMu      sync.Mutex
	loadFlights map[K]*loadFlight[V]
	loadLimit   int

	refresher  *Refresher[K, V]
	refreshCfg func() RefreshConfig
	lifecycle  *Lifecycle
}

// NewLRUCache 构造 LRUCache。Capacity <= 0 返回错误（fail-fast）。
func NewLRUCache[K comparable, V any](cfg Config[K, V]) (*LRUCache[K, V], error) {
	if cfg.Capacity <= 0 {
		return nil, errors.New("entitycache: Config.Capacity must be > 0")
	}
	if cfg.MaxConcurrentLoads < 0 {
		return nil, errors.New("entitycache: Config.MaxConcurrentLoads must be >= 0")
	}
	loadLimit := cfg.MaxConcurrentLoads
	if loadLimit == 0 || loadLimit > 64 {
		loadLimit = 64
	}
	if loadLimit > cfg.Capacity {
		loadLimit = cfg.Capacity
	}
	c := &LRUCache[K, V]{
		cap:         cfg.Capacity,
		loader:      cfg.Loader,
		negativeTTL: cfg.NegativeTTL,
		now:         cfg.Now,
		lifecycle:   cfg.Lifecycle,
		loadFlights: make(map[K]*loadFlight[V]),
		loadLimit:   loadLimit,
	}
	if c.lifecycle == nil {
		c.lifecycle = NewLifecycle()
	}
	if c.now == nil {
		c.now = time.Now
	}
	cache, err := lru.NewWithEvict[K, entry[V]](cfg.Capacity, func(k K, e entry[V]) {
		if !e.missing && cfg.OnEvict != nil {
			cfg.OnEvict(k, e.value)
		}
	})
	if err != nil {
		return nil, err
	}
	c.cache = cache
	if cfg.Refresh != nil {
		c.refreshCfg = cfg.Refresh
		if cfg.Loader != nil {
			c.refresher = NewRefresherWithLifecycle[K, V](cfg.Loader, cfg.Refresh, c.lifecycle)
		}
	}
	return c, nil
}

// Peek 仅查本地。负缓存条目对调用方表现为 miss。
func (c *LRUCache[K, V]) Peek(key K) (V, bool) {
	e, ok := c.cache.Peek(key)
	if !ok || e.missing {
		var zero V
		return zero, false
	}
	return e.value, true
}

// Get 命中返回 (v, true, nil)。
// miss 且配置了 Loader 时通过 singleflight 单飞调用，结果写入缓存。
// Loader 返回 ErrNotFound 时进入负缓存（如启用），返回 (zero, false, ErrNotFound)。
// Loader 返回其他错误透传不缓存。
func (c *LRUCache[K, V]) Get(ctx context.Context, key K) (V, bool, error) {
	if e, ok := c.cache.Get(key); ok {
		if e.missing {
			if c.now().UnixNano() >= e.expireAt {
				// 负缓存过期，移除并视为未命中，走 loader 重拉
				c.cache.Remove(key)
			} else {
				c.negativeHits.Add(1)
				var zero V
				return zero, false, ErrNotFound
			}
		} else {
			c.hits.Add(1)
			c.maybeRefresh(key, e.loadedAt)
			return e.value, true, nil
		}
	}
	if c.loader == nil {
		c.misses.Add(1)
		var zero V
		return zero, false, nil
	}
	c.misses.Add(1)
	return c.loadAndStore(ctx, key)
}

// loadAndStore lets one caller own the key load while followers wait without
// registering lifecycle workers or spawning internal goroutines.
func (c *LRUCache[K, V]) loadAndStore(ctx context.Context, key K) (V, bool, error) {
	if cause := context.Cause(ctx); cause != nil {
		var zero V
		return zero, false, cause
	}
	flight, leader, err := c.beginLoad(key)
	if err != nil {
		var zero V
		return zero, false, err
	}
	if leader {
		if c.refreshCfg == nil {
			c.finishLoad(key, flight, c.runLoad(ctx, key))
		} else if !c.lifecycle.GoLoad(func(parent context.Context) {
			c.finishLoad(key, flight, c.runLoad(parent, key))
		}) {
			c.finishLoad(key, flight, loadOutcome[V]{err: context.Canceled})
		}
	}
	select {
	case <-ctx.Done():
		var zero V
		return zero, false, context.Cause(ctx)
	case <-flight.done:
		return flight.outcome.value, flight.outcome.found, flight.outcome.err
	}
}

func (c *LRUCache[K, V]) beginLoad(key K) (*loadFlight[V], bool, error) {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	if flight := c.loadFlights[key]; flight != nil {
		return flight, false, nil
	}
	if cached, ok := c.cache.Peek(key); ok {
		if !cached.missing {
			return completedLoad(loadOutcome[V]{value: cached.value, found: true}), false, nil
		}
		if c.now().UnixNano() < cached.expireAt {
			return completedLoad(loadOutcome[V]{err: ErrNotFound}), false, nil
		}
		c.cache.Remove(key)
	}
	if len(c.loadFlights) >= c.loadLimit {
		return nil, false, ErrLoadLimitReached
	}
	flight := &loadFlight[V]{done: make(chan struct{})}
	c.loadFlights[key] = flight
	return flight, true, nil
}

func completedLoad[V any](outcome loadOutcome[V]) *loadFlight[V] {
	flight := &loadFlight[V]{done: make(chan struct{}), outcome: outcome}
	close(flight.done)
	return flight
}

func (c *LRUCache[K, V]) finishLoad(key K, flight *loadFlight[V], outcome loadOutcome[V]) {
	c.loadMu.Lock()
	if c.loadFlights[key] != flight || flight.closed {
		c.loadMu.Unlock()
		return
	}
	flight.outcome = outcome
	flight.closed = true
	c.loadMu.Unlock()

	close(flight.done)

	c.loadMu.Lock()
	if c.loadFlights[key] == flight {
		delete(c.loadFlights, key)
	}
	c.loadMu.Unlock()
}

func (c *LRUCache[K, V]) runLoad(parent context.Context, key K) loadOutcome[V] {
	lctx, cancel := c.loadContext(parent)
	defer cancel()
	v, err := c.loader.Load(lctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.storeNegative(key)
			return loadOutcome[V]{err: ErrNotFound}
		}
		c.loadErrors.Add(1)
		return loadOutcome[V]{err: err}
	}
	c.store(key, v)
	return loadOutcome[V]{value: v, found: true}
}

// maybeRefresh 当条目超过 RefreshAfter 且已配 refresher 时,触发一次后台刷新(serve-stale)。
func (c *LRUCache[K, V]) maybeRefresh(key K, loadedAt int64) {
	if c.refresher == nil || c.refreshCfg == nil {
		return
	}
	after := c.refreshCfg().RefreshAfter
	if after <= 0 {
		return
	}
	if c.now().UnixNano()-loadedAt > int64(after) {
		c.refresher.TriggerRefresh(key, c.onRefresh(key))
	}
}

// onRefresh 把 Refresher 的三态结果落到缓存:OK 写回(apply-if-present)、
// Gone 逐出(revocation)、Unavailable 保留旧值(绝不逐出)。
func (c *LRUCache[K, V]) onRefresh(key K) func(RefreshOutcome, V) {
	return func(o RefreshOutcome, v V) {
		switch o {
		case RefreshOK:
			c.Apply(ActionSet, key, v) // 仅更新仍在的条目,不复活已删的
		case RefreshGone:
			c.Delete(key)
		case RefreshUnavailable:
			// 保留旧值
		}
	}
}

// loadContext 决定冷 miss 加载的 ctx:
// 未配 Refresh → 旧行为(沿用调用方 ctx);配了 → detached + 有界(根治 leader-cancel 连坐)。
func (c *LRUCache[K, V]) loadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.refreshCfg == nil {
		return context.WithCancel(ctx)
	}
	if d := c.refreshCfg().LoadTimeout; d > 0 {
		return context.WithTimeout(ctx, d)
	}
	return context.WithCancel(ctx)
}

// store 写入正向条目并盖 loadedAt 时间戳;LRU 淘汰时计数。
func (c *LRUCache[K, V]) store(key K, value V) {
	if c.cache.Add(key, entry[V]{value: value, loadedAt: c.now().UnixNano()}) {
		c.evictions.Add(1)
	}
}

// storeNegative 在启用负缓存时写入"key 不存在"的占位条目。
func (c *LRUCache[K, V]) storeNegative(key K) {
	if c.negativeTTL <= 0 {
		return
	}
	if c.cache.Add(key, entry[V]{
		missing:  true,
		expireAt: c.now().Add(c.negativeTTL).UnixNano(),
	}) {
		c.evictions.Add(1)
	}
}

// Apply：apply-if-present。LRU 仅当 key 存在时写入或删除。
//
// 已知非原子窗口:ActionSet 的 Contains→store(Add) 之间若有并发 Delete,
// 这次 re-add 会复活已删条目(resurrection)。这是被接受的——写入幂等,SWR
// 刷新本就罕见,且 hashicorp/lru 不提供原子的 update-if-present。不加锁。
func (c *LRUCache[K, V]) Apply(action Action, key K, value V) {
	switch action {
	case ActionSet:
		if c.cache.Contains(key) {
			c.store(key, value)
		}
	case ActionDelete:
		c.invalidations.Add(1)
		c.cache.Remove(key)
	}
}

// Set 直接写入（覆盖负缓存）。
func (c *LRUCache[K, V]) Set(key K, value V) {
	c.store(key, value)
}

// Delete 删除。
func (c *LRUCache[K, V]) Delete(key K) {
	c.invalidations.Add(1)
	c.cache.Remove(key)
}

// Len 返回当前条目数（包含负缓存）。
func (c *LRUCache[K, V]) Len() int {
	return c.cache.Len()
}

// Range 遍历所有非负缓存条目。
func (c *LRUCache[K, V]) Range(fn func(K, V) bool) {
	for _, k := range c.cache.Keys() {
		e, ok := c.cache.Peek(k)
		if !ok || e.missing {
			continue
		}
		if !fn(k, e.value) {
			return
		}
	}
}

// Stats 返回快照。
func (c *LRUCache[K, V]) Stats() Stats {
	return Stats{
		Hits:          c.hits.Load(),
		Misses:        c.misses.Load(),
		Evictions:     c.evictions.Load(),
		NegativeHits:  c.negativeHits.Load(),
		LoadErrors:    c.loadErrors.Load(),
		Invalidations: c.invalidations.Load(),
		Size:          c.cache.Len(),
		Capacity:      c.cap,
	}
}

// 编译期断言：LRUCache 实现 EntityCache。
var _ EntityCache[string, int] = (*LRUCache[string, int])(nil)
