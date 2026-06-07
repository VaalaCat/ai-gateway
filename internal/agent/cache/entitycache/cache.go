// Package entitycache 提供通用实体缓存抽象。
//
// EntityCache[K, V] 是 K→V 的缓存接口，由两种实现提供服务：
//   - FullCache：永远写入，等价于现有 SyncMap 行为（用于 admin 维护的小规模实体）
//   - LRUCache：基于 hashicorp/golang-lru，按容量淘汰，支持 miss 时 RPC 拉取、
//     负缓存与 apply-if-present 推送语义（用于高基数实体如 token / user）
//
// 抽象层不知道任何业务概念。Token 双键索引、关联实体 warm 等组合
// 行为由 cache.Store 包内的派生层负责。
package entitycache

import (
	"context"
	"errors"
	"time"
)

// Action 描述一次数据变更事件的语义。
type Action int

const (
	// ActionSet 表示写入或更新一条记录。
	ActionSet Action = iota
	// ActionDelete 表示删除一条记录。
	ActionDelete
)

// ErrNotFound 表示 Loader 已确认 key 在源端不存在。
// LRUCache 据此触发负缓存。
var ErrNotFound = errors.New("entity not found")

// RefreshOutcome 是一次后台刷新的三态结果。
type RefreshOutcome int

const (
	// RefreshOK 成功拿到新值。
	RefreshOK RefreshOutcome = iota
	// RefreshGone 源端明确返回 ErrNotFound(可达且已删/吊销)→ 应逐出。
	RefreshGone
	// RefreshUnavailable 连接/超时错误(源端不可达)→ 应保留旧值。
	RefreshUnavailable
)

// RefreshConfig 是缓存韧性参数快照,由调用方通过 Config.Refresh 动态提供
// (通常映射自管理后台 Settings,支持运行时改值即时生效)。
type RefreshConfig struct {
	LoadTimeout        time.Duration // 冷 miss 阻塞加载超时(detached)
	RefreshAfter       time.Duration // 条目多旧触发后台刷新;<=0 禁用 soft-TTL
	RefreshTimeout     time.Duration // 单次后台刷新尝试超时
	RefreshMaxRetries  int           // 单次触发内 failsafe 重试次数
	RefreshBackoffBase time.Duration
	RefreshBackoffMax  time.Duration
}

// Stats 是缓存的运行计数。LRUCache 按调用累加；FullCache 仅维护 Size。
type Stats struct {
	Hits          int64
	Misses        int64
	Evictions     int64
	NegativeHits  int64
	LoadErrors    int64
	Invalidations int64
	Size          int
	Capacity      int
}

// Loader 封装一次 miss 时的远端拉取。
// 返回 ErrNotFound 时缓存写入负条目；其他错误透传不缓存。
type Loader[K comparable, V any] interface {
	Load(ctx context.Context, key K) (V, error)
}

// LoaderFunc 适配普通函数到 Loader 接口。
type LoaderFunc[K comparable, V any] func(context.Context, K) (V, error)

// Load 实现 Loader 接口。
func (f LoaderFunc[K, V]) Load(ctx context.Context, k K) (V, error) {
	return f(ctx, k)
}

// EntityCache 是通用实体缓存接口。
type EntityCache[K comparable, V any] interface {
	// Get 命中返回 (v, true, nil)；未命中且未配置 Loader 返回 (zero, false, nil)；
	// 已配置 Loader 时同步触发拉取。Loader 返回 ErrNotFound 走负缓存
	// 后续返回 (zero, false, ErrNotFound)；其他错误透传。
	Get(ctx context.Context, key K) (V, bool, error)

	// Peek 仅查本地，不触发 Loader。
	Peek(key K) (V, bool)

	// Apply 处理 push 事件。
	// FullCache 永远写入或删除；LRUCache 仅当 key 已在缓存时操作。
	Apply(action Action, key K, value V)

	// Set 直接写入，不经 apply-if-present 检查（用于 FullSync 等主动加载场景）。
	Set(key K, value V)

	// Delete 直接删除。
	Delete(key K)

	// Len 返回当前条目数。
	Len() int

	// Range 遍历当前所有条目。回调返回 false 终止遍历。
	Range(func(K, V) bool)

	// Stats 返回运行计数快照。
	Stats() Stats
}
