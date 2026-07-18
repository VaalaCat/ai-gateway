// internal/agent/reporter/reporter.go
package reporter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

var _ app.Reporter = (*Reporter)(nil)

var ErrReporterClosing = errors.New("reporter: closing")

// Reporter 订阅 usage.completed,把条目写入 PendingUsageStore,由 UsageUploader
// 经 HTTP acked 投递给 master(spec §4:acked 才清,at-least-once + master 幂等)。
// 不再走 ws Notify(旧路径 fire-and-forget 会静默丢,见 spec §1.2)。
type Reporter struct {
	bus            app.EventBus
	logger         *zap.Logger
	store          PendingUsageStore
	uploader       *UsageUploader
	snapshot       *Snapshotter // nil 时(旧调用方/测试)照旧只走内存,不做磁盘快照
	cancel         context.CancelFunc
	usageSub       eventbus.Subscription
	mu             sync.Mutex
	workers        conc.WaitGroup
	started        bool
	running        bool
	startDone      chan struct{}
	startErr       error
	closing        bool
	usageCallbacks int
	usageChanged   chan struct{}
	closeOnce      sync.Once
	doneOnce       sync.Once
	done           chan struct{}
	activeWorkers  atomic.Int64
}

// New 的 snapshot 参数可以是 nil——旧调用方和大多数测试构造 Reporter 时不关心磁盘
// 快照,nil 时 Start 既不 Restore 也不起 Snapshot.Run,行为等同快照功能上线之前。
func New(bus app.EventBus, logger *zap.Logger, store PendingUsageStore, uploader *UsageUploader, snapshot *Snapshotter) *Reporter {
	return &Reporter{
		bus: bus, logger: logger, store: store, uploader: uploader, snapshot: snapshot,
		done: make(chan struct{}), usageChanged: make(chan struct{}, 1),
	}
}

func (r *Reporter) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("reporter: nil start context")
	}
	r.mu.Lock()
	if r.started {
		done := r.startDone
		r.mu.Unlock()
		<-done
		r.mu.Lock()
		err := r.startErr
		r.mu.Unlock()
		return err
	}
	if r.closing {
		r.mu.Unlock()
		return ErrReporterClosing
	}
	r.started = true
	r.startDone = make(chan struct{})
	done := r.startDone
	r.mu.Unlock()

	if r.snapshot != nil {
		if _, err := r.snapshot.Restore(); err != nil {
			r.logger.Error("backlog snapshot restore failed, starting with empty backlog", zap.Error(err))
		}
	}
	workerCtx, cancel := context.WithCancel(ctx)
	sub, err := events.SubscribeUsageCompleted(r.bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		return r.appendUsage(entry)
	})
	if err != nil {
		cancel()
		err = fmt.Errorf("subscribe usage.completed: %w", err)
		r.finishStart(done, err)
		return err
	}

	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		_ = sub.Unsubscribe()
		cancel()
		err := ErrReporterClosing
		r.finishStart(done, err)
		return err
	}
	r.cancel = cancel
	r.usageSub = sub
	r.running = true
	r.activeWorkers.Add(1)
	r.workers.Go(func() { r.runWorker(func() { r.uploader.Run(workerCtx) }) })
	if r.snapshot != nil {
		r.activeWorkers.Add(1)
		r.workers.Go(func() { r.runWorker(func() { r.snapshot.Run(workerCtx) }) })
	}
	r.startErr = nil
	r.mu.Unlock()
	close(done)
	return nil
}

func (r *Reporter) appendUsage(entry protocol.UsageLogEntry) error {
	if !r.beginUsageCallback() {
		return ErrReporterClosing
	}
	defer r.finishUsageCallback()
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().Unix()
	}
	r.store.Append([]protocol.UsageLogEntry{entry})
	r.uploader.Kick()
	return nil
}

func (r *Reporter) beginUsageCallback() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.usageCallbacks++
	return true
}

