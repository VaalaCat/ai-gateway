package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// ChannelPool 是 single-realModel 的候选 channel 提供者。
// Solver 拿到 realModel 后通过它 Available(realModel) 一次性拿到全部可用 channel，
// 把"取候选 + 白名单过滤 + ForcedChannelID 过滤"三件事打包。
type ChannelPool interface {
	Available(rctx *state.RelayContext, realModel string) []*models.Channel
}

// channelLister 是 channel 候选数据源签名。
// 当前生产实现只有 sharedChannels（共享 channel 池）；
// BYOK 上线后可 append personalChannels(userID) 等扩展点，多源 channel 自然合流。
type channelLister func(rctx *state.RelayContext, realModel string) []*models.Channel

// channelPoolImpl 用一组 lister 收集候选，再依次走白名单 + ForcedChannelID 过滤管道。
// listers 顺序无关——同一个 channel 多次出现也只是 list 多一份，过滤层不去重（行为与原 handler.go 一致）。
type channelPoolImpl struct {
	listers []channelLister
}

// newDefaultChannelPool 返回生产环境用的 ChannelPool。
// 目前只装 sharedChannels；BYOK 上线后改这里加 lister 即可，不动 Solver 调用面。
func newDefaultChannelPool() ChannelPool {
	return channelPoolImpl{listers: []channelLister{sharedChannels}}
}

// sharedChannels 是共享 channel 池数据源——直接走 AgentCache.GetChannelsForModel。
// 与原 handler.go 主循环里"按 realModel 查共享 channel"分支行为完全一致。
func sharedChannels(rctx *state.RelayContext, realModel string) []*models.Channel {
	if rctx == nil || rctx.Agent == nil {
		return nil
	}
	cache := rctx.Agent.GetCache()
	if cache == nil {
		return nil
	}
	return cache.GetChannelsForModel(realModel)
}

// Available 拼装候选 → 白名单（group ∩ token，AND）→ ForcedChannelID。
// 三步顺序复刻原 handler.go 主循环的"取候选 → 白名单过滤 → X-Channel-ID 强制路由"管道。
func (p channelPoolImpl) Available(rctx *state.RelayContext, realModel string) []*models.Channel {
	channels := p.collectCandidates(rctx, realModel)
	channels = p.applyWhitelist(channels, rctx.Input.UserInfo)
	return p.applyForcedID(channels, rctx.Input.ForcedChannelID)
}

// collectCandidates 把所有 lister 的输出顺序 append。
// nil lister 返回的 nil 切片对 append 透明，故不需特判。
func (p channelPoolImpl) collectCandidates(rctx *state.RelayContext, realModel string) []*models.Channel {
	var out []*models.Channel
	for _, list := range p.listers {
		out = append(out, list(rctx, realModel)...)
	}
	return out
}

// applyWhitelist 走 group + token 两层白名单 AND 过滤。
// 任一层为空表示该层不限——与 FilterByAllowedChannels 的 nil 语义一致。
func (channelPoolImpl) applyWhitelist(channels []*models.Channel, ui *app.UserInfo) []*models.Channel {
	if ui == nil {
		return channels
	}
	if len(ui.GroupAllowedChannelIDs) > 0 {
		channels = FilterByAllowedChannels(channels, ui.GroupAllowedChannelIDs)
	}
	if len(ui.AllowedChannelIDs) > 0 {
		channels = FilterByAllowedChannels(channels, ui.AllowedChannelIDs)
	}
	return channels
}

// applyForcedID 实现 X-Channel-ID 强制路由：
//   - id == 0 → 不过滤；
//   - id 命中 → 单元素切片；
//   - id 未命中 → 返回 nil（让上层走 404 而非"静默回退"，行为同原 handler.go X-Channel-ID 过滤分支）。
func (channelPoolImpl) applyForcedID(channels []*models.Channel, id uint) []*models.Channel {
	if id == 0 {
		return channels
	}
	for _, ch := range channels {
		if ch.ID == id {
			return []*models.Channel{ch}
		}
	}
	return nil
}
