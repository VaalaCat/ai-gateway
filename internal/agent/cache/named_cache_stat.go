package cache

import "github.com/VaalaCat/ai-gateway/internal/pkg/protocol"

// NamedCacheStat 是任意"带名字、能自报统计"的缓存/索引。
// 实现它即可被 Store.CacheSnapshot() 自动纳入心跳上报——无需再在多处手抄清单。
// 新增缓存务必实现此接口（或由 Store 的反射完整性测试 TestCacheSnapshot_Complete 拦截遗漏）。
type NamedCacheStat interface {
	CacheName() string // 监控页唯一键，如 "route_index" / "limiter_index"
	CacheStat() protocol.CacheEntityStats
}