func (r *Reporter) finishUsageCallback() {
	r.mu.Lock()
	if r.usageCallbacks > 0 {
		r.usageCallbacks--
	}
	notify := r.usageCallbacks == 0
	r.mu.Unlock()
	if notify {
		select {
		case r.usageChanged <- struct{}{}:
		default:
		}
	}
}

func (r *Reporter) waitUsageCallbacks() {
	for {
		r.mu.Lock()
		active := r.usageCallbacks
		r.mu.Unlock()
		if active == 0 {
			return
		}
		<-r.usageChanged
	}
}

func (r *Reporter) finishStart(done chan struct{}, err error) {
	r.mu.Lock()
	r.startErr = err
	r.mu.Unlock()
	close(done)
}

func (r *Reporter) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *Reporter) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("reporter: nil close context")
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closing = true
		started := r.started
		startDone := r.startDone
		sub := r.usageSub
		cancel := r.cancel
		r.mu.Unlock()
		if sub != nil {
			_ = sub.Unsubscribe()
		}
		if cancel != nil {
			cancel()
		}
		go func() {
			if started {
				<-startDone
			}
			r.mu.Lock()
			running := r.running
			sub := r.usageSub
			cancel := r.cancel
			r.mu.Unlock()
			if sub != nil {
				_ = sub.Unsubscribe()
			}
			if cancel != nil {
				cancel()
			}
			r.waitUsageCallbacks()
			if running {
				<-r.uploader.DrainDone()
			}
			r.uploader.FinalDrain(ctx)
			r.workers.Wait()
			r.uploader.CloseIdleConnections()
			r.doneOnce.Do(func() { close(r.done) })
		}()
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (r *Reporter) Done() <-chan struct{} { return r.done }

func (r *Reporter) runWorker(run func()) {
	defer r.activeWorkers.Add(-1)
	run()
}

func (r *Reporter) ResourceCount() int64 { return r.activeWorkers.Load() }

func (r *Reporter) ResourceCounts() app.ResourceCounts {
	r.mu.Lock()
	usageCallbacks := r.usageCallbacks
	r.mu.Unlock()
	counts := app.ResourceCounts{ReporterWorkers: r.activeWorkers.Load(), Inflight: int64(usageCallbacks)}
	if r.uploader != nil {
		counts.Inflight += int64(r.uploader.InflightCount())
	}
	return counts
}

// SetClient 保留以满足 app.Reporter(server.setClient 仍调用);数据面已不走 ws,no-op。
func (r *Reporter) SetClient(_ app.WSClient) {}

// PendingCount 是心跳上报的 pending_usage 口径:store 已覆盖主队列在飞条目,
// 再加旁路重试队列和已从旁路摘出的 retry inflight。
func (r *Reporter) PendingCount() int {
	return r.store.Len() + r.uploader.RetryLen() + r.uploader.retryInflightCount()
}

// QueueItemSnapshot 是旁路重试队列里一条记录的管理看板视图(见 QueueSnapshot.Items)。
type QueueItemSnapshot struct {
	RequestID    string `json:"request_id"`
	Bytes        int    `json:"bytes"`
	Attempts     int    `json:"attempts"`
	DegradeLevel int    `json:"degrade_level"`
	NextAt       int64  `json:"next_at"` // unix 秒
}

// QueueSnapshot 是 usage 投递两级队列(主队列 store + 旁路 retry)的管理看板快照。
type QueueSnapshot struct {
	StoreLen      int                 `json:"store_len"`
	StoreBytes    int                 `json:"store_bytes"`
	RetryLen      int                 `json:"retry_len"`
	RetryBytes    int                 `json:"retry_bytes"`
	OldestTs      int64               `json:"oldest_ts"`       // 两队列最老 entry.Timestamp,0=空
	LastSuccessAt int64               `json:"last_success_at"` // 0=从未
	LastError     string              `json:"last_error"`
	Inflight      int                 `json:"inflight"`
	Items         []QueueItemSnapshot `json:"items"` // 旁路 top50 按 bytes 降序
}

