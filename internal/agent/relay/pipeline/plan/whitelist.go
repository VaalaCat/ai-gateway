package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// FilterByAllowedChannels returns only the channels whose ID appears in allowed.
// nil/empty allowed → input is returned unchanged ("no whitelist" semantics).
// IDs in allowed that do not match any channel are silently ignored — this is
// how we tolerate ghost IDs left behind by deleted channels.
//
// 导出（首字母大写）是因为 internal/agent/relay/models.go 的 ListModels handler
// 跨包调用。包级别使用者请优先调 Pool（封装好白名单 + Forced 的过滤管道）。
func FilterByAllowedChannels(channels []*models.Channel, allowed []uint) []*models.Channel {
	if len(allowed) == 0 {
		return channels
	}
	set := make(map[uint]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	out := make([]*models.Channel, 0, len(channels))
	for _, ch := range channels {
		if _, ok := set[ch.ID]; ok {
			out = append(out, ch)
		}
	}
	return out
}

// filterScoredByAllowedChannels 是 FilterByAllowedChannels 的 ScoredCandidate 版本。
// 行为：保留所有 SourcePrivate candidate（不受白名单约束）+ 仅保留 ID ∈ allowed 的
// SourceAdmin candidate。
func filterScoredByAllowedChannels(cands []ScoredCandidate, allowed []uint) []ScoredCandidate {
	set := make(map[uint]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	out := make([]ScoredCandidate, 0, len(cands))
	for _, sc := range cands {
		if sc.Source == state.SourcePrivate {
			out = append(out, sc)
			continue
		}
		if _, ok := set[sc.Channel.ID]; ok {
			out = append(out, sc)
		}
	}
	return out
}
