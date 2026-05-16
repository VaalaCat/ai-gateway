package cache

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// tokenStore 把 token 的双键索引（按 API key 和按 id）下沉到 cache 包内的派生层，
// 使 entitycache.EntityCache 抽象保持纯净（仅单键 K→V）。
//
// 主存储 primary 由 EntityCache 实现（Full 或 LRU）持有，
// byID 是派生反向索引（id → key），由 LRU EvictCallback 在 Task 16 装配时维护
// （FullCache 时由 Set/Delete 直接维护）。
type tokenStore struct {
	primary entitycache.EntityCache[string, *models.Token]
	byID    utils.SyncMap[uint, string]
	fetcher TokenIDFetcher
}

// TokenIDFetcher 负责按 id 从 master 拉 token——
// 当 byID 命中但 primary 已被 LRU 淘汰时使用。
// 接入将在 Task 16 / Task 18 完成；当前可传 nil。
type TokenIDFetcher interface {
	LoadByID(ctx context.Context, id uint) (*models.Token, error)
}

// newTokenStore 构造一个 tokenStore。fetcher 可为 nil。
func newTokenStore(primary entitycache.EntityCache[string, *models.Token], fetcher TokenIDFetcher) *tokenStore {
	return &tokenStore{primary: primary, fetcher: fetcher}
}

// Set 写入主存储 + byID 反向索引。
func (s *tokenStore) Set(t *models.Token) {
	if t == nil {
		return
	}
	s.primary.Set(t.Key, t)
	s.byID.Store(t.ID, t.Key)
}

// Delete 删除主存储 + byID。
func (s *tokenStore) Delete(key string) {
	if old, ok := s.primary.Peek(key); ok && old != nil {
		s.byID.Delete(old.ID)
	}
	s.primary.Delete(key)
}

// DeleteByID 通过 id 反查 key 后调用 Delete。
func (s *tokenStore) DeleteByID(id uint) {
	if key, ok := s.byID.Load(id); ok {
		s.primary.Delete(key)
		s.byID.Delete(id)
	}
}

// Get 按 key 取 token。
func (s *tokenStore) Get(ctx context.Context, key string) (*models.Token, bool, error) {
	return s.primary.Get(ctx, key)
}

// GetByID 按 id 取 token：先查 byID 拿 key，再查主存储。
// 若 byID 命中但 primary 已淘汰，且配置了 fetcher，则按 id 直接 RPC 拉取并修正双键。
func (s *tokenStore) GetByID(ctx context.Context, id uint) (*models.Token, bool, error) {
	key, ok := s.byID.Load(id)
	if !ok {
		return s.fallbackByID(ctx, id)
	}
	tok, hit, err := s.primary.Get(ctx, key)
	if hit {
		return tok, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	// byID 指向已被淘汰的 primary 条目：清掉 byID，回退到 fetcher
	s.byID.Delete(id)
	return s.fallbackByID(ctx, id)
}

// fallbackByID 在没有 fetcher 时返回 (nil, false, nil)。
// 有 fetcher 时调用 RPC 并把结果写回双键。
func (s *tokenStore) fallbackByID(ctx context.Context, id uint) (*models.Token, bool, error) {
	if s.fetcher == nil {
		return nil, false, nil
	}
	tok, err := s.fetcher.LoadByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if tok == nil {
		return nil, false, nil
	}
	s.Set(tok)
	return tok, true, nil
}

// Apply 处理 push 事件。
// LRU 模式下 Apply(Set) 走 primary.Apply（apply-if-present——只在 key 已缓存时写入）；
// Delete 同时清理双键（byID 反向索引由本函数显式抹；LRU OnEvict 是另一条容量驱逐路径，亦会同步 byID）。
func (s *tokenStore) Apply(action entitycache.Action, t *models.Token) {
	if t == nil {
		return
	}
	switch action {
	case entitycache.ActionSet:
		s.primary.Apply(entitycache.ActionSet, t.Key, t)
	case entitycache.ActionDelete:
		s.primary.Apply(entitycache.ActionDelete, t.Key, nil)
		s.byID.Delete(t.ID)
	}
}

// PrimaryStats 暴露给 Store 用于统计上报。
func (s *tokenStore) PrimaryStats() entitycache.Stats {
	return s.primary.Stats()
}

// Len 返回主存储 token 数量。
func (s *tokenStore) Len() int {
	return s.primary.Len()
}
