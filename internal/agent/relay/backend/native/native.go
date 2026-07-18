package native

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/dataflow"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Backend 走 codec pipeline 的原生 relay 路径。
// 只持有 app.AgentApplication 拿 logger / transport pool，不再依赖 *Handler。
// 外部访问为 native.Backend；Agent 字段导出方便 backend.Dispatcher 装配。
type Backend struct {
	Agent app.AgentApplication
}

// Relay 处理单次上行 attempt。流程：协商 outbound 协议 → decode 入站 →
// seed/freeze Pass → 通过 dataflow.BuildChannelDataFlow 构建步骤链并 flow.Run
// （含 model mapping、transformer、script、encode 等各 Step）→ dispatch 上行 HTTP
// → 监控 events → encode 回客户端。
//
// 任一阶段失败会通过 Recorder.WithFail 标记 + 返回 state.AttemptResult.Err。
// 不做 token 调和（FinalizeTokenCounts 在 Dispatcher 层统一处理），不做 forwarding 决策
// （Executor 在 attempt 入口处理）。
func (b *Backend) Relay(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	c := rctx.Context
	ch := a.Channel
	modelName := a.RealModel
	isStream := rctx.Input.IsStream
	inboundProto := rctx.Input.InboundProto
	startTime := rctx.Input.StartTime
	rec := rctx.State.Recorder

	logger := b.logger()

	// Bind upstream calls to the client request context so that client
	// disconnection cancels the upstream HTTP call immediately.
	// For non-stream requests, also apply a hard relay timeout when configured.
	ctx := c.Request.Context()
	var cancel context.CancelFunc
	if !isStream && rctx.Agent != nil && rctx.Agent.RelayTimeout() > 0 {
		ctx, cancel = context.WithTimeout(ctx, rctx.Agent.RelayTimeout())
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	outboundProto, inboundCodec, outboundCodec, err := resolveNativeCodecs(ch, inboundProto, modelName)
	if err != nil {
		return state.AttemptResult{Err: err}
	}

	// decode 留在链外:它的产出正是种入 Pass 的初值。
	irReq, err := inboundCodec.DecodeRequest(c.Request)
	if err != nil {
		return state.AttemptResult{Err: fmt.Errorf("decode inbound request: %w", err)}
	}
	// 进入本 channel 处理的模型名是 planner 解析出的 RealModel(对齐旧
	// cfg.InboundModel 与 ApplyModelMapping 入参);body 里的 model 在此被丢弃。
	// 先 seed 再冻结 Original,保证 Original.Model == RealModel。
	irReq.Model = modelName

	pass := &dataflow.Pass{
		Original: dataflow.CloneRequest(irReq),
		Working:  irReq,
		Rec:      rec,
	}
	flow := dataflow.BuildChannelDataFlow(ch, outboundProto, outboundCodec, dataflow.StepDeps{
		Agent:  b.Agent,
		GinCtx: c,
		RCtx:   rctx,
		Logger: logger,
	})
	if err := flow.Run(ctx, pass); err != nil {
		return state.AttemptResult{Err: err}
	}
	if pass.Aborted {
		return pass.AbortResult
	}

	upstreamReq := pass.HTTPReq
	outboundBody := pass.Body
	upstreamModel := pass.Working.Model

	rec.WithOutbound(upstreamReq, outboundBody, ch)
	rec.WithStage(trace.StageUpstreamDispatch)

	resp, err := b.dispatchUpstream(ctx, upstreamReq, ch, rec)
	if err != nil {
		return state.AttemptResult{Err: err}
	}
	// Guarantee resp.Body is closed on every return path. Decoder goroutines
	// also defer Close inside their own goroutine, but if EncodeResponse 半途
	// 异常返回，decoder goroutine 可能因为 channel 背压卡住而一直拿着 body，
	// 导致连接池泄漏。这里在 Relay 函数级别兜底，确保 transport pool 能复用连接。
	// io.ReadCloser.Close 多次调用是安全的（net/http 自身允许）。
	defer resp.Body.Close()

	// Record time-to-first-byte from upstream (used as TTFR for non-stream)
	httpResponseMs := int(time.Since(startTime).Milliseconds())

	rec.WithUpstreamStatus(resp)

	if result, handled := handleNativeErrorStatus(rec, resp, c.Writer, inboundCodec, upstreamModel); handled {
		return result
	}

	return streamNativeResponse(
		ctx,
		c,
		rec,
		resp,
		inboundCodec,
		outboundCodec,
		codec.FunctionFallbackTools(pass.Working),
		isStream,
		startTime,
		httpResponseMs,
		upstreamModel,
		logger,
	)
}

// streamNativeResponse 把 2xx 上行响应通过 outboundCodec.DecodeResponse →
// monitorEvents → inboundCodec.EncodeResponse 推回客户端，
// 同时通过 Recorder.WrapUpstreamBody / WrapClientWriter 捕获 body 给 trace / usage 抽取。
//
// 返回 state.AttemptResult：包含 token usage / TTFR / responseText / 可能的 encode error。
// httpResponseMs 用于 non-stream 时作为 TTFR fallback（事件级别 timing 无意义）。
//
// 副作用：
//   - 修改 resp.Body（wrap 成 TeeReader）。
//   - 修改 c.Writer（wrap 成 Recorder-tracked writer）。
//   - 切换 Recorder stage（StageUpstreamDecode → StageClientEncode → StageNone）。
func streamNativeResponse(
	ctx context.Context,
	c *gin.Context,
	rec *trace.Recorder,
	resp *http.Response,
	inboundCodec codec.InboundCodec,
	outboundCodec codec.OutboundCodec,
	functionFallbackTools map[string]codec.FunctionFallbackTool,
	isStream bool,
	startTime time.Time,
	httpResponseMs int,
	upstreamModel string,
	logger *zap.Logger,
) state.AttemptResult {
	// Wrap resp.Body with Recorder TeeReader so upstream body is captured
	// as it is consumed by the decoder (works for both stream and non-stream).
	resp.Body = rec.WrapUpstreamBody(resp)

	rec.WithStage(trace.StageUpstreamDecode)

	// Decode the upstream response into an IR event stream
	events, err := outboundCodec.DecodeResponse(resp, isStream)
	if err != nil {
		resp.Body.Close()
		rec.WithFail(trace.StageUpstreamDecode, err)
		return state.AttemptResult{Err: fmt.Errorf("decode upstream response: %w", err)}
	}
	events = codec.AdaptFunctionFallbackEvents(ctx, events, functionFallbackTools)

	// Monitor events: collect usage and first-response timing.
	// For non-stream requests, use the HTTP response time as TTFR since the
	// entire response arrives at once and event-level timing is meaningless.
	monitoredEvents, monitor := upstream.MonitorEvents(events, startTime)
	if !isStream {
		monitor.SetFirstResponseMs(httpResponseMs)
	}

	rec.WithStage(trace.StageClientEncode)

	// Wrap c.Writer so client response bytes are captured by Recorder.
	c.Writer = rec.WrapClientWriter(c.Writer)

	// Encode the response back to the client via the inbound codec
	if err := inboundCodec.EncodeResponse(monitoredEvents, c.Writer, isStream); err != nil {
		if logger != nil {
			logger.Warn("failed to encode response to client", zap.Error(err))
		}
		rec.WithFail(trace.StageClientEncode, err)
		usage := upstream.NormalizeUsage(monitor.Usage)
		return state.AttemptResult{
			Written:          true,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			CacheReadTokens:  usage.CacheReadTokens,
			CacheWriteTokens: usage.CacheWriteTokens,
			FirstResponseMs:  monitor.FirstResponseMs(),
			UpstreamModel:    upstreamModel,
			Err:              fmt.Errorf("encode response: %w", err),
			ResponseText:     monitor.ResponseText.String(),
		}
	}

	rec.WithStage(trace.StageNone)

	usage := upstream.NormalizeUsage(monitor.Usage)
	return state.AttemptResult{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		FirstResponseMs:  monitor.FirstResponseMs(),
		UpstreamModel:    upstreamModel,
		Written:          true,
		ResponseText:     monitor.ResponseText.String(),
	}
}

// handleNativeErrorStatus 处理非 2xx/3xx 上行响应。
//
// 本函数职责仅:读 body / 记 trace / 包装成 *common.UpstreamError 返回。
// 是否重试 / fallback / 立即返回的决策由 Executor 统一负责(参见
// pipeline/exec/exec.go Run() 主循环)。
//
// 注意:本函数不再调用 inboundCodec.EncodeError,因为是否写回客户端取决于
// Executor 走例外路径(plan 全部 attempt 耗尽 + 最后一次失败时才写),
// 由 Executor 在终止 attempt 链时统一处理。
//
// 过渡期说明(T5 尚未落地):4xx 错误当前不写回客户端,会经历全部 attempt
// 后由 Executor 统一终止。T5 落地后 Executor 将对 invalid_request_error 做
// 立即短路返回。
func handleNativeErrorStatus(rec *trace.Recorder, resp *http.Response, _w gin.ResponseWriter, _inboundCodec codec.InboundCodec, upstreamModel string) (state.AttemptResult, bool) {
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return state.AttemptResult{}, false
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	upErr := &common.UpstreamError{
		Status:            resp.StatusCode,
		Body:              body,
		ProviderErrorType: common.ParseProviderErrorType(body),
		Header:            resp.Header.Clone(),
	}
	rec.SetUpstreamBody(body)
	rec.WithFail(trace.StageUpstreamStatus, upErr)
	return state.AttemptResult{
		UpstreamModel: upstreamModel,
		Err:           upErr,
		// Written 留默认 false;客户端写回由 Executor 在 plan 结束时统一处理。
	}, true
}

// dispatchUpstream 跑 HTTP 请求；失败时通过 Recorder.WithFail 打 trace + 返回 wrapped error。
// ctx 绑定到请求，使客户端取消或超时能即时传播到上游连接。
// 与原 inline 写法一致，error 文案保留 "upstream request failed"。
func (b *Backend) dispatchUpstream(ctx context.Context, req *http.Request, ch *models.Channel, rec *trace.Recorder) (*http.Response, error) {
	client := upstream.BuildHTTPClient(b.transportPool(), ch)
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		rec.WithFail(trace.StageUpstreamDispatch, err)
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	return resp, nil
}

// resolveNativeCodecs 根据 channel + inbound 协议 + model 解析出最终的 outbound 协议
// 以及对应的 inbound / outbound codec 实例。任一 codec 未注册都返回 error，由调用方
// 包成 state.AttemptResult{Err: ...}。
func resolveNativeCodecs(ch *models.Channel, inboundProto codec.Protocol, modelName string) (codec.Protocol, codec.InboundCodec, codec.OutboundCodec, error) {
	rules := upstream.ChannelOverrideRulesFor(ch)
	override := upstream.ResolveOverride(rules, modelName)
	outboundProto := codec.NegotiateOutboundProtocol(inboundProto, ch.Type, ch.SupportedAPITypes, ch.Endpoints, override)

	inboundCodec := codec.GetInbound(inboundProto)
	if inboundCodec == nil {
		return outboundProto, nil, nil, fmt.Errorf("no inbound codec for protocol %s", inboundProto)
	}
	outboundCodec := codec.GetOutbound(outboundProto)
	if outboundCodec == nil {
		return outboundProto, inboundCodec, nil, fmt.Errorf("no outbound codec for protocol %s", outboundProto)
	}
	return outboundProto, inboundCodec, outboundCodec, nil
}

// logger 是 b.Agent.GetLogger() 的 nil-guarded 包装。
// agent 为 nil 时返回 nil，调用方需要做 nil 检查。
func (b *Backend) logger() *zap.Logger {
	if b.Agent == nil {
		return nil
	}
	return b.Agent.GetLogger()
}

// transportPool 是 b.Agent.GetTransportPool() 的 nil-guarded 包装。
// agent 为 nil 时返回 nil，buildHTTPClient 自带 nil pool fallback。
func (b *Backend) transportPool() app.TransportPool {
	if b.Agent == nil {
		return nil
	}
	return b.Agent.GetTransportPool()
}
