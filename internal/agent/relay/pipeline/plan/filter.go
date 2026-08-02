package plan

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type DropCode int

const (
	DropNone              DropCode = iota
	DropInsufficientQuota          // → state.ErrInsufficientQuota (402)
	DropBYOKOnly                   // → state.ErrBYOKOnlyNoChannel (404)
	DropFactsUnavailable           // read-only inspection must fail closed when quota facts are missing
)

type FilterContext struct {
	Rctx              *state.RelayContext
	Store             app.Store
	UserInfo          *app.UserInfo
	RequestContext    context.Context
	RealModel         string
	RequireQuotaFacts bool
}

func (fctx *FilterContext) candidateStore() app.Store {
	if fctx == nil {
		return nil
	}
	if fctx.Store != nil {
		return fctx.Store
	}
	if fctx.Rctx == nil || fctx.Rctx.Agent == nil {
		return nil
	}
	return fctx.Rctx.Agent.GetCache()
}

func (fctx *FilterContext) viewerInfo() *app.UserInfo {
	if fctx == nil {
		return nil
	}
	if fctx.UserInfo != nil {
		return fctx.UserInfo
	}
	if fctx.Rctx == nil {
		return nil
	}
	return fctx.Rctx.Input.UserInfo
}

func (fctx *FilterContext) requestContext() context.Context {
	if fctx == nil {
		return context.Background()
	}
	if fctx.RequestContext != nil {
		return fctx.RequestContext
	}
	if fctx.Rctx != nil && fctx.Rctx.Request != nil {
		return fctx.Rctx.Request.Context()
	}
	return context.Background()
}

type CandidateFilter interface {
	Name() string
	Apply(fctx *FilterContext, in []ScoredCandidate) (kept []ScoredCandidate, emptiedBy DropCode)
}

func defaultCandidateFilters() []CandidateFilter {
	return []CandidateFilter{byokOnlyFilter{}, quotaFilter{}}
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
