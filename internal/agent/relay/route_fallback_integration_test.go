package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	relayexec "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/exec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

type handlerRouteBuilder struct {
	route  relayexec.AttemptRoute
	routes []relayexec.AttemptRoute
	calls  int
	order  *[]string
	inputs []relayexec.AttemptRouteInput
}

func (b *handlerRouteBuilder) Build(input relayexec.AttemptRouteInput) (relayexec.AttemptRoute, error) {
	b.calls++
	b.inputs = append(b.inputs, input)
	appendHandlerOrder(b.order, "route")
	if len(b.routes) >= b.calls {
		return b.routes[b.calls-1], nil
	}
	return b.route, nil
}

type handlerRemoteExecutor struct {
	outcome            relayexec.AttemptOutcome
	outcomes           []relayexec.AttemptOutcome
	calls              int
	providerDispatches int
	order              *[]string
	targets            []relayexec.AttemptTarget
	bound              []attemptwire.BoundAttempt
	bodies             [][]byte
}

func (e *handlerRemoteExecutor) Execute(
	rctx *state.RelayContext,
	target relayexec.AttemptTarget,
	_ uint,
	bound attemptwire.BoundAttempt,
) relayexec.AttemptOutcome {
	e.calls++
	e.targets = append(e.targets, target)
	e.bound = append(e.bound, bound)
	if rctx != nil && rctx.Resources != nil && rctx.Resources.Body() != nil {
		body, _ := rctx.Resources.Body().Bytes(1 << 20)
		e.bodies = append(e.bodies, body)
	}
	appendHandlerOrder(e.order, "remote")
	outcome := e.outcome
	if len(e.outcomes) >= e.calls {
		outcome = e.outcomes[e.calls-1]
	}
	e.providerDispatches += outcome.Dispatches
	return outcome
}

type handlerLocalExecutor struct {
	calls              int
	providerDispatches int
	outcomes           []relayexec.AttemptOutcome
	attempts           []state.Attempt
}

func (e *handlerLocalExecutor) Execute(_ *state.RelayContext, attempt state.Attempt) relayexec.AttemptOutcome {
	e.calls++
	e.attempts = append(e.attempts, attempt)
	outcome := relayexec.AttemptOutcome{
		Kind: relayexec.AttemptSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
		ExecutionAgentID: "source", Path: app.RoutePathLocal, Commit: tunnel.Committed,
		Dispatches: 1,
	}
	if len(e.outcomes) >= e.calls {
		outcome = e.outcomes[e.calls-1]
	}
	e.providerDispatches += outcome.Dispatches
	return outcome
}

type handlerOrderedPlanner struct {
	calls int
	model string
	order *[]string
	plan  state.AttemptPlan
}

func (p *handlerOrderedPlanner) Solve(rctx *state.RelayContext) error {
	p.calls++
	p.model = rctx.Input.Model
	appendHandlerOrder(p.order, "planner")
	rctx.State.Plan = p.plan
	return nil
}

func appendHandlerOrder(order *[]string, step string) {
	if order != nil {
		*order = append(*order, step)
	}
}

// behavior change: request scripts run before a hard attempt route is built.
func TestRouteFallbackHardRunsAfterRequestScripts(t *testing.T) {
	store := newAttemptRoutingStore(t)
	store.LoadScripts([]models.AdminScript{{
		ID: 1, Name: "reject-before-hard", Enabled: true,
		Code:  `function onRequest(ctx) { ctx.reject(418, "script ran first") }`,
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}})
	routes := &handlerRouteBuilder{route: hardRemoteRoute("peer")}
	remote := &handlerRemoteExecutor{outcome: successfulRemoteOutcome("peer")}
	h := newAttemptRoutingHandler(store, routes, &handlerLocalExecutor{}, remote)

	w := serveAttemptRouting(t, h, app.AgentSelector{AgentID: "peer"})

	require.Equal(t, http.StatusTeapot, w.Code)
	require.Contains(t, w.Body.String(), "script ran first")
	require.Zero(t, routes.calls)
	require.Zero(t, remote.calls)
}

