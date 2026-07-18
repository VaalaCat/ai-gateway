// internal/agent/reporter/retry_queue.go
package reporter

import (
	"sort"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// retryIsolateAfterAttempts 是一条旁路条目自己失败到第几次,才不再和别的条目拼批,
// 改成单条 batch 发送——这就是"毒条目隔离"本身:一条屡次失败的坏条目,单独发
// 再也连累不到任何邻居;attempts 还小(可能只是恰好撞上一次网络抖动)的条目,
// 仍然可以和别人拼批发送,没必要一开始就把谁都当成毒条目单发。
const retryIsolateAfterAttempts = 2

// retryItem 是 retryQueue 里的一条记录:一个投递失败过的 UsageLogEntry,带着它
// 自己的失败次数和下次可重试时刻——每条独立计时,互不拖累(这正是旁路队列存在
// 的意义:主队列的一条毒条目挪到这里之后,不该再让任何别的条目跟着它的节奏走)。
type retryItem struct {
	entry    protocol.UsageLogEntry
	attempts int       // 已失败次数
	nextAt   time.Time // 下次可重试时刻
	bytes    int       // push 时算一次;degrade 后重算
	degrade  int       // 持久降级级别(DegradeNone/StripTrace/BillingOnly;L1 不落这里)
}

// retryQueue 是失败旁路队列:主队列(store)里投递失败的批次挪进这里,按自己的
// 退避节奏单独重试,不再阻塞后面排队的正常条目(毒条目隔离,见 uploader.go 的
// Run/processRetryQueue)。有界——溢出丢最老一条并 error 日志,镜像 store.go 的
// 溢出处理风格:这里同样是在丢用量数据,必须可见,不能悄悄没了。
type retryQueue struct {
	mu     sync.Mutex
	items  []retryItem
	limit  int
	logger *zap.Logger
}

func newRetryQueue(limit int, logger *zap.Logger) *retryQueue {
	return &retryQueue{limit: limit, logger: logger}
}

// push 把 entries 逐条追加成独立的 retryItem,共享同样的 attempts/nextAt——调用方
// 要么是"整批刚从主队列失败移过来"(attempts=1,nextAt 相同),要么是重试失败后
// 把单条条目重新排回去(单条调用即可)。溢出时按 store.go 的风格从最老开始丢,
// 并 error 日志留痕。
func (q *retryQueue) push(entries []protocol.UsageLogEntry, attempts int, nextAt time.Time) {
	if len(entries) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range entries {
		q.items = append(q.items, retryItem{entry: e, attempts: attempts, nextAt: nextAt, bytes: entrySize(e)})
	}
	if over := len(q.items) - q.limit; over > 0 {
		q.items = q.items[over:]
		// ④ 诊断打点:溢出即在丢数据,必须可见
		q.logger.Error("retry queue overflow, dropped oldest entries",
			zap.Int("dropped", over), zap.Int("pending_len", len(q.items)))
	}
}

// due 取出最多 max 个 nextAt<=now 的条目,并把它们从队列里移除——调用方要么送
// 成功(这些条目就此彻底消失),要么失败后自己重新 push 回来(带上新的
// attempts/nextAt)。
//
// 注意:不能只扫队列前缀就停手。不同条目的退避时长按各自 attempts 指数增长,
// 一条 attempts 很大、退避很长的老条目排在前面还没到期,不该挡住排在它后面、
// attempts 小、退避短、已经到期的新条目——所以这里要扫完整个队列,按"到期"与
// "未到期"分别收集,再把未到期的原样放回去(相对顺序不变)。
func (q *retryQueue) due(now time.Time, max int) []retryItem {
	if max <= 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	due := make([]retryItem, 0, max)
	kept := make([]retryItem, 0, len(q.items))
	for _, it := range q.items {
		if len(due) < max && !it.nextAt.After(now) {
			due = append(due, it)
		} else {
			kept = append(kept, it)
		}
	}
	q.items = kept
	if len(due) == 0 {
		return nil
	}
	return due
}

// drainAll 无视 nextAt,取走并清空全部条目——只在进程关闭时的"能捞多少是多少"
// 场景下使用(见 uploader.go 的 drainOnShutdown),关机窗口有限,等不起正常的
// 退避节奏。
func (q *retryQueue) drainAll() []retryItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.items
	q.items = nil
	return out
}

