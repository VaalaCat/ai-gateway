// internal/agent/reporter/uploader.go
package reporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc/pool"
	"go.uber.org/zap"
)

const uploadBackoffBase = time.Second

// uploadBatchByteBudget 是单次上传请求体的软预算上限(poison batch 兜底)。
//
// PeekBatch(BatchMax) 只按“条数”限流,但 UsageLogEntry 可以携带体积很大的 trace payload
// (TraceData 受 trace_max_body_size 管控,默认 64KiB、管理员可调到 16MiB;还有 AttemptTraces
// 里逐候选的 trace body)。如果队列里堆积了几条大 trace 的日志,PeekBatch 拼出来的一批 marshal
// 后可能超过 master 摄取端点的 10MiB MaxBytesReader 上限(internal/master/sync/usage_http.go 的
// usageIngestMaxBodyBytes),master 直接 400。uploader 把 400 当普通失败退避重试——但 PeekBatch
// 永远返回队头那一批,同一批会被原样重试到天荒地老,投递永久卡死("poison batch")。
//
// 这里按“累计 marshal 字节数”在 uploader 侧提前切一刀,预算定得比服务端上限宽松得多
// (4MiB vs 10MiB),给 UsageReport 外壳和其它字段的开销留足余量。
//
// 已知局限:切分保证“至少含 1 条”——哪怕单条本身已经超预算也必须尝试送出去(不能砍成 0 条,
// 那样反而彻底卡死)。如果管理员把 trace_max_body_size 调到 ~5MiB 以上,单条日志自己就可能
// 超过服务端 10MiB 上限,这种情况这里管不了,需要管理员按 trace 体积自行把上限设在合理范围。
const uploadBatchByteBudget = 4 << 20 // 4MiB

type UploaderConfig struct {
	Store         PendingUsageStore
	MasterURL     string
	AgentID       string
	Secret        string
	FlushInterval time.Duration
	BatchMax      int
	// RetryLimit 是旁路重试队列(retryQueue)的容量,语义同 Store 的有界丢最老——
	// <=0 时回退到 defaultRetryLimit,避免调用方忘记设置导致重试队列变成 0 容量的
	// 黑洞(推进去立刻当溢出丢掉)。
	RetryLimit               int
	BackoffMaxSec            func() int // 每次退避时读(settings 热更)
	Concurrency              func() int // usage_upload_concurrency
	SlimBodyAfterAttempts    func() int // L1 门槛(取代 slimAfterConsecutiveFailures 常量)
	StripTraceAfterAttempts  func() int // L2 门槛
	BillingOnlyAfterAttempts func() int // L3 门槛
	Logger                   *zap.Logger
}

// defaultRetryLimit 是 RetryLimit 未设置(<=0)时的兜底容量。
const defaultRetryLimit = 1000

// intFnOr 返回 fn,如果 fn 为 nil 则返回一个返回 def 的常数函数。
// 用于 UploaderConfig 字段的兜底(防止 nil 调用)。
func intFnOr(fn func() int, def int) func() int {
	if fn == nil {
		return func() int { return def }
	}
	return fn
}

// UsageUploader 是数据面投递器:PeekBatch → POST /api/agents/usage → 2xx 才 Ack。
// 单协程消费(Run);Kick 触发即时 flush。失败的批次会挪进旁路 retry 队列单独
// 退避重试,主队列(Store)继续往前流,不再被一条卡住的毒条目拖住所有人
// (见 Run/drainMainQueue/processRetryQueue)。
type UsageUploader struct {
	cfg              UploaderConfig
	client           *http.Client
	url              string
	kick             chan struct{}
	retry            *retryQueue
	concurrency      func() int
	slimBodyAfter    func() int
	stripTraceAfter  func() int
	billingOnlyAfter func() int

	// inflightRetry 登记"已经被 due() 摘出旁路队列、还没确认送达/失败"的条目——
	// 这些条目此刻既不在 store 里也不在 retry 队列里,快照(Task 8)/pending 总数
	// 必须靠这张表才能覆盖到它们,见 dispatchRetryItems/sendRetryBatch 的登记时机。
	inflightMu    sync.Mutex
	inflightSeq   int64
	inflightRetry map[int64][]protocol.UsageLogEntry
	// mainInflight 只是计数:主队列在飞条目全程留在 store 里,不需要额外登记内容,
	// 这里仅用于 InflightCount() 报出"当前有多少条正在传输中"。
	mainInflight atomic.Int64

	lastSuccessAt atomic.Int64
	lastErr       atomic.Value // string

	// drainDone 在 Run 退出(含关停收尾)后关闭——Task 8 的磁盘快照器等它保证不会
	// 在 Run 还在写 store/retry 的时候读到半途状态。
	drainDone      chan struct{}
	finalDrainDone chan struct{}
	finalDrainOnce sync.Once
}

