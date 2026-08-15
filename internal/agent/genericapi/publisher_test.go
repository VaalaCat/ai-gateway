package genericapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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
