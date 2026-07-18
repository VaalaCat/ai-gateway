package reporter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

type subscribeErrorBus struct {
	app.EventBus
	err error
}

type blockingPendingUsageStore struct {
	*MemPendingUsageStore
	entered                chan struct{}
	release                chan struct{}
	blockOnce              sync.Once
	finalDrainDone         <-chan struct{}
	finalDrainBeforeAppend atomic.Bool
}

type delayedUsageBus struct {
	base         *eventbus.MemoryBus
	mu           sync.Mutex
	handler      eventbus.EventHandler
	snapshotted  chan struct{}
	release      chan struct{}
	snapshotOnce sync.Once
}

type delayedUsageSubscription struct {
	bus  *delayedUsageBus
	once sync.Once
}

func (s *delayedUsageSubscription) Unsubscribe() error {
	s.once.Do(func() {
		s.bus.mu.Lock()
		s.bus.handler = nil
		s.bus.mu.Unlock()
	})
	return nil
}

func newDelayedUsageBus() *delayedUsageBus {
	return &delayedUsageBus{
		base: eventbus.NewMemoryBus(), snapshotted: make(chan struct{}), release: make(chan struct{}),
	}
}

func (b *delayedUsageBus) Publish(ctx context.Context, event eventbus.Event) error {
	if event.Topic != events.UsageCompletedTopic.Value() {
		return b.base.Publish(ctx, event)
	}
	b.mu.Lock()
	handler := b.handler
	b.mu.Unlock()
	b.snapshotOnce.Do(func() { close(b.snapshotted) })
	<-b.release
	if handler == nil {
		return nil
	}
	return handler(ctx, event)
}

func (b *delayedUsageBus) Subscribe(topic string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	if topic != events.UsageCompletedTopic.Value() {
		return b.base.Subscribe(topic, handler)
	}
	b.mu.Lock()
	b.handler = handler
	b.mu.Unlock()
	return &delayedUsageSubscription{bus: b}, nil
}

func (b *delayedUsageBus) SubscribePattern(pattern string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	return b.base.SubscribePattern(pattern, handler)
}

func (b *delayedUsageBus) Close() error { return b.base.Close() }

func (s *blockingPendingUsageStore) Append(entries []protocol.UsageLogEntry) {
	blocked := false
	s.blockOnce.Do(func() {
		blocked = true
		close(s.entered)
		<-s.release
	})
	if blocked && s.finalDrainDone != nil {
		select {
		case <-s.finalDrainDone:
			s.finalDrainBeforeAppend.Store(true)
		default:
		}
	}
	s.MemPendingUsageStore.Append(entries)
}

func newBlockingReporterFixture(t *testing.T) (*Reporter, *blockingPendingUsageStore) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	store := &blockingPendingUsageStore{
		MemPendingUsageStore: NewMemPendingUsageStore(100, zap.NewNop()),
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
	}
	uploader, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: server.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: time.Hour, BatchMax: 10,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.finalDrainDone = uploader.FinalDrainDone()
	return New(eventbus.NewMemoryBus(), zap.NewNop(), store, uploader, nil), store
}

func (b *subscribeErrorBus) Subscribe(string, eventbus.EventHandler) (eventbus.Subscription, error) {
	return nil, b.err
}

func newReporterFixture(t *testing.T, handler http.HandlerFunc) (*Reporter, *MemPendingUsageStore) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	up, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: srv.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 10,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := eventbus.NewMemoryBus()
	return New(bus, zap.NewNop(), store, up, nil), store
}

func TestReporterStartReturnsUsageSubscriptionError(t *testing.T) {
	r, _ := newReporterFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	subscribeErr := errors.New("subscribe usage")
	r.bus = &subscribeErrorBus{EventBus: r.bus, err: subscribeErr}
	if err := r.Start(context.Background()); !errors.Is(err, subscribeErr) {
		t.Fatalf("Start error = %v, want %v", err, subscribeErr)
	}
}