func NewUsageUploader(cfg UploaderConfig) (*UsageUploader, error) {
	client, target, err := netaddr.MasterHTTPTarget(cfg.MasterURL, "/api/agents/usage")
	if err != nil {
		return nil, fmt.Errorf("resolve usage upload target: %w", err)
	}
	// 固定 30s 会掐死大批次的跨区上传(多 MiB 的 trace body 传输本身就可能超过 30s)。
	// 超时改为在 uploadOnce 里按本次请求体积逐请求计算(见 uploadTimeoutFor),
	// 这里的 client.Timeout 留 0(不设限),真正的上限由每次请求的 context.WithTimeout 控制。
	client.Timeout = 0
	retryLimit := cfg.RetryLimit
	if retryLimit <= 0 {
		retryLimit = defaultRetryLimit
	}
	return &UsageUploader{
		cfg:              cfg,
		client:           client,
		url:              target,
		kick:             make(chan struct{}, 1),
		retry:            newRetryQueue(retryLimit, cfg.Logger),
		concurrency:      intFnOr(cfg.Concurrency, 2),
		slimBodyAfter:    intFnOr(cfg.SlimBodyAfterAttempts, 3),
		stripTraceAfter:  intFnOr(cfg.StripTraceAfterAttempts, 6),
		billingOnlyAfter: intFnOr(cfg.BillingOnlyAfterAttempts, 9),
		inflightRetry:    make(map[int64][]protocol.UsageLogEntry),
		drainDone:        make(chan struct{}),
		finalDrainDone:   make(chan struct{}),
	}, nil
}

func (u *UsageUploader) Kick() {
	select {
	case u.kick <- struct{}{}:
	default:
	}
}

func (u *UsageUploader) CloseIdleConnections() {
	if u != nil && u.client != nil {
		u.client.CloseIdleConnections()
	}
}

// RetryLen 是旁路重试队列里还有多少条待重试——Reporter.PendingCount 靠它把
// "挪到旁路里的条目"也算进总的未送达计数,而不是让 Store.Len() 单独失真变小。
func (u *UsageUploader) RetryLen() int { return u.retry.Len() }