func TestRouteFallbackScriptAndPlannerRunOnceBeforeHardExecution(t *testing.T) {
	store := newAttemptRoutingStore(t)
	store.LoadScripts([]models.AdminScript{{
		ID: 1, Name: "rewrite-before-plan", Enabled: true,
		Code:  `function onRequest(ctx) { ctx.body.model = "scripted-model"; ctx.body.script_runs = (ctx.body.script_runs || 0) + 1 }`,
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}})
	order := []string{}
	attempt := state.Attempt{
		Channel: store.GetChannel(1), Source: state.SourceAdmin, SourceID: 1,
		RealModel: "scripted-real", Mode: state.ModeNative,
	}
	routes := &handlerRouteBuilder{route: hardRemoteRoute("peer"), order: &order}
	remote := &handlerRemoteExecutor{outcome: successfulRemoteOutcome("peer"), order: &order}
	h := newAttemptRoutingHandler(store, routes, &handlerLocalExecutor{}, remote)
	planner := &handlerOrderedPlanner{order: &order, plan: state.AttemptPlan{Attempts: []state.Attempt{attempt}}}
	h.planner = planner

	w := serveAttemptRouting(t, h, app.AgentSelector{AgentID: "peer"})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, planner.calls)
	require.Equal(t, "scripted-model", planner.model)
	require.Equal(t, []string{"planner", "route", "remote"}, order)
	require.Equal(t, 1, routes.calls)
	require.Equal(t, 1, remote.calls)
	require.Equal(t, []relayexec.AttemptTarget{{AgentID: "peer", Kind: relayexec.AttemptTargetRemote}}, remote.targets)
	require.Equal(t, []attemptwire.BoundAttempt{{
		Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 1},
		RealModel: "scripted-real", Mode: attemptwire.ModeNative,
	}}, remote.bound)
	require.Len(t, remote.bodies, 1)
	require.JSONEq(t, `{"model":"scripted-model","script_runs":1}`, string(remote.bodies[0]))
}

func TestRouteFallbackHardTransportUnavailableDoesNotRunSourceLocal(t *testing.T) {
	store := newAttemptRoutingStore(t)
	routes := &handlerRouteBuilder{route: hardRemoteRoute("peer")}
	remote := &handlerRemoteExecutor{outcome: relayexec.AttemptOutcome{
		Kind:             relayexec.AttemptTransportUnavailable,
		Result:           state.AttemptResult{Err: http.ErrServerClosed},
		ExecutionAgentID: "peer", Path: app.RoutePathRelay,
		Commit: tunnel.PreCommit, ReasonCode: "relay_not_ready",
	}}
	local := &handlerLocalExecutor{}
	h := newAttemptRoutingHandler(store, routes, local, remote)

	w := serveAttemptRouting(t, h, app.AgentSelector{AgentID: "peer"})

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), http.ErrServerClosed.Error())
	require.Equal(t, 1, remote.calls)
	require.Zero(t, local.calls)
}

func TestRouteFallbackCurrentAttemptRunsLocalBeforeLaterRemote(t *testing.T) {
	store := newAttemptRoutingStore(t)
	attempts := []state.Attempt{
		handlerAttemptWithIdentity(store, state.SourceAdmin, 1, "model-a"),
		handlerAttemptWithIdentity(store, state.SourcePrivate, 2, "model-b"),
		handlerAttemptWithIdentity(store, state.SourceAdmin, 3, "model-c"),
	}
	firstRoute := softRemoteRoute("target-a")
	firstRoute.Targets = append(firstRoute.Targets, relayexec.AttemptTarget{AgentID: "source", Kind: relayexec.AttemptTargetLocal})
	routes := &handlerRouteBuilder{routes: []relayexec.AttemptRoute{
		firstRoute, softRemoteRoute("target-b"),
	}}
	local := &handlerLocalExecutor{outcomes: []relayexec.AttemptOutcome{retryableLocalHandlerOutcome("source")}}
	remote := &handlerRemoteExecutor{outcomes: []relayexec.AttemptOutcome{
		unavailableHandlerOutcome("target-a"), successfulRemoteOutcome("target-b"),
	}}
	h := newAttemptRoutingHandler(store, routes, local, remote)
	h.planner = &handlerOrderedPlanner{plan: state.AttemptPlan{Attempts: attempts}}

	w := serveAttemptRouting(t, h, app.AgentSelector{})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, attempts[:2], []state.Attempt{routes.inputs[0].Attempt, routes.inputs[1].Attempt})
	require.Equal(t, []state.Attempt{attempts[0]}, local.attempts)
	require.Equal(t, []relayexec.AttemptTarget{
		{AgentID: "target-a", Kind: relayexec.AttemptTargetRemote},
		{AgentID: "target-b", Kind: relayexec.AttemptTargetRemote},
	}, remote.targets)
	require.Equal(t, []attemptwire.BoundAttempt{
		{Channel: attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 1}, RealModel: "model-a", Mode: attemptwire.ModeNative},
		{Channel: attemptwire.ChannelRef{Source: attemptwire.SourcePrivate, ID: 2}, RealModel: "model-b", Mode: attemptwire.ModeNative},
	}, remote.bound)
	require.Len(t, routes.inputs, 2, "successful B must stop before route C is built")
}

