package genericapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/auth"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type allowPermissionGate struct{}

func (allowPermissionGate) AllowInvoke(context.Context, uint, uint, uint, uint) error { return nil }

type allowQuotaGate struct{}

func (allowQuotaGate) Allow(context.Context, uint, protocol.SyncedAPIService) error { return nil }

type statusProtocolHandler struct{ status int }

func (h statusProtocolHandler) Serve(_ context.Context, c *RequestContext) error {
	c.Context.Status(h.status)
	return nil
}

type recordingAPIUsageReporter struct{ entries []protocol.APIUsageEntry }

func (r *recordingAPIUsageReporter) EnqueueAPI(entry protocol.APIUsageEntry) error {
	r.entries = append(r.entries, entry)
	return nil
}

type countingProviderProtocolHandler struct{ calls int }

func (h *countingProviderProtocolHandler) Serve(_ context.Context, c *RequestContext) error {
	h.calls++
	c.Execution.ProviderDispatchKnown = true
	c.Execution.ProviderDispatched = true
	c.Context.Status(http.StatusNoContent)
	return nil
}

type capturingProtocolHandler struct {
	calls   int
	routeID uint
	subpath string
	err     error
}

func (h *capturingProtocolHandler) Serve(_ context.Context, c *RequestContext) error {
	h.calls++
	h.routeID = c.Route.ID
	h.subpath = c.Subpath
	if h.err == nil {
		c.Context.Status(http.StatusNoContent)
	}
	return h.err
}

func TestGenericAPIIgnoresClientRequestIDForSettlementIdentity(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	reporter := &recordingAPIUsageReporter{}
	provider := &countingProviderProtocolHandler{}
	responseRequestIDs := make([]string, 0, 2)
	handler := NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(serviceFinderIndex(t, service, route)),
		Permission: allowPermissionGate{},
		Quota:      allowQuotaGate{},
		Usage:      NewUsageBuilder(nil),
		Reporter:   reporter,
		Handlers:   map[string]ProtocolHandler{ProtocolHTTP: provider},
	})
	router := genericAPIRouter(handler)

	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
		request.Header.Set(consts.HeaderXRequestID, "client-controlled-id")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusNoContent, response.Code)
		responseRequestIDs = append(responseRequestIDs, response.Header().Get(consts.HeaderXRequestID))
	}

	require.Equal(t, 2, provider.calls, "client request IDs must not suppress provider dispatch")
	require.Len(t, reporter.entries, 2, "each external request needs its own settlement record")
	require.NotEmpty(t, reporter.entries[0].RequestID)
	require.NotEqual(t, "client-controlled-id", reporter.entries[0].RequestID)
	require.NotEqual(t, reporter.entries[0].RequestID, reporter.entries[1].RequestID)
	require.Equal(t, []string{reporter.entries[0].RequestID, reporter.entries[1].RequestID}, responseRequestIDs)
}

func TestHandlerForwardsStructuredFailureLogger(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{
			order: &[]string{}, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP},
		},
		Permission: &recordingPermissionGate{err: ErrAPIForbidden}, Quota: allowQuotaGate{},
		Usage: NewUsageBuilder(nil), Reporter: &recordingAPIUsageReporter{}, Logger: zap.New(core),
	})

	response := httptest.NewRecorder()
	genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Len(t, logs.FilterMessage("generic api request failed").All(), 1)
}

type committedErrorProtocolHandler struct{ err error }

func (h committedErrorProtocolHandler) Serve(_ context.Context, c *RequestContext) error {
	c.Context.String(http.StatusOK, "partial-response")
	return h.err
}

func TestHandlerDoesNotAppendGatewayErrorAfterCommittedResponse(t *testing.T) {
	streamErr := errors.New("remote response failed after commit")
	order := []string{}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: committedErrorProtocolHandler{err: streamErr}},
	})
	response := httptest.NewRecorder()
	genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "partial-response", response.Body.String())
}

type recordingPermissionGate struct {
	tokenID, userID, serviceID, routeID uint
	order                               *[]string
	err                                 error
	calls                               int
}

func (g *recordingPermissionGate) AllowInvoke(_ context.Context, tokenID, userID, serviceID, routeID uint) error {
	g.calls++
	g.tokenID, g.userID, g.serviceID, g.routeID = tokenID, userID, serviceID, routeID
	if g.order != nil {
		*g.order = append(*g.order, "permission")
	}
	return g.err
}

