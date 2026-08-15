// internal/agent/reporter/snapshot_test.go
package reporter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage_backlog.snapshot.gz")
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u := newTestUploader(t, "http://127.0.0.1:0") // 不会真发
	trigger := autoDisableTrigger()
	store.Append([]protocol.UsageLogEntry{{
		RequestID: "s1", Timestamp: 1, AutoDisableTriggers: []attemptproxy.ChannelAutoDisableTrigger{trigger},
	}})
	retryEntry := protocol.UsageLogEntry{
		RequestID: "r1", AutoDisableTriggers: []attemptproxy.ChannelAutoDisableTrigger{trigger},
	}
	u.retry.pushItem(retryItem{entry: protocol.ReportedUsage{LLM: &retryEntry},
		attempts: 7, degrade: DegradeStripTrace, bytes: 10, nextAt: time.Now().Add(time.Hour)})
	snap := &Snapshotter{Store: store, Uploader: u, Path: path, Logger: zap.NewNop()}
	if err := snap.WriteNow(); err != nil {
		t.Fatal(err)
	}

	// 全新的一套(模拟重启)
	store2 := NewMemPendingUsageStore(100, zap.NewNop())
	u2 := newTestUploader(t, "http://127.0.0.1:0")
	snap2 := &Snapshotter{Store: store2, Uploader: u2, Path: path, Logger: zap.NewNop()}
	n, err := snap2.Restore()
	if err != nil || n != 2 {
		t.Fatalf("restore n=%d err=%v, want 2/nil", n, err)
	}
	if store2.Len() != 1 || u2.retry.Len() != 1 {
		t.Fatalf("store=%d retry=%d, want 1/1", store2.Len(), u2.retry.Len())
	}
	it := u2.retry.snapshotTop(1)[0]
	require.Equal(t, []attemptproxy.ChannelAutoDisableTrigger{trigger}, it.entry.LLM.AutoDisableTriggers)
	require.Equal(t, []attemptproxy.ChannelAutoDisableTrigger{trigger}, store2.PeekBatch(1)[0].AutoDisableTriggers)
	if it.attempts != 7 || it.degrade != DegradeStripTrace {
		t.Fatalf("retry item lost fields: %+v", it)
	}
	// 恢复的条目立即可重试(nextAt 按 attempts 重算,不会是零值远古时间导致语义错乱)
	if it.nextAt.IsZero() {
		t.Fatal("restored nextAt must be recomputed")
	}
}

func TestSnapshotRoundTripPreservesMixedUsageTypeIdentityAndRetryState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage_backlog.snapshot.gz")
	store := NewMemPendingUsageStore(100, zap.NewNop())
	uploader := newTestUploader(t, "http://127.0.0.1:0")
	llm := protocol.ReportedUsage{LLM: &protocol.UsageLogEntry{RequestID: "shared", Timestamp: 10}}
	api := protocol.ReportedUsage{API: &protocol.APIUsageEntry{
		RequestID: "shared", Timestamp: 20, ProviderDispatchKnown: true, ProviderDispatched: true,
	}}
	store.AppendReported([]protocol.ReportedUsage{llm, api})
	retryAPI := protocol.ReportedUsage{API: &protocol.APIUsageEntry{RequestID: "retry-api", Timestamp: 5}}
	uploader.retry.pushItem(retryItem{entry: retryAPI, attempts: 7, degrade: DegradeStripTrace, bytes: entrySize(retryAPI)})
	require.NoError(t, (&Snapshotter{Store: store, Uploader: uploader, Path: path, Logger: zap.NewNop()}).WriteNow())

	restoredStore := NewMemPendingUsageStore(100, zap.NewNop())
	restoredUploader := newTestUploader(t, "http://127.0.0.1:0")
	restored := &Snapshotter{Store: restoredStore, Uploader: restoredUploader, Path: path, Logger: zap.NewNop()}
	count, err := restored.Restore()

	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.Equal(t, []protocol.ReportedUsage{llm, api}, restoredStore.PeekReportedBatch(10, true))
	retry := restoredUploader.retry.snapshotAll()
	require.Len(t, retry, 1)
	require.True(t, retry[0].entry.IsAPI())
	require.Equal(t, 7, retry[0].attempts)
	require.Equal(t, DegradeStripTrace, retry[0].degrade)
}

func TestSnapshotEmptyDeletesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage_backlog.snapshot.gz")
	os.WriteFile(path, []byte("old"), 0o600)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u := newTestUploader(t, "http://127.0.0.1:0")
	snap := &Snapshotter{Store: store, Uploader: u, Path: path, Logger: zap.NewNop()}
	if err := snap.WriteNow(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("empty snapshot must remove the file")
	}
}

func TestSnapshotCorruptFileQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage_backlog.snapshot.gz")
	os.WriteFile(path, []byte("garbage-not-gzip"), 0o600)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u := newTestUploader(t, "http://127.0.0.1:0")
	snap := &Snapshotter{Store: store, Uploader: u, Path: path, Logger: zap.NewNop()}
	n, err := snap.Restore()
	if err == nil || n != 0 {
		t.Fatalf("corrupt restore must be observable without restoring entries: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatal("corrupt file must be renamed for forensics")
	}
	if store.Len() != 0 {
		t.Fatal("nothing should be restored from corrupt file")
	}
}

func TestSnapshotMissingFileIsNoop(t *testing.T) {
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u := newTestUploader(t, "http://127.0.0.1:0")
	snap := &Snapshotter{Store: store, Uploader: u,
		Path: filepath.Join(t.TempDir(), "none.gz"), Logger: zap.NewNop()}
	if n, err := snap.Restore(); n != 0 || err != nil {
		t.Fatalf("missing file must be silent noop: n=%d err=%v", n, err)
	}
}

// TestShutdownFailedRetryDrainSurvivesInFinalSnapshot 是 review 发现的回归用例:
// drainRetryOnShutdown 对旁路队列的收尾投递如果失败(比如关停这一刻 master 恰好
// 不可达),旧实现直接就地放弃——条目既不在 store、也不在 retry 队列、也不在
// inflight,Task 8 的最终快照(Snapshotter.Run 在 DrainDone 之后的 WriteNow)自然
// 也看不到它们,数据凭空消失。修复后失败条目要重新 push 回 retry 队列,让最终
// 快照能捞到它们。
func TestShutdownFailedRetryDrainSurvivesInFinalSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage_backlog.snapshot.gz")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close() // 关停期 master 不可达(连接被拒)

	u := newTestUploader(t, srv.URL)
	u.retry.push([]protocol.UsageLogEntry{entry("doomed-1"), entry("doomed-2")}, 2, time.Now().Add(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run 启动前 ctx 已 Done,第一次 select 直接走关停收尾分支
	go u.Run(ctx)
	<-u.DrainDone()

	store := NewMemPendingUsageStore(100, zap.NewNop())
	snap := &Snapshotter{Store: store, Uploader: u, Path: path, Logger: zap.NewNop()}
	if err := snap.WriteNow(); err != nil {
		t.Fatal(err)
	}

	store2 := NewMemPendingUsageStore(100, zap.NewNop())
	u2 := newTestUploader(t, srv.URL)
	snap2 := &Snapshotter{Store: store2, Uploader: u2, Path: path, Logger: zap.NewNop()}
	n, err := snap2.Restore()
	if err != nil || n != 2 {
		t.Fatalf("restore n=%d err=%v, want 2 survivors of failed shutdown drain", n, err)
	}
}
