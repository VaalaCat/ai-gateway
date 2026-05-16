package exec

import (
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

// maybeForward 检查当前 attempt 起的剩余 batch 是否有 AgentRoute 命中，命中即转发。
//
// 按 attempt 粒度评估 forward 时机（spec §1，相对 main 外层一次性检查的修正），
// 但 batch channelIDs 对齐 main:handler.go:357-360 ——
// 每次评估都用 plan.Attempts[idx:] 全部剩余 channelID 去查 RouteIndex，
// 避免 route 配在后位 channel 时白白先 dispatch 前面的（审计 #12）。
//
// 旧 HEAD（修复前）：MatchRoute(... , []uint{a.Channel.ID}) —— 单 channel
// 新 HEAD（修复后）：MatchRoute(... , restChannelIDs(plan.Attempts[idx:])) —— 剩余 batch
//
// 返回 true 表示 forwarder 已接管请求；err 报 warn 后继续走本地路径（与旧逻辑一致）。
func maybeForward(rctx *state.RelayContext, idx int, plan *state.AttemptPlan) bool {
	fwd := rctx.Agent.GetRouteForwarder()
	if fwd == nil {
		return false
	}
	cache := rctx.Agent.GetCache()
	if cache == nil {
		return false
	}

	tokenID := uint(0)
	if ui := rctx.Input.UserInfo; ui != nil {
		tokenID = ui.TokenID
	}

	channelIDs := restChannelIDs(plan.Attempts[idx:])
	realModel := plan.Attempts[idx].RealModel
	route := cache.MatchRoute(tokenID, realModel, channelIDs)
	if route == nil {
		return false
	}

	forwarded, err := fwd.ForwardByRoute(rctx.Context, route)
	if err != nil {
		if logger := rctx.Agent.GetLogger(); logger != nil {
			logger.Warn("route forwarding failed, processing locally",
				zap.Uint("route_id", route.ID), zap.Error(err))
		}
	}
	return forwarded
}

// restChannelIDs 从一批 attempts 收集去重的 channel ID，保持首次出现顺序。
// 用于 maybeForward 的 batch RouteIndex 查询（对齐 main:handler.go:357-360）。
func restChannelIDs(attempts []state.Attempt) []uint {
	seen := make(map[uint]bool, len(attempts))
	ids := make([]uint, 0, len(attempts))
	for _, a := range attempts {
		if a.Channel == nil {
			continue
		}
		if !seen[a.Channel.ID] {
			seen[a.Channel.ID] = true
			ids = append(ids, a.Channel.ID)
		}
	}
	return ids
}
