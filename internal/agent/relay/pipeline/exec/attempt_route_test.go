package exec

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

type attemptRouteCache struct {
	app.AgentCache
	tokenRoute   *models.AgentRoute
	channelRoute *models.AgentRoute
	agents       map[string]*models.Agent
	byTag        map[string][]*models.Agent
	tokenCalls   int
	channelCalls int
	tokenID      uint
	tokenModel   string
	channelID    uint
	channelModel string
	getAgentIDs  []string
	getTags      []string
}

func (c *attemptRouteCache) FindTokenRoute(tokenID uint, realModel string) *models.AgentRoute {
	c.tokenCalls++
	c.tokenID = tokenID
	c.tokenModel = realModel
	return c.tokenRoute
}

func (c *attemptRouteCache) FindAdminChannelRoute(channelID uint, realModel string) *models.AgentRoute {
	c.channelCalls++
	c.channelID = channelID
	c.channelModel = realModel
	return c.channelRoute
}

func (c *attemptRouteCache) GetAgent(agentID string) *models.Agent {
	c.getAgentIDs = append(c.getAgentIDs, agentID)
	return c.agents[agentID]
}

func (c *attemptRouteCache) GetAgentsByTag(tag string) []*models.Agent {
	c.getTags = append(c.getTags, tag)
	return c.byTag[tag]
}

