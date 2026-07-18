package legacy

import (
	newapi "github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"go.uber.org/zap"
)

// Backend 走 new-api adaptor 的 legacy relay 路径。
// 只持有 app.AgentApplication 拿 logger（newapi.Relay 需要日志），无 transport pool 概念
// （new-api 内部自管理 client）。
// 外部访问为 legacy.Backend；Agent 字段导出方便 backend.Dispatcher 装配。
//
// 注：本包名 `legacy` 与 `internal/agent/relay/legacy`（new-api adaptor 包）同名，
// 后者以 import 别名 `newapi` 引入避免歧义。
type Backend struct {
	Agent app.AgentApplication
}

// Relay 把原来 (*Handler).relayLegacy 内化到 backend 里。
// 转一道 newapi.Relay 拿到 *newapi.RelayResult，桥接 TraceData / Stage 错误到 Recorder。
//
// 不做 token 调和（FinalizeTokenCounts 在 Dispatcher 层统一处理）。
// 不累积 ResponseText：legacy path 由 new-api 内部估算 completion，
// handler 这一层做不到补估。
func (b *Backend) Relay(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	c := rctx.Context
	ch := a.Channel
	bodyBytes := rctx.Input.Body
	modelName := a.RealModel
	isStream := rctx.Input.IsStream
	rec := rctx.State.Recorder

	rec.WithStage(trace.StageUpstreamDispatch)

	relayMode := newapi.Path2RelayMode(c.Request.URL.Path)
	var owner *newapi.TransportOwner
	if provider, ok := b.Agent.(interface{ GetLegacyTransportOwner() *newapi.TransportOwner }); ok {
		owner = provider.GetLegacyTransportOwner()
	}
	lr := newapi.RelayWithOwner(owner, c, ch, bodyBytes, modelName, isStream, relayMode, true, b.logger())

	result := state.AttemptResult{
		PromptTokens:     lr.PromptTokens,
		CompletionTokens: lr.CompletionTokens,
		CacheReadTokens:  lr.CacheReadTokens,
		CacheWriteTokens: lr.CacheWriteTokens,
		FirstResponseMs:  lr.FirstResponseMs,
		UpstreamModel:    lr.UpstreamModel,
		Written:          lr.Written,
		Err:              lr.Err,
	}

	// 把 newapi 自己的 TraceData 桥接到 Recorder
	if lr.Trace != nil {
		rec.WithLegacyTrace(lr.Trace, ch)
	}
	if lr.Err != nil {
		// newapi 不细分阶段，默认归到 upstream_dispatch
		rec.WithFail(trace.StageUpstreamDispatch, lr.Err)
	}
	return result
}

// logger 是 b.Agent.GetLogger() 的 nil-guarded 包装。newapi.Relay 接受 nil logger
// 也不 panic，所以这里返回 nil 让调用直接透传更朴素。
func (b *Backend) logger() *zap.Logger {
	if b.Agent == nil {
		return nil
	}
	return b.Agent.GetLogger()
}
