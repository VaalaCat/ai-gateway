package cache

import (
	"sort"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

type scriptEntry struct {
	enabled  bool
	priority int
	compiled *script.Compiled
}

// scriptStore 持有已编译脚本，实现 script.ScriptProvider，并懒持有一个共享 Engine。
type scriptStore struct {
	mu     sync.RWMutex
	items  map[uint]scriptEntry
	logger *zap.Logger
	engine *script.Engine
}

func newScriptStore(logger *zap.Logger) *scriptStore {
	ss := &scriptStore{items: map[uint]scriptEntry{}, logger: logger}
	ss.engine = script.NewEngine(ss, logger, 0) // 0 => 默认 50ms
	return ss
}

// setLogger 把真实 logger 接到 scriptStore 与其共享 engine。
// 仅启动期调用（Store.SetLogger），无并发，直接替换 engine 安全。
func (ss *scriptStore) setLogger(l *zap.Logger) {
	ss.logger = l
	ss.engine = script.NewEngine(ss, l, 0)
}

func (ss *scriptStore) compileInto(dst map[uint]scriptEntry, s models.AdminScript) {
	compiled, err := script.Compile(s)
	if err != nil {
		if ss.logger != nil {
			ss.logger.Warn("script compile failed; excluded from execution",
				zap.String("name", s.Name), zap.Error(err))
		}
		return
	}
	dst[s.ID] = scriptEntry{enabled: s.Enabled, priority: s.Priority, compiled: compiled}
}

func (ss *scriptStore) set(s models.AdminScript) {
	tmp := map[uint]scriptEntry{}
	ss.compileInto(tmp, s)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if e, ok := tmp[s.ID]; ok {
		ss.items[s.ID] = e
	} else {
		delete(ss.items, s.ID) // 坏脚本：从执行列表移除
	}
}

func (ss *scriptStore) remove(id uint) {
	ss.mu.Lock()
	delete(ss.items, id)
	ss.mu.Unlock()
}

func (ss *scriptStore) count() int {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return len(ss.items)
}

// MatchScripts 实现 script.ScriptProvider：命中 enabled + scope 的脚本，按 Priority 升序。
func (ss *scriptStore) MatchScripts(channelID uint, model string) []*script.Compiled {
	ss.mu.RLock()
	entries := make([]scriptEntry, 0, len(ss.items))
	for _, e := range ss.items {
		if e.enabled && e.compiled != nil && script.MatchScope(e.compiled.Scope, channelID, model) {
			entries = append(entries, e)
		}
	}
	ss.mu.RUnlock()

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].priority != entries[j].priority {
			return entries[i].priority < entries[j].priority
		}
		return entries[i].compiled.ID < entries[j].compiled.ID
	})
	out := make([]*script.Compiled, len(entries))
	for i, e := range entries {
		out[i] = e.compiled
	}
	return out
}

// --- Store 门面 ---

// LoadScripts 增量入列（full sync，分页友好——逐条 set，不整体替换，
// 避免分页第 2 页清掉第 1 页）。坏脚本不入列、disabled 入列但 match 时被过滤。
func (s *Store) LoadScripts(list []models.AdminScript) {
	for _, sc := range list {
		s.scripts.set(sc)
	}
}

// MatchScripts 实现 script.ScriptProvider，转发给内部 scriptStore。
func (s *Store) MatchScripts(channelID uint, model string) []*script.Compiled {
	return s.scripts.MatchScripts(channelID, model)
}

// ScriptEngine 返回共享的脚本执行引擎。
func (s *Store) ScriptEngine() *script.Engine { return s.scripts.engine }

// ScriptCount 返回成功编译入列的脚本数。
func (s *Store) ScriptCount() int { return s.scripts.count() }