func TestAttemptRouteBuilderPriorityAndMetadata(t *testing.T) {
	hardCache := newAttemptRouteCache(
		enabledAgent("hard-a", ""),
		enabledAgent("token-a", ""),
		enabledAgent("channel-a", ""),
	)
	hardCache.tokenRoute = routeByID(11, "token-a")
	hardCache.channelRoute = routeByID(12, "channel-a")
	tokenCache := newAttemptRouteCache(enabledAgent("token-a", ""), enabledAgent("source", ""))
	tokenCache.tokenRoute = routeByID(21, "token-a")
	tokenCache.channelRoute = routeByID(22, "channel-a")
	adminCache := newAttemptRouteCache(enabledAgent("channel-a", ""), enabledAgent("source", ""))
	adminCache.channelRoute = routeByID(31, "channel-a")
	privateCache := newAttemptRouteCache(enabledAgent("channel-a", ""), enabledAgent("source", ""))
	privateCache.channelRoute = routeByID(41, "channel-a")
	localTagCache := newAttemptRouteCache(enabledAgent("source", "gpu"), enabledAgent("remote", "gpu"))
	localTagCache.tokenRoute = routeByTag(51, "gpu")
	localTagCache.byTag["gpu"] = []*models.Agent{localTagCache.agents["remote"], localTagCache.agents["source"]}
	frozenCache := newAttemptRouteCache(enabledAgent("hard-b", ""), enabledAgent("stale", "gpu"))
	frozenCache.tokenRoute = routeByID(61, "token-a")
	frozenCache.channelRoute = routeByID(62, "channel-a")
	frozenCache.byTag["gpu"] = []*models.Agent{frozenCache.agents["stale"]}

	tests := []struct {
		name          string
		cache         *attemptRouteCache
		in            AttemptRouteInput
		wantKind      AgentRouteKind
		wantRouteID   uint
		wantIDs       []string
		wantHard      bool
		wantToken     int
		wantChannel   int
		wantTokenID   uint
		wantChannelID uint
	}{
		{
			name: "hard overrides token and channel", cache: hardCache,
			in:       AttemptRouteInput{Attempt: state.Attempt{RealModel: "real-hard", Source: state.SourceAdmin, SourceID: 8}, HardSelector: app.AgentSelector{AgentID: "hard-a"}, TokenID: 7, SourceAgentID: "source", RequestID: "request-hard"},
			wantKind: AgentRouteHard, wantIDs: []string{"hard-a"}, wantHard: true,
		},
		{
			name: "token exact uses real model", cache: tokenCache,
			in:       AttemptRouteInput{Attempt: state.Attempt{RealModel: "real-token", Source: state.SourceAdmin, SourceID: 18}, TokenID: 17, SourceAgentID: "source", RequestID: "request-token"},
			wantKind: AgentRouteToken, wantRouteID: 21, wantIDs: []string{"token-a", "source"}, wantToken: 1, wantTokenID: 17,
		},
		{
			name: "current admin channel route", cache: adminCache,
			in:       AttemptRouteInput{Attempt: state.Attempt{RealModel: "real-admin", Source: state.SourceAdmin, SourceID: 28}, TokenID: 27, SourceAgentID: "source", RequestID: "request-admin"},
			wantKind: AgentRouteChannel, wantRouteID: 31, wantIDs: []string{"channel-a", "source"}, wantToken: 1, wantChannel: 1, wantTokenID: 27, wantChannelID: 28,
		},
		{
			name: "private ignores same id admin route", cache: privateCache,
			in:       AttemptRouteInput{Attempt: state.Attempt{RealModel: "real-private", Source: state.SourcePrivate, SourceID: 28}, TokenID: 27, SourceAgentID: "source", RequestID: "request-private"},
			wantKind: AgentRouteNone, wantIDs: []string{"source"}, wantToken: 1, wantTokenID: 27,
		},
		{
			name: "tag containing source is local only", cache: localTagCache,
			in:       AttemptRouteInput{Attempt: state.Attempt{RealModel: "real-local-tag", Source: state.SourceAdmin, SourceID: 38}, TokenID: 37, SourceAgentID: "source", RequestID: "request-local-tag"},
			wantKind: AgentRouteToken, wantRouteID: 51, wantIDs: []string{"source"}, wantToken: 1, wantTokenID: 37,
		},
		{
			name: "frozen hard target ignores changed tag", cache: frozenCache,
			in:       AttemptRouteInput{Attempt: state.Attempt{RealModel: "real-frozen", Source: state.SourceAdmin, SourceID: 48}, HardSelector: app.AgentSelector{AgentID: "stale", AgentTag: "gpu"}, FrozenHardAgentID: "hard-b", TokenID: 47, SourceAgentID: "source", RequestID: "request-frozen"},
			wantKind: AgentRouteHard, wantIDs: []string{"hard-b"}, wantHard: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, err := NewAttemptRouteBuilder(tt.cache).Build(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.wantKind, route.Kind)
			require.Equal(t, tt.wantRouteID, route.AgentRouteID)
			require.Equal(t, tt.wantHard, route.Hard)
			require.Equal(t, tt.wantIDs, targetIDs(route.Targets))
			requireTargetKinds(t, route.Targets, tt.in.SourceAgentID)
			require.Equal(t, tt.wantToken, tt.cache.tokenCalls)
			require.Equal(t, tt.wantChannel, tt.cache.channelCalls)
			if tt.wantToken > 0 {
				require.Equal(t, tt.wantTokenID, tt.cache.tokenID)
				require.Equal(t, tt.in.Attempt.RealModel, tt.cache.tokenModel)
			}
			if tt.wantChannel > 0 {
				require.Equal(t, tt.wantChannelID, tt.cache.channelID)
				require.Equal(t, tt.in.Attempt.RealModel, tt.cache.channelModel)
			}
		})
	}
	require.Empty(t, frozenCache.getTags, "frozen target must not re-read the mutable hard tag")
}

