package oauth

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

type StateEntry struct {
	ProviderID uint
	Kind       string // "login" | "link"
	UserID     uint   // link 模式的发起者，login 模式为 0
	ReturnTo   string
	ExpiresAt  int64
}

type StateStore struct {
	m utils.SyncMap[string, *StateEntry]
}

func NewStateStore() *StateStore {
	return &StateStore{}
}

func (s *StateStore) Put(state string, e *StateEntry) {
	s.m.Store(state, e)
}

// Take 返回并删除 state 对应 entry；过期视为 miss。
func (s *StateStore) Take(state string) (*StateEntry, bool) {
	e, ok := s.m.LoadAndDelete(state)
	if !ok {
		return nil, false
	}
	if time.Now().Unix() > e.ExpiresAt {
		return nil, false
	}
	return e, true
}

// Sweep 删除所有过期 entry，避免无限增长。建议 1min ticker 调用。
func (s *StateStore) Sweep() {
	now := time.Now().Unix()
	s.m.Range(func(k string, e *StateEntry) bool {
		if now > e.ExpiresAt {
			s.m.Delete(k)
		}
		return true
	})
}