type recordingQuotaGate struct {
	order *[]string
	err   error
	calls int
}

type orderedServiceRouteFinder struct {
	order *[]string
	route ServiceRoute
}

func (f orderedServiceRouteFinder) Find(_, _, _, _ string) (ServiceRoute, string, error) {
	*f.order = append(*f.order, "finder")
	return f.route, "", nil
}

type countingServiceRouteFinder struct {
	inner ServiceRouteFinder
	calls int
}

func (f *countingServiceRouteFinder) Find(serviceSlug, requestPath, method, requestedProtocol string) (ServiceRoute, string, error) {
	f.calls++
	return f.inner.Find(serviceSlug, requestPath, method, requestedProtocol)
}

type orderedSourceLimiter struct{ order *[]string }

func (g orderedSourceLimiter) Acquire(_ context.Context, _ APIRequestFacts) (APIPermit, error) {
	*g.order = append(*g.order, "limiter")
	return orderedAPIPermit{order: g.order}, nil
}

type orderedAPIPermit struct{ order *[]string }

func (p orderedAPIPermit) Release() { *p.order = append(*p.order, "release") }

type orderedExecutionAgentPicker struct{ order *[]string }

func (p orderedExecutionAgentPicker) Pick(_, _, _ uint, _ string) (AgentPick, error) {
	*p.order = append(*p.order, "agent_pick")
	return AgentPick{ExecutionAgentID: "source-agent"}, nil
}

type orderedProtocolHandler struct{ order *[]string }

func (h orderedProtocolHandler) Serve(_ context.Context, rc *RequestContext) error {
	*h.order = append(*h.order, "dispatch")
	rc.Execution.ProviderDispatchKnown = true
	return nil
}

type orderedAPIUsageBuilder struct {
	order *[]string
	calls int
}

func (b *orderedAPIUsageBuilder) Build(_ APIExecution) protocol.APIUsageEntry {
	b.calls++
	*b.order = append(*b.order, "usage_build")
	return protocol.APIUsageEntry{RequestID: "pipeline-order"}
}

type orderedAPIUsageReporter struct {
	order *[]string
	calls int
}

type blockedStageLimiter struct{ calls int }

func (g *blockedStageLimiter) Acquire(context.Context, APIRequestFacts) (APIPermit, error) {
	g.calls++
	return nil, nil
}

type recordingStageLimiter struct {
	calls   int
	routeID uint
	err     error
}

func (g *recordingStageLimiter) Acquire(_ context.Context, facts APIRequestFacts) (APIPermit, error) {
	g.calls++
	g.routeID = facts.APIRouteID
	return nil, g.err
}

type blockedStageAgentPicker struct{ calls int }

func (p *blockedStageAgentPicker) Pick(uint, uint, uint, string) (AgentPick, error) {
	p.calls++
	return AgentPick{}, nil
}

type blockedStageProvider struct{ calls int }

func (p *blockedStageProvider) Serve(context.Context, *RequestContext) error {
	p.calls++
	return nil
}

func (r *orderedAPIUsageReporter) EnqueueAPI(_ protocol.APIUsageEntry) error {
	r.calls++
	*r.order = append(*r.order, "usage_enqueue")
	return nil
}

func TestSourcePipelineOrderIsRBACQuotaLimiterRouteDispatch(t *testing.T) {
	order := []string{}
	permission := &recordingPermissionGate{order: &order}
	quota := &recordingQuotaGate{order: &order}
	usage := &orderedAPIUsageBuilder{order: &order}
	reporter := &orderedAPIUsageReporter{order: &order}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	handler := NewHandler(HandlerOptions{
		Finder:        orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP}},
		Permission:    permission,
		Quota:         quota,
		Limiter:       orderedSourceLimiter{order: &order},
		AgentPicker:   orderedExecutionAgentPicker{order: &order},
		Usage:         usage,
		Reporter:      reporter,
		SourceAgentID: "source-agent",
		Handlers:      map[string]ProtocolHandler{ProtocolHTTP: orderedProtocolHandler{order: &order}},
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Set(consts.HeaderXRequestID, "pipeline-order")
	response := httptest.NewRecorder()

	genericAPIRouter(handler).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, []string{
		"finder", "permission", "quota", "limiter", "agent_pick", "dispatch",
		"release", "usage_build", "usage_enqueue",
	}, order)
	require.Equal(t, 1, usage.calls)
	require.Equal(t, 1, reporter.calls)
}