// Run 是唤醒后调 cycle 的薄循环:cycle 失败(主队列本轮有失败)就按原来的节奏
// 退避 sleep 一轮再回 select,成功则把 backoff 重置回基线——退避节奏本身跟以前
// 一样,只是"发送"这部分现在挪进 cycle/drainMainQueue/processRetryQueue 做并发。
func (u *UsageUploader) Run(ctx context.Context) {
	defer close(u.drainDone)
	ticker := time.NewTicker(u.cfg.FlushInterval)
	defer ticker.Stop()
	backoff := uploadBackoffBase
	for {
		select {
		case <-ctx.Done():
			u.drainOnShutdown(ctx)
			return
		case <-ticker.C:
		case <-u.kick:
		}
		if u.cycle(ctx, &backoff) {
			select {
			case <-ctx.Done():
				u.drainOnShutdown(ctx)
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if max := time.Duration(u.cfg.BackoffMaxSec()) * time.Second; backoff > max {
				backoff = max
			}
			continue
		}
		backoff = uploadBackoffBase
	}
}

// cycle 是一次唤醒周期:主队列先(新鲜数据优先),旁路殿后(失败货排队尾)——
// 这正是"失败移动到队尾"的调度语义,测试直接调用它来断言这个顺序。返回主队列
// 本周期是否出现失败(驱动 Run 的周期退避)。
func (u *UsageUploader) cycle(ctx context.Context, backoff *time.Duration) bool {
	failed := u.drainMainQueue(ctx)
	u.processRetryQueue(ctx)
	return failed
}

// drainMainQueue 排空一次主队列(Store):PeekBatch 一次,按字节预算切成至多
// concurrency() 个子批并行发送。成功的直接 Ack;失败的整批 push-then-Ack 挪进
// 旁路 retry 队列,主队列继续往前流,不再被同一条卡住(毒条目隔离)。主队列在飞
// 条目全程留在 store 里(成功才 Ack / 失败挪旁路后才 Ack),快照天然覆盖,不需要
// 进 inflight registry,mainInflight 只是给 InflightCount() 报数用的计数器。
// 一轮里只要有子批失败就停手,退避 sleep 交给 Run;剩余条目下个周期继续。
func (u *UsageUploader) drainMainQueue(ctx context.Context) bool {
	n := u.concurrency()
	if n < 1 {
		n = 1
	}
	anyFailed := false
	for u.cfg.Store.Len() > 0 && ctx.Err() == nil {
		peek := u.cfg.Store.PeekBatch(u.cfg.BatchMax)
		var batches [][]protocol.UsageLogEntry
		for len(peek) > 0 && len(batches) < n {
			b := trimBatchToByteBudget(peek, uploadBatchByteBudget)
			batches = append(batches, b)
			peek = peek[len(b):]
		}
		var mu sync.Mutex
		roundFailed := false
		p := pool.New().WithMaxGoroutines(n)
		for _, batch := range batches {
			p.Go(func() {
				u.mainInflight.Add(int64(len(batch)))
				defer u.mainInflight.Add(-int64(len(batch)))
				if err := u.uploadOnce(ctx, batch); err != nil {
					now := time.Now()
					// 先入旁路队列再从主队列摘除——中间崩溃留下的是"两边都有"(重发
					// 经 master request_id 去重吸收),而不是"两边都无"(丢失)。
					u.retry.push(batch, 1, now.Add(u.retryBackoff(1)))
					u.cfg.Store.Ack(batch)
					u.recordError(err)
					// ④ 诊断打点:投递失败,批次挪去旁路重试,数据不丢
					u.cfg.Logger.Warn("moving failed batch to retry queue",
						zap.Strings("request_ids", requestIDs(batch)), zap.Int("batch_size", len(batch)),
						zap.Int("pending", u.cfg.Store.Len()), zap.Int("retry_pending", u.retry.Len()),
						zap.Error(err))
					mu.Lock()
					roundFailed = true
					mu.Unlock()
					return
				}
				u.cfg.Store.Ack(batch)
				u.markSuccess()
			})
		}
		p.Wait()
		if roundFailed {
			anyFailed = true
			break // 本周期到此为止,退避交给 Run;剩余条目下周期继续
		}
	}
	return anyFailed
}

// processRetryQueue 在每次唤醒时、主队列排空之后,尝试一遍旁路队列里已到期的
// 条目:attempts>=retryIsolateAfterAttempts 的条目各自单独成批发送(毒隔离——
// 一条屡次失败的坏条目,单独发再也连累不到任何邻居);attempts 还小的条目仍可以
// 拼在一起按字节预算发送。失败的各自重新 push 回旁路队列,attempts+1、退避相应
// 拉长;送成功的已经被 due() 摘出队列,什么都不用做。
func (u *UsageUploader) processRetryQueue(ctx context.Context) {
	items := u.retry.due(time.Now(), u.cfg.BatchMax)
	if len(items) == 0 {
		return
	}
	u.dispatchRetryItems(ctx, items, func(batch []retryItem, err error) {
		// behavior change: 修复陈旧 now 导致的退避坍缩 —— 在失败真正发生的这一刻
		// 取 now,而不是复用 dispatch 前捕获的旧时间戳;并发子批各自的失败可能落在
		// 明显不同的时刻,复用一个旧 now 会把它们的退避错误地压缩到同一个基准点。
		failedAt := time.Now()
		attemptsAfter := make([]int, len(batch))
		for i := range batch {
			it := batch[i]
			it.attempts++
			it.nextAt = failedAt.Add(u.retryBackoff(it.attempts))
			attemptsAfter[i] = it.attempts
			u.retry.pushItem(it) // pushItem 保住 degrade/bytes,不能用 push() 清零重来
		}
		u.cfg.Logger.Warn("retry upload failed, backing off item(s)",
			zap.Strings("request_ids", retryItemIDs(batch)), zap.Ints("attempts", attemptsAfter), zap.Error(err))
	})
}

// retryFailureHandler 是一次旁路批次投递失败后的收尾动作:processRetryQueue 的
// 正常路径要把失败条目重新 push 回队列(attempts+1、新的 nextAt);
// drainRetryOnShutdown 的关机收尾路径等不起退避,失败就地放弃、只留日志。
type retryFailureHandler func(batch []retryItem, err error)

// dispatchRetryItems 把一批到期的旁路条目按 attempts 升序排序后分组(年轻优先):
// attempts 已经达到隔离线的各自单独发送;还年轻的凑在一起按字节预算发送。所有
// 小批(隔离单条 + young 拼批)提交进同一个 pool.New().WithMaxGoroutines(n) 并发
// 发送。
//
// 每一小批在提交进池子之前,先做完持久降级(applyDegrade,就地改 batch 里的
// retryItem)和 L1 送时瘦身、再同步登记进 inflight registry——这一步必须在
// p.Go 之前完成,不能拖到 goroutine 真正跑起来才登记:池子并发度不够时,批次会
// 在这里排队等 worker,如果登记推迟到 goroutine 内部,排队等待的这段时间条目就
// 会"既不在旁路队列里(due() 已摘出)也不在 registry 里",快照/严格 pending 断
// 言会漏记(欠冲不可接受,见 sendRetryBatch 的收尾顺序注释)。
func (u *UsageUploader) dispatchRetryItems(ctx context.Context, items []retryItem, onFailure retryFailureHandler) {
	sorted := make([]retryItem, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].attempts < sorted[j].attempts })

	var young, poisonCandidates []retryItem
	for _, it := range sorted {
		if it.attempts >= retryIsolateAfterAttempts {
			poisonCandidates = append(poisonCandidates, it)
		} else {
			young = append(young, it)
		}
	}

	var batches [][]retryItem
	for len(young) > 0 {
		entries := retryEntries(young)
		trimmed := trimBatchToByteBudget(entries, uploadBatchByteBudget)
		batches = append(batches, young[:len(trimmed)])
		young = young[len(trimmed):]
	}
	for _, it := range poisonCandidates {
		batches = append(batches, []retryItem{it})
	}

	n := u.concurrency()
	if n < 1 {
		n = 1
	}
	p := pool.New().WithMaxGoroutines(n)
	for _, batch := range batches {
		entries := u.degradeAndSlimForSend(batch)
		id := u.trackInflight(entries)
		p.Go(func() { u.sendRetryBatch(ctx, id, entries, batch, onFailure) })
	}
	p.Wait()
}

