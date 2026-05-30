package publish

import (
	"encoding/json"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// attachTraceData 把 Recorder 状态收成 UsageLogEntry 上的字段。
//
// 轻量字段（5 个 _ms 列 + error_stage）来自 Finalize() 结果，总是填。
//
// 多候选路径（recorder.Attempts() 非空）：把每条已快照的 TraceRecord 转成
// models.UsageLogTrace，append 到 e.AttemptTraces（index 即候选顺序）。
// 不写 AttemptIndex 字段——该字段由 Task 5 添加到 models；settler 按切片顺序赋值。
//
// 单候选 / 无快照兜底（Attempts() 为空）：回退到旧行为——Finalize() 单条 →
// record.HasBody() 时 JSON 序列化进 e.TraceData。保证既有单次请求行为不变。
//
// 显式接收 *Recorder 取代之前从 gin.Context 取 Recorder 的隐式 API。
func attachTraceData(e *protocol.UsageLogEntry, rec *trace.Recorder) {
	if rec == nil {
		return
	}
	// Recorder.Finalize 永不返 nil（契约，见 trace/recorder.go）—— 无需 nil 防御。
	record := rec.Finalize()

	e.ErrorStage = string(record.FailStage)
	e.InboundDecodeMs = state.MsOf(record.Timings[trace.StageInboundDecode])
	e.OutboundEncodeMs = state.MsOf(record.Timings[trace.StageOutboundEncode])
	e.UpstreamDispatchMs = state.MsOf(record.Timings[trace.StageUpstreamDispatch])
	e.UpstreamDecodeMs = state.MsOf(record.Timings[trace.StageUpstreamDecode])
	e.ClientEncodeMs = state.MsOf(record.Timings[trace.StageClientEncode])

	// 每候选快照 → UsageLogTrace。只保留 verbose（开了 trace 或该候选失败）的快照：
	// 否则关闭 trace 的成功请求每次都会落一行空 trace 并误置 has_trace=true。
	// AttemptIndex 用过滤前的真实候选序号（= 链路 seq-1），跳过空快照不打乱对齐。
	for i, a := range rec.Attempts() {
		if a == nil || !a.Verbose {
			continue
		}
		tr := traceRecordToUsageLogTrace(a)
		tr.AttemptIndex = i
		e.AttemptTraces = append(e.AttemptTraces, tr)
	}

	// TraceData = 被采纳（最后一次）attempt 的 trace，HasBody() 决定填不填。
	// 与 AttemptTraces 并存：保持既有单条 trace 契约（含老集成测试与旧消费方），
	// settler 在 AttemptTraces 非空时优先写每候选行、TraceData 仅作旧 agent 兜底，不会双写。
	if record.HasBody() {
		if b, err := record.MarshalJSON(); err == nil {
			e.TraceData = string(b)
		}
	}
}

// traceRecordToUsageLogTrace 把一条已经 mask 过的 TraceRecord 直接搬进
// models.UsageLogTrace。不做任何新的 mask 或截断——TraceRecord 字段已由
// Recorder.buildTraceRecord() 完成 mask，这里只做字段映射。
func traceRecordToUsageLogTrace(rec *trace.TraceRecord) models.UsageLogTrace {
	if rec == nil {
		return models.UsageLogTrace{}
	}
	inboundHeaders := marshalHeader(rec.InboundHeaders)
	outboundHeaders := marshalHeader(rec.OutboundHeaders)
	responseHeaders := marshalHeader(rec.ResponseHeaders)
	return models.UsageLogTrace{
		InboundPath:        rec.InboundPath,
		OutboundPath:       rec.OutboundPath,
		InboundHeaders:     inboundHeaders,
		OutboundHeaders:    outboundHeaders,
		InboundBody:        rec.InboundBody,
		OutboundBody:       rec.OutboundBody,
		ResponseHeaders:    responseHeaders,
		ResponseBody:       rec.UpstreamBody,
		ClientResponseBody: rec.ClientResponseBody,
		UpstreamStatus:     rec.UpstreamStatus,
		ErrorStage:         string(rec.FailStage),
	}
}

// marshalHeader 把 http.Header 序列化成 JSON 字符串，与 UsageLogTrace 存储约定对齐。
func marshalHeader(h http.Header) string {
	b, _ := json.Marshal(map[string][]string(h))
	return string(b)
}
