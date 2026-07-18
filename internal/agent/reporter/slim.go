// internal/agent/reporter/slim.go
package reporter

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// slimThresholdBytes 是"值得剥离 trace body"的门槛:只有单条 marshal 后超过这个体积,
// 才可能是把上传拖到超时、卡死队头的元凶。门槛须高于单字段 trace 上限的常见配置(如 640KB),
// 否则纯网络故障时 batch 里那些远小于这个阈值的正常条目也会被误伤;2MiB 以内在体积缩放超时
// (30s+30s/MiB)下传输无压力,真毒条目都是 4MiB+ 的多 attempt 大快照,见 slimOversizedEntries
// 的"一个都没超阈值就不剥离"。
const slimThresholdBytes = 2 << 20 // 2MiB

// slimMarker 替换掉被剥离的 body 字段:字段还在,但内容不再是原始 payload,
// 一眼能看出这条日志被瘦身过而不是本来就没数据。
const slimMarker = "[trimmed: oversized entry exceeded upload budget after repeated failures]"

// slimEntry 剥离单条日志的 trace body 字段,只留 header/路径/状态码/账单等其它字段。
//
// 调用方必须保证 e 指向的是 PeekBatch 返回的拷贝而非 store 内部数据本身——
// AttemptTraces 是 slice,浅拷贝只复制了 slice header、底层数组仍与 store 共享,
// 这里会先克隆一份底层数组再改字段,不会污染 store 里仍要重试的原始条目
// (对应 uploader.go 里"操作 peeked 副本,不需要 store 侧的可变 API"的设计)。
func slimEntry(e *protocol.UsageLogEntry) {
	if len(e.AttemptTraces) > 0 {
		traces := make([]models.UsageLogTrace, len(e.AttemptTraces))
		copy(traces, e.AttemptTraces)
		for i := range traces {
			traces[i].InboundBody = slimMarker
			traces[i].OutboundBody = slimMarker
			traces[i].ResponseBody = slimMarker
			traces[i].ClientResponseBody = slimMarker
		}
		e.AttemptTraces = traces
	}

	if e.TraceData == "" {
		return
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(e.TraceData), &blob); err != nil {
		// legacy 单次 trace blob 格式已经解不出来,没法安全地只摘 body key,直接清空——
		// 它只是诊断用的旧字段,计费数据全在 UsageLogEntry 顶层其它字段里,不受影响。
		e.TraceData = ""
		return
	}
	for k := range blob {
		if strings.Contains(strings.ToLower(k), "body") {
			delete(blob, k)
		}
	}
	if b, err := json.Marshal(blob); err == nil {
		e.TraceData = string(b)
	} else {
		e.TraceData = ""
	}
}

// slimOversizedEntries 剥离 batch 里"个头大到可能是传不动的元凶"的条目——只处理
// marshal 后超过 slimThresholdBytes 的条目。如果一个都没超阈值(说明这是纯网络故障,
// 不是某条巨型 trace 卡住了队头),原样返回、什么都不剥离,避免误伤小条目的诊断数据。
//
// 输入 batch 不会被就地修改(返回的是新 slice);输出用于本次上传尝试,不影响 store。
func slimOversizedEntries(batch []protocol.UsageLogEntry, logger *zap.Logger) []protocol.UsageLogEntry {
	type oversizedEntry struct {
		idx    int
		id     string
		before int
	}
	var targets []oversizedEntry
	for i, e := range batch {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if len(b) > slimThresholdBytes {
			targets = append(targets, oversizedEntry{idx: i, id: e.RequestID, before: len(b)})
		}
	}
	if len(targets) == 0 {
		return batch
	}

	out := make([]protocol.UsageLogEntry, len(batch))
	copy(out, batch)

	ids := make([]string, 0, len(targets))
	bytesBefore, bytesAfter := 0, 0
	for _, target := range targets {
		slimEntry(&out[target.idx])
		after := target.before
		if b, err := json.Marshal(out[target.idx]); err == nil {
			after = len(b)
		}
		ids = append(ids, target.id)
		bytesBefore += target.before
		bytesAfter += after
	}
	logger.Warn("slimming oversized usage entries after repeated upload failures",
		zap.Strings("request_ids", ids),
		zap.Int("count", len(ids)),
		zap.Int("bytes_before", bytesBefore),
		zap.Int("bytes_after", bytesAfter))
	return out
}