// degradeAndSlimForSend 就地把 batch 里每条按 attempts 对照阶梯门槛做持久降级
// (billingOnlyAfter()→L3、stripTraceAfter()→L2;mutates batch[i].entry/degrade/
// bytes,失败重新入队后级别保留),再判断是否需要 L1 送时瘦身(slimBodyAfter() +
// slimOversizedEntries 的 >2MiB 门槛;不落 retryItem,只影响这次发送的副本)。
// 返回本次实际要发送、也是 inflight registry 要登记的 entries。
func (u *UsageUploader) degradeAndSlimForSend(batch []retryItem) []protocol.UsageLogEntry {
	for i := range batch {
		it := &batch[i]
		target := DegradeNone
		switch {
		case it.attempts >= u.billingOnlyAfter():
			target = DegradeBillingOnly
		case it.attempts >= u.stripTraceAfter():
			target = DegradeStripTrace
		}
		if target > it.degrade {
			before := it.bytes
			applyDegrade(&it.entry, target)
			it.degrade = target
			it.bytes = entrySize(it.entry)
			u.cfg.Logger.Warn("degrading usage entry after repeated failures",
				zap.String("request_id", it.entry.RequestID), zap.Int("level", target),
				zap.Int("attempts", it.attempts), zap.Int("bytes_before", before), zap.Int("bytes_after", it.bytes))
		}
	}
	entries := retryEntries(batch)
	for _, it := range batch {
		if it.attempts >= u.slimBodyAfter() {
			return slimOversizedEntries(entries, u.cfg.Logger)
		}
	}
	return entries
}