func TestReporterCloseUnsubscribesUsageHandler(t *testing.T) {
	r, store := newReporterFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := events.PublishUsageCompleted(context.Background(), r.bus, protocol.UsageLogEntry{RequestID: "after-close"}); err != nil {
		t.Fatalf("Publish after Close: %v", err)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("store.Len() after Close = %d, want 0", got)
	}
}

func TestReporterCloseWaitsForAcceptedUsageAppendBeforeFinalDrain(t *testing.T) {
	r, store := newBlockingReporterFixture(t)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- events.PublishUsageCompleted(context.Background(), r.bus, protocol.UsageLogEntry{RequestID: "accepted"})
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("usage callback did not enter Append")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- r.Close(closeCtx) }()
	doneBeforeAppend := false
	select {
	case <-r.Done():
		doneBeforeAppend = true
	case <-time.After(100 * time.Millisecond):
	}
	close(store.release)
	if err := <-publishDone; err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if doneBeforeAppend {
		t.Error("Reporter.Done closed before accepted usage callback completed")
	}
	if store.finalDrainBeforeAppend.Load() {
		t.Error("FinalDrain completed before accepted usage callback appended")
	}
	if got := r.PendingCount(); got != 1 {
		t.Errorf("PendingCount after failed final drain = %d, want 1", got)
	}
}

func TestReporterSnapshottedCallbackLosesCloseAdmission(t *testing.T) {
	bus := newDelayedUsageBus()
	store := NewMemPendingUsageStore(100, zap.NewNop())
	uploader := newTestUploader(t, "http://127.0.0.1:0")
	uploader.cfg.Store = store
	r := New(bus, zap.NewNop(), store, uploader, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- events.PublishUsageCompleted(context.Background(), bus, protocol.UsageLogEntry{RequestID: "snapshotted"})
	}()
	select {
	case <-bus.snapshotted:
	case <-time.After(time.Second):
		t.Fatal("Publish did not snapshot usage callback")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(bus.release)
	if err := <-publishDone; !errors.Is(err, ErrReporterClosing) {
		t.Fatalf("snapshotted callback error = %v, want %v", err, ErrReporterClosing)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("store.Len() after rejected callback = %d, want 0", got)
	}
}

func TestReporterCloseDeadlineReturnsBeforeOwnerJoinsAcceptedCallback(t *testing.T) {
	r, store := newBlockingReporterFixture(t)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- events.PublishUsageCompleted(context.Background(), r.bus, protocol.UsageLogEntry{RequestID: "deadline"})
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("usage callback did not enter Append")
	}
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.Close(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close = %v, want deadline", err)
	}
	select {
	case <-r.Done():
		t.Fatal("Reporter.Done closed before accepted callback was released")
	default:
	}
	secondClose := make(chan error, 1)
	go func() { secondClose <- r.Close(context.Background()) }()
	close(store.release)
	if err := <-publishDone; err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := <-secondClose; err != nil {
		t.Fatalf("second Close: %v", err)
	}
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("Reporter owner did not join accepted callback")
	}
}

// success:事件进 store 并被上传清空
func TestReporter_EventFlowsToMaster(t *testing.T) {
	var received atomic.Int32
	r, store := newReporterFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	defer r.Stop()
	if err := events.PublishUsageCompleted(ctx, r.bus, protocol.UsageLogEntry{RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return store.Len() == 0 && received.Load() >= 1 })
}

// failure:master 全程 5xx → 数据滞留 store 不丢
func TestReporter_RetainsWhenMasterDown(t *testing.T) {
	r, _ := newReporterFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	defer r.Stop()
	_ = events.PublishUsageCompleted(ctx, r.bus, protocol.UsageLogEntry{RequestID: "r1"})
	waitFor(t, time.Second, func() bool { return r.PendingCount() == 1 })
	time.Sleep(100 * time.Millisecond) // 再等几轮 flush
	if r.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1 (retained)", r.PendingCount())
	}
}