// uploadTimeoutFor 按 body 体积算出这次上传该给多长的超时:基础 30s,每凑满 1MiB
// 再加 30s,封顶 5 分钟——固定 30s 会掐死跨区上传的大批次(即便不再是 poison batch,
// 正常的大 batch 也可能真的需要更久才能传完),但也不能让单次请求无限期挂着。
const (
	uploadTimeoutBase   = 30 * time.Second
	uploadTimeoutPerMiB = 30 * time.Second
	uploadTimeoutCap    = 5 * time.Minute
)

func uploadTimeoutFor(bodyLen int) time.Duration {
	mib := bodyLen / (1 << 20)
	timeout := uploadTimeoutBase + time.Duration(mib)*uploadTimeoutPerMiB
	if timeout > uploadTimeoutCap {
		timeout = uploadTimeoutCap
	}
	return timeout
}

// 降级级别:挂在 retryItem 上的持久阶梯(L1 例外,发送时现算不落条目,见 spec §5)。
const (
	DegradeNone        = 0
	DegradeSlimBody    = 1
	DegradeStripTrace  = 2
	DegradeBillingOnly = 3
)

// degradeMarkers 落进 Other JSON 的 degrade 标记值;落库后 logs 页可识别这条被降过级。
var degradeMarkers = map[int]string{
	DegradeStripTrace:  "trace_stripped",
	DegradeBillingOnly: "billing_only",
}

// degradeSteps 是各级别的剥离动作策略表;applyDegrade 从当前级别的下一级逐级执行到
// 目标级别,保证 L3 蕴含 L2 的全部剥离。
var degradeSteps = map[int]func(*protocol.UsageLogEntry){
	DegradeStripTrace: func(e *protocol.UsageLogEntry) {
		e.AttemptTraces = nil
		e.TraceData = ""
	},
	DegradeBillingOnly: func(e *protocol.UsageLogEntry) {
		e.FallbackChain = nil
	},
}

// applyDegrade 把 e 就地推到 level:只升不降、幂等;当前级别从 Other 的 degrade
// 标记反推(条目本身不带级别字段,持久级别由 retryItem 记录,这里只管剥离动作)。
func applyDegrade(e *protocol.UsageLogEntry, level int) {
	if level < DegradeStripTrace {
		return // L0/L1 无就地动作
	}
	if level > DegradeBillingOnly {
		level = DegradeBillingOnly
	}
	for l := DegradeStripTrace; l <= level; l++ {
		if step, ok := degradeSteps[l]; ok {
			step(e)
		}
	}
	markDegrade(e, degradeMarkers[level])
}

// markDegrade 把 degrade 标记合并进 Other(JSON object);Other 解析失败(裸文本/
// 损坏)时用全新对象替换——标记的可见性优先于保住一段本来就坏掉的字段。
func markDegrade(e *protocol.UsageLogEntry, marker string) {
	blob := map[string]any{}
	if e.Other != "" {
		if err := json.Unmarshal([]byte(e.Other), &blob); err != nil {
			blob = map[string]any{}
		}
	}
	if blob == nil {
		blob = map[string]any{} // json "null" 解析成功但给 nil map,同 hub.go 的已知坑
	}
	if existing, ok := blob["degrade"].(string); ok && existing == degradeMarkers[DegradeBillingOnly] {
		return // 已是最高级标记,不回写低级
	}
	blob["degrade"] = marker
	if b, err := json.Marshal(blob); err == nil {
		e.Other = string(b)
	}
}
