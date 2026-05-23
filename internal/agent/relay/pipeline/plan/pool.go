package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// ScoredCandidate 是 ChannelPool 输出的最小信息单元：channel 本体 + 来源标记。
// 来源标记保留到 Solver 装 Attempt 时一并传递，避免在 *models.Channel 字段上塞
// 编码（如 Tag）这种破坏命名语义的做法。
type ScoredCandidate struct {
	Channel  *models.Channel
	Source   state.ChannelSource
	SourceID uint
}

// ChannelPool 是 single-realModel 的候选 channel 提供者。
// Solver 拿到 realModel 后通过它 Available(realModel) 一次性拿到全部可用 candidate，
// 把"取候选 + 白名单过滤 + ForcedChannelID 过滤"三件事打包。
type ChannelPool interface {
	Available(rctx *state.RelayContext, realModel string) []ScoredCandidate
}

// channelLister 是 channel 候选数据源签名。
// 当前生产实现 sharedChannels（共享 channel 池）；BYOK lister 在 Task 13 加入。
// 每个 lister 自己决定输出 candidate 的 Source（admin/private）。
type channelLister func(rctx *state.RelayContext, realModel string) []ScoredCandidate

type channelPoolImpl struct {
	listers []channelLister
}

// newDefaultChannelPool 返回生产环境用的 ChannelPool。
// privateChannelsVisibleToCaller 排在 sharedChannels 之前——lister 顺序只决定
// collectCandidates 的 append 顺序；"private 优先 + shared 兜底"的真正契约
// 由 plan.sort 的 source rank 二级排序保证。
func newDefaultChannelPool() ChannelPool {
	return channelPoolImpl{listers: []channelLister{
		privateChannelsVisibleToCaller,
		sharedChannels,
	}}
}

// privateChannelsVisibleToCaller 是 BYOK lister：对当前认证用户，
// 从缓存 LRU 拉取可见私有 channel，逐一投影为 *models.Channel（priority 直接透传），
// 包成 SourcePrivate ScoredCandidate。
//
// 顺序说明："private 优先 + shared 兜底"由 plan.sort 的 source rank 主导排序
// 保证，与本 lister 的输出顺序无关。
func privateChannelsVisibleToCaller(rctx *state.RelayContext, realModel string) []ScoredCandidate {
	if rctx == nil || rctx.Agent == nil {
		return nil
	}
	ui := rctx.Input.UserInfo
	if ui == nil || ui.UserID == 0 {
		return nil
	}
	cache := rctx.Agent.GetCache()
	if cache == nil {
		return nil
	}
	privs := cache.GetVisiblePrivateChannelsForUser(ui.UserID, realModel)
	if len(privs) == 0 {
		return nil
	}
	out := make([]ScoredCandidate, 0, len(privs))
	for _, pc := range privs {
		ch := upstream.ProjectPrivateChannelToChannel(pc)
		out = append(out, ScoredCandidate{
			Channel:  ch,
			Source:   state.SourcePrivate,
			SourceID: pc.ID,
		})
	}
	return out
}

// sharedChannels 是共享 channel 池数据源——AgentCache.GetChannelsForModel 包成
// SourceAdmin candidate。
func sharedChannels(rctx *state.RelayContext, realModel string) []ScoredCandidate {
	if rctx == nil || rctx.Agent == nil {
		return nil
	}
	cache := rctx.Agent.GetCache()
	if cache == nil {
		return nil
	}
	chans := cache.GetChannelsForModel(realModel)
	out := make([]ScoredCandidate, 0, len(chans))
	for _, ch := range chans {
		out = append(out, ScoredCandidate{
			Channel:  ch,
			Source:   state.SourceAdmin,
			SourceID: ch.ID,
		})
	}
	return out
}

func (p channelPoolImpl) Available(rctx *state.RelayContext, realModel string) []ScoredCandidate {
	cands := p.collectCandidates(rctx, realModel)
	cands = p.applyWhitelist(cands, rctx.Input.UserInfo)
	return p.applyForcedID(cands, rctx.Input.ForcedChannelID)
}

func (p channelPoolImpl) collectCandidates(rctx *state.RelayContext, realModel string) []ScoredCandidate {
	var out []ScoredCandidate
	for _, list := range p.listers {
		out = append(out, list(rctx, realModel)...)
	}
	return out
}

// applyWhitelist 走 group + token 两层白名单 AND 过滤。
// private channel 不受 token/group 白名单约束（白名单只针对 admin shared channel ID 集合，
// private channel 由 owner 独立控制可见性）；当 candidate.Source == SourcePrivate 时直接放行。
func (channelPoolImpl) applyWhitelist(cands []ScoredCandidate, ui *app.UserInfo) []ScoredCandidate {
	if ui == nil {
		return cands
	}
	if len(ui.GroupAllowedChannelIDs) > 0 {
		cands = filterScoredByAllowedChannels(cands, ui.GroupAllowedChannelIDs)
	}
	if len(ui.AllowedChannelIDs) > 0 {
		cands = filterScoredByAllowedChannels(cands, ui.AllowedChannelIDs)
	}
	return cands
}

// applyForcedID 实现 X-Channel-ID 强制路由：
//   - id == 0 → 不过滤
//   - id 命中 admin candidate → 单元素切片
//   - id 未命中 → 返回 nil（让上层走 404，与老 handler.go 行为一致）
//
// X-Channel-ID 历来只指 admin channel ID；private channel 不参与此路径。
func (channelPoolImpl) applyForcedID(cands []ScoredCandidate, id uint) []ScoredCandidate {
	if id == 0 {
		return cands
	}
	for _, sc := range cands {
		if sc.Source == state.SourceAdmin && sc.Channel.ID == id {
			return []ScoredCandidate{sc}
		}
	}
	return nil
}