type terminalProtocolHandler struct {
	status int
	err    error
}

func (h terminalProtocolHandler) Serve(_ context.Context, rc *RequestContext) error {
	if h.status != 0 {
		rc.Context.Status(h.status)
	}
	return h.err
}

func TestUsageBuilderRunsExactlyOnceAtSourceForEveryTerminalPath(t *testing.T) {
	transportErr := errors.New("transport failed")
	for _, test := range []struct {
		name       string
		permission error
		handler    ProtocolHandler
	}{
		{name: "pre-dispatch", permission: ErrAPIForbidden, handler: terminalProtocolHandler{status: http.StatusNoContent}},
		{name: "transport", handler: terminalProtocolHandler{err: transportErr}},
		{name: "upstream 4xx", handler: terminalProtocolHandler{status: http.StatusTeapot}},
		{name: "success", handler: terminalProtocolHandler{status: http.StatusNoContent}},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := []string{}
			usage := &orderedAPIUsageBuilder{order: &order}
			reporter := &orderedAPIUsageReporter{order: &order}
			service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
			route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
			handler := NewHandler(HandlerOptions{
				Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP}},
				Permission: &recordingPermissionGate{err: test.permission}, Quota: allowQuotaGate{},
				Usage: usage, Reporter: reporter, SourceAgentID: "source",
				Handlers: map[string]ProtocolHandler{ProtocolHTTP: test.handler},
			})
			response := httptest.NewRecorder()
			genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))
			require.Equal(t, 1, usage.calls)
			require.Equal(t, 1, reporter.calls)
		})
	}
}

func TestUsageBuilderStillRunsOnceWhileAPIReporterSeamIsUnavailable(t *testing.T) {
	order := []string{}
	usage := &orderedAPIUsageBuilder{order: &order}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, Usage: usage,
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: terminalProtocolHandler{status: http.StatusNoContent}},
	})

	response := httptest.NewRecorder()
	genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))
	require.Equal(t, 1, usage.calls)
}

type rateFactPermit struct{ result apiattempt.APIExecutionResult }

func (p rateFactPermit) Release()                                       {}
func (p rateFactPermit) RateLimitResult() apiattempt.APIExecutionResult { return p.result }

type rateFactLimiter struct{ result apiattempt.APIExecutionResult }

func (l rateFactLimiter) Acquire(context.Context, APIRequestFacts) (APIPermit, error) {
	return rateFactPermit{result: l.result}, nil
}

type rateFactProtocolHandler struct{ result apiattempt.APIExecutionResult }

func (h rateFactProtocolHandler) Serve(_ context.Context, rc *RequestContext) error {
	rc.Execution = h.result
	return nil
}

type capturingUsageBuilder struct {
	execution APIExecution
	calls     int
}

func (b *capturingUsageBuilder) Build(execution APIExecution) protocol.APIUsageEntry {
	b.calls++
	b.execution = execution
	return protocol.APIUsageEntry{}
}

type failingExecutionAgentPicker struct{ err error }

func (p failingExecutionAgentPicker) Pick(uint, uint, uint, string) (AgentPick, error) {
	return AgentPick{}, p.err
}

type releasingRateFactPermit struct {
	result   apiattempt.APIExecutionResult
	releases atomic.Int32
}

func (p *releasingRateFactPermit) Release() { p.releases.Add(1) }
func (p *releasingRateFactPermit) RateLimitResult() apiattempt.APIExecutionResult {
	return p.result
}

type fixedPermitLimiter struct{ permit APIPermit }

func (l fixedPermitLimiter) Acquire(context.Context, APIRequestFacts) (APIPermit, error) {
	return l.permit, nil
}