// minNonZero 返回两个"0 表示空/不适用"的时间戳里较早的那个非零值——两者都是 0
// 才返回 0(两个队列都空)。
func minNonZero(a, b int64) int64 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// QueueSnapshot 汇总主队列(store)和旁路重试队列(uploader.retry)的管理看板视图。
// Items 的 DegradeLevel 是"报告口径":L1(DegradeSlimBody)只在发送时现算、不落
// retryItem.degrade 字段,这里按同样的门槛(attempts>=slimBodyAfter() 且 bytes
// 超过 slimThresholdBytes)派生出来,让管理端能看到即将被瘦身的条目(spec §7.1)。
func (r *Reporter) QueueSnapshot() QueueSnapshot {
	u := r.uploader
	snap := QueueSnapshot{
		StoreLen: r.store.Len(), StoreBytes: r.store.Bytes(),
		RetryLen: u.retry.Len(), RetryBytes: u.retry.totalBytes(),
		LastSuccessAt: u.LastSuccessAt(), LastError: u.LastError(),
		Inflight: u.InflightCount(),
	}
	snap.OldestTs = minNonZero(r.store.OldestTimestamp(), u.retry.oldestTimestamp())
	slimAfter := u.slimBodyAfter()
	for _, it := range u.retry.snapshotTop(50) {
		level := it.degrade
		if level == DegradeNone && it.attempts >= slimAfter && it.bytes > slimThresholdBytes {
			level = DegradeSlimBody // L1 发送时现算不落条目,报告口径派生(spec §7.1)
		}
		nextAt := int64(0)
		if !it.nextAt.IsZero() {
			nextAt = it.nextAt.Unix() // 0 = 立即可重试(retry_now 清过),与 last_success_at/oldest_ts 的 0=N/A 口径一致
		}
		snap.Items = append(snap.Items, QueueItemSnapshot{
			RequestID: it.entry.RequestID, Bytes: it.bytes, Attempts: it.attempts,
			DegradeLevel: level, NextAt: nextAt})
	}
	return snap
}

// queueOpFn 是一种旁路队列管理操作的实现;queueOps 是分发表(策略表替代
// if-else 链),QueueOp 的主分发只有一行查表+调用。
type queueOpFn func(r *Reporter, ids []string, level int) (int, error)

var queueOps = map[string]queueOpFn{
	"retry_now": func(r *Reporter, ids []string, _ int) (int, error) {
		n := r.uploader.retry.retryNow(ids)
		r.uploader.Kick()
		return n, nil
	},
	"degrade": func(r *Reporter, ids []string, level int) (int, error) {
		if level != DegradeStripTrace && level != DegradeBillingOnly {
			return 0, fmt.Errorf("invalid degrade level %d", level)
		}
		return r.uploader.retry.degrade(ids, level), nil
	},
	"drop": func(r *Reporter, ids []string, _ int) (int, error) {
		if len(ids) == 0 {
			return 0, fmt.Errorf("drop requires explicit request_ids")
		}
		n := r.uploader.retry.remove(ids)
		// 人工丢弃 = 计费数据真丢,agent 侧留痕(master 侧另有审计,双端可对账)
		r.logger.Error("usage entries dropped by operator",
			zap.Strings("request_ids", ids), zap.Int("removed", n))
		return n, nil
	},
}

// QueueOp 分发一次管理端旁路队列操作(retry_now/degrade/drop),只作用旁路 retry
// 队列——store 主队列不提供人工干预入口(正常流转,不该被操作员绕过重试节奏)。
func (r *Reporter) QueueOp(op string, ids []string, level int) (int, error) {
	fn, ok := queueOps[op]
	if !ok {
		return 0, fmt.Errorf("unknown queue op %q", op)
	}
	return fn(r, ids, level)
}
