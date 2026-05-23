// Package publish 是 relay pipeline 的第 4 阶段（最末端）：把累加好的 RelayContext 状态
// 一次性收成 protocol.UsageLogEntry 并经 EventBus 发布 usage.completed。
//
// 设计目标：UsageLog 写入的单一出口。所有路径（成功 / ctxBuild fail / plan fail /
// execute fail / forwarded）汇聚到 Publisher.Publish() 一处，按 rctx.State.FailPhase
// 选填字段集，避免老 handler.go 每个分支重复拼装 UsageLogEntry。
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Publisher 是 UsageLog 写入的单一出口。按 rctx.State.FailPhase 选填字段集，
// 通过 EventBus 发布 protocol.UsageLogEntry。所有路径（成功 / ctxBuild fail /
// plan fail / execute fail / forwarded）汇聚到 Publish() 一处。
type Publisher struct {
	bus    app.EventBus
	logger *zap.Logger
}

// NewPublisher 构造 Publisher。logger 为 nil 时 Publish 默默吞 publish 错误。
func NewPublisher(bus app.EventBus, logger *zap.Logger) *Publisher {
	return &Publisher{bus: bus, logger: logger}
}

// Publish 把 rctx 累加的状态收成 UsageLogEntry 并发 usage.completed。
// rctx.State.Forwarded=true 时跳过（请求被转发到另一个 agent，对方记录）。
func (p *Publisher) Publish(rctx *state.RelayContext) {
	if rctx == nil || rctx.State == nil {
		return
	}
	if rctx.State.Forwarded {
		return
	}

	e := p.buildBase(rctx)
	p.fillByPhase(&e, rctx)
	attachTraceData(&e, rctx.State.Recorder)

	if err := events.PublishUsageCompleted(context.Background(), p.bus, e); err != nil {
		if p.logger != nil {
			p.logger.Error("publish usage.completed failed", zap.Error(err))
		}
	}
}

// buildBase 装好与 FailPhase 无关的请求级字段：身份 / 模型名 / 时长 / 客户端 IP / inbound 协议。
func (p *Publisher) buildBase(rctx *state.RelayContext) protocol.UsageLogEntry {
	e := protocol.UsageLogEntry{
		RequestID:       rctx.Input.RequestID,
		ModelName:       rctx.Input.Model,
		IsStream:        rctx.Input.IsStream,
		Duration:        int(time.Since(rctx.Input.StartTime).Milliseconds()),
		InboundProtocol: string(rctx.Input.InboundProto),
		Timestamp:       time.Now().Unix(),
	}
	if rctx.Context != nil {
		e.ClientIP = rctx.Context.ClientIP()
	}
	if ui := rctx.Input.UserInfo; ui != nil {
		e.UserID = ui.UserID
		e.TokenID = ui.TokenID
		e.TokenName = ui.TokenName
	}
	return e
}

// fillByPhase 按失败阶段决定填哪些字段。CtxBuild 只填 base+error；Plan 额外补 routing_name；
// Execute/None 走完整的 channel + token 拼装。
//
// CtxBuild / Plan 的 ErrorMessage 走 state.UserFacingErrorMessage —— 老 handler.go 每个 publishUsage
// 调用点写入的是带 model 名的完整文案（"no channel available for model gpt-4" 而非 "no channel
// available for model"），与 HTTP body 完全一致。Sentinel error 本身不带 model 名，必须靠
// state.StatusFromState 重建。
func (p *Publisher) fillByPhase(e *protocol.UsageLogEntry, rctx *state.RelayContext) {
	switch rctx.State.FailPhase {
	case state.PhaseCtxBuild:
		e.Status = 0
		if rctx.State.Err != nil {
			e.ErrorMessage = state.UserFacingErrorMessage(rctx)
		}
	case state.PhasePlan:
		// main:handler.go 502 fallback (lastErr==nil) 分支 502 fallback 路径走 buildBaseUsageLogEntry，
		// 不带 RoutingName → UsageLog.RoutingName 保持空字符串。strict parity 要求
		// ErrRoutingFallback 时跳过 RoutingName 写入，其它 Plan 阶段失败照写。
		if !errors.Is(rctx.State.Err, state.ErrRoutingFallback) {
			e.RoutingName = rctx.State.Plan.RoutingName
		}
		e.Status = 0
		if rctx.State.Err != nil {
			e.ErrorMessage = state.UserFacingErrorMessage(rctx)
		}
	case state.PhaseExecute, state.PhaseNone:
		p.fillExecution(e, rctx)
	}
}

