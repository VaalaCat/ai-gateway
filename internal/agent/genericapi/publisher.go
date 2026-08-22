package genericapi

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Publisher owns the terminal lifecycle shared by every Generic API request.
// It does not execute requests or select routes.
type Publisher struct {
	usage         APIUsageBuilder
	reporter      APIUsageReporter
	sourceAgentID string
	metrics       *APIMetrics
	logger        *zap.Logger
}

type PublisherOptions struct {
	Usage         APIUsageBuilder
	Reporter      APIUsageReporter
	SourceAgentID string
	Metrics       *APIMetrics
	Logger        *zap.Logger
}

func NewPublisher(options PublisherOptions) *Publisher {
	logger := options.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Publisher{
		usage: options.Usage, reporter: options.Reporter,
		sourceAgentID: options.SourceAgentID, metrics: options.Metrics, logger: logger,
	}
}

// Publication is the request-scoped terminal state created before any gate
// runs. Publish must be called exactly once for every request that reaches the
// Generic API handler.
type Publication struct {
	publisher     *Publisher
	requestID     string
	startedAt     time.Time
	finishMetrics func(string)
}

func (p *Publisher) Begin(requestID, protocol string) *Publication {
	var metrics *APIMetrics
	if p != nil {
		metrics = p.metrics
	}
	return &Publication{
		publisher: p, requestID: requestID, startedAt: time.Now(),
		finishMetrics: metrics.beginRequest(protocol),
	}
}

func (p *Publication) StartExecution(request *RequestContext) {
	startSourceAPITrace(request)
}

// FinishMetrics closes a request rejected before the Generic API usage
// contract is available. Such requests keep their historical no-usage behavior.
func (p *Publication) FinishMetrics(executionErr error) {
	if p == nil {
		return
	}
	outcome := "success"
	if executionErr != nil {
		outcome = "error"
	}
	p.finishMetrics(outcome)
}

func (p *Publication) Publish(c *gin.Context, request *RequestContext, permit APIPermit, executionErr error) {
	if p == nil {
		if permit != nil {
			permit.Release()
		}
		return
	}
	outcome := "error"
	if executionErr == nil {
		outcome = "success"
	}
	defer p.finishMetrics(outcome)

	if request != nil {
		finishSourceAPITrace(request, executionErr)
		mergeRateLimitResult(&request.Execution, rateLimitResult(permit))
	}
	if permit != nil {
		permit.Release()
	}
	entry := p.publishUsage(c, request, executionErr)
	p.logFailure(request, entry, executionErr)
	p.observeDispatch(request, executionErr)
	if executionErr != nil && (request == nil || !request.ClientUpgradeCommitted) && c != nil && !c.Writer.Written() {
		writeHandlerError(c, p.requestID, executionErr)
	}
}

func (p *Publication) publishUsage(c *gin.Context, request *RequestContext, executionErr error) protocol.APIUsageEntry {
	if p.publisher == nil || p.publisher.usage == nil {
		return protocol.APIUsageEntry{}
	}
	statusCode := 0
	if c != nil {
		statusCode = c.Writer.Status()
	}
	if request != nil && request.ClientStatusCode != 0 {
		statusCode = request.ClientStatusCode
	}
	entry := p.publisher.usage.Build(APIExecution{
		Request: request, Result: executionResult(request), Err: executionErr,
		StatusCode: statusCode, DurationMs: int(time.Since(p.startedAt) / time.Millisecond),
		SourceAgentID: p.publisher.sourceAgentID, QuotaGateDecision: quotaGateDecision(request),
	})
	if p.publisher.reporter != nil {
		if err := p.publisher.reporter.EnqueueAPI(entry); err != nil {
			p.publisher.logger.Error("enqueue generic api usage failed",
				zap.String("request_id", p.requestID),
				zap.String("error_message", safeAPIErrorMessage(err, protocol.APIUpstreamCredential{})),
			)
		}
	}
	return entry
}

func (p *Publication) logFailure(request *RequestContext, entry protocol.APIUsageEntry, executionErr error) {
	if p.publisher == nil || (executionErr == nil && entry.ErrorStage == "" && entry.ErrorCode == "") {
		return
	}
	p.publisher.logger.Warn("generic api request failed",
		zap.String("request_id", p.requestID),
		zap.Uint("api_service_id", apiServiceID(request)),
		zap.Uint("api_route_id", apiRouteID(request)),
		zap.Uint("api_upstream_id", executionResult(request).APIUpstreamID),
		zap.String("source_agent_id", p.publisher.sourceAgentID),
		zap.String("execution_agent_id", executionAgentID(request)),
		zap.String("error_stage", entry.ErrorStage),
		zap.String("error_code", entry.ErrorCode),
		zap.String("error_message", safePublishedErrorMessage(entry, executionErr)),
	)
}

func safePublishedErrorMessage(entry protocol.APIUsageEntry, executionErr error) string {
	if entry.ErrorMessage != "" {
		return entry.ErrorMessage
	}
	return safeAPIErrorMessage(executionErr, protocol.APIUpstreamCredential{})
}

func apiServiceID(request *RequestContext) uint {
	if request == nil {
		return 0
	}
	return request.Service.ID
}

func apiRouteID(request *RequestContext) uint {
	if request == nil {
		return 0
	}
	return request.Route.ID
}

func executionAgentID(request *RequestContext) string {
	if request == nil {
		return ""
	}
	return request.Agent.ExecutionAgentID
}

func (p *Publication) observeDispatch(request *RequestContext, executionErr error) {
	if p.publisher == nil || p.publisher.metrics == nil || request == nil ||
		!request.Execution.ProviderDispatchKnown || !request.Execution.ProviderDispatched {
		return
	}
	outcome := "success"
	if executionErr != nil {
		outcome = "error"
	}
	p.publisher.metrics.observeDispatch(string(request.Agent.AgentRoutePath), outcome)
}

func writeHandlerError(c *gin.Context, requestID string, err error) {
	gateway := gatewayErrorFor(err)
	writeGatewayError(c, requestID, gateway.status, gateway.code, gateway.allow)
}
