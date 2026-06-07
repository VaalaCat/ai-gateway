package limiter

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// BucketKey 唯一标识一个计数桶。
type BucketKey struct {
	LimiterID uint
	Bucket    string
}

// PermitStore 是计数层抽象：并发=信号量，速率=固定窗口计数。
// v1 进程内实现；预留 Redis（换实现即可，热路径只认接口）。
type PermitStore interface {
	// TryConcurrency 非阻塞试占一个并发槽；成功返回 release 回调与 true。
	TryConcurrency(key BucketKey, capacity int64) (release func(), ok bool)
	// TryRate 当前固定窗口内 +1；成功 true。窗口过期自然恢复，无需 release。
	TryRate(key BucketKey, capacity int64, windowMs int) (ok bool)
	// WaitC 返回某桶"有名额释放"的通知通道，供 wait 循环 select（并发桶有效；其它桶恒阻塞）。
	WaitC(key BucketKey) <-chan struct{}
	// AddWaiter 若当前等待者 < max 则 +1 返回 true；否则 false（max=0 恒 false=不排）。
	AddWaiter(key BucketKey, max int) bool
	// RemoveWaiter 等待结束 -1。
	RemoveWaiter(key BucketKey)
}

var noopRelease = func() {}

type semaphore struct {
	ch       chan struct{}
	cap      int64
	occupied atomic.Int64
}

type window struct {
	mu    sync.Mutex
	start int64 // 窗口起点(unix ms)
	count int64
}

// MemStore 是进程内 PermitStore。每 agent 建一个、跨请求复用（同 resilience.Registry）。
type MemStore struct {
	conc  utils.SyncMap[BucketKey, *semaphore]
	rate  utils.SyncMap[BucketKey, *window]
	waits utils.SyncMap[BucketKey, *bucketWait]
	now   func() int64 // 可注入时钟(测试)，默认 time.Now().UnixMilli
}

func NewMemStore() *MemStore {
	return &MemStore{now: func() int64 { return time.Now().UnixMilli() }}
}

func (s *MemStore) TryConcurrency(key BucketKey, capacity int64) (func(), bool) {
	if capacity <= 0 {
		return noopRelease, false
	}
	sem := s.getSem(key, capacity)
	select {
	case sem.ch <- struct{}{}:
		sem.occupied.Add(1)
		return func() {
			<-sem.ch
			sem.occupied.Add(-1)
			select {
			case s.bw(key).notify <- struct{}{}:
			default:
			}
		}, true
	default:
		return noopRelease, false
	}
}

func (s *MemStore) getSem(key BucketKey, capacity int64) *semaphore {
	if e, ok := s.conc.Load(key); ok && e.cap == capacity {
		return e
	}
	e := &semaphore{ch: make(chan struct{}, capacity), cap: capacity}
	actual, loaded := s.conc.LoadOrStore(key, e)
	if loaded && actual.cap == capacity {
		return actual
	}
	if loaded {
		// 容量被 admin 改过：覆盖重建（本地 v1 容忍重建瞬间轻微误差，见 spec §17）。
		s.conc.Store(key, e)
	}
	return e
}

func (s *MemStore) TryRate(key BucketKey, capacity int64, windowMs int) bool {
	if capacity <= 0 || windowMs <= 0 {
		return false
	}
	w, _ := s.rate.LoadOrStore(key, &window{})
	w.mu.Lock()
	defer w.mu.Unlock()
	now := s.now()
	if now-w.start >= int64(windowMs) {
		w.start = now
		w.count = 0
	}
	if w.count >= capacity {
		return false
	}
	w.count++
	return true
}

type bucketWait struct {
	notify  chan struct{} // 缓冲 1：非阻塞信号
	waiters atomic.Int64
}

func (s *MemStore) bw(key BucketKey) *bucketWait {
	if e, ok := s.waits.Load(key); ok {
		return e
	}
	e := &bucketWait{notify: make(chan struct{}, 1)}
	actual, _ := s.waits.LoadOrStore(key, e)
	return actual
}

func (s *MemStore) WaitC(key BucketKey) <-chan struct{} { return s.bw(key).notify }

func (s *MemStore) AddWaiter(key BucketKey, max int) bool {
	bw := s.bw(key)
	for {
		cur := bw.waiters.Load()
		if cur >= int64(max) {
			return false
		}
		if bw.waiters.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (s *MemStore) RemoveWaiter(key BucketKey) { s.bw(key).waiters.Add(-1) }

// occupiedOf 读某并发桶当前占用（测试/快照用）。桶不存在为 0。
func (s *MemStore) occupiedOf(key BucketKey) int64 {
	if e, ok := s.conc.Load(key); ok {
		return e.occupied.Load()
	}
	return 0
}

// BucketUsage 是 MemStore 内一个桶的原始实时读数（未含 capacity/规则元数据）。
type BucketUsage struct {
	Key           BucketKey
	Occupied      int64 // 并发:占用; 速率:窗口计数
	WindowStartMs int64 // 速率桶窗口起点; 并发为 0
	Waiters       int64
	IsRate        bool
}

// SnapshotBuckets 遍历并发/速率/等待三张表，按 union-of-keys 汇出每桶实时读数。
func (s *MemStore) SnapshotBuckets() []BucketUsage {
	acc := map[BucketKey]*BucketUsage{}
	get := func(k BucketKey) *BucketUsage {
		if b, ok := acc[k]; ok {
			return b
		}
		b := &BucketUsage{Key: k}
		acc[k] = b
		return b
	}
	s.conc.Range(func(k BucketKey, sem *semaphore) bool {
		get(k).Occupied = sem.occupied.Load()
		return true
	})
	s.rate.Range(func(k BucketKey, w *window) bool {
		w.mu.Lock()
		b := get(k)
		b.IsRate = true
		b.Occupied = w.count
		b.WindowStartMs = w.start
		w.mu.Unlock()
		return true
	})
	s.waits.Range(func(k BucketKey, bw *bucketWait) bool {
		get(k).Waiters = bw.waiters.Load()
		return true
	})
	out := make([]BucketUsage, 0, len(acc))
	for _, b := range acc {
		out = append(out, *b)
	}
	return out
}