func (q *retryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// pushItem 把一条已经带 attempts/bytes/degrade 状态的条目放回队列——用于"取出来
// 试了一下,又原样放回去"的场景(比如管理端 retryNow 点名后、或单条重试失败后
// 重新入队),不能像 push() 那样把 bytes/degrade 清零重来。溢出处理镜像 push()。
func (q *retryQueue) pushItem(it retryItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, it)
	if over := len(q.items) - q.limit; over > 0 {
		q.items = q.items[over:]
		q.logger.Error("retry queue overflow, dropped oldest entries",
			zap.Int("dropped", over), zap.Int("pending_len", len(q.items)))
	}
}

func (q *retryQueue) totalBytes() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for _, it := range q.items {
		total += it.bytes
	}
	return total
}

// oldestTimestamp 返回全队最小的 entry.Timestamp;队列为空返回 0。
func (q *retryQueue) oldestTimestamp() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	var oldest int64
	for _, it := range q.items {
		ts := it.entry.Timestamp
		if ts != 0 && (oldest == 0 || ts < oldest) {
			oldest = ts
		}
	}
	return oldest
}

// snapshotTop 拷贝队列并按 bytes 降序取前 n 条,供管理看板展示"占用最大的条目"
// ——拷贝之后排序,绝不改动 due()/push() 依赖的原队列顺序。
func (q *retryQueue) snapshotTop(n int) []retryItem {
	q.mu.Lock()
	out := make([]retryItem, len(q.items))
	copy(out, q.items)
	q.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].bytes > out[j].bytes })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// snapshotAll 持锁全量拷贝队列,不排序——供 Task 8 磁盘快照使用(落盘顺序无所谓,
// 恢复时都会重新 pushItem 回去);对比 snapshotTop,后者是给管理看板挑"最大的
// 几条"用的,按 bytes 排序且只取前 n 条。
func (q *retryQueue) snapshotAll() []retryItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]retryItem, len(q.items))
	copy(out, q.items)
	return out
}

// matchIDs 把 ids 变成命中判断函数;nil 表示"全部命中"(retryNow(nil) 之类的
// 批量操作入口)。
func matchIDs(ids []string) func(string) bool {
	if ids == nil {
		return func(string) bool { return true }
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return func(id string) bool { _, ok := set[id]; return ok }
}

// retryNow 清零命中条目的退避,让下一次 due() 扫描立刻捞到它们——管理端"立即重试"
// 操作。返回命中数。
func (q *retryQueue) retryNow(ids []string) int {
	match := matchIDs(ids)
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for i := range q.items {
		if match(q.items[i].entry.RequestID) {
			q.items[i].nextAt = time.Time{}
			n++
		}
	}
	return n
}

// remove 从队列里直接删除命中条目(管理端人工丢弃)。返回命中数;丢弃日志由
// 调用方(RPC 层)打,队列层保持纯数据结构。
func (q *retryQueue) remove(ids []string) int {
	match := matchIDs(ids)
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.items[:0]
	n := 0
	for _, it := range q.items {
		if match(it.entry.RequestID) {
			n++
			continue
		}
		kept = append(kept, it)
	}
	q.items = kept
	return n
}

// degrade 就地把命中条目推到 level(只升不降),重算 bytes。返回实际升级的条数——
// 已经达到或超过 level 的条目不算命中,让调用方(管理端操作回执)能区分 no-op。
func (q *retryQueue) degrade(ids []string, level int) int {
	match := matchIDs(ids)
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for i := range q.items {
		it := &q.items[i]
		if !match(it.entry.RequestID) || it.degrade >= level {
			continue
		}
		applyDegrade(&it.entry, level)
		it.degrade = level
		it.bytes = entrySize(it.entry)
		n++
	}
	return n
}
