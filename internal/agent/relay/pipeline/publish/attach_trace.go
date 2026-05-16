package publish

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// attachTraceData 把 Recorder 状态收成 UsageLogEntry 上的字段。
// 轻量字段（5 个 _ms 列 + error_stage）总是填；
// TraceData JSON（含 4 body + 4 headers）仅在 record.HasBody() 为 true 时填，
// 即 TraceEnabled=true 或请求失败时（见 design §3.1 verbose 规则）。
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

	if record.HasBody() {
		if b, err := record.MarshalJSON(); err == nil {
			e.TraceData = string(b)
		}
	}
}
