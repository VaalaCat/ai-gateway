package exec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func headersTraceContext(attempts ...state.Attempt) *state.RelayContext {
	rctx := portExecutorContext(attempts...)
	rctx.State.Recorder = trace.NewRecorder(trace.CaptureHeaders, 1024)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	rctx.State.Recorder.WithInbound(request, []byte(`{"model":"public","secret":"body"}`))
	return rctx
}

func requireFullFailureTrace(t *testing.T, record *trace.TraceRecord, wantStage trace.Stage) {
	t.Helper()
	require.True(t, record.Verbose)
	require.Equal(t, wantStage, record.FailStage)
	require.Equal(t, "/v1/chat/completions", record.InboundPath)
	require.NotEmpty(t, record.InboundBody)
}

func TestExecutorRequestGateFailureForcesFullTrace(t *testing.T) {
	rctx := headersTraceContext(portAttempt(1))
	executor := &Executor{RequestGate: stubGate{onRequest: func() (state.RateLease, error) {
		return nil, state.ErrRateLimited
	}}}

	executor.Run(rctx)

	require.ErrorIs(t, rctx.State.Execution.Err, state.ErrRateLimited)
	require.Empty(t, rctx.State.Recorder.Attempts(), "request rejection happens before an attempt")
	requireFullFailureTrace(t, rctx.State.Recorder.Finalize(), trace.StageInternal)
}

func TestExecutorRouteBuildFailureForcesFullAttemptTrace(t *testing.T) {
	buildErr := errors.New("route build failed")
	rctx := headersTraceContext(portAttempt(1))
	routes := &recordingRouteBuilder{routes: []AttemptRoute{{}}, errs: []error{buildErr}}

	(&Executor{SourceAgentID: "source", Routes: routes}).Run(rctx)

	require.ErrorIs(t, rctx.State.Execution.Err, buildErr)
	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	requireFullFailureTrace(t, attempts[0], trace.StageInternal)
}

func TestExecutorLocalOutcomeAppliesFailureTracePolicy(t *testing.T) {
	providerErr := errors.New("provider failed")
	for _, tc := range []struct {
		name     string
		provider attemptexec.ProviderResult
		wantFull bool
	}{
		{
			name: "provider failure is full",
			provider: attemptexec.ProviderResult{
				Outcome: state.AttemptResult{Err: providerErr}, Dispatches: 1, ProviderDispatched: true,
			},
			wantFull: true,
		},
		{
			name: "success stays headers",
			provider: attemptexec.ProviderResult{
				Outcome: state.AttemptResult{PromptTokens: 1}, Dispatches: 1, ProviderDispatched: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rctx := headersTraceContext(portAttempt(1))
			provider := &injectedProvider{result: tc.provider}
			executor := &Executor{
				SourceAgentID: "source",
				Routes:        &recordingRouteBuilder{routes: []AttemptRoute{localPortRoute()}},
				Local:         NewLocalAttemptExecutor("source", provider),
			}

			executor.Run(rctx)

			attempts := rctx.State.Recorder.Attempts()
			require.Len(t, attempts, 1)
			if tc.wantFull {
				requireFullFailureTrace(t, attempts[0], trace.StageInternal)
				return
			}
			require.True(t, attempts[0].Verbose)
			require.Equal(t, trace.StageNone, attempts[0].FailStage)
			require.Empty(t, attempts[0].InboundBody)
		})
	}
}

type traceMarkingLocalExecutor struct {
	err error
}

func (e traceMarkingLocalExecutor) Execute(rctx *state.RelayContext, _ state.Attempt) AttemptOutcome {
	rctx.State.Recorder.WithFail(trace.StageUpstreamStatus, e.err)
	return AttemptOutcome{
		Kind: AttemptProviderFailed, Result: state.AttemptResult{Err: e.err},
		ExecutionAgentID: "source", Path: app.RoutePathLocal, Commit: tunnel.Committed,
		ProviderResultKnown: true, ProviderDispatched: true,
	}
}

func TestExecutorFailureTracePreservesConcreteStage(t *testing.T) {
	providerErr := errors.New("upstream status failed")
	rctx := headersTraceContext(portAttempt(1))
	executor := &Executor{
		SourceAgentID: "source",
		Routes:        &recordingRouteBuilder{routes: []AttemptRoute{localPortRoute()}},
		Local:         traceMarkingLocalExecutor{err: providerErr},
	}

	executor.Run(rctx)

	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	requireFullFailureTrace(t, attempts[0], trace.StageUpstreamStatus)
}