// TestReporter_PendingCountIncludesRetryQueue 是旁路重试队列上线后的回归用例:
// PendingCount 是心跳 pending_usage 的口径,一条条目投递失败后会从 store 挪进
// uploader 的旁路 retry 队列——如果 PendingCount 只看 store.Len(),outage 期间
// 运维会看到 pending_usage 骤降到 0,误以为数据已经清空,实际上还有一堆在旁路
// 队列里反复重试。这里让 master 全程 5xx,断言 PendingCount 稳定等于种子条目数,
// 且确实是靠 store+retry 两边合计撑起来的(不是巧合地只有一边非零)。
func TestReporter_PendingCountIncludesRetryQueue(t *testing.T) {
	r, store := newReporterFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	defer r.Stop()
	_ = events.PublishUsageCompleted(ctx, r.bus, protocol.UsageLogEntry{RequestID: "r1"})
	_ = events.PublishUsageCompleted(ctx, r.bus, protocol.UsageLogEntry{RequestID: "r2"})

	// 两次 Publish 背靠背发生,uploader 的 Run 协程可能在 r2 append 完成前就已经被 r1
	// 的 Kick 唤醒、抢先把 r1 单独迁去 retry——所以要等两条都确实落地 retry 才能断言
	// PendingCount,不能只看 RetryLen()>0(那样会在只有 r1 迁移过去时就提前判定"完成")。
	waitFor(t, 2*time.Second, func() bool { return store.Len() == 0 && r.uploader.RetryLen() == 2 })
	if got := r.PendingCount(); got != 2 {
		t.Fatalf("PendingCount = %d, want 2 (store + retry queue combined)", got)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("store.Len() = %d, want 0 (failed batch moved off the main queue)", got)
	}
	if got := r.uploader.RetryLen(); got != 2 {
		t.Fatalf("uploader.RetryLen() = %d, want 2 (both entries parked in the retry queue)", got)
	}
}

func TestReporterPendingCountDoesNotDoubleCountMainInflight(t *testing.T) {
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	r, store := newReporterFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		close(requestEntered)
		<-releaseRequest
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	defer r.Stop()
	if err := events.PublishUsageCompleted(ctx, r.bus, protocol.UsageLogEntry{RequestID: "blocked-main"}); err != nil {
		t.Fatal(err)
	}
	<-requestEntered
	if got := store.Len(); got != 1 {
		t.Fatalf("store.Len() = %d, want retained main inflight entry", got)
	}
	if got := r.uploader.InflightCount(); got != 1 {
		t.Fatalf("InflightCount() = %d, want 1", got)
	}
	if got := r.PendingCount(); got != 1 {
		t.Fatalf("PendingCount() = %d, want 1 without double-counting main inflight", got)
	}
	if got := r.ResourceCounts().Inflight; got != 1 {
		t.Fatalf("ResourceCounts.Inflight = %d, want total inflight 1", got)
	}
	release()
	waitFor(t, time.Second, func() bool { return r.PendingCount() == 0 })
}

// boundary:Timestamp 为零时补当前时间;SetClient 是 no-op 不 panic
func TestReporter_TimestampDefaultAndSetClientNoop(t *testing.T) {
	var got atomic.Int64
	r, _ := newReporterFixture(t, func(w http.ResponseWriter, req *http.Request) {
		var rep protocol.UsageReport
		decodeMaybeGzip(t, req, &rep)
		if len(rep.Logs) > 0 {
			got.Store(rep.Logs[0].Timestamp)
		}
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	defer r.Stop()
	r.SetClient(nil) // 兼容 no-op
	_ = events.PublishUsageCompleted(ctx, r.bus, protocol.UsageLogEntry{RequestID: "r1", Timestamp: 0})
	waitFor(t, 2*time.Second, func() bool { return got.Load() > 0 })
}

func TestReporterCloseDeadlineCancelsPendingFinalDrainAndJoinsWorkers(t *testing.T) {
	requestEntered := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		requestEntered <- struct{}{}
		<-req.Context().Done()
		requestCanceled <- struct{}{}
	}))
	defer server.Close()
	store := NewMemPendingUsageStore(100, zap.NewNop())
	uploader, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: server.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: time.Hour, BatchMax: 10,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	r := New(eventbus.NewMemoryBus(), zap.NewNop(), store, uploader, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	store.Append([]protocol.UsageLogEntry{{RequestID: "pending"}})

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shutdownCancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- r.Close(shutdownCtx) }()
	<-requestEntered
	err = <-closeDone
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want deadline exceeded", err)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("pending upload did not observe shutdown deadline")
	}
	select {
	case <-r.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Reporter.Done did not converge after shutdown deadline")
	}
	if got := r.ResourceCount(); got != 0 {
		t.Fatalf("Reporter workers after Done = %d", got)
	}
}