// sendRetryBatch 投递一小批已经完成降级/瘦身的旁路条目。entries 是即将上线的
// 副本(degradeAndSlimForSend 算好的),batch 是失败时需要重新入队的 retryItem
// (降级级别已经就地改好)。id 对应的 inflight 登记在提交进池子之前就已经完成
// (见 dispatchRetryItems),这里只管上传和收尾的 untrack。
//
// 收尾顺序刻意安排成 track→upload→{成功: untrack}/{失败: onFailure 把条目重新
// 入队之后再 untrack}——绝不能先 untrack 再 onFailure,那样会有一个条目"既不在
// 旁路队列、也不在 inflight registry"的空档;失败路径下 onFailure 重新入队和
// untrack 之间短暂的双计(retry 队列 + registry 都算到)是可接受的过冲,漏记的
// 欠冲不可接受。
func (u *UsageUploader) sendRetryBatch(ctx context.Context, id int64, entries []protocol.UsageLogEntry, batch []retryItem, onFailure retryFailureHandler) {
	err := u.uploadOnce(ctx, entries)
	if err != nil {
		u.recordError(err)
		onFailure(batch, err)
		u.untrackInflight(id)
		return
	}
	u.untrackInflight(id)
	u.markSuccess()
}

// markSuccess/recordError/LastSuccessAt/LastError 是运行态的读写口——心跳/管理
// 看板靠它们判断"上一次成功是什么时候""最近一次失败长什么样",不需要额外轮询
// store/retry 的内容。
func (u *UsageUploader) markSuccess() { u.lastSuccessAt.Store(time.Now().Unix()) }

func (u *UsageUploader) recordError(err error) { u.lastErr.Store(err.Error()) }

// LastSuccessAt 返回上一次投递成功的 unix 秒时间戳,从未成功过返回 0。
func (u *UsageUploader) LastSuccessAt() int64 { return u.lastSuccessAt.Load() }

// LastError 返回最近一次投递失败的错误文本,从未失败过返回空字符串。
func (u *UsageUploader) LastError() string {
	if v, ok := u.lastErr.Load().(string); ok {
		return v
	}
	return ""
}

// trackInflight 把一批已经被 due() 摘出旁路队列、即将上传的 entries 登记进
// registry,返回登记用的 id(untrackInflight 用它精确摘除这一批,不影响同时在飞
// 的其它批次)。
func (u *UsageUploader) trackInflight(entries []protocol.UsageLogEntry) int64 {
	u.inflightMu.Lock()
	defer u.inflightMu.Unlock()
	u.inflightSeq++
	u.inflightRetry[u.inflightSeq] = entries
	return u.inflightSeq
}

// untrackInflight 撤销 trackInflight 的登记——批次已经确认成功,或者失败已经
// 重新 push 回旁路队列(调用方必须保证这个顺序,见 sendRetryBatch 的收尾注释)。
func (u *UsageUploader) untrackInflight(id int64) {
	u.inflightMu.Lock()
	defer u.inflightMu.Unlock()
	delete(u.inflightRetry, id)
}

// inflightEntries 返回当前所有旁路在飞条目(仅旁路——主队列在飞条目全程留在
// store 里,快照/心跳走 store 就已经覆盖,不用在这里重复列出)。
func (u *UsageUploader) inflightEntries() []protocol.UsageLogEntry {
	u.inflightMu.Lock()
	defer u.inflightMu.Unlock()
	var out []protocol.UsageLogEntry
	for _, ents := range u.inflightRetry {
		out = append(out, ents...)
	}
	return out
}

// InflightCount 是主队列在飞条数(mainInflight,条目仍在 store 里,只计数)加上
// 旁路在飞条数(inflightRetry registry,条目已经不在任何队列里)。
func (u *UsageUploader) InflightCount() int {
	return u.retryInflightCount() + int(u.mainInflight.Load())
}

func (u *UsageUploader) retryInflightCount() int {
	u.inflightMu.Lock()
	n := 0
	for _, ents := range u.inflightRetry {
		n += len(ents)
	}
	u.inflightMu.Unlock()
	return n
}

// DrainDone 在 Run 退出(含关停收尾 drainOnShutdown 跑完)后关闭,供 Task 8 的
// 磁盘快照器等待"确定不会再有并发写入"再做最后一次落盘。
func (u *UsageUploader) DrainDone() <-chan struct{} { return u.drainDone }

func (u *UsageUploader) FinalDrainDone() <-chan struct{} { return u.finalDrainDone }

func (u *UsageUploader) FinalDrain(ctx context.Context) {
	u.finalDrainOnce.Do(func() {
		u.drainOnShutdown(ctx)
		close(u.finalDrainDone)
	})
}

// retryEntries 摘出一批 retryItem 里的 UsageLogEntry,供 uploadOnce/slim 使用。
func retryEntries(items []retryItem) []protocol.UsageLogEntry {
	out := make([]protocol.UsageLogEntry, len(items))
	for i, it := range items {
		out[i] = it.entry
	}
	return out
}

