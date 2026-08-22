package genericapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/auth"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type PermissionChecker interface {
	AllowInvoke(context.Context, uint, uint, uint, uint) error
}

type QuotaChecker interface {
	Allow(context.Context, uint, protocol.SyncedAPIService) error
}

type ProtocolHandler interface {
	Serve(context.Context, *RequestContext) error
}

type ServiceRouteFinder interface {
	Find(serviceSlug, requestPath, method, requestedProtocol string) (ServiceRoute, string, error)
}

type APIPermit interface {
	Release()
}

type APIRequestFacts struct {
	UserID, GroupID, TokenID uint
	APIServiceID, APIRouteID uint
	APIUpstreamID            uint
	RequestID                string
	NoWait                   bool
}

type SourceLimiter interface {
	Acquire(context.Context, APIRequestFacts) (APIPermit, error)
}

type ExecutionAgentPicker interface {
	Pick(tokenID, routeID, serviceID uint, requestID string) (AgentPick, error)
}

type ExecutionAgentCapabilityFinder interface {
	SupportsGenericAPIExecution(agentID string) bool
}

type APIUsageBuilder interface {
	Build(APIExecution) protocol.APIUsageEntry
}

type APIUsageReporter interface {
	EnqueueAPI(protocol.APIUsageEntry) error
}

type GenericAPIUsageSupport interface {
	SupportsGenericAPIUsage() bool
}

type AgentPick struct {
	ExecutionAgentID string
	AgentRouteID     uint
	AgentRoutePath   app.RoutePath
	Target           models.Agent
}

type APIExecution struct {
	Request           *RequestContext
	Result            apiattempt.APIExecutionResult
	Err               error
	StatusCode        int
	DurationMs        int
	QuotaGateDecision string
	SourceAgentID     string
}

type RequestContext struct {
	Context                *gin.Context
	Service                protocol.SyncedAPIService
	Route                  protocol.SyncedAPIRoute
	Protocol               string
	Subpath                string
	TokenID                uint
	TokenName              string
	UserID                 uint
	RequestID              string
	GroupID                uint
	Agent                  AgentPick
	Execution              apiattempt.APIExecutionResult
	UpstreamName           string
	QuotaGateDecision      string
	TracePolicy            apiattempt.APITracePolicy
	ClientUpgradeCommitted bool
	ClientStatusCode       int
	sourceTrace            *sourceAPITraceCapture
}

type HandlerOptions struct {
	Finder                ServiceRouteFinder
	Permission            PermissionChecker
	Quota                 QuotaChecker
	Limiter               SourceLimiter
	AgentPicker           ExecutionAgentPicker
	ExecutionCapabilities ExecutionAgentCapabilityFinder
	Usage                 APIUsageBuilder
	Reporter              APIUsageReporter
	MasterUsageSupport    GenericAPIUsageSupport
	SourceAgentID         string
	TraceSettings         APITraceSettingsReader
	Metrics               *APIMetrics
	Logger                *zap.Logger
	Executor              RequestExecutor
	// Handlers is retained for test and embedding compatibility. New production
	// assembly should inject Executor explicitly.
	Handlers map[string]ProtocolHandler
}

type Handler struct {
	finder                ServiceRouteFinder
	permission            PermissionChecker
	quota                 QuotaChecker
	limiter               SourceLimiter
	agentPicker           ExecutionAgentPicker
	executionCapabilities ExecutionAgentCapabilityFinder
	masterUsageSupport    GenericAPIUsageSupport
	sourceAgentID         string
	traceSettings         APITraceSettingsReader
	executor              RequestExecutor
	publisher             *Publisher
}

const requestIDContextKey = "generic_api_internal_request_id"

func NewHandler(options HandlerOptions) *Handler {
	executor := options.Executor
	if executor == nil {
		executor = NewExecutor(options.Handlers)
	}
	return &Handler{
		finder: options.Finder, permission: options.Permission, quota: options.Quota,
		limiter: options.Limiter, agentPicker: options.AgentPicker,
		executionCapabilities: options.ExecutionCapabilities, masterUsageSupport: options.MasterUsageSupport,
		sourceAgentID: options.SourceAgentID, traceSettings: options.TraceSettings,
		executor: executor, publisher: NewPublisher(PublisherOptions{
			Usage: options.Usage, Reporter: options.Reporter,
			SourceAgentID: options.SourceAgentID, Metrics: options.Metrics, Logger: options.Logger,
		}),
	}
}

