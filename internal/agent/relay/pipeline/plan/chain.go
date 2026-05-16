package plan

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/state"

// ModelChainBuilder 把入参 model 一次性预求值成完整的 realModel 链。
// "失败→MarkMemberExhausted→再 Resolve"的迭代由 Build 内部完成，
// 让 Solver 拿到的就是完整顺序，channel 选完后只需按链顺序往下走。
type ModelChainBuilder interface {
	Build(rctx *state.RelayContext) ModelChain
}

// ModelChain 是一次 Resolve 链的结果快照。
//   - Models: realModel 列表（按 routing 优先级 + 失败回退顺序）；
//     未配置 routing 时退化为 [Input.Model] 一条。
//   - RoutingName: 命中 routing 时填用户可见名；未命中时为空。
//   - Trace: ResolveCtx.Trace 拍照，写 UsageLog.RoutingTrace 用。
type ModelChain struct {
	Models      []string
	RoutingName string
	Trace       []string
}

// routingChainBuilder 是 ModelChainBuilder 的默认实现。
// 直接复用 ResolveToRealModel + MarkMemberExhausted 迭代，把整条链一次预求值出来。
type routingChainBuilder struct{}

// Build 从 rctx.Agent.GetCache() 取出 RoutingStore，然后预求值整条 model 链。
// 取不到 RoutingStore（理论上不会发生，因为 *cache.Store 满足）则返回空 chain。
func (routingChainBuilder) Build(rctx *state.RelayContext) ModelChain {
	return buildChainFromStore(routingStoreFromContext(rctx), rctx)
}

// routingStoreFromContext 从 rctx.Agent 反向取出 RoutingStore。
// 当前生产实现 agentCache 嵌入 *cache.Store，自然满足 RoutingStore；
// 测试可以传一个手写的 fake AgentCache。
func routingStoreFromContext(rctx *state.RelayContext) RoutingStore {
	if rctx == nil || rctx.Agent == nil {
		return nil
	}
	cache := rctx.Agent.GetCache()
	if cache == nil {
		return nil
	}
	if rs, ok := cache.(RoutingStore); ok {
		return rs
	}
	return nil
}

// buildChainFromStore 是 routingChainBuilder.Build 的纯逻辑版本，
// 不依赖 RelayContext 的 Agent 装配——单测可以直接传 stubRoutingStore。
//
// Trace 字段语义：只保留 *首次* Resolve 完成后的 ctx.Trace() 快照。
// 成功路径下只 Resolve 一次（即只 push 一条 "global:smart"），
// 而预求值整条链会反复 Mark+Resolve 把同一 routing 名累加 N+1 条 trace，
// 几何级数放大写到 UsageLog.Other.routing_trace。
// 解决：拍照后续 Resolve 仍用同一 ctx（excluded 状态需累积），但 trace 增量不传出。
func buildChainFromStore(rs RoutingStore, rctx *state.RelayContext) ModelChain {
	if rs == nil || rctx == nil {
		return ModelChain{}
	}

	userID := uint(0)
	if ui := rctx.Input.UserInfo; ui != nil {
		userID = ui.UserID
	}

	ctx := NewResolveCtx()
	first := ResolveToRealModel(rs, rctx.Input.Model, userID, ctx)
	// 首次 Resolve 完成后立刻拍照 trace——这是 Plan.Trace 的最终值。
	// 后续 Mark+Resolve 仍累积 ctx.trace，但不传出（否则
	// UsageLog.Other.routing_trace 被几何级数放大，违反 main parity）。
	firstTrace := ctx.Trace()

	if first == "" {
		// 整链 cycle / depth_exceeded：返回空 Models，Trace 仍保留供 UsageLog 写。
		return ModelChain{Trace: firstTrace}
	}

	models := []string{first}
	for {
		last := models[len(models)-1]
		ctx.MarkMemberExhausted(last)
		next := ResolveToRealModel(rs, rctx.Input.Model, userID, ctx)
		// next == "" 表示 routing 链耗尽；next == last 是退化情况（非 routing 路径）
		if next == "" || next == last {
			break
		}
		models = append(models, next)
	}

	// RoutingName 只在第一个 realModel 与入参不同时填。
	routingName := ""
	if models[0] != rctx.Input.Model {
		routingName = rctx.Input.Model
	}

	return ModelChain{
		Models:      models,
		RoutingName: routingName,
		Trace:       firstTrace, // 不是 ctx.Trace()，否则 trace 会被 N+1 放大
	}
}
