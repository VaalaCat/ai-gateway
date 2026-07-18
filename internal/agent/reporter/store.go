// internal/agent/reporter/store.go
package reporter

import (
	"encoding/json"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// PendingUsageStore 存放尚未确认送达 master 的用量条目。
// 默认内存实现;接口预留磁盘实现位(agent 崩溃零丢失时启用,见 spec §4.2)。
// PeekBatch/Ack 由单一 Uploader 协程消费;Append 可并发。
type PendingUsageStore interface {
	Append(entries []protocol.UsageLogEntry)
	PeekBatch(max int) []protocol.UsageLogEntry
	Ack(entries []protocol.UsageLogEntry)
	Len() int
	Bytes() int
	OldestTimestamp() int64
}

// storedEntry 缓存 entry 的 marshal 体积,Append 算一次,Bytes()/快照/看板复用,
// 不必反复 marshal 整条队列。
type storedEntry struct {
	entry protocol.UsageLogEntry
	bytes int
}

type MemPendingUsageStore struct {
	mu     sync.Mutex
	items  []storedEntry
	bytes  int
	limit  int
	logger *zap.Logger
}

var _ PendingUsageStore = (*MemPendingUsageStore)(nil)

func NewMemPendingUsageStore(limit int, logger *zap.Logger) *MemPendingUsageStore {
	return &MemPendingUsageStore{limit: limit, logger: logger}
}

func entrySize(e protocol.UsageLogEntry) int {
	b, err := json.Marshal(e)
	if err != nil {
		return 0
	}
	return len(b)
}

func (s *MemPendingUsageStore) Append(entries []protocol.UsageLogEntry) {
	if len(entries) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		sz := entrySize(e)
		s.items = append(s.items, storedEntry{entry: e, bytes: sz})
		s.bytes += sz
	}
	if over := len(s.items) - s.limit; over > 0 {
		for _, it := range s.items[:over] {
			s.bytes -= it.bytes
		}
		s.items = s.items[over:]
		// ④ 诊断打点:溢出即在丢数据,必须可见
		s.logger.Error("pending usage store overflow, dropped oldest entries",
			zap.Int("dropped", over), zap.Int("pending_len", len(s.items)))
	}
}

func (s *MemPendingUsageStore) PeekBatch(max int) []protocol.UsageLogEntry {
	if max <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.items)
	if n == 0 {
		return nil
	}
	if n > max {
		n = max
	}
	out := make([]protocol.UsageLogEntry, n)
	for i := 0; i < n; i++ {
		out[i] = s.items[i].entry
	}
	return out
}

// Ack 按 RequestID 集合从任意位置删除。并发上传后成功回执可能乱序到达,
// 不再依赖"被确认批次一定是当前队首前缀"的单消费者 FIFO 假设;
// 找不到的 id(已被溢出丢弃并计过数)静默跳过。
func (s *MemPendingUsageStore) Ack(entries []protocol.UsageLogEntry) {
	if len(entries) == 0 {
		return
	}
	ids := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		ids[e.RequestID] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.items[:0]
	for _, it := range s.items {
		if _, ok := ids[it.entry.RequestID]; ok {
			s.bytes -= it.bytes
			continue
		}
		kept = append(kept, it)
	}
	s.items = kept
}

func (s *MemPendingUsageStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *MemPendingUsageStore) Bytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

func (s *MemPendingUsageStore) OldestTimestamp() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return 0
	}
	return s.items[0].entry.Timestamp
}