// RegisterRoutes binds the service root and its catch-all under an authenticated /v1 group.
func RegisterRoutes(router gin.IRoutes, handler *Handler) {
	if router == nil || handler == nil {
		return
	}
	router.Any("/api/:service_slug", handler.Serve)
	router.Any("/api/:service_slug/*path", handler.Serve)
}

// RequestIDMiddleware canonicalizes the request ID before Generic API auth
// can produce a gateway-shaped failure response.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		setRequestID(c)
		c.Next()
	}
}

// WriteTokenAuthFailure adapts the shared TokenAuth failure fact to the
// Generic API error envelope without coupling auth back to genericapi.
func WriteTokenAuthFailure(c *gin.Context, failure auth.TokenAuthFailure) {
	requestID := setRequestID(c)
	writeGatewayError(c, requestID, failure.Status, failure.Code, "")
}

func (h *Handler) Serve(c *gin.Context) {
	requestID := setRequestID(c)
	requested, requestedErr := requestedProtocol(c.Request)
	publication := h.beginPublication(requestID, requested)
	if requestedErr != nil {
		writeHandlerError(c, requestID, requestedErr)
		publication.FinishMetrics(requestedErr)
		return
	}
	if h != nil && h.masterUsageSupport != nil && !h.masterUsageSupport.SupportsGenericAPIUsage() {
		writeHandlerError(c, requestID, ErrExecutionUnavailable)
		publication.FinishMetrics(ErrExecutionUnavailable)
		return
	}
	rc, permit, err := h.prepare(c, requestID, requested)
	if err == nil {
		publication.StartExecution(rc)
		err = h.dispatch(c.Request.Context(), rc)
	}
	publication.Publish(c, rc, permit, err)
}

func (h *Handler) beginPublication(requestID, protocol string) *Publication {
	if h == nil {
		return (*Publisher)(nil).Begin(requestID, protocol)
	}
	return h.publisher.Begin(requestID, protocol)
}

func (h *Handler) prepare(c *gin.Context, requestID, requested string) (*RequestContext, APIPermit, error) {
	if h == nil || h.finder == nil {
		return nil, nil, ErrExecutionUnavailable
	}
	route, subpath, err := h.finder.Find(c.Param("service_slug"), c.Param("path"), c.Request.Method, requested)
	if err != nil {
		return nil, nil, err
	}
	if subpath != "" && !route.Route.ForwardSubpath {
		return nil, nil, gatewayError(CodeAPINotFound, http.StatusNotFound, "", nil)
	}
	rc := newRequestContext(c, route, subpath, requestID, apiTracePolicy(c, h.traceSettings))
	rc.QuotaGateDecision, err = h.authorize(c.Request.Context(), c, route)
	if err != nil {
		return rc, nil, err
	}
	permit, err := h.acquire(c.Request.Context(), rc)
	if err != nil {
		mergeRateLimitResult(&rc.Execution, rateLimitResult(err))
		return rc, nil, err
	}
	if h.agentPicker != nil {
		rc.Agent, err = h.agentPicker.Pick(rc.TokenID, rc.Route.ID, rc.Service.ID, rc.RequestID)
		if err != nil {
			mergeRateLimitResult(&rc.Execution, rateLimitResult(permit))
			permit.Release()
			return rc, nil, err
		}
	}
	if rc.Agent.ExecutionAgentID != "" && rc.Agent.ExecutionAgentID != h.sourceAgentID &&
		(h.executionCapabilities == nil || !h.executionCapabilities.SupportsGenericAPIExecution(rc.Agent.ExecutionAgentID)) {
		if permit != nil {
			permit.Release()
		}
		return rc, nil, ErrExecutionAgentIncompatible
	}
	return rc, permit, nil
}

func newRequestContext(c *gin.Context, route ServiceRoute, subpath, requestID string, tracePolicy apiattempt.APITracePolicy) *RequestContext {
	return &RequestContext{
		Context: c, Service: route.Service, Route: route.Route, Protocol: route.Protocol,
		Subpath: subpath, TokenID: userTokenID(c), TokenName: userTokenName(c), UserID: userID(c), GroupID: userGroupID(c), RequestID: requestID,
		TracePolicy: tracePolicy,
	}
}

func (h *Handler) acquire(ctx context.Context, rc *RequestContext) (APIPermit, error) {
	if h.limiter == nil {
		return nil, nil
	}
	return h.limiter.Acquire(ctx, APIRequestFacts{
		UserID: rc.UserID, GroupID: rc.GroupID, TokenID: rc.TokenID,
		APIServiceID: rc.Service.ID, APIRouteID: rc.Route.ID, RequestID: rc.RequestID,
		NoWait: rc.Protocol == ProtocolWebSocket,
	})
}

