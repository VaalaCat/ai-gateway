package exec

import (
	"errors"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type AgentRouteKind string
type AttemptTargetKind string

const (
	AgentRouteNone    AgentRouteKind = "none"
	AgentRouteHard    AgentRouteKind = "hard"
	AgentRouteToken   AgentRouteKind = "token"
	AgentRouteChannel AgentRouteKind = "channel"

	AttemptTargetLocal  AttemptTargetKind = "local"
	AttemptTargetRemote AttemptTargetKind = "remote"
)

var ErrAttemptRouteBuild = errors.New("attempt route build failed")

type AttemptRouteBuildError struct {
	code string
}

func (e *AttemptRouteBuildError) Error() string {
	if e == nil || e.code == "" {
		return ErrAttemptRouteBuild.Error()
	}
	return e.code
}

func (e *AttemptRouteBuildError) Unwrap() error {
	return ErrAttemptRouteBuild
}

func (e *AttemptRouteBuildError) RouteBuildCode() string {
	if e == nil {
		return agentproxy.CodeSelectorInvalid
	}
	return e.code
}

type AttemptRouteInput struct {
	Attempt           state.Attempt
	HardSelector      app.AgentSelector
	FrozenHardAgentID string
	TokenID           uint
	SourceAgentID     string
	RequestID         string
}

type AttemptTarget struct {
	AgentID string
	Kind    AttemptTargetKind
}

type AttemptRoute struct {
	Kind         AgentRouteKind
	AgentRouteID uint
	Hard         bool
	Targets      []AttemptTarget
}

type AttemptRouteBuilder interface {
	Build(AttemptRouteInput) (AttemptRoute, error)
}

type attemptRouteBuilder struct {
	cache app.AgentCache
}

func NewAttemptRouteBuilder(cache app.AgentCache) AttemptRouteBuilder {
	return &attemptRouteBuilder{cache: cache}
}

func (b *attemptRouteBuilder) Build(input AttemptRouteInput) (AttemptRoute, error) {
	if input.FrozenHardAgentID != "" {
		return b.buildHard(input, app.AgentSelector{AgentID: input.FrozenHardAgentID})
	}
	if selectorPresent(input.HardSelector) {
		return b.buildHard(input, input.HardSelector)
	}
	if b == nil || b.cache == nil {
		return localAttemptRoute(input.SourceAgentID), nil
	}
	if route := b.cache.FindTokenRoute(input.TokenID, input.Attempt.RealModel); route != nil {
		return b.buildSoft(input, AgentRouteToken, route), nil
	}
	if input.Attempt.Source == state.SourceAdmin {
		if route := b.cache.FindAdminChannelRoute(input.Attempt.SourceID, input.Attempt.RealModel); route != nil {
			return b.buildSoft(input, AgentRouteChannel, route), nil
		}
	}
	return localAttemptRoute(input.SourceAgentID), nil
}

func (b *attemptRouteBuilder) buildHard(input AttemptRouteInput, selector app.AgentSelector) (AttemptRoute, error) {
	route := AttemptRoute{Kind: AgentRouteHard, Hard: true}
	targets, err := b.targetsForSelector(input, selector, 0, true)
	if err != nil {
		return route, err
	}
	route.Targets = targets
	return route, nil
}

func (b *attemptRouteBuilder) buildSoft(input AttemptRouteInput, kind AgentRouteKind, rule *models.AgentRoute) AttemptRoute {
	route := AttemptRoute{Kind: kind, AgentRouteID: rule.ID}
	selector := app.AgentSelector{AgentID: rule.AgentID, AgentTag: rule.AgentTag}
	targets, err := b.targetsForSelector(input, selector, rule.ID, false)
	if err != nil {
		route.Targets = localAttemptTargets(input.SourceAgentID)
		return route
	}
	route.Targets = targets
	return route
}

func (b *attemptRouteBuilder) targetsForSelector(input AttemptRouteInput, selector app.AgentSelector, routeID uint, hard bool) ([]AttemptTarget, error) {
	hasID := selector.AgentID != ""
	hasTag := selector.AgentTag != ""
	if hasID == hasTag {
		return nil, newAttemptRouteBuildError(agentproxy.CodeSelectorInvalid)
	}
	if hasID {
		return b.targetsForAgentID(input.SourceAgentID, selector.AgentID, hard)
	}
	return b.targetsForAgentTag(input, selector.AgentTag, routeID, hard)
}

func (b *attemptRouteBuilder) targetsForAgentID(sourceAgentID, agentID string, hard bool) ([]AttemptTarget, error) {
	if agentID == sourceAgentID {
		return localAttemptTargets(sourceAgentID), nil
	}
	if b == nil || b.cache == nil {
		return nil, newAttemptRouteBuildError(agentproxy.CodeTargetNotFound)
	}
	target := b.cache.GetAgent(agentID)
	if target == nil {
		return nil, newAttemptRouteBuildError(agentproxy.CodeTargetNotFound)
	}
	if target.Status != consts.StatusEnabled {
		return nil, newAttemptRouteBuildError(agentproxy.CodeTargetDisabled)
	}
	targets := []AttemptTarget{{AgentID: agentID, Kind: AttemptTargetRemote}}
	return appendLocalTarget(targets, sourceAgentID, hard), nil
}

func (b *attemptRouteBuilder) targetsForAgentTag(input AttemptRouteInput, tag string, routeID uint, hard bool) ([]AttemptTarget, error) {
	if b == nil || b.cache == nil {
		return nil, newAttemptRouteBuildError(agentproxy.CodeTagNoCandidate)
	}
	ids, containsSource := eligibleAgentIDs(b.cache.GetAgentsByTag(tag), tag, input.SourceAgentID)
	if containsSource {
		return localAttemptTargets(input.SourceAgentID), nil
	}
	ring := agentproxy.StableAgentRing(input.RequestID, routeID, tag, ids)
	if len(ring) == 0 {
		return nil, newAttemptRouteBuildError(agentproxy.CodeTagNoCandidate)
	}
	targets := make([]AttemptTarget, 0, len(ring)+1)
	for _, agentID := range ring {
		targets = append(targets, AttemptTarget{AgentID: agentID, Kind: AttemptTargetRemote})
	}
	return appendLocalTarget(targets, input.SourceAgentID, hard), nil
}

func eligibleAgentIDs(candidates []*models.Agent, tag, sourceAgentID string) ([]string, bool) {
	ids := make([]string, 0, len(candidates))
	containsSource := false
	for _, candidate := range candidates {
		if candidate == nil || candidate.AgentID == "" || candidate.Status != consts.StatusEnabled || !agentHasExactTag(candidate, tag) {
			continue
		}
		ids = append(ids, candidate.AgentID)
		containsSource = containsSource || candidate.AgentID == sourceAgentID
	}
	return ids, containsSource
}

func agentHasExactTag(agent *models.Agent, tag string) bool {
	for member := range strings.SplitSeq(agent.Tags, ",") {
		if strings.TrimSpace(member) == tag {
			return true
		}
	}
	return false
}

func appendLocalTarget(targets []AttemptTarget, sourceAgentID string, hard bool) []AttemptTarget {
	if hard {
		return targets
	}
	return append(targets, AttemptTarget{AgentID: sourceAgentID, Kind: AttemptTargetLocal})
}

func localAttemptRoute(sourceAgentID string) AttemptRoute {
	return AttemptRoute{Kind: AgentRouteNone, Targets: localAttemptTargets(sourceAgentID)}
}

func localAttemptTargets(sourceAgentID string) []AttemptTarget {
	return []AttemptTarget{{AgentID: sourceAgentID, Kind: AttemptTargetLocal}}
}

func selectorPresent(selector app.AgentSelector) bool {
	return selector.AgentID != "" || selector.AgentTag != ""
}

func newAttemptRouteBuildError(code string) error {
	return &AttemptRouteBuildError{code: code}
}
