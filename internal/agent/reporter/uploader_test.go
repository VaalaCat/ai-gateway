// internal/agent/reporter/uploader_test.go
package reporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// randomASCII returns a deterministic (same n → same string), gzip-resistant ASCII
// payload of length n. Uploads are now gzip-compressed (this task), and a maximally
// repetitive payload like strings.Repeat("x", n) collapses to a few KB regardless of n
// (DEFLATE loves runs) — that would silently defeat every test below that simulates a
// wire-level body-size limit, or relies on byte size to trigger slimming.
func randomASCII(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rnd := rand.New(rand.NewSource(int64(n)))
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rnd.Intn(len(alphabet))]
	}
	return string(b)
}

// entryWithTraceBody 造一条 AttemptTraces 里携带 ~bodyBytes 字节 ResponseBody 的日志,
// 外加一点看家的账单/身份字段——模拟带图片输入的失败请求留下的巨型 trace。
func entryWithTraceBody(id string, bodyBytes int) protocol.UsageLogEntry {
	return protocol.UsageLogEntry{
		RequestID:    id,
		PromptTokens: 42,
		TokenName:    "test-token",
		Status:       500,
		AttemptTraces: []models.UsageLogTrace{
			{
				RequestID:    id,
				AttemptIndex: 0,
				InboundPath:  "/v1/messages",
				ResponseBody: randomASCII(bodyBytes),
			},
		},
	}
}

// entryWithPayload 造一条携带 payloadBytes 字节 TraceData 的日志——模拟大 trace body
// (trace_max_body_size 默认 64KiB、管理员可调到 16MiB)积压的场景。
func entryWithPayload(id string, payloadBytes int) protocol.UsageLogEntry {
	return protocol.UsageLogEntry{RequestID: id, TraceData: randomASCII(payloadBytes)}
}

func newUploaderFixture(t *testing.T, handler http.HandlerFunc) (*UsageUploader, *MemPendingUsageStore, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: srv.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 2, RetryLimit: 100,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u, store, srv
}

// newTestUploader builds a UsageUploader against an already-running httptest server
// (caller owns the server's lifecycle) — for tests that need to construct the server
// first (e.g. to close over per-request state) and only then wire up the uploader.
func newTestUploader(t *testing.T, url string) *UsageUploader {
	t.Helper()
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: url, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 2, RetryLimit: 100,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUsageUploaderCloseIdleConnectionsClosesRealSocket(t *testing.T) {
	idle := make(chan struct{})
	closed := make(chan struct{})
	var idleOnce, closedOnce sync.Once
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateIdle:
			idleOnce.Do(func() { close(idle) })
		case http.StateClosed:
			closedOnce.Do(func() { close(closed) })
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	u := newTestUploader(t, srv.URL)
	if err := u.uploadOnce(context.Background(), []protocol.UsageLogEntry{{RequestID: "socket"}}); err != nil {
		t.Fatalf("uploadOnce: %v", err)
	}
	<-idle
	u.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("uploader idle socket remained open")
	}
}

// newTestUploaderWithConcurrency is newTestUploader but with concurrency() pinned to n
// (instead of the intFnOr default of 2) — tests asserting "how many sub-batches ran in
// parallel" need a fixed, known concurrency rather than depending on the default drifting.
func newTestUploaderWithConcurrency(t *testing.T, url string, n int) *UsageUploader {
	t.Helper()
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: url, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 100, RetryLimit: 100,
		BackoffMaxSec: func() int { return 1 }, Concurrency: func() int { return n },
		Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// decodeMaybeGzip reads r's body off the wire (returned as raw, still-compressed-if-gzip
// bytes — callers simulating a server-side wire body-size limit assert against this),
// transparently gunzip'ing it when Content-Encoding: gzip is set, and JSON-decodes the
// plaintext result into report.
//
// Uses t.Errorf (never Fatal/FailNow) throughout: this runs inside httptest handler
// goroutines, not the test's own goroutine, and calling Fatal there only Goexits the
// handler goroutine — it leaves the HTTP response half-written and the client hanging,
// which surfaces as a baffling waitFor timeout instead of the real decode error.
func decodeMaybeGzip(t *testing.T, r *http.Request, report *protocol.UsageReport) []byte {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
		return raw
	}
	plain := raw
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Errorf("gzip reader: %v", err)
			return raw
		}
		defer zr.Close()
		plain, err = io.ReadAll(zr)
		if err != nil {
			t.Errorf("gunzip: %v", err)
			return raw
		}
	}
	if err := json.Unmarshal(plain, report); err != nil {
		t.Errorf("unmarshal body: %v", err)
	}
	return raw
}

