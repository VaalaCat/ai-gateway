package exec_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	agentattemptproxy "github.com/VaalaCat/ai-gateway/internal/agent/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	relayexec "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/exec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

// behavior change: oversized verbose trace must not turn a known provider
// failure into attempt_result_missing at the source.
func TestOversizedControlTracePreservesProviderFailureRoundTrip(t *testing.T) {
	provider := roundTripProvider(func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
		return attemptexec.ProviderResult{
			Outcome:    state.AttemptResult{Err: &common.UpstreamError{Status: http.StatusInternalServerError}},
			Dispatches: 3, ProviderDispatched: true,
		}
	})
	direct := &roundTripDirect{provider: provider}
	executor := relayexec.NewRemoteAttemptExecutor(relayexec.RemoteAttemptExecutorOptions{
		SourceAgentID: "source", Direct: direct, Targets: roundTripTargets{},
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) { return "ticket", nil },
	})
	rctx, client := newRoundTripSourceContext(t)

	outcome := executor.Execute(rctx, relayexec.AttemptTarget{AgentID: "target-a", Kind: relayexec.AttemptTargetRemote}, 7,
		attemptwire.BoundAttempt{Channel: attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 9}, RealModel: "gpt-4o", Mode: attemptwire.ModeNative})

	require.Equal(t, relayexec.AttemptProviderFailed, outcome.Kind)
	require.Equal(t, 3, outcome.Dispatches)
	require.True(t, outcome.ProviderDispatched)
	require.True(t, outcome.PlanAdvanceAllowed)
	require.Empty(t, client.Body.String(), "control result must not write a provider body to the client")
}

type roundTripProvider func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult

func (f roundTripProvider) Execute(rctx *state.RelayContext, attempt state.Attempt) attemptexec.ProviderResult {
	return f(rctx, attempt)
}

type roundTripDirect struct {
	provider attemptexec.ProviderAttemptExecutor
}

func (d *roundTripDirect) Forward(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
	c, _ := gin.CreateTestContext(dst)
	c.Request = httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
	recorder := trace.NewRecorder(true, 2*attemptwire.MaxResultWireBytes)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Header.Set("X-Oversized-Trace", strings.Repeat("h", attemptwire.MaxResultWireBytes))
	recorder.WithInbound(request, []byte(strings.Repeat("b", attemptwire.MaxResultWireBytes)))
	rctx := &state.RelayContext{Context: c, State: &state.RelayState{Recorder: recorder}}

	agentattemptproxy.NewResponseExecutor().Execute(rctx, state.Attempt{}, d.provider)
	return agentproxy.DirectOutcome{Commit: tunnel.Committed}
}

type roundTripTargets struct{}

func (roundTripTargets) SnapshotRemoteTarget(string) (relayexec.RemoteTargetSnapshot, bool) {
	return relayexec.RemoteTargetSnapshot{
		Enabled: true, HTTPAddresses: `[{"url":"http://target.invalid:8139","tag":"direct"}]`, AddressTag: "direct",
	}, true
}

type roundTripBodyStore struct{}

func (roundTripBodyStore) Capture(_ context.Context, src io.Reader, _ app.BodyLimits) (app.ReplayBody, error) {
	body, err := io.ReadAll(src)
	return &roundTripBody{data: body}, err
}

type roundTripBody struct {
	data []byte
}

func (b *roundTripBody) Size() int64 { return int64(len(b.data)) }
func (b *roundTripBody) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *roundTripBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, io.ErrShortBuffer
	}
	return append([]byte(nil), b.data...), nil
}
func (b *roundTripBody) Close() error { return nil }

func newRoundTripSourceContext(t *testing.T) (*state.RelayContext, *httptest.ResponseRecorder) {
	t.Helper()
	resources := &state.RequestResources{}
	require.NoError(t, resources.Replace(t.Context(), roundTripBodyStore{}, strings.NewReader("request-body"), app.BodyLimits{}))
	t.Cleanup(func() { require.NoError(t, resources.Close()) })
	client := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(client)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return &state.RelayContext{
		Context: c, Input: state.RelayInput{RequestID: "request-a"}, State: &state.RelayState{}, Resources: resources,
	}, client
}