// fillExecution 在 attempt 选定后拼 channel / token / outbound 协议等字段。
// Used.Channel 为 nil 时（execute 阶段尚未挑出任何 channel）只能填 base，余下字段保留零值。
func (p *Publisher) fillExecution(e *protocol.UsageLogEntry, rctx *state.RelayContext) {
	exec := rctx.State.Execution
	u := exec.Used
	out := exec.Outcome
	if u.Channel == nil {
		// 即便 channel 没敲定也得把 Status / ErrorMessage 标对。
		if exec.Err != nil {
			e.Status = 0
			e.ErrorMessage = exec.Err.Error()
		}
		return
	}

	rules := upstream.ChannelOverrideRulesFor(u.Channel)
	override := upstream.ResolveOverride(rules, u.RealModel)

	// Source-based ID routing (BYOK Task 14):
	//   admin   → e.ChannelID = u.SourceID; e.OwnerType = "admin"
	//   private → e.PrivateChannelID = u.SourceID; e.ChannelID = 0; e.OwnerType = "private"
	// Zero/unknown Source falls back to admin path with Channel.ID for defensive compatibility
	// (any pre-Task-12 callsite that hasn't been updated would have Source="" and still work).
	switch u.Source {
	case state.SourcePrivate:
		e.PrivateChannelID = u.SourceID
		e.ChannelID = 0
		e.OwnerType = "private"
	default:
		// SourceAdmin or "" (zero value)
		if u.SourceID != 0 {
			e.ChannelID = u.SourceID
		} else {
			e.ChannelID = u.Channel.ID
		}
		e.OwnerType = "admin"
	}
	e.ModelName = u.RealModel
	e.RoutingName = rctx.State.Plan.RoutingName

	e.UpstreamModel = out.UpstreamModel
	if e.UpstreamModel == "" {
		e.UpstreamModel = state.ApplyModelMapping(u.Channel, u.RealModel)
	}
	// Token / cache / first-response 字段策略，1:1 对齐 main:handler.go：
	//   - 成功 (Err=nil)：成功路径 struct literal 含完整字段
	//     PromptTokens / CompletionTokens / CacheReadTokens / CacheWriteTokens /
	//     FirstResponseMs / TokenSource。
	//   - 失败 + Written=true（mid-stream fail 分支）：用户已收到部分 token，
	//     **只写 PromptTokens / CompletionTokens / TokenSource**——老 struct literal
	//     里没列 CacheReadTokens / CacheWriteTokens / FirstResponseMs，保持零值。
	//   - 失败 + Written=false：final fallback 分支，三类 token 字段都不写。
	switch {
	case exec.Err == nil:
		e.PromptTokens = out.PromptTokens
		e.CompletionTokens = out.CompletionTokens
		e.CacheReadTokens = out.CacheReadTokens
		e.CacheWriteTokens = out.CacheWriteTokens
		e.TokenSource = out.TokenSource
		e.FirstResponseMs = out.FirstResponseMs
	case out.Written:
		// 老 handler.go mid-stream fail 分支只写 prompt / completion / token_source。
		e.PromptTokens = out.PromptTokens
		e.CompletionTokens = out.CompletionTokens
		e.TokenSource = out.TokenSource
		// CacheReadTokens / CacheWriteTokens / FirstResponseMs 保持零值——
		// 复刻老 struct literal 没列这三个字段的行为。
	}
	e.UseLegacy = u.Mode == state.ModeLegacy
	e.Other = buildOtherJSON(u.Channel, u.Mode, rctx.State.Plan.Trace)
	e.OutboundProtocol = string(codec.NegotiateOutboundProtocol(
		rctx.Input.InboundProto,
		u.Channel.Type,
		u.Channel.SupportedAPITypes,
		u.Channel.Endpoints,
		override,
	))

	if exec.Err != nil {
		e.Status = 0
		e.ErrorMessage = exec.Err.Error()
	} else {
		e.Status = 1
	}
}

// buildOtherJSON 把 channel 类型 / 名字 / passthrough 开关 / routing trace 序列化进
// UsageLogEntry.Other 字段。routingTrace 由 routing resolver 产生（spec §3.5），
// 仅在 routing 命中时填。
func buildOtherJSON(ch *models.Channel, mode state.RelayMode, routingTrace []string) string {
	m := map[string]any{
		"relay_mode":          string(mode),
		"channel_type":        ch.Type,
		"channel_name":        ch.Name,
		"passthrough_enabled": ch.PassthroughEnabled,
	}
	if len(routingTrace) > 0 {
		m["routing_trace"] = strings.Join(routingTrace, " > ")
	}
	data, _ := json.Marshal(m)
	return string(data)
}