func TestQueueSnapshotShape(t *testing.T) {
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u := newTestUploader(t, "http://127.0.0.1:0")
	r := New(nil, zap.NewNop(), store, u, nil)
	store.Append([]protocol.UsageLogEntry{{RequestID: "s1", Timestamp: 50}})
	// 大条目 + attempts 达 L1 门槛(默认3):degrade_level 应派生报告为 1
	big := protocol.UsageLogEntry{RequestID: "r1", Timestamp: 100,
		TraceData: `{"b":"` + strings.Repeat("x", (2<<20)+1024) + `"}`}
	u.retry.push([]protocol.UsageLogEntry{big}, 3, time.Now().Add(time.Minute))
	snap := r.QueueSnapshot()
	if snap.StoreLen != 1 || snap.RetryLen != 1 || snap.OldestTs != 50 {
		t.Fatalf("snapshot totals wrong: %+v", snap)
	}
	if len(snap.Items) != 1 || snap.Items[0].DegradeLevel != DegradeSlimBody {
		t.Fatalf("L1 derivation missing: %+v", snap.Items)
	}

	// zero-value nextAt after retry_now 报告为 0,不是 -62135596800
	u.retry.push([]protocol.UsageLogEntry{{RequestID: "r2", Timestamp: 200}}, 1, time.Time{})
	n, err := r.QueueOp("retry_now", []string{"r2"}, 0)
	if err != nil || n != 1 {
		t.Fatalf("retry_now: n=%d err=%v", n, err)
	}
	snap = r.QueueSnapshot()
	for i, item := range snap.Items {
		if item.RequestID == "r2" {
			if item.NextAt != 0 {
				t.Fatalf("Items[%d].NextAt after retry_now = %d, want 0 (立即可重试)", i, item.NextAt)
			}
		}
	}
}

func TestQueueOpDispatch(t *testing.T) {
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u := newTestUploader(t, "http://127.0.0.1:0")
	r := New(nil, zap.NewNop(), store, u, nil)
	u.retry.push([]protocol.UsageLogEntry{{RequestID: "a"}, {RequestID: "b"}}, 2, time.Now().Add(time.Hour))

	if n, err := r.QueueOp("retry_now", nil, 0); err != nil || n != 2 {
		t.Fatalf("retry_now: n=%d err=%v", n, err)
	}
	if n, err := r.QueueOp("degrade", []string{"a"}, DegradeStripTrace); err != nil || n != 1 {
		t.Fatalf("degrade: n=%d err=%v", n, err)
	}
	if _, err := r.QueueOp("degrade", []string{"a"}, 1); err == nil {
		t.Fatal("degrade to level 1 must be rejected")
	}
	if _, err := r.QueueOp("drop", nil, 0); err == nil {
		t.Fatal("drop without explicit ids must be rejected")
	}
	if n, err := r.QueueOp("drop", []string{"b"}, 0); err != nil || n != 1 {
		t.Fatalf("drop: n=%d err=%v", n, err)
	}
	if _, err := r.QueueOp("nuke", nil, 0); err == nil {
		t.Fatal("unknown op must be rejected")
	}
}