func TestSourcePickerFailureKeepsCommittedRateFactsAndBuildsUsageOnce(t *testing.T) {
	order := []string{}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	permit := &releasingRateFactPermit{result: apiattempt.APIExecutionResult{
		RateLimitDecision: "allow",
		RateLimitHits:     []models.RateLimitHit{{LimiterID: 1, Decision: "allow", Dimension: "rate/shared"}},
	}}
	usage := &capturingUsageBuilder{}
	handler := NewHandler(HandlerOptions{
		Finder:      orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP}},
		Permission:  allowPermissionGate{},
		Quota:       allowQuotaGate{},
		Limiter:     fixedPermitLimiter{permit: permit},
		AgentPicker: failingExecutionAgentPicker{err: ErrExecutionUnavailable},
		Usage:       usage,
		Handlers:    map[string]ProtocolHandler{ProtocolHTTP: terminalProtocolHandler{status: http.StatusNoContent}},
	})
	response := httptest.NewRecorder()
	genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))

	require.Equal(t, int32(1), permit.releases.Load())
	require.Equal(t, 1, usage.calls)
	require.Equal(t, "allow", usage.execution.Result.RateLimitDecision)
	require.Len(t, usage.execution.Result.RateLimitHits, 1)
	require.Equal(t, uint(1), usage.execution.Result.RateLimitHits[0].LimiterID)
	require.ErrorIs(t, usage.execution.Err, ErrExecutionUnavailable)
}

func TestSourceMergesSourceAndExecutionLimiterFactsIntoOneUsageBuild(t *testing.T) {
	order := []string{}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	usage := &capturingUsageBuilder{}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolHTTP}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, Usage: usage,
		Limiter: rateFactLimiter{result: apiattempt.APIExecutionResult{
			RateLimitDecision: "allow", RateLimitHits: []models.RateLimitHit{{LimiterID: 1, Decision: "allow"}},
		}},
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: rateFactProtocolHandler{result: apiattempt.APIExecutionResult{
			ProviderDispatchKnown: true, RateLimitDecision: "queued", RateLimitWaitMs: 4,
			RateLimitHits: []models.RateLimitHit{{LimiterID: 2, Decision: "queued", WaitMs: 4}},
		}}},
	})

	response := httptest.NewRecorder()
	genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))
	require.Equal(t, "queued", usage.execution.Result.RateLimitDecision)
	require.Equal(t, 4, usage.execution.Result.RateLimitWaitMs)
	require.Len(t, usage.execution.Result.RateLimitHits, 2)
}

func (g *recordingQuotaGate) Allow(_ context.Context, _ uint, _ protocol.SyncedAPIService) error {
	g.calls++
	if g.order != nil {
		*g.order = append(*g.order, "quota")
	}
	return g.err
}

func TestGenericAPIRootRouteAndExplicitRouteShareServiceCatchAll(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	index := serviceFinderIndex(t, service,
		protocol.SyncedAPIRoute{ID: 8, ServiceID: 7, Slug: "", Status: 1, Protocols: []string{ProtocolHTTP}, ForwardSubpath: true},
		protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}},
		protocol.SyncedAPIRoute{ID: 10, ServiceID: 7, Slug: "files", Status: 1, Protocols: []string{ProtocolHTTP}, ForwardSubpath: true},
	)

	for _, test := range []struct {
		name        string
		path        string
		wantRouteID uint
		wantSubpath string
	}{
		{name: "service root", path: "/v1/api/weather", wantRouteID: 8},
		{name: "service root trailing slash", path: "/v1/api/weather/", wantRouteID: 8, wantSubpath: "/"},
		{name: "dynamic first segment uses root route", path: "/v1/api/weather/acme/users", wantRouteID: 8, wantSubpath: "/acme/users"},
		{name: "explicit route remains unchanged", path: "/v1/api/weather/current", wantRouteID: 9},
		{name: "explicit route receives only remaining subpath", path: "/v1/api/weather/files/a/b", wantRouteID: 10, wantSubpath: "/a/b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &capturingProtocolHandler{}
			handler := NewHandler(HandlerOptions{
				Finder: NewServiceFinder(index), Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
				Handlers: map[string]ProtocolHandler{ProtocolHTTP: capture},
			})
			response := httptest.NewRecorder()
			genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, nil))

			require.Equal(t, http.StatusNoContent, response.Code)
			require.Equal(t, test.wantRouteID, capture.routeID)
			require.Equal(t, test.wantSubpath, capture.subpath)
		})
	}
}