func TestAttemptRouteBuilderTagOrderingIsStableAfterCandidateFiltering(t *testing.T) {
	cache := newAttemptRouteCache(
		enabledAgent("agent-a", "gpu"),
		enabledAgent("agent-b", "gpu, fast"),
		enabledAgent("agent-c", " gpu "),
		enabledAgent("wrong-tag", "gpu-extra"),
		&models.Agent{AgentID: "disabled", Status: consts.StatusDisabled, Tags: "gpu"},
		&models.Agent{AgentID: "", Status: consts.StatusEnabled, Tags: "gpu"},
	)
	cache.tokenRoute = routeByTag(42, "gpu")
	ordered := []*models.Agent{
		cache.agents["agent-c"], nil, cache.agents["disabled"], cache.agents["agent-a"],
		cache.agents["wrong-tag"], cache.agents["agent-b"], cache.agents["agent-a"], cache.agents[""],
	}
	shuffled := []*models.Agent{
		cache.agents["agent-b"], cache.agents["agent-a"], cache.agents[""], cache.agents["agent-c"],
		nil, cache.agents["agent-b"], cache.agents["wrong-tag"], cache.agents["disabled"],
	}
	in := AttemptRouteInput{
		Attempt: state.Attempt{RealModel: "real-model", Source: state.SourceAdmin, SourceID: 9},
		TokenID: 7, SourceAgentID: "source", RequestID: "request-a",
	}

	for i := range 100 {
		if i%2 == 0 {
			cache.byTag["gpu"] = ordered
		} else {
			cache.byTag["gpu"] = shuffled
		}
		route, err := NewAttemptRouteBuilder(cache).Build(in)
		require.NoError(t, err)
		require.Equal(t, []string{"agent-b", "agent-c", "agent-a", "source"}, targetIDs(route.Targets))
	}
}

func TestAttemptRouteBuilderSoftAndHardTargetShapes(t *testing.T) {
	cache := newAttemptRouteCache(
		enabledAgent("source", "local-tag"),
		enabledAgent("remote-a", "gpu, local-tag"),
		enabledAgent("remote-b", "gpu"),
	)
	cache.byTag["local-tag"] = []*models.Agent{cache.agents["remote-a"], cache.agents["source"]}
	cache.byTag["gpu"] = []*models.Agent{cache.agents["remote-b"], cache.agents["remote-a"]}

	tests := []struct {
		name     string
		selector app.AgentSelector
		hard     bool
		wantIDs  []string
	}{
		{name: "tag containing source soft is local", selector: app.AgentSelector{AgentTag: "local-tag"}, wantIDs: []string{"source"}},
		{name: "tag containing source hard is local", selector: app.AgentSelector{AgentTag: "local-tag"}, hard: true, wantIDs: []string{"source"}},
		{name: "tag without source soft appends local", selector: app.AgentSelector{AgentTag: "gpu"}, wantIDs: append(agentproxy.StableAgentRing("request-shapes", 77, "gpu", []string{"remote-a", "remote-b"}), "source")},
		{name: "tag without source hard is remote ring", selector: app.AgentSelector{AgentTag: "gpu"}, hard: true, wantIDs: agentproxy.StableAgentRing("request-shapes", 0, "gpu", []string{"remote-a", "remote-b"})},
		{name: "explicit source soft is local", selector: app.AgentSelector{AgentID: "source"}, wantIDs: []string{"source"}},
		{name: "explicit source hard is local", selector: app.AgentSelector{AgentID: "source"}, hard: true, wantIDs: []string{"source"}},
		{name: "explicit remote soft appends local", selector: app.AgentSelector{AgentID: "remote-a"}, wantIDs: []string{"remote-a", "source"}},
		{name: "explicit remote hard has no local", selector: app.AgentSelector{AgentID: "remote-a"}, hard: true, wantIDs: []string{"remote-a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := AttemptRouteInput{Attempt: state.Attempt{RealModel: "real", Source: state.SourceAdmin, SourceID: 5}, SourceAgentID: "source", RequestID: "request-shapes"}
			if tt.hard {
				in.HardSelector = tt.selector
			} else {
				cache.tokenRoute = &models.AgentRoute{ID: 77, AgentID: tt.selector.AgentID, AgentTag: tt.selector.AgentTag}
			}
			route, err := NewAttemptRouteBuilder(cache).Build(in)
			require.NoError(t, err)
			require.Equal(t, tt.wantIDs, targetIDs(route.Targets))
			require.Equal(t, tt.hard, route.Hard)
			requireTargetKinds(t, route.Targets, "source")
		})
	}
}