// behavior change: a known provider response advances only to the next
// business attempt; it never replays the same attempt on another agent/local.
func TestRouteFallbackProviderFailureAdvancesOnlyAcrossBusinessAttempts(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		withNext   bool
		wantStatus int
	}{
		{name: "429 advances to B", status: http.StatusTooManyRequests, withNext: true, wantStatus: http.StatusOK},
		{name: "500 advances to B", status: http.StatusInternalServerError, withNext: true, wantStatus: http.StatusOK},
		{name: "only A preserves 429", status: http.StatusTooManyRequests, wantStatus: http.StatusTooManyRequests},
		{name: "only A maps 500", status: http.StatusInternalServerError, wantStatus: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newAttemptRoutingStore(t)
			attempts := []state.Attempt{handlerAttemptWithIdentity(store, state.SourceAdmin, 1, "model-a")}
			firstRoute := softRemoteRoute("target-a")
			firstRoute.Targets = append(firstRoute.Targets,
				relayexec.AttemptTarget{AgentID: "target-a-other", Kind: relayexec.AttemptTargetRemote},
				relayexec.AttemptTarget{AgentID: "source", Kind: relayexec.AttemptTargetLocal},
			)
			routeList := []relayexec.AttemptRoute{firstRoute}
			if tt.withNext {
				attempts = append(attempts, handlerAttemptWithIdentity(store, state.SourcePrivate, 2, "model-b"))
				routeList = append(routeList, localHandlerRoute())
			}
			routes := &handlerRouteBuilder{routes: routeList}
			remote := &handlerRemoteExecutor{outcome: providerFailureHandlerOutcome("target-a", tt.status)}
			local := &handlerLocalExecutor{}
			h := newAttemptRoutingHandler(store, routes, local, remote)
			h.planner = &handlerOrderedPlanner{plan: state.AttemptPlan{Attempts: attempts}}

			w := serveAttemptRouting(t, h, app.AgentSelector{})

			require.Equal(t, tt.wantStatus, w.Code)
			require.Equal(t, []relayexec.AttemptTarget{{AgentID: "target-a", Kind: relayexec.AttemptTargetRemote}}, remote.targets)
			require.Equal(t, 1, remote.providerDispatches, "A provider dispatch count")
			if tt.withNext {
				require.Equal(t, []state.Attempt{attempts[1]}, local.attempts, "only B may run source-local")
				require.Equal(t, 1, local.providerDispatches, "B provider dispatch count")
				require.Len(t, routes.inputs, 2)
			} else {
				require.Empty(t, local.attempts, "A source-local replay is forbidden")
				require.Zero(t, local.providerDispatches)
				require.Len(t, routes.inputs, 1)
			}
		})
	}
}

func TestRouteFallbackHardAgentIDFreezesAcrossPlan(t *testing.T) {
	store := newAttemptRoutingStore(t)
	attempts := []state.Attempt{handlerAttempt(store, 1), handlerAttempt(store, 2)}
	routes := &handlerRouteBuilder{routes: []relayexec.AttemptRoute{
		hardRemoteRoute("peer"), hardRemoteRoute("peer"),
	}}
	remote := &handlerRemoteExecutor{outcomes: []relayexec.AttemptOutcome{
		retryableHandlerOutcome("peer"), successfulRemoteOutcome("peer"),
	}}
	h := newAttemptRoutingHandler(store, routes, &handlerLocalExecutor{}, remote)
	h.planner = &handlerOrderedPlanner{plan: state.AttemptPlan{Attempts: attempts}}

	w := serveAttemptRouting(t, h, app.AgentSelector{AgentID: "peer"})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"peer", "peer"}, []string{remote.targets[0].AgentID, remote.targets[1].AgentID})
	require.Empty(t, routes.inputs[0].FrozenHardAgentID)
	require.Equal(t, "peer", routes.inputs[1].FrozenHardAgentID)
}

func TestRouteFallbackSoftTransportUnavailableExecutesSourceLocal(t *testing.T) {
	store := newAttemptRoutingStore(t)
	route := softRemoteRoute("peer")
	route.Targets = append(route.Targets, relayexec.AttemptTarget{AgentID: "source", Kind: relayexec.AttemptTargetLocal})
	routes := &handlerRouteBuilder{route: route}
	remote := &handlerRemoteExecutor{outcome: unavailableHandlerOutcome("peer")}
	local := &handlerLocalExecutor{}
	h := newAttemptRoutingHandler(store, routes, local, remote)

	w := serveAttemptRouting(t, h, app.AgentSelector{})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, remote.calls)
	require.Equal(t, 1, local.calls)
}

func hardRemoteRoute(agentID string) relayexec.AttemptRoute {
	return relayexec.AttemptRoute{
		Kind: relayexec.AgentRouteHard, Hard: true,
		Targets: []relayexec.AttemptTarget{{AgentID: agentID, Kind: relayexec.AttemptTargetRemote}},
	}
}

