package plan

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/state"

type DropCode int

const (
	DropNone DropCode = iota
	DropInsufficientQuota // → state.ErrInsufficientQuota (402)
)

type FilterContext struct {
	Rctx      *state.RelayContext
	RealModel string
}

type CandidateFilter interface {
	Name() string
	Apply(fctx *FilterContext, in []ScoredCandidate) (kept []ScoredCandidate, emptiedBy DropCode)
}

// runFilters 按序跑 filters;任一 filter 把候选收空且带原因则中断并返回该原因。
func runFilters(fctx *FilterContext, cands []ScoredCandidate, filters []CandidateFilter) ([]ScoredCandidate, DropCode) {
	for _, f := range filters {
		if len(cands) == 0 {
			break
		}
		var code DropCode
		cands, code = f.Apply(fctx, cands)
		if len(cands) == 0 && code != DropNone {
			return cands, code
		}
	}
	return cands, DropNone
}