func executionResult(rc *RequestContext) apiattempt.APIExecutionResult {
	if rc == nil {
		return apiattempt.APIExecutionResult{}
	}
	return rc.Execution
}

func (h *Handler) authorize(ctx context.Context, c *gin.Context, route ServiceRoute) (string, error) {
	identity, ok := c.Get(consts.CtxKeyUserInfo)
	user, ok := identity.(*app.UserInfo)
	if !ok || user == nil || h == nil || h.permission == nil || h.quota == nil {
		return "", ErrPermissionFactsUnavailable
	}
	if err := h.permission.AllowInvoke(ctx, user.TokenID, user.UserID, route.Service.ID, route.Route.ID); err != nil {
		return "", err
	}
	if err := h.quota.Allow(ctx, user.UserID, route.Service); err != nil {
		return "rejected", err
	}
	return "allow", nil
}

func quotaGateDecision(rc *RequestContext) string {
	if rc == nil {
		return ""
	}
	return rc.QuotaGateDecision
}

func (h *Handler) dispatch(ctx context.Context, rc *RequestContext) error {
	if h == nil || h.executor == nil || rc == nil {
		return ErrExecutionUnavailable
	}
	return h.executor.Execute(ctx, rc)
}

func requestedProtocol(request *http.Request) (string, error) {
	if request == nil {
		return "", gatewayError(CodeInvalidRequest, http.StatusBadRequest, "", nil)
	}
	connectionValues := request.Header.Values(consts.HeaderConnection)
	upgradeValues := request.Header.Values("Upgrade")
	connectionUpgrade := headerValuesHaveToken(connectionValues, "upgrade")
	upgradeClaimed := len(upgradeValues) > 0
	upgradeWebSocket := headerValuesHaveToken(upgradeValues, ProtocolWebSocket)
	if !connectionUpgrade && !upgradeClaimed {
		return ProtocolHTTP, nil
	}
	if !connectionUpgrade || !upgradeWebSocket || !validWebSocketHandshake(request.Header) {
		return "", gatewayError(CodeInvalidUpgrade, http.StatusBadRequest, "", nil)
	}
	return ProtocolWebSocket, nil
}

func headerHasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func headerValuesHaveToken(values []string, token string) bool {
	for _, value := range values {
		if headerHasToken(value, token) {
			return true
		}
	}
	return false
}

func validWebSocketHandshake(headers http.Header) bool {
	versions := headers.Values("Sec-WebSocket-Version")
	if len(versions) != 1 || strings.TrimSpace(versions[0]) != "13" {
		return false
	}
	keys := headers.Values("Sec-WebSocket-Key")
	if len(keys) != 1 {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	return err == nil && len(key) == 16
}

func writeGatewayError(c *gin.Context, requestID string, status int, code, allow string) {
	if allow != "" {
		c.Header("Allow", allow)
	}
	c.Header(consts.HeaderXRequestID, requestID)
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "request_id": requestID}})
}

func setRequestID(c *gin.Context) string {
	if c != nil {
		if existing, ok := c.Get(requestIDContextKey); ok {
			if requestID, ok := existing.(string); ok && requestID != "" {
				return requestID
			}
		}
	}
	// X-Vaala-Request-ID is client controlled. Settlement and usage deduplication
	// therefore always use a trusted, per-ingress identity instead.
	requestID := agentproxy.CanonicalRequestID("")
	if c != nil {
		c.Set(requestIDContextKey, requestID)
		c.Header(consts.HeaderXRequestID, requestID)
	}
	c.Request.Header.Set(consts.HeaderXRequestID, requestID)
	return requestID
}

func userTokenID(c *gin.Context) uint {
	identity, _ := c.Get(consts.CtxKeyUserInfo)
	if user, ok := identity.(*app.UserInfo); ok && user != nil {
		return user.TokenID
	}
	return 0
}

func userTokenName(c *gin.Context) string {
	identity, _ := c.Get(consts.CtxKeyUserInfo)
	if user, ok := identity.(*app.UserInfo); ok && user != nil {
		return user.TokenName
	}
	return ""
}

func userID(c *gin.Context) uint {
	identity, _ := c.Get(consts.CtxKeyUserInfo)
	if user, ok := identity.(*app.UserInfo); ok && user != nil {
		return user.UserID
	}
	return 0
}

func userGroupID(c *gin.Context) uint {
	identity, _ := c.Get(consts.CtxKeyUserInfo)
	if user, ok := identity.(*app.UserInfo); ok && user != nil {
		return user.GroupID
	}
	return 0
}