// Break caught: decoded or literal extra leading slashes must be rejected
// before the root route can become the request's authorization identity.
func TestGenericAPIRejectsExtraLeadingSlashBeforeRootRouteGates(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "users", Status: consts.StatusEnabled}
	root := protocol.SyncedAPIRoute{
		ID: 8, ServiceID: service.ID, Slug: "", Status: consts.StatusEnabled,
		Protocols: []string{ProtocolHTTP}, ForwardSubpath: true,
	}

	for _, path := range []string{
		"/v1/api/users//accounts",
		"/v1/api/users/%2Faccounts",
		"/v1/api/users/%252Faccounts",
	} {
		t.Run(path, func(t *testing.T) {
			permission := &recordingPermissionGate{}
			provider := &countingProviderProtocolHandler{}
			handler := NewHandler(HandlerOptions{
				Finder: NewServiceFinder(serviceFinderIndex(t, service, root)), Permission: permission, Quota: allowQuotaGate{},
				Handlers: map[string]ProtocolHandler{ProtocolHTTP: provider},
			})
			response := httptest.NewRecorder()
			genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, CodeInvalidRequest, responseBodyCode(t, response))
			require.Zero(t, permission.calls)
			require.Zero(t, provider.calls)
		})
	}
}

func TestGenericAPIExplicitRouteFailuresAfterFindNeverFallBackToRootRoute(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "users", Status: consts.StatusEnabled}
	root := protocol.SyncedAPIRoute{ID: 8, ServiceID: 7, Slug: "", Status: consts.StatusEnabled, Protocols: []string{ProtocolHTTP}}
	explicit := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "accounts", Status: consts.StatusEnabled, Protocols: []string{ProtocolHTTP}}

	for _, test := range []struct {
		name           string
		permissionErr  error
		quotaErr       error
		limiterErr     error
		dispatchErr    error
		wantStatus     int
		wantQuotaCalls int
		wantLimitCalls int
		wantDispatch   int
	}{
		{name: "permission", permissionErr: ErrAPIForbidden, wantStatus: http.StatusForbidden},
		{name: "quota", quotaErr: ErrInsufficientQuota, wantStatus: http.StatusPaymentRequired, wantQuotaCalls: 1},
		{name: "limiter", limiterErr: ErrAPIRateLimited, wantStatus: http.StatusTooManyRequests, wantQuotaCalls: 1, wantLimitCalls: 1},
		{name: "dispatch", dispatchErr: ErrExecutionUnavailable, wantStatus: http.StatusServiceUnavailable, wantQuotaCalls: 1, wantLimitCalls: 1, wantDispatch: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			finder := &countingServiceRouteFinder{inner: NewServiceFinder(serviceFinderIndex(t, service, root, explicit))}
			permission := &recordingPermissionGate{err: test.permissionErr}
			quota := &recordingQuotaGate{err: test.quotaErr}
			limiter := &recordingStageLimiter{err: test.limiterErr}
			dispatch := &capturingProtocolHandler{err: test.dispatchErr}
			handler := NewHandler(HandlerOptions{
				Finder: finder, Permission: permission, Quota: quota, Limiter: limiter,
				Handlers: map[string]ProtocolHandler{ProtocolHTTP: dispatch},
			})
			response := httptest.NewRecorder()
			genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/users/accounts", nil))

			require.Equal(t, test.wantStatus, response.Code)
			require.Equal(t, 1, finder.calls, "handler must select the route only once")
			require.Equal(t, 1, permission.calls)
			require.Equal(t, explicit.ID, permission.routeID)
			require.Equal(t, test.wantQuotaCalls, quota.calls)
			require.Equal(t, test.wantLimitCalls, limiter.calls)
			if limiter.calls > 0 {
				require.Equal(t, explicit.ID, limiter.routeID)
			}
			require.Equal(t, test.wantDispatch, dispatch.calls)
			if dispatch.calls > 0 {
				require.Equal(t, explicit.ID, dispatch.routeID)
			}
		})
	}
}

func TestGenericAPIReturnsCacheNotReadyBeforeEntityLookup(t *testing.T) {
	handler := NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(nil),
		Permission: allowPermissionGate{},
		Quota:      allowQuotaGate{},
	})
	router := genericAPIRouter(handler)
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Set(consts.HeaderXRequestID, "cache-not-ready")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, CodeCacheNotReady, responseBodyCode(t, response))
	require.NotEmpty(t, response.Header().Get(consts.HeaderXRequestID))
	require.NotEqual(t, "cache-not-ready", response.Header().Get(consts.HeaderXRequestID))
}