func softRemoteRoute(agentID string) relayexec.AttemptRoute {
	return relayexec.AttemptRoute{
		Kind:    relayexec.AgentRouteToken,
		Targets: []relayexec.AttemptTarget{{AgentID: agentID, Kind: relayexec.AttemptTargetRemote}},
	}
}

func localHandlerRoute() relayexec.AttemptRoute {
	return relayexec.AttemptRoute{
		Kind:    relayexec.AgentRouteNone,
		Targets: []relayexec.AttemptTarget{{AgentID: "source", Kind: relayexec.AttemptTargetLocal}},
	}
}

func successfulRemoteOutcome(agentID string) relayexec.AttemptOutcome {
	return relayexec.AttemptOutcome{
		Kind: relayexec.AttemptSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
		ExecutionAgentID: agentID, Path: app.RoutePathDirect, Commit: tunnel.Committed,
		Result: state.AttemptResult{PromptTokens: 4, CompletionTokens: 2},
	}
}

func retryableHandlerOutcome(agentID string) relayexec.AttemptOutcome {
	return relayexec.AttemptOutcome{
		Kind:             relayexec.AttemptProviderFailed,
		Result:           state.AttemptResult{Err: http.ErrHandlerTimeout},
		ExecutionAgentID: agentID, Path: app.RoutePathDirect, Commit: tunnel.Committed,
		ProviderResultKnown: true, ProviderDispatched: true, PlanAdvanceAllowed: true,
		Dispatches: 1,
	}
}

func retryableLocalHandlerOutcome(agentID string) relayexec.AttemptOutcome {
	outcome := retryableHandlerOutcome(agentID)
	outcome.Path = app.RoutePathLocal
	return outcome
}

func unavailableHandlerOutcome(agentID string) relayexec.AttemptOutcome {
	return relayexec.AttemptOutcome{
		Kind:             relayexec.AttemptTransportUnavailable,
		Result:           state.AttemptResult{Err: http.ErrServerClosed},
		ExecutionAgentID: agentID, Path: app.RoutePathRelay,
		Commit: tunnel.PreCommit, ReasonCode: "relay_not_ready",
	}
}

func providerFailureHandlerOutcome(agentID string, status int) relayexec.AttemptOutcome {
	return relayexec.AttemptOutcome{
		Kind: relayexec.AttemptProviderFailed,
		Result: state.AttemptResult{Err: &common.UpstreamError{
			Status: status, Body: []byte(fmt.Sprintf(`{"error":"provider %d"}`, status)),
		}},
		ExecutionAgentID: agentID, Path: app.RoutePathDirect, Commit: tunnel.Committed,
		ProviderResultKnown: true, ProviderDispatched: true, PlanAdvanceAllowed: true,
		Dispatches: 1,
	}
}

func handlerAttempt(store *cache.Store, id uint) state.Attempt {
	return handlerAttemptWithIdentity(store, state.SourceAdmin, id, "gpt-4o")
}

func handlerAttemptWithIdentity(store *cache.Store, source state.ChannelSource, id uint, realModel string) state.Attempt {
	channel := store.GetChannel(1)
	copy := *channel
	copy.ID = id
	return state.Attempt{
		Channel: &copy, Source: source, SourceID: id,
		RealModel: realModel, Mode: state.ModeNative,
	}
}

func newAttemptRoutingStore(t *testing.T) *cache.Store {
	t.Helper()
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.LoadSettings([]models.Setting{{Key: "retry_max_channels", Value: "5"}})
	store.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{
		ID: 1, Name: "local", Type: consts.ChannelTypeOpenAI,
		Status: consts.StatusEnabled, Weight: 1,
	}, Key: "key", Models: "gpt-4o,scripted-model"})
	store.RebuildModelIndex()
	return store
}

func newAttemptRoutingHandler(
	store *cache.Store,
	routes relayexec.AttemptRouteBuilder,
	local relayexec.LocalAttemptExecutor,
	remote relayexec.RemoteAttemptExecutor,
) *Handler {
	agent := agentappkg.NewDefaultAgentApplication(
		store, relayTestBodyStore{}, nil, &config.AgentRuntimeConfig{}, nil,
	)
	return NewHandler(
		eventbus.NewMemoryBus(), agent, nil, nil, nil, nil,
		WithAttemptRouting("source", routes, local, remote),
	)
}

func serveAttemptRouting(t *testing.T, h *Handler, selector app.AgentSelector) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	if selector.AgentID != "" {
		req.Header.Set(consts.HeaderXAgentID, selector.AgentID)
	}
	if selector.AgentTag != "" {
		req.Header.Set(consts.HeaderXAgentTag, selector.AgentTag)
	}
	c.Request = req
	h.Relay(c)
	return w
}