func TestUploader_AcksOnSuccess(t *testing.T) {
	var gotAuth atomic.Bool
	u, store, _ := newUploaderFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(consts.HeaderXAgentID) == "agent-t" && r.Header.Get(consts.HeaderXAgentSecret) == "sec-t" {
			gotAuth.Store(true)
		}
		var rep protocol.UsageReport
		_ = json.NewDecoder(r.Body).Decode(&rep)
		w.WriteHeader(http.StatusOK)
	})
	store.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")}) // 2 批(BatchMax=2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	u.Kick()
	waitFor(t, time.Second, func() bool { return store.Len() == 0 })
	if !gotAuth.Load() {
		t.Fatal("auth headers not sent")
	}
}

// TestUploader_RetainsOnFailureThenRetries 是基本回归用例:失败后条目不会丢——但
// 新架构下"retains"的意思变了:5xx 后这条不再原地留在 store 里等下一轮,而是立刻
// 被挪进旁路 retry 队列(store.Len() 会先归零),自己按退避节奏单独重试直到成功。
// 所以判定"排空完成"要看 store 和 retry 两边都清零,而不能只看 store。
func TestUploader_RetainsOnFailureThenRetries(t *testing.T) {
	var calls atomic.Int32
	u, store, _ := newUploaderFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError) // 第一次 5xx
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	store.Append([]protocol.UsageLogEntry{entry("a")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	u.Kick()
	// 5xx 后挪进 retry 旁路队列(不丢);退避后重试成功才彻底清空。
	//
	// 门槛必须把 calls.Load()>=2 这个 ground truth 也纳入 waitFor 本身,不能只看两个
	// 队列长度再事后断言——due() 会先把到期条目从 retry 队列摘出来,再逐条发 HTTP
	// 请求确认;这两步之间有个短暂窗口,store.Len()==0 && RetryLen()==0 已经同时成立,
	// 但重试请求其实还没发出去(calls 仍然是 1)。只看队列长度会在这个窗口误判"已
	// 完成",导致 waitFor 提前返回、事后的 calls>=2 断言偶发失败(约 7% 复现率)。
	waitFor(t, 3*time.Second, func() bool {
		return calls.Load() >= 2 && store.Len() == 0 && u.RetryLen() == 0
	})
}

// TestUploader_DrainsOnShutdown 验证 Run 取消只停止普通循环，最终投递由拥有
// shutdown deadline 的 caller 通过 FinalDrain 显式执行。
func TestUploader_DrainsOnShutdown(t *testing.T) {
	u, store, _ := newUploaderFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	store.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run 启动前 ctx 已 Done:第一次 select 必然走关闭分支
	go u.Run(ctx)
	<-u.DrainDone()
	if got := store.Len(); got != 3 {
		t.Fatalf("store after canceled Run = %d, want 3 pending for caller-owned final drain", got)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	u.FinalDrain(shutdownCtx)
	waitFor(t, 2*time.Second, func() bool { return store.Len() == 0 })
	select {
	case <-u.FinalDrainDone():
	default:
		t.Fatal("FinalDrainDone remained open after FinalDrain returned")
	}
}

func TestUploader_EmptyStoreNoRequest(t *testing.T) { // boundary
	var calls atomic.Int32
	u, _, _ := newUploaderFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	u.Run(ctx) // 跑完整个窗口
	if calls.Load() != 0 {
		t.Fatalf("calls = %d, want 0 for empty store", calls.Load())
	}
}

// TestUploader_ByteBudgetSplitsOversizedBacklog 是 final-review MUST-FIX F 的回归用例:
// PeekBatch(BatchMax) 只按条数限流,trace 体积大的日志堆积起来可以让一批 marshal 后超过
// master 摄取端点的 body 上限(真实上限是 usage_http.go 的 10MiB MaxBytesReader,这里用
// 4.5MiB 模拟同样效果,让测试更快)。旧代码:10 条 ~1MB 的日志一次性拼进同一批,超过阈值,
// server 400,uploader 把 400 当普通失败退避重试——但 PeekBatch 永远返回队头那一批,同一批
// 原样重试到死,store 永不清空,waitFor 超时。新代码应该按累计字节数把这批切成多个请求,
// 每个请求体都在阈值以内,10 条最终全部投递、store 排空。
func TestUploader_ByteBudgetSplitsOversizedBacklog(t *testing.T) {
	const payloadBytes = 1_000_000                   // ~1MB/条,10 条合计约 10MB,超过下面模拟的服务端上限
	const serverRejectAbove = 4*1024*1024 + 512*1024 // 4.5MiB,模拟 master 的 body 上限

	var requestCount atomic.Int32
	var maxBatchLen atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.UsageReport
		raw := decodeMaybeGzip(t, r, &rep) // raw = wire bytes (gzip-compressed)
		// serverRejectAbove 模拟真实 master 的 MaxBytesReader,按实际过线的字节数(gzip
		// 压缩后)判定——同一份 wire-level 限制,不因请求体现在是 gzip 就该失效。
		if len(raw) > serverRejectAbove {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestCount.Add(1)
		if n := int32(len(rep.Logs)); n > maxBatchLen.Load() {
			maxBatchLen.Store(n)
		}
		w.WriteHeader(http.StatusOK)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: srv.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 100, // BatchMax 足够大,不靠条数拆批
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}

	entries := make([]protocol.UsageLogEntry, 0, 10)
	for i := 0; i < 10; i++ {
		entries = append(entries, entryWithPayload(fmt.Sprintf("e%d", i), payloadBytes))
	}
	store.Append(entries)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	u.Kick()

	// 10MB 的种子数据现在要先被 gzip 压缩才上线(本任务改动);压缩本身是纯 CPU 计算,
	// go test -race 的影子内存追踪能把它拖慢一个数量级以上(实测 4MiB 随机内容从 ~56ms
	// 拖到 ~1.2s),3 个批次连续压缩累计很容易顶到原来 3s 的窗口——放宽到 15s 只是给
	// -race 的开销留够余量,不改变这条用例本身要验证的行为。
	waitFor(t, 15*time.Second, func() bool { return store.Len() == 0 })

	if requestCount.Load() < 2 {
		t.Fatalf("requestCount = %d, want >=2 (backlog must be split across multiple requests)", requestCount.Load())
	}
}

// TestUploader_ByteBudgetStillSendsSingleOversizedEntry 是边界用例:单条日志自己的体积就
// 超过 uploadBatchByteBudget(比如 trace_max_body_size 被管理员调大之后),切批逻辑不能把
// batch 砍成 0 条——哪怕明知这一条本身可能撞上服务端上限,也必须作为单条 batch 尝试投递,
// 而不是永远卡住不发。
func TestUploader_ByteBudgetStillSendsSingleOversizedEntry(t *testing.T) {
	const payloadBytes = 5 * 1024 * 1024 // 5MiB,单条本身就超过 4MiB 预算

	var requestCount atomic.Int32
	var lastBatchLen atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.UsageReport
		decodeMaybeGzip(t, r, &rep)
		requestCount.Add(1)
		lastBatchLen.Store(int32(len(rep.Logs)))
		w.WriteHeader(http.StatusOK)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	store := NewMemPendingUsageStore(10, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: srv.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 100,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Append([]protocol.UsageLogEntry{entryWithPayload("solo", payloadBytes)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	u.Kick()

	// 见上一条用例同样的 -race 压缩开销说明:5MiB 单条压缩本身在 -race 下就可能逼近
	// 原来 2s 的窗口,放宽到 10s。
	waitFor(t, 10*time.Second, func() bool { return store.Len() == 0 })

	if requestCount.Load() != 1 {
		t.Fatalf("requestCount = %d, want 1", requestCount.Load())
	}
	if lastBatchLen.Load() != 1 {
		t.Fatalf("last batch length = %d, want 1 (single oversized entry must still be sent alone)", lastBatchLen.Load())
	}
}

// TestUploader_PoisonIsolation_GoodEntriesDeliverEarly_PoisonEventuallySlims 是本次
// retry 旁路队列的 headline 回归用例:队头一条 ~3MiB trace body 的日志(失败的图片输入
// 请求留下的巨型 trace)让任何包含它的批次都超过服务端体积上限而 500。旧架构下
// PeekBatch 永远捞到队头这一条,同一批原样重试到死,后面排队的小条目跟着永远发不
// 出去("poison batch"卡住整个队列)。新架构下:主队列第一次失败就把整批挪进旁路
// retry 队列,主队列瞬间清空;旁路重试几轮后,poison 按 attempts 升到隔离线就被拆
// 成单条 batch,不再连累 good1/good2——它俩应该在 poison 还没投递成功之前就先送达;
// poison 自己重试到 attempts>=3 后触发瘦身,body 落回服务端能接受的体积,最终投递
// 成功;store + retry 都应完全排空。
func TestUploader_PoisonIsolation_GoodEntriesDeliverEarly_PoisonEventuallySlims(t *testing.T) {
	const serverRejectAbove = 600 * 1024 // 600KiB,模拟真实场景里更小的服务端/网络上限

	var mu sync.Mutex
	delivered := map[string]bool{}
	var poisonTrace models.UsageLogTrace
	poisonAccepted := make(chan struct{})
	releasePoison := make(chan struct{})
	var poisonAcceptedOnce sync.Once
	var releasePoisonOnce sync.Once
	releaseAcceptedPoison := func() { releasePoisonOnce.Do(func() { close(releasePoison) }) }
	t.Cleanup(releaseAcceptedPoison)
	handler := func(w http.ResponseWriter, r *http.Request) {
		var rep protocol.UsageReport
		raw := decodeMaybeGzip(t, r, &rep) // raw = wire bytes (gzip-compressed)
		if len(raw) > serverRejectAbove {
			w.WriteHeader(http.StatusInternalServerError) // 模拟"传不动/被拒绝"
			return
		}
		for _, e := range rep.Logs {
			if e.RequestID == "poison" {
				poisonAcceptedOnce.Do(func() { close(poisonAccepted) })
				<-releasePoison
				break
			}
		}
		mu.Lock()
		for _, e := range rep.Logs {
			delivered[e.RequestID] = true
			if e.RequestID == "poison" && len(e.AttemptTraces) > 0 {
				poisonTrace = e.AttemptTraces[0]
			}
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: srv.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 10, RetryLimit: 100,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}

	poison := entryWithTraceBody("poison", 3*1024*1024) // ~3MiB,超过服务端上限(600KiB),在瘦身阈值(2MiB)之上
	store.Append([]protocol.UsageLogEntry{poison, entry("good1"), entry("good2")})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	u.Kick()

	isDelivered := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return delivered[id]
	}

	// 因果屏障:poison 瘦身后第一次成为服务端可接受的单条请求时,handler 暂停在
	// delivered 写入之前。dispatchRetryItems 在进入下一次 poison retry 前已经等待
	// 上一轮隔离 batch 全部完成,因此此刻 good1/good2 必须已经先交付。
	select {
	case <-poisonAccepted:
	case <-time.After(20 * time.Second):
		t.Fatal("slimmed poison request did not reach acceptance barrier")
	}
	if !isDelivered("good1") || !isDelivered("good2") {
		t.Fatal("good entries were not delivered before the slimmed poison request")
	}
	if isDelivered("poison") {
		t.Fatal("poison delivered no later than good1/good2; isolation did not happen (it should still be retrying alone)")
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("store.Len() = %d, want 0 (good entries acked out of the main queue already)", got)
	}
	// 不在这里再拿 u.RetryLen()==0 当"poison 还没搞定"的证据——它和
	// TestUploader_RetryQueue_NetworkOutage 里记录的是同一个窗口:旁路重试一轮内
	// due() 先把到期条目整批摘出队列、再逐条发 HTTP 请求,"摘出"和"这一条最终成功/
	// 失败确认"之间天然有个短暂空档,期间 RetryLen() 可能读到 0 但 poison 其实正在
	// 半空中(已被摘出、还没收到响应)。本任务给上传体接上了真实 gzip 压缩,大 body
	// 的压缩耗时不再是可忽略的几微秒——poison 那一轮的 HTTP 往返可能就正好落在这个
	// 窗口里,让 RetryLen() 瞬时快照变得不可靠。isDelivered("poison") 上面已经确认
	// 过是 false,这才是不受这个窗口影响的 ground truth,足以证明 poison 还没真正
	// 投递成功。

	releaseAcceptedPoison()
	waitFor(t, time.Second, func() bool { return isDelivered("poison") })
	waitFor(t, time.Second, func() bool { return store.Len() == 0 && u.RetryLen() == 0 })

	mu.Lock()
	defer mu.Unlock()
	if poisonTrace.ResponseBody != slimMarker {
		t.Fatalf("poison entry's trace body should have been slimmed, got %+v", poisonTrace)
	}
	if poisonTrace.InboundPath != "/v1/messages" {
		t.Fatalf("non-body trace fields must survive slimming, got %+v", poisonTrace)
	}
}

// TestUploader_RetryQueue_NetworkOutage_RecoversFullyWithoutSlimming 是误伤防护 +
// 数据不丢的回归用例:服务器无论请求体大小一律 500(模拟纯网络故障/master 挂了,
// 不是某条巨型 trace 卡住了队头),3 条都是远低于 slimThresholdBytes 的小条目——
// 它们应该迁移进旁路 retry 队列反复重试,期间 store+retry 的总数始终等于种子条目数
// (什么都没丢),也不该被误诊为"某条日志太胖"而被剥离;master 恢复后应完整投递。
func TestUploader_RetryQueue_NetworkOutage_RecoversFullyWithoutSlimming(t *testing.T) {
	var serverUp atomic.Bool
	var mu sync.Mutex
	delivered := map[string]models.UsageLogTrace{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if !serverUp.Load() {
			io.Copy(io.Discard, r.Body) // 永远失败,模拟纯网络故障;仍需读干净 body
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var rep protocol.UsageReport
		decodeMaybeGzip(t, r, &rep)
		mu.Lock()
		for _, e := range rep.Logs {
			if len(e.AttemptTraces) > 0 {
				delivered[e.RequestID] = e.AttemptTraces[0]
			}
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	store := NewMemPendingUsageStore(100, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: srv.URL, AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: 20 * time.Millisecond, BatchMax: 10, RetryLimit: 100,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := []protocol.UsageLogEntry{
		entryWithTraceBody("small-1", 1024),
		entryWithTraceBody("small-2", 1024),
		entryWithTraceBody("small-3", 1024),
	}
	// originalBody 记住每条种子日志本来的 trace body,用于收尾时判定"body 全须全尾地
	// 送达,没有被悄悄剥离/篡改"——不能再硬编码前缀断言,种子内容现在是
	// randomASCII(见 entryWithTraceBody 顶部注释)而不是 strings.Repeat("x", n)。
	originalBody := make(map[string]string, len(seed))
	for _, e := range seed {
		originalBody[e.RequestID] = e.AttemptTraces[0].ResponseBody
	}
	store.Append(seed)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.Run(ctx)
	u.Kick()

	// 熬过 slimBodyAfter()(3)好几轮,确认外面挂着的 3 条小条目从没丢过,也稳定停
	// 在旁路队列/inflight registry 里(不因为反复失败被悄悄吞掉)。间隔拉大到 250ms
	// (覆盖好几轮 ~1s 的退避周期)。Task 7 之前,"due() 摘出→HTTP 在飞→失败回推"
	// 之间有个 store+retry 两个计数都看不见在飞条目的窗口,只能靠宽限重读容忍;
	// 现在 inflight registry 精确覆盖了这段在飞窗口(track 在提交进 worker 池之前
	// 就同步完成,untrack 在失败重新入队之后才做,见 uploader.go 的
	// dispatchRetryItems/sendRetryBatch),store+retry+inflight 三者之和在任意采样点
	// 都应严格等于 3——不再需要事后宽限重读。
	pendingTotal := func() int { return store.Len() + u.RetryLen() + u.InflightCount() }
	waitFor(t, 3*time.Second, func() bool { return pendingTotal() == 3 && u.RetryLen() > 0 })
	for i := 0; i < 10; i++ {
		time.Sleep(250 * time.Millisecond)
		if got := pendingTotal(); got != 3 {
			t.Fatalf("pendingTotal() = %d, want 3 (store+retry+inflight accounting gap)", got)
		}
	}

	serverUp.Store(true)
	// 判定"全部投递完成"必须看服务端真的收到过的 ground truth(delivered map),不能看
	// store/retry 的长度归零——旁路重试一轮内会先 due() 把整批摘出队列、再逐条顺序发
	// HTTP 请求,摘出和"最后一条真正投递确认"之间有个短暂窗口,retry.Len() 可能提前
	// 归零,但其中一两条其实还没收到 2xx(甚至这一轮还失败要重新入队)。
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 3
	})
	// 到这里已经拿到了"3 条都被服务端收到过"的确凿证据(mu 保护的 map 更新发生在
	// uploadOnce 观察到 2xx 之前,构成 happens-before);此时旁路/主队列必然已经清空。
	if got := store.Len(); got != 0 {
		t.Fatalf("store.Len() = %d, want 0 after full delivery confirmed", got)
	}
	if got := u.RetryLen(); got != 0 {
		t.Fatalf("RetryLen() = %d, want 0 after full delivery confirmed", got)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"small-1", "small-2", "small-3"} {
		trace, ok := delivered[id]
		if !ok {
			t.Fatalf("entry %q never delivered", id)
		}
		if trace.ResponseBody == slimMarker {
			t.Fatalf("small entries must never be slimmed on pure network failure, got %+v", trace)
		}
		if trace.ResponseBody != originalBody[id] {
			t.Fatalf("small entry body should remain the original payload, got %q, want %q", trace.ResponseBody, originalBody[id])
		}
	}
}

// TestUploadOnceSendsGzip is the headline regression test for this change: uploadOnce
// must always gzip its request body (Content-Encoding: gzip) instead of sending plain
// JSON — the measured ~12KB/s agent uplink makes compression a large win.
func TestUploadOnceSendsGzip(t *testing.T) {
	var gotEncoding string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL)
	err := u.uploadOnce(context.Background(), []protocol.UsageLogEntry{{RequestID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotEncoding != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", gotEncoding)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gotBody))
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	var report protocol.UsageReport
	if err := json.Unmarshal(plain, &report); err != nil || len(report.Logs) != 1 {
		t.Fatalf("decompressed body invalid: %v %s", err, plain)
	}
}

// TestUploadOncePlaintextFallbackOn400 covers the deployment-order escape hatch: an
// old master that doesn't understand gzip yet rejects it with 400 — uploadOnce must
// resend the exact same payload uncompressed once, rather than treating it as a
// terminal failure.
func TestUploadOncePlaintextFallbackOn400(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := r.Header.Get("Content-Encoding")
		calls = append(calls, enc)
		if enc == "gzip" {
			w.WriteHeader(400) // 老 master 不识别 gzip
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL)
	if err := u.uploadOnce(context.Background(), []protocol.UsageLogEntry{{RequestID: "a"}}); err != nil {
		t.Fatalf("fallback should succeed: %v", err)
	}
	if len(calls) != 2 || calls[0] != "gzip" || calls[1] != "" {
		t.Fatalf("calls = %v, want [gzip, plain]", calls)
	}
}

// TestUploadOnceFallbackOnlyOnce is the boundary case: if the plaintext retry *also*
// fails, uploadOnce must not loop forever alternating encodings — it returns the error
// after exactly one fallback attempt (a genuinely bad request, not a stale master).
func TestUploadOnceFallbackOnlyOnce(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(400) // 明文也 400:真的坏请求,不能循环
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL)
	if err := u.uploadOnce(context.Background(), []protocol.UsageLogEntry{{RequestID: "a"}}); err == nil {
		t.Fatal("must return error when plaintext also fails")
	}
	if n != 2 {
		t.Fatalf("attempts = %d, want exactly 2 (gzip + plaintext)", n)
	}
}

// TestNewUsageUploader_WiringLocksConfigFuncsToPrivateFields is a Task 4 deferred-minor
// regression: UploaderConfig's four func() int fields must map to the matching private
// field, defaulting to 2/3/6/9 (concurrency/slimBodyAfter/stripTraceAfter/billingOnlyAfter)
// when left nil — a silent field swap here would fire the wrong degrade threshold at the
// wrong attempts count. Also locks newTestUploaderWithConcurrency's override wiring.
func TestNewUsageUploader_WiringLocksConfigFuncsToPrivateFields(t *testing.T) {
	store := NewMemPendingUsageStore(10, zap.NewNop())
	u, err := NewUsageUploader(UploaderConfig{
		Store: store, MasterURL: "http://127.0.0.1:0", AgentID: "agent-t", Secret: "sec-t",
		FlushInterval: time.Second, BatchMax: 2, RetryLimit: 10,
		BackoffMaxSec: func() int { return 1 }, Logger: zap.NewNop(),
		// Concurrency/SlimBodyAfterAttempts/StripTraceAfterAttempts/BillingOnlyAfterAttempts
		// deliberately left nil to exercise intFnOr's defaults.
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := u.concurrency(); got != 2 {
		t.Fatalf("concurrency() = %d, want 2", got)
	}
	if got := u.slimBodyAfter(); got != 3 {
		t.Fatalf("slimBodyAfter() = %d, want 3", got)
	}
	if got := u.stripTraceAfter(); got != 6 {
		t.Fatalf("stripTraceAfter() = %d, want 6", got)
	}
	if got := u.billingOnlyAfter(); got != 9 {
		t.Fatalf("billingOnlyAfter() = %d, want 9", got)
	}

	u2 := newTestUploaderWithConcurrency(t, "http://127.0.0.1:0", 5)
	if got := u2.concurrency(); got != 5 {
		t.Fatalf("newTestUploaderWithConcurrency concurrency() = %d, want 5", got)
	}
}

// TestCycleDrainsMainBeforeRetry is the headline scheduling-inversion regression: cycle
// must drain the main queue (fresh data) before touching the retry queue (stale, failed
// data) — a server that records arrival order sees "fresh" before "stale".
func TestCycleDrainsMainBeforeRetry(t *testing.T) {
	var mu sync.Mutex
	var order []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report protocol.UsageReport
		decodeMaybeGzip(t, r, &report)
		mu.Lock()
		for _, e := range report.Logs {
			order = append(order, e.RequestID)
		}
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL)                                             // concurrency=1 保证串行可断言顺序
	u.retry.push([]protocol.UsageLogEntry{{RequestID: "stale"}}, 1, time.Time{}) // 已到期
	u.cfg.Store.Append([]protocol.UsageLogEntry{{RequestID: "fresh"}})
	backoff := uploadBackoffBase
	u.cycle(context.Background(), &backoff)
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "fresh" || order[1] != "stale" {
		t.Fatalf("order = %v, want [fresh stale]", order)
	}
	if u.cfg.Store.Len() != 0 || u.retry.Len() != 0 {
		t.Fatal("both queues should drain")
	}
}

// TestMainQueueUploadsConcurrently: concurrency 2, two slow batches must run in parallel,
// not one after another.
//
// Proof-of-parallelism note: an earlier version of this test asserted total elapsed time
// (< 260ms, well under the 2x150ms serial sum) as the signal. That's contaminated by -race:
// marshaling+gzipping a 3MiB batch is CPU-bound and -race's shadow-memory tracking measured
// ~150-300ms per goroutine for it (vs a few ms without -race) — a debug run confirmed the
// two HTTP requests genuinely overlapped at the server (both 150ms sleeps ran concurrently),
// yet total elapsed still landed around 420-455ms under -race, above the fixed 260ms
// threshold, purely from client-side prep overhead the threshold never accounted for.
// Widening the threshold enough to tolerate that -race overhead would also let a real
// regression to serial dispatch slip through in the non-race build (serial's non-race time
// is only ~320ms, well under a race-safe threshold). So instead this asserts what "runs in
// parallel" actually means directly: track the peak number of requests the server had open
// at once and require it to reach 2 — immune to client-side CPU overhead from -race, and a
// strictly more direct signal than inferring concurrency from wall-clock elapsed time.
func TestMainQueueUploadsConcurrently(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			cur := maxInFlight.Load()
			if n <= cur || maxInFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u := newTestUploaderWithConcurrency(t, srv.URL, 2)
	// 两条各 ~3MiB 的大条目:字节预算 4MiB 会切成两个子批
	big := strings.Repeat("x", 3<<20)
	u.cfg.Store.Append([]protocol.UsageLogEntry{
		{RequestID: "a", TraceData: big}, {RequestID: "b", TraceData: big}})
	start := time.Now()
	backoff := uploadBackoffBase
	u.cycle(context.Background(), &backoff)
	if got := maxInFlight.Load(); got < 2 {
		t.Fatalf("maxInFlight = %d, want 2 (both sub-batches must overlap on the wire)", got)
	}
	// 防御性上限:哪怕 CPU 侧的 marshal/gzip 在 -race 下变慢,总耗时也不该逼近两轮
	// 串行(2x150ms sleep + 2x prep)的量级——这里给足余量,只用来兜底彻底跑飞的
	// 情况,不作为并行与否的主要判据(见上面的注释)。
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("elapsed %v unexpectedly large", elapsed)
	}
	if u.cfg.Store.Len() != 0 {
		t.Fatal("store should drain")
	}
}

// TestRetryDispatchAppliesDegradeLadder: retry entries whose attempts reach the L2/L3
// thresholds must go out already stripped, and a failed re-push must preserve the level.
func TestRetryDispatchAppliesDegradeLadder(t *testing.T) {
	var mu sync.Mutex
	got := map[string]protocol.UsageLogEntry{}
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report protocol.UsageReport
		decodeMaybeGzip(t, r, &report)
		mu.Lock()
		for _, e := range report.Logs {
			got[e.RequestID] = e
		}
		failNow := fail
		mu.Unlock()
		if failNow {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL) // 门槛用默认 3/6/9
	mk := func(id string) protocol.UsageLogEntry {
		return protocol.UsageLogEntry{RequestID: id,
			TraceData:     `{"inbound_body":"big"}`,
			FallbackChain: []models.AttemptRecord{{Seq: 1}}}
	}
	u.retry.push([]protocol.UsageLogEntry{mk("l2")}, 6, time.Time{})
	u.retry.push([]protocol.UsageLogEntry{mk("l3")}, 9, time.Time{})
	backoff := uploadBackoffBase
	u.cycle(context.Background(), &backoff)

	mu.Lock()
	if e := got["l2"]; e.TraceData != "" || len(e.FallbackChain) != 1 {
		t.Fatalf("l2 not strip-trace degraded: %+v", e)
	}
	if e := got["l3"]; e.TraceData != "" || e.FallbackChain != nil {
		t.Fatalf("l3 not billing-only degraded: %+v", e)
	}
	fail = false
	mu.Unlock()

	// 失败回队后级别保留:直接看队列快照
	for _, it := range u.retry.snapshotTop(10) {
		switch it.entry.RequestID {
		case "l2":
			if it.degrade != DegradeStripTrace {
				t.Fatalf("l2 degrade lost on re-push: %d", it.degrade)
			}
		case "l3":
			if it.degrade != DegradeBillingOnly {
				t.Fatalf("l3 degrade lost on re-push: %d", it.degrade)
			}
		}
	}
}

// TestRetryFlightTrackedInInflight: a retry entry that due() has already pulled out of
// the queue must be visible via the inflight registry while its upload is in flight —
// the queue no longer holds it, so nothing else would cover it in a snapshot.
func TestRetryFlightTrackedInInflight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL)
	u.retry.push([]protocol.UsageLogEntry{{RequestID: "flying"}}, 1, time.Time{})
	go func() {
		backoff := uploadBackoffBase
		u.cycle(context.Background(), &backoff)
	}()
	<-entered
	ents := u.inflightEntries()
	if len(ents) != 1 || ents[0].RequestID != "flying" {
		t.Fatalf("inflight = %+v, want [flying]", ents)
	}
	if u.InflightCount() != 1 {
		t.Fatalf("InflightCount = %d, want 1", u.InflightCount())
	}
	close(release)
}

// TestMainFailureMovesToRetryAndSignalsBackoff: a main-queue failure still goes out via
// push-then-Ack — the batch ends up in the retry queue, vanishes from the store, and
// cycle reports "there was a failure" so Run knows to back off.
func TestMainFailureMovesToRetryAndSignalsBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	u := newTestUploader(t, srv.URL)
	u.cfg.Store.Append([]protocol.UsageLogEntry{{RequestID: "a"}})
	backoff := uploadBackoffBase
	failed := u.cycle(context.Background(), &backoff)
	if !failed {
		t.Fatal("cycle must report failure for backoff")
	}
	if u.cfg.Store.Len() != 0 || u.retry.Len() != 1 {
		t.Fatalf("store=%d retry=%d, want 0/1", u.cfg.Store.Len(), u.retry.Len())
	}
	if u.LastError() == "" {
		t.Fatal("LastError must be recorded")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