func TestGenericAPIRejectsNonWebSocketHTTPUpgrade(t *testing.T) {
	handler := newHandlerForTest(t)
	router := genericAPIRouter(handler)
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Set(consts.HeaderConnection, "Upgrade")
	request.Header.Set("Upgrade", "h2c")
	request.Header.Set(consts.HeaderXRequestID, "bad-upgrade")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, CodeInvalidUpgrade, responseBodyCode(t, response))
	require.NotEmpty(t, response.Header().Get(consts.HeaderXRequestID))
	require.NotEqual(t, "bad-upgrade", response.Header().Get(consts.HeaderXRequestID))
}

func TestGenericAPIAcceptsBrowserKeepAliveAsHTTP(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Set(consts.HeaderConnection, "keep-alive")
	response := httptest.NewRecorder()

	genericAPIRouter(newHandlerForTest(t)).ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestGenericAPIPreContractUpgradeRejectionsPublishNoUsage(t *testing.T) {
	for _, test := range []struct {
		name    string
		upgrade string
	}{
		{name: "missing Upgrade header"},
		{name: "h2c", upgrade: "h2c"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter := &recordingAPIUsageReporter{}
			handler := NewHandler(HandlerOptions{
				Usage: NewUsageBuilder(nil), Reporter: reporter,
			})
			request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
			request.Header.Set(consts.HeaderConnection, "Upgrade")
			if test.upgrade != "" {
				request.Header.Set("Upgrade", test.upgrade)
			}
			response := httptest.NewRecorder()

			genericAPIRouter(handler).ServeHTTP(response, request)

			require.Equal(t, http.StatusBadRequest, response.Code)
			require.NotEmpty(t, response.Header().Get(consts.HeaderXRequestID))
			require.Contains(t, response.Body.String(), `"code":"invalid_upgrade"`)
			require.Empty(t, reporter.entries, "pre-contract rejection must not create an identity-less usage entry")
		})
	}
}

func TestGenericAPIRejectsSubpathWhenRouteDoesNotForwardIt(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	root := protocol.SyncedAPIRoute{ID: 8, ServiceID: 7, Slug: "", Status: 1, Protocols: []string{ProtocolHTTP}, ForwardSubpath: true}
	provider := &countingProviderProtocolHandler{}
	handler := NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(serviceFinderIndex(t, service, root, route)),
		Permission: allowPermissionGate{},
		Quota:      allowQuotaGate{},
		Handlers:   map[string]ProtocolHandler{ProtocolHTTP: provider},
	})
	router := genericAPIRouter(handler)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current/a", nil))

	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, CodeAPINotFound, responseBodyCode(t, response))
	require.Zero(t, provider.calls, "the root route must not bypass an explicit route's ForwardSubpath policy")
}

