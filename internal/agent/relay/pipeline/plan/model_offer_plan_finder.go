package plan

import (
	"context"
	"errors"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

var (
	ErrModelOfferPlanUnavailable      = errors.New("model offer planner unavailable")
	ErrModelOfferPlanFactsUnavailable = errors.New("model offer planning facts unavailable")
)

type ModelOfferPlanBlock string

const (
	ModelOfferPlanBlockedInsufficientQuota ModelOfferPlanBlock = "insufficient_quota"
	ModelOfferPlanBlockedBYOKOnly          ModelOfferPlanBlock = "byok_only"
	ModelOfferPlanBlockedModelWhitelist    ModelOfferPlanBlock = "model_not_allowed"
)

type ModelOfferPlanQuery struct {
	Model            string
	UserInfo         *app.UserInfo
	ForcedChannelID  uint
	InboundProtocols []codec.Protocol
}

type ModelOfferPlan struct {
	RequestModel string
	Routing      bool
	RealModels   []string
	Candidates   []ModelOfferCandidate
	Blocked      []ModelOfferPlanBlock
}

type ModelOfferCandidate struct {
	RealModel string
	Source    state.ChannelSource
	SourceID  uint
	Modes     []ModelOfferCandidateMode
}

type ModelOfferCandidateMode struct {
	Protocol codec.Protocol
	Mode     state.RelayMode
}

type ModelOfferPlanStore interface {
	app.Store
	RoutingStore
}

// ModelOfferPlanFinder returns a deterministic, exhaustive snapshot of the
// candidates Relay can generate before runtime execution. It deliberately does
// not sort by weight, apply affinity, consume RetryMax, run scripts, acquire a
// limiter, or invoke an upstream.
type ModelOfferPlanFinder struct {
	store ModelOfferPlanStore
}

func NewModelOfferPlanFinder(store ModelOfferPlanStore) *ModelOfferPlanFinder {
	return &ModelOfferPlanFinder{store: store}
}

func (f *ModelOfferPlanFinder) Find(ctx context.Context, query ModelOfferPlanQuery) (ModelOfferPlan, error) {
	result := ModelOfferPlan{RequestModel: query.Model}
	if f == nil || f.store == nil || query.Model == "" {
		return result, ErrModelOfferPlanUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}

	owner := routingOwner(query.UserInfo)
	result.Routing = f.store.ResolveRouting(ctx, query.Model, owner) != nil
	chain := buildDeterministicChainFromInput(f.store, ctx, query.Model, query.UserInfo)
	result.RealModels = sortedUniqueModels(chain.Models)

	pool := channelPoolImpl{}
	for _, realModel := range result.RealModels {
		if !allowedModelForViewer(realModel, query.UserInfo) {
			result.addBlock(ModelOfferPlanBlockedModelWhitelist)
			continue
		}
		candidates := pool.availableFromStore(f.store, query.UserInfo, realModel, query.ForcedChannelID)
		if len(candidates) == 0 {
			continue
		}
		filtered, dropCode := runFilters(&FilterContext{
			Store:             f.store,
			UserInfo:          query.UserInfo,
			RequestContext:    ctx,
			RealModel:         realModel,
			RequireQuotaFacts: true,
		}, candidates, defaultCandidateFilters())
		if dropCode == DropFactsUnavailable {
			return result.withoutCandidates(), ErrModelOfferPlanFactsUnavailable
		}
		result.addDropBlock(dropCode)
		for _, candidate := range filtered {
			result.Candidates = append(result.Candidates, offerCandidateFromScored(
				realModel,
				candidate,
				query.InboundProtocols,
			))
		}
	}

	sort.SliceStable(result.Candidates, func(i, j int) bool {
		left, right := result.Candidates[i], result.Candidates[j]
		if left.RealModel != right.RealModel {
			return left.RealModel < right.RealModel
		}
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		return left.SourceID < right.SourceID
	})
	return result, nil
}

func routingOwner(ui *app.UserInfo) protocol.RoutingOwner {
	if ui == nil {
		return protocol.RoutingOwner{}
	}
	return protocol.RoutingOwner{UserID: ui.UserID, TokenID: ui.TokenID}
}

func sortedUniqueModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

func allowedModelForViewer(model string, ui *app.UserInfo) bool {
	if ui == nil {
		return true
	}
	return modelAllowedByWhitelist(model, *ui)
}

func offerCandidateFromScored(
	realModel string,
	candidate ScoredCandidate,
	protocols []codec.Protocol,
) ModelOfferCandidate {
	result := ModelOfferCandidate{
		RealModel: realModel,
		Source:    candidate.Source,
		SourceID:  candidate.SourceID,
		Modes:     make([]ModelOfferCandidateMode, 0, len(protocols)),
	}
	picker := defaultModePicker{}
	for _, inboundProtocol := range protocols {
		result.Modes = append(result.Modes, ModelOfferCandidateMode{
			Protocol: inboundProtocol,
			Mode:     picker.Pick(candidate.Channel, realModel, inboundProtocol),
		})
	}
	return result
}

func (plan *ModelOfferPlan) addDropBlock(code DropCode) {
	switch code {
	case DropInsufficientQuota:
		plan.addBlock(ModelOfferPlanBlockedInsufficientQuota)
	case DropBYOKOnly:
		plan.addBlock(ModelOfferPlanBlockedBYOKOnly)
	}
}

func (plan *ModelOfferPlan) addBlock(block ModelOfferPlanBlock) {
	for _, existing := range plan.Blocked {
		if existing == block {
			return
		}
	}
	plan.Blocked = append(plan.Blocked, block)
}

func (plan ModelOfferPlan) withoutCandidates() ModelOfferPlan {
	plan.Candidates = nil
	return plan
}