func TestAttemptRouteBuilderInvalidTargetsFailClosedOnlyForHardRoutes(t *testing.T) {
	cache := newAttemptRouteCache(
		&models.Agent{AgentID: "disabled", Status: consts.StatusDisabled},
		enabledAgent("source", ""),
	)

	tests := []struct {
		name      string
		selector  app.AgentSelector
		hard      bool
		wantKind  AgentRouteKind
		wantID    uint
		wantError bool
		wantCode  string
	}{
		{name: "soft missing keeps route metadata", selector: app.AgentSelector{AgentID: "missing"}, wantKind: AgentRouteToken, wantID: 82},
		{name: "soft disabled keeps route metadata", selector: app.AgentSelector{AgentID: "disabled"}, wantKind: AgentRouteToken, wantID: 82},
		{name: "soft invalid selector degrades local", selector: app.AgentSelector{AgentID: "missing", AgentTag: "gpu"}, wantKind: AgentRouteToken, wantID: 82},
		{name: "soft empty tag degrades local", selector: app.AgentSelector{AgentTag: "gpu"}, wantKind: AgentRouteToken, wantID: 82},
		{name: "hard missing fails closed", selector: app.AgentSelector{AgentID: "missing"}, hard: true, wantError: true, wantCode: agentproxy.CodeTargetNotFound},
		{name: "hard disabled fails closed", selector: app.AgentSelector{AgentID: "disabled"}, hard: true, wantError: true, wantCode: agentproxy.CodeTargetDisabled},
		{name: "hard empty tag fails closed", selector: app.AgentSelector{AgentTag: "gpu"}, hard: true, wantError: true, wantCode: agentproxy.CodeTagNoCandidate},
		{name: "hard both id and tag fails closed", selector: app.AgentSelector{AgentID: "missing", AgentTag: "gpu"}, hard: true, wantError: true, wantCode: agentproxy.CodeSelectorInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := AttemptRouteInput{Attempt: state.Attempt{RealModel: "real", Source: state.SourcePrivate, SourceID: 3}, SourceAgentID: "source", RequestID: "request-invalid"}
			if tt.hard {
				in.HardSelector = tt.selector
			} else {
				cache.tokenRoute = &models.AgentRoute{ID: 82, AgentID: tt.selector.AgentID, AgentTag: tt.selector.AgentTag}
			}

			route, err := NewAttemptRouteBuilder(cache).Build(in)
			if tt.wantError {
				requireAttemptRouteBuildError(t, err, tt.wantCode)
				require.Equal(t, AgentRouteHard, route.Kind)
				require.True(t, route.Hard)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantKind, route.Kind)
			require.Equal(t, tt.wantID, route.AgentRouteID)
			require.Equal(t, []string{"source"}, targetIDs(route.Targets))
		})
	}
}

func TestAttemptRouteBuilderFrozenHardTarget(t *testing.T) {
	cache := newAttemptRouteCache(enabledAgent("frozen", ""), enabledAgent("stale", "gpu"))
	cache.byTag["gpu"] = []*models.Agent{cache.agents["stale"]}

	route, err := NewAttemptRouteBuilder(cache).Build(AttemptRouteInput{
		Attempt:      state.Attempt{RealModel: "real", Source: state.SourceAdmin, SourceID: 3},
		HardSelector: app.AgentSelector{AgentID: "stale", AgentTag: "gpu"}, FrozenHardAgentID: "frozen",
		TokenID: 2, SourceAgentID: "source", RequestID: "request-frozen",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"frozen"}, targetIDs(route.Targets))
	require.Equal(t, []string{"frozen"}, cache.getAgentIDs)
	require.Empty(t, cache.getTags)

	local, err := NewAttemptRouteBuilder(nil).Build(AttemptRouteInput{
		HardSelector: app.AgentSelector{AgentTag: "changed"}, FrozenHardAgentID: "source", SourceAgentID: "source",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"source"}, targetIDs(local.Targets))

	_, err = NewAttemptRouteBuilder(cache).Build(AttemptRouteInput{
		FrozenHardAgentID: "missing", SourceAgentID: "source",
	})
	requireAttemptRouteBuildError(t, err, agentproxy.CodeTargetNotFound)
}