func TestGenericAPIWebSocketUpgradeAcceptsHeaderListsAndRepeatedFields(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolWebSocket}}
	handler := NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(serviceFinderIndex(t, service, route)),
		Permission: allowPermissionGate{},
		Quota:      allowQuotaGate{},
		Handlers:   map[string]ProtocolHandler{ProtocolWebSocket: statusProtocolHandler{status: http.StatusNoContent}},
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Add(consts.HeaderConnection, "keep-alive")
	request.Header.Add(consts.HeaderConnection, "Upgrade")
	request.Header.Add("Upgrade", "h2c")
	request.Header.Add("Upgrade", "WebSocket")
	setWebSocketHandshakeHeaders(request, "13", "AQIDBAUGBwgJCgsMDQ4PEA==")
	response := httptest.NewRecorder()

	genericAPIRouter(handler).ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestGenericAPIRejectsMalformedWebSocketUpgrade(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func(*http.Request)
		wantCode string
	}{
		{name: "missing connection side", mutate: func(request *http.Request) { request.Header.Set("Upgrade", "websocket") }},
		{name: "missing upgrade side", mutate: func(request *http.Request) { request.Header.Set(consts.HeaderConnection, "Upgrade") }},
		{name: "h2c", mutate: func(request *http.Request) {
			request.Header.Set(consts.HeaderConnection, "Upgrade")
			request.Header.Set("Upgrade", "h2c")
		}},
		{name: "missing version", mutate: func(request *http.Request) { setWebSocketHandshakeHeaders(request, "", "AQIDBAUGBwgJCgsMDQ4PEA==") }},
		{name: "wrong version", mutate: func(request *http.Request) { setWebSocketHandshakeHeaders(request, "12", "AQIDBAUGBwgJCgsMDQ4PEA==") }},
		{name: "missing key", mutate: func(request *http.Request) { setWebSocketHandshakeHeaders(request, "13", "") }},
		{name: "wrong key", mutate: func(request *http.Request) { setWebSocketHandshakeHeaders(request, "13", "not-base64") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
			test.mutate(request)
			if request.Header.Get("Upgrade") == "" && test.name != "missing upgrade side" {
				request.Header.Set("Upgrade", "websocket")
			}
			if request.Header.Get(consts.HeaderConnection) == "" && test.name != "missing connection side" {
				request.Header.Set(consts.HeaderConnection, "Upgrade")
			}
			response := httptest.NewRecorder()
			genericAPIRouter(newHandlerForTest(t)).ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Equal(t, CodeInvalidUpgrade, responseBodyCode(t, response))
		})
	}
}

func TestGenericAPIHandlerMapsGateFailuresAndPreservesGateOrder(t *testing.T) {
	for _, test := range []struct {
		name       string
		permission error
		quota      error
		wantStatus int
		wantCode   string
		wantOrder  []string
	}{
		{name: "permission forbidden", permission: ErrAPIForbidden, wantStatus: http.StatusForbidden, wantCode: CodeAPIForbidden, wantOrder: []string{"permission"}},
		{name: "permission facts unavailable", permission: ErrPermissionFactsUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "permission_facts_unavailable", wantOrder: []string{"permission"}},
		{name: "quota insufficient", quota: ErrInsufficientQuota, wantStatus: http.StatusPaymentRequired, wantCode: CodeInsufficientQuota, wantOrder: []string{"permission", "quota"}},
		{name: "quota facts unavailable", quota: ErrQuotaFactsUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "quota_facts_unavailable", wantOrder: []string{"permission", "quota"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := []string{}
			permission := &recordingPermissionGate{order: &order, err: test.permission}
			quota := &recordingQuotaGate{order: &order, err: test.quota}
			handler := handlerWithGates(t, permission, quota)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
			request.Header.Set(consts.HeaderXRequestID, "gate-"+test.name)
			genericAPIRouter(handler).ServeHTTP(response, request)

			require.Equal(t, test.wantStatus, response.Code)
			require.Equal(t, test.wantCode, responseBodyCode(t, response))
			require.Equal(t, test.wantOrder, order)
		})
	}
}

// Break caught: an API role compile/full-sync failure leaves the role index
// dirty. Generic invocation must return the existing explicit 503 mapping
// before quota, limiter, Agent selection, protocol dispatch, or provider I/O.
func TestAPIRBACIsolationDirtyRoleIndexFailsGenericHandlerClosed(t *testing.T) {
	index := permissionTestIndex(t)
	index.MarkDirty(events.EntityAPIRole)
	facts := &permissionFacts{
		tokens: map[uint]*models.Token{3: {
			ID: 3, UserID: 5, APIRoleMode: models.APIRoleModeExplicit,
		}},
		tokenRoles: map[uint]*protocol.APIRoleSet{3: {RoleIDs: []uint{1}}},
	}
	quota := &recordingQuotaGate{}
	limiter := &blockedStageLimiter{}
	picker := &blockedStageAgentPicker{}
	provider := &blockedStageProvider{}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP},
	}
	handler := NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(serviceFinderIndex(t, service, route)),
		Permission: NewPermissionGate(facts, facts, index), Quota: quota,
		Limiter: limiter, AgentPicker: picker,
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: provider},
	})
	response := httptest.NewRecorder()

	genericAPIRouter(handler).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil),
	)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, CodeCacheNotReady, responseBodyCode(t, response))
	require.Zero(t, quota.calls)
	require.Zero(t, limiter.calls)
	require.Zero(t, picker.calls)
	require.Zero(t, provider.calls)
}

