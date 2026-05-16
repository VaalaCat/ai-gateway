package entitycache

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// FullCache 永远写入，等价于现有 SyncMap 行为。无 LRU 淘汰、无 Loader。
type FullCache[K comparable, V any] struct {
	m utils.SyncMap[K, V]
}

// NewFullCache 创建一个空的 FullCache。
func NewFullCache[K comparable, V any]() *FullCache[K, V] {
	return &FullCache[K, V]{}
}

// Get 仅查本地。FullCache 不接 Loader，未命中返回 (zero, false, nil)。
func (c *FullCache[K, V]) Get(_ context.Context, key K) (V, bool, error) {
	v, ok := c.m.Load(key)
	return v, ok, nil
}

// Peek 同 Get，无副作用语义。
func (c *FullCache[K, V]) Peek(key K) (V, bool) {
	return c.m.Load(key)
}

// Apply 永远写入或删除（FullCache 不区分 push 与 set 语义）。
func (c *FullCache[K, V]) Apply(action Action, key K, value V) {
	switch action {
	case ActionSet:
		c.m.Store(key, value)
	case ActionDelete:
		c.m.Delete(key)
	}
}

// Set 直接写入。
func (c *FullCache[K, V]) Set(key K, value V) {
	c.m.Store(key, value)
}

// Delete 直接删除。
func (c *FullCache[K, V]) Delete(key K) {
	c.m.Delete(key)
}

// Len 返回条目数。
func (c *FullCache[K, V]) Len() int {
	return c.m.Len()
}

// Range 遍历所有条目。
func (c *FullCache[K, V]) Range(fn func(K, V) bool) {
	c.m.Range(fn)
}

// Stats 仅维护 Size。
func (c *FullCache[K, V]) Stats() Stats {
	return Stats{Size: c.m.Len()}
}

// 编译期断言：FullCache 实现 EntityCache 接口。
var _ EntityCache[string, int] = (*FullCache[string, int])(nil)