func TestAttemptRouteBuilderNoRuleAndNilCacheAreLocalAndPanicFree(t *testing.T) {
	tests := []struct {
		name  string
		cache app.AgentCache
		in    AttemptRouteInput
	}{
		{name: "nil cache", in: AttemptRouteInput{SourceAgentID: "source"}},
		{name: "empty input", cache: newAttemptRouteCache(nil)},
		{name: "private no rules", cache: newAttemptRouteCache(nil), in: AttemptRouteInput{Attempt: state.Attempt{Source: state.SourcePrivate}, SourceAgentID: "source"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				route, err := NewAttemptRouteBuilder(tt.cache).Build(tt.in)
				require.NoError(t, err)
				require.Equal(t, AgentRouteNone, route.Kind)
				require.Zero(t, route.AgentRouteID)
				require.False(t, route.Hard)
				require.Equal(t, []string{tt.in.SourceAgentID}, targetIDs(route.Targets))
			})
		})
	}

	_, err := NewAttemptRouteBuilder(nil).Build(AttemptRouteInput{
		HardSelector: app.AgentSelector{AgentID: "remote"}, SourceAgentID: "source",
	})
	requireAttemptRouteBuildError(t, err, agentproxy.CodeTargetNotFound)
}

func TestAttemptRouteBuildErrorNilReceiverContract(t *testing.T) {
	var typed *AttemptRouteBuildError

	require.Equal(t, ErrAttemptRouteBuild.Error(), typed.Error())
	require.Equal(t, agentproxy.CodeSelectorInvalid, typed.RouteBuildCode())
	require.ErrorIs(t, typed, ErrAttemptRouteBuild)
}

func newAttemptRouteCache(agents ...*models.Agent) *attemptRouteCache {
	cache := &attemptRouteCache{
		agents: make(map[string]*models.Agent, len(agents)),
		byTag:  make(map[string][]*models.Agent),
	}
	for _, agent := range agents {
		if agent != nil {
			cache.agents[agent.AgentID] = agent
		}
	}
	return cache
}

func enabledAgent(agentID, tags string) *models.Agent {
	return &models.Agent{AgentID: agentID, Status: consts.StatusEnabled, Tags: tags}
}

func routeByID(routeID uint, agentID string) *models.AgentRoute {
	return &models.AgentRoute{ID: routeID, AgentID: agentID}
}

func routeByTag(routeID uint, tag string) *models.AgentRoute {
	return &models.AgentRoute{ID: routeID, AgentTag: tag}
}

func targetIDs(targets []AttemptTarget) []string {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.AgentID)
	}
	return ids
}

func requireTargetKinds(t *testing.T, targets []AttemptTarget, sourceAgentID string) {
	t.Helper()
	for _, target := range targets {
		if target.AgentID == sourceAgentID {
			require.Equal(t, AttemptTargetLocal, target.Kind)
		} else {
			require.Equal(t, AttemptTargetRemote, target.Kind)
		}
	}
}

func requireAttemptRouteBuildError(t *testing.T, err error, wantCode string) {
	t.Helper()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAttemptRouteBuild)
	var typed *AttemptRouteBuildError
	require.True(t, errors.As(err, &typed))
	require.Equal(t, wantCode, typed.RouteBuildCode())
}
