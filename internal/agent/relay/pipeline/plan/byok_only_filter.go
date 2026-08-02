package plan

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/state"

// byokOnlyFilter 实现 Token.BYOKOnly：开启时只保留私有(BYOK)候选、剔除所有共享(admin)候选；
// 若剔除后为空（用户对该模型无任何可用私有渠道）→ DropBYOKOnly（硬闸门，绝不降级到共享）。
// 关闭或系统 token（UserInfo==nil / UserID==0）→ 原样放行，行为不变。
type byokOnlyFilter struct{}

func (byokOnlyFilter) Name() string { return "byok_only" }

func (byokOnlyFilter) Apply(fctx *FilterContext, in []ScoredCandidate) ([]ScoredCandidate, DropCode) {
	ui := fctx.viewerInfo()
	if ui == nil || ui.UserID == 0 || !ui.BYOKOnly {
		return in, DropNone
	}
	kept := make([]ScoredCandidate, 0, len(in))
	for _, c := range in {
		if c.Source == state.SourcePrivate {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return nil, DropBYOKOnly
	}
	return kept, DropNone
}
