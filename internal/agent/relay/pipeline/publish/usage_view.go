package publish

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// ProjectUsageEntry 把当前 rctx 投影成一条 UsageLogEntry——"请求当前形状"的唯一事实源。
// publish 结算末尾调它落库；inflight 在阶段边界调它得"进行中的 usage_log"。
// 纯函数无副作用（不含 affinity 记录，那是 publish 专属，见 Publisher.Publish）。
func ProjectUsageEntry(rctx *state.RelayContext) protocol.UsageLogEntry {
	if rctx == nil || rctx.State == nil {
		return protocol.UsageLogEntry{}
	}
	e := projectBase(rctx)
	projectByPhase(&e, rctx)
	attachTraceData(&e, rctx.State.Recorder)
	if rl := rctx.State.RateLimit; rl != nil {
		e.RateLimitDecision = rl.Decision
		e.RateLimitWaitMs = rl.WaitMs
		e.RateLimitReason = rl.Reason
		e.RateLimitHits = rl.Hits
	}
	return e
}

// projectBase 装好与 FailPhase 无关的请求级字段：身份 / 模型名 / 时长 / 客户端 IP / inbound 协议。
func projectBase(rctx *state.RelayContext) protocol.UsageLogEntry {
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

// projectByPhase 按失败阶段决定填哪些字段。CtxBuild 只填 base+error；Plan 额外补 routing_name；
// Execute/None 走完整的 channel + token 拼装。
//
// CtxBuild / Plan 的 ErrorMessage 走 state.UserFacingErrorMessage —— 老 handler.go 每个 publishUsage
// 调用点写入的是带 model 名的完整文案（"no channel available for model gpt-4" 而非 "no channel
// available for model"），与 HTTP body 完全一致。Sentinel error 本身不带 model 名，必须靠
// state.StatusFromState 重建。
func projectByPhase(e *protocol.UsageLogEntry, rctx *state.RelayContext) {
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
		projectExecution(e, rctx)
	}
}

// projectExecution 在 attempt 选定后拼 channel / token / outbound 协议等字段。
// Used.Channel 为 nil 时（execute 阶段尚未挑出任何 channel）只能填 base，余下字段保留零值。
//
// 纯投影：不含 affinity 记录。affinity 是 publish 专属副作用，由 Publisher.recordAffinity
// 在 Publish 内 ProjectUsageEntry 之后单独执行（顺序保持不变）。
func projectExecution(e *protocol.UsageLogEntry, rctx *state.RelayContext) {
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
		e.PriceRatio = u.Channel.PriceRatio // 公共 channel 才快照倍率;private 分支不填,保持零值 0
		e.Free = u.Channel.Free             // 免费渠道标记快照;private 分支不填,保持 false
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
	e.FallbackChain = exec.History
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
