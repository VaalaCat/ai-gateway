// Package affinity 实现 agent 本地的渠道缓存粘性：把 (用户, 真实模型) 上次发生
// 缓存活动的上游 channel 记下来，供 plan 阶段优先选用。叶子包，只依赖
// state（取 ChannelSource）+ settings + utils，不反向 import plan/publish/exec。
package affinity

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// Key 粘性映射键：用户 + 真实模型名。
type Key struct {
	UserID    uint
	RealModel string
}

// Entry 粘性记录值。带 Source 是因为 private/admin 的 ID 是两套空间，
// 重排时用 (Source, SourceID) 精确匹配候选，避免串号。
type Entry struct {
	Source    state.ChannelSource
	SourceID  uint
	ExpiresAt time.Time
}

// Store 是粘性表抽象。
type Store interface {
	Lookup(k Key) (Entry, bool) // 纯读：命中且未过期返回；过期当 miss 并惰性删除
	Remember(k Key, e Entry)    // 写/续期
	Forget(k Key)               // 删除
}

// maxEntries 是容量上限的软守卫（SOFT cap）；超过即在 Remember 时扫除过期项。
// 注意：当所有条目都未过期时，sweepExpired 不释放任何项，map 可超过 maxEntries
// （可接受——key 粒度细(用户×模型) + TTL 短，自然换手即有界，这里只防极端泄漏）。
const maxEntries = 50000

type ttlStore struct {
	m utils.SyncMap[Key, Entry]
}

func newTTLStore() *ttlStore { return &ttlStore{} }

func (s *ttlStore) Lookup(k Key) (Entry, bool) {
	e, ok := s.m.Load(k)
	if !ok {
		return Entry{}, false
	}
	if !time.Now().Before(e.ExpiresAt) {
		// Load-then-Delete 是已知的良性竞态：并发 Remember 时最坏情况是误删一条新鲜
		// 条目，仅导致一次自愈式 cache miss；不值得为此引入 utils.SyncMap 没有的 CAS 原语。
		s.m.Delete(k)
		return Entry{}, false
	}
	return e, true
}

func (s *ttlStore) Remember(k Key, e Entry) {
	if s.m.Len() >= maxEntries {
		s.sweepExpired()
	}
	s.m.Store(k, e)
}

func (s *ttlStore) Forget(k Key) { s.m.Delete(k) }

func (s *ttlStore) sweepExpired() {
	now := time.Now()
	s.m.Range(func(k Key, _ Entry) bool {
		// Range 在调用回调时已解锁，期间可能有并发 Remember 续期同一 key；
		// 重新 Load 最新值，仅当它仍过期才删除，避免误删新鲜条目。
		if cur, ok := s.m.Load(k); ok && !now.Before(cur.ExpiresAt) {
			s.m.Delete(k)
		}
		return true
	})
}