// requestIDs 摘出一批日志的 RequestID,供警告日志诊断用。
func requestIDs(batch []protocol.UsageLogEntry) []string {
	ids := make([]string, len(batch))
	for i, e := range batch {
		ids[i] = e.RequestID
	}
	return ids
}

// retryItemIDs 摘出一批 retryItem 的 RequestID,供警告日志诊断用。
func retryItemIDs(batch []retryItem) []string {
	ids := make([]string, len(batch))
	for i, it := range batch {
		ids[i] = it.entry.RequestID
	}
	return ids
}

// retryBackoff 算出旁路队列里一条已失败 attempts 次的条目,下次该等多久再单独
// 重试:指数退避(1s、2s、4s……),同 BackoffMaxSec 热更设置封顶——和主队列节流
// "多久 hit 一次 master" 用的是同一套节奏,只是这里按每条条目自己的 attempts 算,
// 互不影响。
func (u *UsageUploader) retryBackoff(attempts int) time.Duration {
	max := time.Duration(u.cfg.BackoffMaxSec()) * time.Second
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 32 { // 避免 1s<<attempts 移位溢出;这么多次早就该顶格了
		return max
	}
	backoff := uploadBackoffBase << uint(attempts)
	if backoff <= 0 || backoff > max {
		return max
	}
	return backoff
}

// drainOnShutdown 在 Run 的循环 ctx 已 Done 之后做一次尽力而为的收尾投递:
// 循环用的 ctx 已经取消,不能拿它再发 HTTP 请求,所以用一个全新的短超时
// context。先持续 PeekBatch/uploadOnce/Ack 排空主队列,直到排空、遇到第一次
// 失败,或这个新 context 到期——三者谁先到都立即停手,不无限重试拖慢进程退出;
// 主队列收尾完再尽力对旁路队列里的全部条目各投一次(见 drainRetryOnShutdown)。
func (u *UsageUploader) drainOnShutdown(ctx context.Context) {
	for u.cfg.Store.Len() > 0 {
		batch := u.cfg.Store.PeekBatch(u.cfg.BatchMax)
		batch = trimBatchToByteBudget(batch, uploadBatchByteBudget)
		u.mainInflight.Add(int64(len(batch)))
		err := u.uploadOnce(ctx, batch)
		u.mainInflight.Add(-int64(len(batch)))
		if err != nil {
			u.cfg.Logger.Warn("final drain on shutdown failed, giving up",
				zap.Int("pending", u.cfg.Store.Len()), zap.Error(err))
			break
		}
		u.cfg.Store.Ack(batch)
	}
	// 主队列收尾无论是排空成功还是中途放弃,旁路重试队列都必须走一遍——否则
	// master 挂掉时旁路里还没到期的条目会随着进程退出悄悄消失,连一条日志都
	// 不会留下(此前的实现在主队列失败时直接 return,drainRetryOnShutdown 根本
	// 没机会跑)。
	u.drainRetryOnShutdown(ctx)

	if u.cfg.Store.Len() > 0 || u.retry.Len() > 0 {
		// Task 8 的磁盘快照器(Snapshotter.Run)在 ctx.Done 之后会等这次 drain 收尾、
		// 再对这里剩下的条目做一次最终 WriteNow;drainRetryOnShutdown 失败时会把
		// 条目重新 push 回 u.retry(见其内部注释),所以下面这两个计数精确反映了
		// 最终快照即将落盘覆盖的内容——真正的丢失只发生在那次最终快照本身也写
		// 失败的时候(由 Snapshotter.Run 自己的 error 日志负责),所以这里降级为
		// warn,措辞也从"abandoning"改成"留给最终快照兜底"。
		u.cfg.Logger.Warn("shutdown drain left pending usage in memory, final snapshot will cover the leftovers",
			zap.Int("store_pending", u.cfg.Store.Len()), zap.Int("retry_pending", u.retry.Len()))
	}
}