func TestGenericAPIKeepsExecutionUnavailableAsAPIUnavailable(t *testing.T) {
	handler := handlerWithGates(t, allowPermissionGate{}, allowQuotaGate{})
	handler.executor = nil
	response := httptest.NewRecorder()
	genericAPIRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil))

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, CodeUnavailable, responseBodyCode(t, response))
}

func TestGenericAPIGateReceivesTokenAuthIdentity(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.SetToken(&models.Token{ID: 31, UserID: 47, Key: "gateway-token", Status: consts.StatusEnabled, ExpiredAt: -1})
	order := []string{}
	permission := &recordingPermissionGate{order: &order}
	quota := &recordingQuotaGate{order: &order}
	router := gin.New()
	router.Use(auth.TokenAuth(store))
	RegisterRoutes(router.Group("/v1"), handlerWithGates(t, permission, quota))
	request := httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	request.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"gateway-token")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Equal(t, uint(31), permission.tokenID)
	require.Equal(t, uint(47), permission.userID)
	require.Equal(t, uint(7), permission.serviceID)
	require.Equal(t, uint(9), permission.routeID)
	require.Equal(t, []string{"permission", "quota"}, order)
}

func newHandlerForTest(t *testing.T) *Handler {
	t.Helper()
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}, ForwardSubpath: true}
	return NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(serviceFinderIndex(t, service, route)),
		Permission: allowPermissionGate{},
		Quota:      allowQuotaGate{},
		Handlers:   map[string]ProtocolHandler{ProtocolHTTP: statusProtocolHandler{status: http.StatusNoContent}},
	})
}

func handlerWithGates(t *testing.T, permission PermissionChecker, quota QuotaChecker) *Handler {
	t.Helper()
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: 1, Protocols: []string{ProtocolHTTP}}
	return NewHandler(HandlerOptions{
		Finder:     NewServiceFinder(serviceFinderIndex(t, service, route)),
		Permission: permission,
		Quota:      quota,
		Handlers:   map[string]ProtocolHandler{ProtocolHTTP: statusProtocolHandler{status: http.StatusNoContent}},
	})
}

func setWebSocketHandshakeHeaders(request *http.Request, version, key string) {
	if version != "" {
		request.Header.Set("Sec-WebSocket-Version", version)
	}
	if key != "" {
		request.Header.Set("Sec-WebSocket-Key", key)
	}
}

func genericAPIRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/v1")
	v1.Use(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TokenID: 3, UserID: 5})
	})
	RegisterRoutes(v1, handler)
	return router
}

func TestNewRequestContextProjectsTokenNameFromAuthenticatedIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity any
		want     string
	}{
		{name: "named token", identity: &app.UserInfo{TokenID: 3, TokenName: "production"}, want: "production"},
		{name: "empty token name", identity: &app.UserInfo{TokenID: 3}},
		{name: "missing identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/api/weather/forecast", nil)
			if test.identity != nil {
				c.Set(consts.CtxKeyUserInfo, test.identity)
			}

			rc := newRequestContext(c, ServiceRoute{}, "", "request", apiattempt.APITracePolicy{})

			require.Equal(t, test.want, rc.TokenName)
		})
	}
}

func responseBodyCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Error.Code
}

type staticGenericAPIUsageSupport bool

func (supported staticGenericAPIUsageSupport) SupportsGenericAPIUsage() bool { return bool(supported) }

type panicRequestBody struct{}

func (panicRequestBody) Read([]byte) (int, error) { panic("request body read before capability gate") }
func (panicRequestBody) Close() error             { return nil }

func TestGenericAPICapabilityRejectsBeforeFinderBodyAndDispatch(t *testing.T) {
	order := []string{}
	handler := NewHandler(HandlerOptions{
		MasterUsageSupport: staticGenericAPIUsageSupport(false),
		Finder:             orderedServiceRouteFinder{order: &order},
		Handlers:           map[string]ProtocolHandler{ProtocolHTTP: orderedProtocolHandler{order: &order}},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", nil)
	request.Body = panicRequestBody{}
	response := httptest.NewRecorder()

	genericAPIRouter(handler).ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, CodeUnavailable, responseBodyCode(t, response))
	require.Empty(t, order)
}