func TestExecutorRemoteFailureTracePolicy(t *testing.T) {
	remoteErr := errors.New("remote failed")
	for _, tc := range []struct {
		name    string
		path    app.RoutePath
		wire    *trace.TraceRecord
		wantNil bool
	}{
		{
			name: "direct non-nil target failure is preserved and full",
			path: app.RoutePathDirect,
			wire: &trace.TraceRecord{
				InboundPath: "/target", InboundHeaders: http.Header{"X-Target": {"kept"}},
				InboundBody: "target-inbound", Verbose: true, FailStage: trace.StageUpstreamDispatch,
			},
		},
		{name: "relay missing trace falls back to local full snapshot", path: app.RoutePathRelay, wantNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := AttemptOutcome{
				Kind: AttemptProviderFailed, Result: state.AttemptResult{Err: remoteErr},
				ExecutionAgentID: "target", Path: tc.path, Commit: tunnel.Committed,
				ProviderResultKnown: true, ProviderDispatched: true, Trace: tc.wire,
			}
			rctx := headersTraceContext(portAttempt(1))
			executor := &Executor{
				SourceAgentID: "source",
				Routes:        &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(false, "target")}},
				Remote:        &recordingRemoteExecutor{outcomes: []AttemptOutcome{outcome}},
			}

			executor.Run(rctx)

			attempts := rctx.State.Recorder.Attempts()
			require.Len(t, attempts, 1, "failed remote attempt must retain its index")
			if tc.wantNil {
				requireFullFailureTrace(t, attempts[0], trace.StageInternal)
				return
			}
			require.Equal(t, "/target", attempts[0].InboundPath)
			require.Equal(t, "kept", attempts[0].InboundHeaders.Get("X-Target"))
			require.Equal(t, trace.StageUpstreamDispatch, attempts[0].FailStage)
			require.Equal(t, "target-inbound", attempts[0].InboundBody)
		})
	}
}

func TestExecutorSourceInterruptionUpgradesSuccessfulRemoteHeadersTrace(t *testing.T) {
	transportErr := errors.New("result stream interrupted")
	receiver := newAttemptResponseReceiver(httptest.NewRecorder())
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	receiver.WriteHeader(http.StatusOK)
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true,
		ResponseStarted: true, Written: true,
		Trace: &attemptwire.AttemptTraceWire{
			InboundPath: "/target/inbound", InboundHeaders: `{"X-Target":["kept"]}`,
			OutboundPath: "/target/outbound", OutboundHeaders: `{"X-Upstream":["kept"]}`,
			ResponseHeaders: `{"X-Response":["kept"]}`, UpstreamStatus: http.StatusOK,
			ErrorStage: string(trace.StageNone),
		},
	}
	outcome := receiver.FinishWithResult(
		"target", app.RoutePathDirect, tunnel.Committed, result,
		agentproxy.CodeRelayResponseInterrupted, transportErr,
	)
	require.ErrorIs(t, outcome.Result.Err, transportErr)
	require.NotNil(t, outcome.Trace)
	require.Empty(t, outcome.Trace.InboundBody, "target returned a successful headers-only trace")

	rctx := headersTraceContext(portAttempt(1))
	executor := &Executor{
		SourceAgentID: "source",
		Routes:        &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(false, "target")}},
		Remote:        &recordingRemoteExecutor{outcomes: []AttemptOutcome{outcome}},
	}

	executor.Run(rctx)

	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	merged := attempts[0]
	require.True(t, merged.Verbose)
	require.Equal(t, trace.StageInternal, merged.FailStage)
	require.Equal(t, "/target/inbound", merged.InboundPath)
	require.Equal(t, "kept", merged.InboundHeaders.Get("X-Target"))
	require.Equal(t, "/target/outbound", merged.OutboundPath)
	require.Equal(t, "kept", merged.OutboundHeaders.Get("X-Upstream"))
	require.Equal(t, "kept", merged.ResponseHeaders.Get("X-Response"))
	require.Equal(t, http.StatusOK, merged.UpstreamStatus)
	require.NotEmpty(t, merged.InboundBody, "source must recover its retained inbound body")
}

func TestExecutorRemoteSuccessWithoutTraceKeepsEmptyAttemptIndex(t *testing.T) {
	outcome := successfulPortOutcome("target")
	outcome.Trace = nil
	rctx := headersTraceContext(portAttempt(1))
	executor := &Executor{
		SourceAgentID: "source",
		Routes:        &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(false, "target")}},
		Remote:        &recordingRemoteExecutor{outcomes: []AttemptOutcome{outcome}},
	}

	executor.Run(rctx)

	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	require.False(t, attempts[0].Verbose)
	require.Empty(t, attempts[0].FailStage)
}

func TestExecutorRemoteHeadersSuccessStaysHeadersOnly(t *testing.T) {
	outcome := successfulPortOutcome("target")
	outcome.Trace = &trace.TraceRecord{
		InboundPath: "/target/inbound", InboundHeaders: http.Header{"X-Target": {"kept"}},
		OutboundPath: "/target/outbound", UpstreamStatus: http.StatusOK,
		FailStage: trace.StageNone, Verbose: true,
	}
	rctx := headersTraceContext(portAttempt(1))
	executor := &Executor{
		SourceAgentID: "source",
		Routes:        &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(false, "target")}},
		Remote:        &recordingRemoteExecutor{outcomes: []AttemptOutcome{outcome}},
	}

	executor.Run(rctx)

	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, "/target/inbound", attempts[0].InboundPath)
	require.Equal(t, "kept", attempts[0].InboundHeaders.Get("X-Target"))
	require.True(t, attempts[0].Verbose)
	require.Empty(t, attempts[0].InboundBody)
	require.Empty(t, attempts[0].OutboundBody)
	require.Empty(t, attempts[0].UpstreamBody)
	require.Empty(t, attempts[0].ClientResponseBody)
}