// drainRetryOnShutdown 尽力而为地把旁路队列里所有条目(无视 nextAt)各投一次。
// 失败的条目重新 push 回旁路队列——不是为了在这个进程里再退避重试(马上要退出
// 了,等不起),而是让 Task 8 的最终快照(Snapshotter.Run 在 DrainDone 之后做的
// WriteNow)能看得见它们、把它们落盘,不然关机这一刻 master 恰好短暂不可达
// (比如协调发布重启 master),这批条目就会既不在 store、也不在 retry 队列、
// 也不在 inflight,凭空消失。
func (u *UsageUploader) drainRetryOnShutdown(ctx context.Context) {
	items := u.retry.drainAll()
	if len(items) == 0 {
		return
	}
	u.dispatchRetryItems(ctx, items, func(batch []retryItem, err error) {
		// behavior change: 关停失败不再就地放弃——最终快照(Task 8)会把留在队列里
		// 的条目落盘,重新入队正是让快照看得见它们。attempts/degrade 原样保留(不
		// 像 processRetryQueue 那样 +1),nextAt 也不重算——进程马上退出,这个值
		// 不会被用到,Restore 时会按 attempts 重新算一个可用的 nextAt。
		for _, it := range batch {
			u.retry.pushItem(it)
		}
		u.cfg.Logger.Warn("final retry-queue drain on shutdown failed, re-queued for final snapshot",
			zap.Strings("request_ids", retryItemIDs(batch)), zap.Error(err))
	})
}

// trimBatchToByteBudget 把 batch 按累计 marshal 字节数砍到 budget 以内,但至少保留 1 条——
// 哪怕队首那一条自己已经超预算,也要作为单条 batch 尝试投递,总好过整批一条都不发。
// 被砍掉的尾部条目仍留在 store 里,下一次循环的 PeekBatch(Store.Len()>0 已经保证会有
// 下一次)会再捞到它们。
func trimBatchToByteBudget(batch []protocol.UsageLogEntry, budget int) []protocol.UsageLogEntry {
	if len(batch) <= 1 {
		return batch
	}
	total := 0
	for i, e := range batch {
		size := 0
		if b, err := json.Marshal(e); err == nil {
			size = len(b)
		}
		if i > 0 && total+size > budget {
			return batch[:i]
		}
		total += size
	}
	return batch
}

// uploadOnce marshals batch and posts it gzip-compressed (the measured ~12KB/s agent
// uplink makes compression a large win). Old masters that don't yet decompress gzip
// reject it with 400/415 (see postReport's Content-Encoding handling on the receiving
// end — a deployment-order concern, not a data problem); in that case the exact same
// plaintext bytes are resent uncompressed exactly once. A plaintext retry that also
// fails non-2xx is a genuinely bad request, not a stale master, so it is not retried
// again here — the caller's normal batch-level retry/degrade path takes over.
func (u *UsageUploader) uploadOnce(ctx context.Context, batch []protocol.UsageLogEntry) error {
	plain, err := json.Marshal(protocol.UsageReport{AgentID: u.cfg.AgentID, Logs: batch})
	if err != nil {
		return err
	}
	status, err := u.postReport(ctx, plain, true)
	if err == nil && (status == http.StatusBadRequest || status == http.StatusUnsupportedMediaType) {
		u.cfg.Logger.Warn("gzip upload rejected, falling back to plaintext once", zap.Int("status", status))
		status, err = u.postReport(ctx, plain, false)
	}
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("usage ingest returned %d", status)
	}
	return nil
}

// postReport sends plain once, optionally gzip-compressed, and returns the HTTP status
// code; network-level errors (no response at all) are returned as err instead. The
// per-request timeout (uploadTimeoutFor) is sized off the actual bytes going out on the
// wire — compressed when useGzip succeeds — so gzip'd uploads aren't saddled with a
// timeout budget computed for the much larger uncompressed body.
func (u *UsageUploader) postReport(ctx context.Context, plain []byte, useGzip bool) (int, error) {
	body := plain
	encoding := ""
	if useGzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(plain); err == nil && zw.Close() == nil {
			body = buf.Bytes()
			encoding = "gzip"
		} // 压缩失败极罕见:静默退回明文发送
	}
	// 按本次请求体积算超时(见 uploadTimeoutFor):子 context 只会让上限更短,不会更长——
	// 外层 ctx(比如 drainOnShutdown 的 5s 收尾窗口)仍然管着总的上限。
	ctx, cancel := context.WithTimeout(ctx, uploadTimeoutFor(len(body)))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	req.Header.Set(consts.HeaderXAgentID, u.cfg.AgentID)
	req.Header.Set(consts.HeaderXAgentSecret, u.cfg.Secret)
	resp, err := u.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// 读干净 body 才能让底层连接被 keep-alive 复用,否则每次都新建连接。
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
