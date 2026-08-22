package genericapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type publicationPermit struct{ releases atomic.Int32 }

func (p *publicationPermit) Release() { p.releases.Add(1) }

func publicationContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/api/weather/current", nil)
	return context, recorder
}

func TestPublicationReleasesPermitBuildsUsageAndObservesDispatch(t *testing.T) {
	usage := &capturingUsageBuilder{}
	reporter := &recordingAPIUsageReporter{}
	permit := &publicationPermit{}
	context, recorder := publicationContext()
	request := &RequestContext{
		Context: context, RequestID: "request-a", Protocol: ProtocolHTTP,
		Agent: AgentPick{AgentRoutePath: app.RoutePathLocal},
		Execution: apiattempt.APIExecutionResult{
			ProviderDispatchKnown: true, ProviderDispatched: true,
		},
	}
	publication := NewPublisher(PublisherOptions{
		Usage: usage, Reporter: reporter, SourceAgentID: "source-a",
	}).Begin(request.RequestID, request.Protocol)

	publication.Publish(context, request, permit, nil)

	require.Equal(t, int32(1), permit.releases.Load())
	require.Equal(t, 1, usage.calls)
	require.Len(t, reporter.entries, 1)
	require.Equal(t, "source-a", usage.execution.SourceAgentID)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestPublicationWritesGatewayErrorUnlessWebSocketUpgradeCommitted(t *testing.T) {
	terminalErr := errors.New("transport failed")
	context, recorder := publicationContext()
	NewPublisher(PublisherOptions{}).Begin("request-a", ProtocolHTTP).Publish(context, nil, nil, terminalErr)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, CodeUnavailable, responseBodyCode(t, recorder))

	context, recorder = publicationContext()
	request := &RequestContext{ClientUpgradeCommitted: true}
	NewPublisher(PublisherOptions{}).Begin("request-b", ProtocolWebSocket).Publish(context, request, nil, terminalErr)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Body.String())
}

func TestNilPublicationOwnerStillReleasesPermit(t *testing.T) {
	permit := &publicationPermit{}
	(*Publisher)(nil).Begin("request", ProtocolHTTP).Publish(nil, nil, permit, ErrExecutionUnavailable)
	require.Equal(t, int32(1), permit.releases.Load())
}

type errorAPIUsageReporter struct{ err error }

func (r errorAPIUsageReporter) EnqueueAPI(protocol.APIUsageEntry) error { return r.err }

func TestPublicationLogsSafeFailureAndReporterFailure(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	context, _ := publicationContext()
	request := &RequestContext{
		Context: context, RequestID: "request-a", Protocol: ProtocolHTTP,
		Service: protocol.SyncedAPIService{ID: 7},
		Route:   protocol.SyncedAPIRoute{ID: 9},
		Agent:   AgentPick{ExecutionAgentID: "execution-a"},
		Execution: apiattempt.APIExecutionResult{
			APIUpstreamID: 11, ProviderDispatchKnown: true,
			ErrorStage: "transport", ErrorCode: CodeUnavailable, ErrorMessage: "connection refused",
		},
	}
	reporterErr := &url.Error{
		Op: "POST", URL: "https://master.example/usage?token=reporter-secret", Err: errors.New("queue unavailable"),
	}
	publication := NewPublisher(PublisherOptions{
		Usage: NewUsageBuilder(nil), Reporter: errorAPIUsageReporter{err: reporterErr},
		SourceAgentID: "source-a", Logger: logger,
	}).Begin(request.RequestID, request.Protocol)

	publication.Publish(context, request, nil, errors.New("raw secret must not be logged"))

	warnings := logs.FilterMessage("generic api request failed").All()
	require.Len(t, warnings, 1)
	warnFields := warnings[0].ContextMap()
	require.Equal(t, "request-a", warnFields["request_id"])
	require.Equal(t, uint64(7), warnFields["api_service_id"])
	require.Equal(t, uint64(9), warnFields["api_route_id"])
	require.Equal(t, uint64(11), warnFields["api_upstream_id"])
	require.Equal(t, "source-a", warnFields["source_agent_id"])
	require.Equal(t, "execution-a", warnFields["execution_agent_id"])
	require.Equal(t, "transport", warnFields["error_stage"])
	require.Equal(t, CodeUnavailable, warnFields["error_code"])
	require.Equal(t, "connection refused", warnFields["error_message"])
	require.NotContains(t, warnFields["error_message"], "raw secret")

	reporterFailures := logs.FilterMessage("enqueue generic api usage failed").All()
	require.Len(t, reporterFailures, 1)
	reporterFields := reporterFailures[0].ContextMap()
	require.Equal(t, "request-a", reporterFields["request_id"])
	require.Contains(t, reporterFields["error_message"], "queue unavailable")
	require.NotContains(t, reporterFields["error_message"], "reporter-secret")
	require.NotContains(t, reporterFields["error_message"], "master.example")
}

func TestPublicationLogsEarlyFailureWithoutRequest(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	context, _ := publicationContext()
	publication := NewPublisher(PublisherOptions{
		Usage: NewUsageBuilder(nil), SourceAgentID: "source-a", Logger: zap.New(core),
	}).Begin("early-request", ProtocolHTTP)
	executionErr := &url.Error{
		Op: "Get", URL: "https://private.example/path?token=secret", Err: errors.New("connection refused"),
	}

	publication.Publish(context, nil, nil, executionErr)

	warnings := logs.FilterMessage("generic api request failed").All()
	require.Len(t, warnings, 1)
	fields := warnings[0].ContextMap()
	require.Equal(t, "early-request", fields["request_id"])
	require.Equal(t, CodeUnavailable, fields["error_code"])
	require.Contains(t, fields["error_message"], "connection refused")
	require.NotContains(t, fields["error_message"], "private.example")
	require.NotContains(t, fields["error_message"], "secret")
}

func TestPublicationLogsExecutionResultFailureWithoutReturnedError(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	context, _ := publicationContext()
	request := &RequestContext{
		Context: context, RequestID: "request-result-failure", Protocol: ProtocolHTTP,
		Execution: apiattempt.APIExecutionResult{
			ProviderDispatchKnown: true, ErrorStage: "transport", ErrorCode: CodeUnavailable,
			ErrorMessage: "connection reset by peer",
		},
	}
	publication := NewPublisher(PublisherOptions{
		Usage: NewUsageBuilder(nil), Reporter: &recordingAPIUsageReporter{}, Logger: zap.New(core),
	}).Begin(request.RequestID, request.Protocol)

	publication.Publish(context, request, nil, nil)

	warnings := logs.FilterMessage("generic api request failed").All()
	require.Len(t, warnings, 1)
	require.Equal(t, "connection reset by peer", warnings[0].ContextMap()["error_message"])
}

func TestPublicationSuccessDoesNotLogRequestFailure(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	context, _ := publicationContext()
	request := &RequestContext{Context: context, RequestID: "request-success", Protocol: ProtocolHTTP}
	publication := NewPublisher(PublisherOptions{
		Usage: NewUsageBuilder(nil), Reporter: &recordingAPIUsageReporter{}, Logger: zap.New(core),
	}).Begin(request.RequestID, request.Protocol)

	publication.Publish(context, request, nil, nil)

	require.Empty(t, logs.All())
}
