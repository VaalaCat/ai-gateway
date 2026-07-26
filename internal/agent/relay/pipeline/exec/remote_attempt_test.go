package exec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type remoteTargetsStub struct {
	snapshots map[string]RemoteTargetSnapshot
	calls     []string
}

type recordingDirectPathDisabled struct {
	events []agentproxy.DirectPathDisabledEvent
}

func TestTraceRecordFromWirePreservesHeaderOnlyVerboseSemantics(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wire     *attemptwire.AttemptTraceWire
		wantBody bool
	}{
		{name: "nil trace"},
		{
			name: "headers only",
			wire: &attemptwire.AttemptTraceWire{
				InboundPath: "/v1/chat/completions", InboundHeaders: `{"X-Inbound":["kept"]}`,
				OutboundPath: "/v1/chat/completions", OutboundHeaders: `{"X-Outbound":["kept"]}`,
				ResponseHeaders: `{"X-Upstream":["kept"]}`, UpstreamStatus: http.StatusOK,
				ErrorStage:  string(trace.StageNone),
				InboundBody: "must-strip-main", OutboundBody: "must-strip-main",
				ResponseBody: "must-strip-main", ClientResponseBody: "must-strip-main",
				FailureFallback: &attemptwire.AttemptTraceBodyWire{
					InboundBody: "must-not-persist", OutboundBody: "must-not-persist",
					ResponseBody: "must-not-persist", ClientResponseBody: "must-not-persist",
				},
			},
		},
		{
			name: "failed full",
			wire: &attemptwire.AttemptTraceWire{
				InboundPath: "/v1/chat/completions", InboundHeaders: `{"X-Inbound":["kept"]}`,
				InboundBody: `{"model":"public"}`, OutboundBody: `{"model":"upstream"}`,
				ResponseBody: `{"error":"failed"}`, ClientResponseBody: `{"error":"client"}`,
				ErrorStage: string(trace.StageUpstreamStatus),
			},
			wantBody: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := traceRecordFromWire(tc.wire)
			if tc.wire == nil {
				require.Nil(t, record)
				return
			}
			require.True(t, record.Verbose, "every non-nil wire trace is persistable")
			require.Equal(t, tc.wire.InboundPath, record.InboundPath)
			require.Equal(t, "kept", record.InboundHeaders.Get("X-Inbound"))
			gotBody := record.InboundBody != "" || record.OutboundBody != "" || record.UpstreamBody != "" || record.ClientResponseBody != ""
			require.Equal(t, tc.wantBody, gotBody)
		})
	}
}

func (r *recordingDirectPathDisabled) RecordDirectPathDisabled(event agentproxy.DirectPathDisabledEvent) {
	r.events = append(r.events, event)
}

func (s *remoteTargetsStub) SnapshotRemoteTarget(agentID string) (RemoteTargetSnapshot, bool) {
	s.calls = append(s.calls, agentID)
	snapshot, ok := s.snapshots[agentID]
	return snapshot, ok
}

type remoteDirectStub struct {
	calls   int
	request agentproxy.DirectRequest
	forward func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome
}

func (s *remoteDirectStub) Forward(ctx context.Context, request agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
	s.calls++
	s.request = request
	capture := &remoteResponseCapture{ResponseWriter: dst}
	outcome := s.forward(ctx, request, capture)
	if outcome.AttemptResult == nil {
		outcome.AttemptResult = capture.result()
	}
	return outcome
}

type remoteRelayLinkStub struct {
	calls   int
	request app.AttemptStreamRequest
	stream  *remoteRelayStreamStub
	err     error
}

func (s *remoteRelayLinkStub) OpenAttemptStream(_ context.Context, request app.AttemptStreamRequest) (app.AttemptStream, error) {
	s.calls++
	s.request = request
	if s.err != nil {
		return nil, s.err
	}
	return s.stream, nil
}

type remoteRelayStreamStub struct {
	commit             tunnel.CommitState
	commitErr          error
	uploadErr          error
	copyErr            error
	copyResult         *attemptwire.AttemptProxyResult
	respond            func(http.ResponseWriter)
	dispatchProvider   bool
	providerDispatches int
	order              []string
	uploaded           string
	canceled           error
}

type remoteResponseCapture struct {
	http.ResponseWriter
	attemptResult *attemptwire.AttemptProxyResult
}

func (w *remoteResponseCapture) WriteHeader(status int) {
	if strings.TrimSpace(w.Header().Get(attemptwire.HeaderMode)) == "" && status >= http.StatusBadRequest {
		w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
		w.ResponseWriter.WriteHeader(http.StatusOK)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *remoteResponseCapture) Write(body []byte) (int, error) {
	if strings.TrimSpace(w.Header().Get(attemptwire.HeaderMode)) != attemptwire.ModeResponse {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}

func (w *remoteResponseCapture) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *remoteResponseCapture) result() *attemptwire.AttemptProxyResult {
	return w.attemptResult
}

func (s *remoteRelayStreamStub) Commit(context.Context) error {
	s.order = append(s.order, "commit")
	return s.commitErr
}

func (s *remoteRelayStreamStub) Upload(_ context.Context, source io.Reader) error {
	s.order = append(s.order, "upload")
	body, _ := io.ReadAll(source)
	s.uploaded = string(body)
	return s.uploadErr
}

func (s *remoteRelayStreamStub) CopyAttemptResponse(_ context.Context, dst http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	s.order = append(s.order, "copy")
	if s.dispatchProvider {
		s.providerDispatches++
	}
	if s.respond != nil {
		capture := &remoteResponseCapture{ResponseWriter: dst}
		s.respond(capture)
		if s.copyResult == nil {
			s.copyResult = capture.result()
		}
	}
	if s.copyErr != nil {
		if s.copyResult != nil {
			return *s.copyResult, s.copyErr
		}
		return attemptwire.AttemptProxyResult{}, s.copyErr
	}
	if s.copyResult != nil {
		return *s.copyResult, nil
	}
	return attemptwire.AttemptProxyResult{}, attemptwire.ErrInvalidContract
}

func TestRemoteAttemptResultFollowedByMissingEndIsUncertainAndPreservesDiagnostics(t *testing.T) {
	interrupted := errors.New("result received but End was lost")
	wireResult := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 2, PromptTokens: 17, CompletionTokens: 3, CacheReadTokens: 5,
		ResponseStarted: true, PlanAdvanceAllowed: true,
		Trace: &attemptwire.AttemptTraceWire{
			InboundPath: "/target/inbound", InboundHeaders: `{"X-Target-In":["kept"]}`,
			OutboundPath: "/target/outbound", OutboundHeaders: `{"X-Target-Out":["kept"]}`,
			ResponseHeaders: `{"X-Target-Response":["kept"]}`, UpstreamStatus: http.StatusOK,
			ErrorStage: string(trace.StageNone),
			FailureFallback: &attemptwire.AttemptTraceBodyWire{
				InboundBody: "target-inbound", OutboundBody: "target-outbound-transformed",
				ResponseBody: "target-provider-raw", ClientResponseBody: "target-client-encoded",
			},
		},
	}
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 38)
	rctx.State.Recorder = trace.NewRecorder(trace.CaptureHeaders, 1024)
	inbound := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rctx.State.Recorder.WithInbound(inbound, []byte("source-inbound-body"))
	stream := &remoteRelayStreamStub{
		commit: tunnel.Committed, copyErr: interrupted, copyResult: &wireResult, dispatchProvider: true,
		respond: func(dst http.ResponseWriter) {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			dst.WriteHeader(http.StatusOK)
			_, err := dst.Write([]byte("target-client-encoded"))
			require.NoError(t, err)
		},
	}
	relay := &remoteRelayLinkStub{stream: stream}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Relay: relay, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 38, validRemoteBoundAttempt())

	require.Equal(t, AttemptCommitUncertain, outcome.Kind)
	require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
	require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
		CurrentPath: app.RoutePathRelay, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
	}))
	require.True(t, outcome.ResponseStarted)
	require.True(t, outcome.ProviderResultKnown)
	require.True(t, outcome.ProviderDispatched)
	require.False(t, outcome.PlanAdvanceAllowed)
	require.Equal(t, 2, outcome.Dispatches)
	require.Equal(t, 17, outcome.Result.PromptTokens)
	require.Equal(t, 3, outcome.Result.CompletionTokens)
	require.Equal(t, 5, outcome.Result.CacheReadTokens)
	require.ErrorIs(t, outcome.Result.Err, interrupted)
	require.Equal(t, agentproxy.CodeRelayResponseInterrupted, outcome.ReasonCode)
	require.NotNil(t, outcome.Trace)
	require.Equal(t, "/target/inbound", outcome.Trace.InboundPath)
	require.Equal(t, "target-inbound", outcome.Trace.InboundBody)

	recordAttemptTrace(rctx.State.Recorder, outcome)
	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	upgraded := attempts[0]
	require.Equal(t, trace.StageInternal, upgraded.FailStage)
	require.Equal(t, "kept", upgraded.InboundHeaders.Get("X-Target-In"))
	require.Equal(t, "kept", upgraded.OutboundHeaders.Get("X-Target-Out"))
	require.Equal(t, "kept", upgraded.ResponseHeaders.Get("X-Target-Response"))
	require.Equal(t, "target-inbound", upgraded.InboundBody)
	require.Equal(t, "target-outbound-transformed", upgraded.OutboundBody)
	require.Equal(t, "target-provider-raw", upgraded.UpstreamBody)
	require.Equal(t, "target-client-encoded", upgraded.ClientResponseBody)
}

func TestDirectRemoteAttemptLateInterruptionUsesTargetFailureFallback(t *testing.T) {
	interrupted := errors.New("direct result received but End was lost")
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 1, ResponseStarted: true, Written: true,
		Trace: &attemptwire.AttemptTraceWire{
			InboundPath: "/target/inbound", InboundHeaders: `{"X-Target-In":["kept"]}`,
			OutboundPath: "/target/outbound", OutboundHeaders: `{"X-Target-Out":["kept"]}`,
			ResponseHeaders: `{"X-Target-Response":["kept"]}`, UpstreamStatus: http.StatusOK,
			ErrorStage: string(trace.StageNone),
			FailureFallback: &attemptwire.AttemptTraceBodyWire{
				InboundBody: "target-inbound", OutboundBody: "target-outbound-transformed",
				ResponseBody: "target-provider-raw", ClientResponseBody: "target-client-encoded",
			},
		},
	}
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 382)
	rctx.State.Recorder = trace.NewRecorder(trace.CaptureHeaders, 1024)
	direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
		dst.WriteHeader(http.StatusOK)
		_, err := dst.Write([]byte("target-client-encoded"))
		require.NoError(t, err)
		return agentproxy.AttemptTransportOutcome{
			Commit: tunnel.Committed, ResponseStarted: true, Code: agentproxy.CodeDirectResponseInterrupted,
			AttemptResult: &result, Err: interrupted,
		}
	}}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return true }, RelayOutboundEnabled: func() bool { return false },
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 382, validRemoteBoundAttempt())
	recordAttemptTrace(rctx.State.Recorder, outcome)

	require.Equal(t, app.RoutePathDirect, outcome.Path)
	require.ErrorIs(t, outcome.Result.Err, interrupted)
	require.Len(t, rctx.State.Recorder.Attempts(), 1)
	got := rctx.State.Recorder.Attempts()[0]
	require.Equal(t, trace.StageInternal, got.FailStage)
	require.Equal(t, "target-inbound", got.InboundBody)
	require.Equal(t, "target-outbound-transformed", got.OutboundBody)
	require.Equal(t, "target-provider-raw", got.UpstreamBody)
	require.Equal(t, "target-client-encoded", got.ClientResponseBody)
}

func TestRemoteAttemptInterruptedProviderFailureKeepsTargetClientBody(t *testing.T) {
	interrupted := errors.New("provider result received but response ended early")
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 1, ResponseStarted: true, ErrorMessage: "provider failed",
		Trace: &attemptwire.AttemptTraceWire{
			InboundPath: "/target/inbound", ClientResponseBody: "target-provider-error",
			ErrorStage: string(trace.StageUpstreamStatus),
		},
	}
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 381)
	stream := &remoteRelayStreamStub{
		commit: tunnel.Committed, copyErr: interrupted, copyResult: &result, dispatchProvider: true,
		respond: func(dst http.ResponseWriter) {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			dst.WriteHeader(http.StatusBadGateway)
			_, err := dst.Write([]byte("x"))
			require.NoError(t, err)
		},
	}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Relay: &remoteRelayLinkStub{stream: stream}, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 381, validRemoteBoundAttempt())

	require.ErrorIs(t, outcome.Result.Err, interrupted)
	require.NotNil(t, outcome.Trace)
	require.Equal(t, "target-provider-error", outcome.Trace.ClientResponseBody)
}

func TestRemoteAttemptHeadersSuccessDoesNotPersistRetainedBodies(t *testing.T) {
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 1, ResponseStarted: true, Written: true,
		Trace: &attemptwire.AttemptTraceWire{
			InboundPath: "/target/inbound", InboundHeaders: `{"X-Target-In":["kept"]}`,
			OutboundPath: "/target/outbound", OutboundHeaders: `{"X-Target-Out":["kept"]}`,
			ResponseHeaders: `{"X-Target-Response":["kept"]}`, UpstreamStatus: http.StatusOK,
			ErrorStage: string(trace.StageNone),
			FailureFallback: &attemptwire.AttemptTraceBodyWire{
				InboundBody: "target-inbound", OutboundBody: "target-outbound",
				ResponseBody: "target-upstream", ClientResponseBody: "target-client",
			},
		},
	}
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 39)
	rctx.State.Recorder = trace.NewRecorder(trace.CaptureHeaders, 1024)
	rctx.State.Recorder.WithInbound(httptest.NewRequest(http.MethodPost, "/v1/responses", nil), []byte("source-inbound-body"))
	stream := &remoteRelayStreamStub{
		commit: tunnel.Committed, copyResult: &result, dispatchProvider: true,
		respond: func(dst http.ResponseWriter) {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			dst.WriteHeader(http.StatusOK)
			_, err := dst.Write([]byte("remote-response-body"))
			require.NoError(t, err)
		},
	}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Relay: &remoteRelayLinkStub{stream: stream}, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 39, validRemoteBoundAttempt())
	recordAttemptTrace(rctx.State.Recorder, outcome)

	require.NoError(t, outcome.Result.Err)
	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 1)
	require.True(t, attempts[0].Verbose)
	require.Empty(t, attempts[0].InboundBody)
	require.Empty(t, attempts[0].OutboundBody)
	require.Empty(t, attempts[0].UpstreamBody)
	require.Empty(t, attempts[0].ClientResponseBody)
}

func TestRemoteAttemptResultFollowedByCancellationIsUncertainAndPreservesDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name       string
		cause      error
		wantReason string
	}{
		{name: "canceled", cause: context.Canceled, wantReason: agentproxy.CodeRequestCancelled},
		{name: "deadline", cause: context.DeadlineExceeded, wantReason: agentproxy.CodeRequestDeadline},
	} {
		t.Run(test.name, func(t *testing.T) {
			wireResult := attemptwire.AttemptProxyResult{
				Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ProviderDispatched: true,
				Dispatches: 2, PromptTokens: 23, CompletionTokens: 5, ResponseStarted: true,
				ErrorMessage: "provider failed before transport ended",
				Trace:        &attemptwire.AttemptTraceWire{InboundPath: "/v1/responses"},
			}
			rctx, _ := newRemoteAttemptContext(t, context.Background(), 40)
			stream := &remoteRelayStreamStub{
				commit: tunnel.Committed, copyErr: test.cause, copyResult: &wireResult, dispatchProvider: true,
				respond: func(dst http.ResponseWriter) {
					dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
					dst.WriteHeader(http.StatusOK)
					_, err := dst.Write([]byte("partial"))
					require.NoError(t, err)
				},
			}
			executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
				SourceAgentID: "source-a", Relay: &remoteRelayLinkStub{stream: stream}, Targets: enabledRemoteTargets("target-a"),
				DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
			})

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 40, validRemoteBoundAttempt())

			require.Equal(t, AttemptCommitUncertain, outcome.Kind)
			require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
			require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
				CurrentPath: app.RoutePathRelay, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
			}))
			require.True(t, outcome.ProviderResultKnown)
			require.True(t, outcome.ProviderDispatched)
			require.Equal(t, 2, outcome.Dispatches)
			require.Equal(t, 23, outcome.Result.PromptTokens)
			require.Equal(t, 5, outcome.Result.CompletionTokens)
			require.ErrorIs(t, outcome.Result.Err, test.cause)
			require.Equal(t, test.wantReason, outcome.ReasonCode)
			require.NotNil(t, outcome.Trace)
			require.Equal(t, "/v1/responses", outcome.Trace.InboundPath)
		})
	}
}

func (s *remoteRelayStreamStub) CommitState() tunnel.CommitState { return s.commit }
func (s *remoteRelayStreamStub) Cancel(err error)                { s.canceled = err }
func (s *remoteRelayStreamStub) Close() error {
	s.order = append(s.order, "close")
	return nil
}

func TestRemoteAttemptDirectSuccess(t *testing.T) {
	const currentRouteID = uint(42)
	rctx, client := newRemoteAttemptContext(t, context.Background(), 0)
	targets := enabledRemoteTargets("target-a")
	providerDispatches := 0
	direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		providerDispatches++
		writeRemoteResponse(t, dst, http.StatusCreated, []byte("direct-response"), attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
		})
		return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed, ResponseStarted: true}
	}}
	relay := &remoteRelayLinkStub{}
	executor := newRemoteExecutorForTest(targets, direct, relay)

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, currentRouteID, validRemoteBoundAttempt())

	require.Equal(t, AttemptSucceeded, outcome.Kind)
	require.Equal(t, app.RoutePathDirect, outcome.Path)
	require.Equal(t, "target-a", outcome.ExecutionAgentID)
	require.Equal(t, tunnel.Committed, outcome.Commit)
	require.Equal(t, "direct-response", client.Body.String())
	require.Equal(t, 1, direct.calls)
	require.Zero(t, relay.calls)
	require.Equal(t, 1, providerDispatches)
	require.Equal(t, currentRouteID, direct.request.RouteID)
	require.Equal(t, attemptwire.AttemptProxyMeta{Attempt: validRemoteBoundAttempt(), RequestPath: "/v1/responses"}, direct.request.Attempt)
	require.Equal(t, []string{"target-a"}, targets.calls)
	require.Equal(t, []models.AgentPathKind{models.AgentPathDirect}, agentPathKinds(outcome.AgentPaths))
}

func TestRemoteAttemptDirectedTransportPolicyMatrix(t *testing.T) {
	tests := []struct {
		name                            string
		sourceDirect, sourceRelay       bool
		targetDirect, targetRelay       bool
		wantPath                        app.RoutePath
		wantDirectCalls, wantRelayCalls int
		wantDisabledReasons             []string
		wantDirectDisabled              []agentproxy.DirectPathDisabledReason
	}{
		{name: "all enabled uses direct only", sourceDirect: true, sourceRelay: true, targetDirect: true, targetRelay: true, wantPath: app.RoutePathDirect, wantDirectCalls: 1},
		{name: "source direct disabled falls back to relay", sourceRelay: true, targetDirect: true, targetRelay: true, wantPath: app.RoutePathRelay, wantRelayCalls: 1, wantDisabledReasons: []string{consts.RouteErrorSourceDirectOutboundDisabled}, wantDirectDisabled: []agentproxy.DirectPathDisabledReason{agentproxy.DirectPathDisabledSourceOutbound}},
		{name: "target direct disabled falls back to relay", sourceDirect: true, sourceRelay: true, targetRelay: true, wantPath: app.RoutePathRelay, wantRelayCalls: 1, wantDisabledReasons: []string{consts.RouteErrorTargetDirectInboundDisabled}, wantDirectDisabled: []agentproxy.DirectPathDisabledReason{agentproxy.DirectPathDisabledTargetInbound}},
		{name: "source relay disabled does not block direct success", sourceDirect: true, targetDirect: true, targetRelay: true, wantPath: app.RoutePathDirect, wantDirectCalls: 1},
		{name: "source direct and target relay disabled perform no transport", sourceRelay: true, targetDirect: true, wantPath: app.RoutePathRelay, wantDisabledReasons: []string{consts.RouteErrorSourceDirectOutboundDisabled, consts.RouteErrorTargetRelayInboundDisabled}, wantDirectDisabled: []agentproxy.DirectPathDisabledReason{agentproxy.DirectPathDisabledSourceOutbound}},
		{name: "all directions disabled prefer source reasons and perform no transport", wantPath: app.RoutePathRelay, wantDisabledReasons: []string{consts.RouteErrorSourceDirectOutboundDisabled, consts.RouteErrorSourceRelayOutboundDisabled}, wantDirectDisabled: []agentproxy.DirectPathDisabledReason{agentproxy.DirectPathDisabledSourceOutbound}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rctx, _ := newRemoteAttemptContext(t, context.Background(), 42)
			direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				writeRemoteResponse(t, dst, http.StatusOK, nil, attemptwire.AttemptProxyResult{
					Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
				})
				return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed, ResponseStarted: true}
			}}
			relay := &remoteRelayLinkStub{stream: &remoteRelayStreamStub{
				commit: tunnel.Committed,
				respond: func(dst http.ResponseWriter) {
					writeRemoteResponse(t, dst, http.StatusOK, nil, attemptwire.AttemptProxyResult{
						Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
					})
				},
			}}
			targets := &remoteTargetsStub{snapshots: map[string]RemoteTargetSnapshot{
				"target-a": {
					Enabled: true, DirectInboundEnabled: test.targetDirect, RelayInboundEnabled: test.targetRelay,
					HTTPAddresses: `[{"url":"http://target.invalid:8139","tag":"direct"}]`, AddressTag: "direct",
				},
			}}
			disabledRecorder := &recordingDirectPathDisabled{}
			executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
				SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: targets,
				DirectOutboundEnabled: func() bool { return test.sourceDirect },
				RelayOutboundEnabled:  func() bool { return test.sourceRelay },
				DirectPathDisabled:    disabledRecorder,
			})

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 42, validRemoteBoundAttempt())

			require.Equal(t, test.wantPath, outcome.Path)
			require.Equal(t, test.wantDirectCalls, direct.calls)
			require.Equal(t, test.wantRelayCalls, relay.calls)
			disabled := make([]models.AgentPathRecord, 0, len(outcome.AgentPaths))
			for _, record := range outcome.AgentPaths {
				if record.Result == models.AgentPathResult("disabled") {
					disabled = append(disabled, record)
				}
			}
			var reasons []string
			for _, record := range disabled {
				reasons = append(reasons, record.ReasonCode)
				require.Equal(t, models.AgentPathStage("policy"), record.Stage)
				require.Equal(t, models.AgentPathNotCommitted, record.CommitState)
			}
			require.Equal(t, test.wantDisabledReasons, reasons)
			var gotDisabled []agentproxy.DirectPathDisabledReason
			for _, event := range disabledRecorder.events {
				require.Equal(t, "source-a", event.SourceAgentID)
				require.Equal(t, "target-a", event.TargetAgentID)
				gotDisabled = append(gotDisabled, event.Reason)
			}
			require.Equal(t, test.wantDirectDisabled, gotDisabled)
		})
	}
}

func TestRemoteAttemptTransportPathDecisionTableIsOrderedAndDefersSourceReaders(t *testing.T) {
	var reads []app.RoutePath
	executor := &remoteExecutor{
		DirectOutboundEnabled: func() bool {
			reads = append(reads, app.RoutePathDirect)
			return true
		},
		RelayOutboundEnabled: func() bool {
			reads = append(reads, app.RoutePathRelay)
			return true
		},
	}

	decisions := executor.transportPathDecisions(remoteTransportAttempt{})

	require.Empty(t, reads, "constructing the decision table must not freeze source policy")
	require.Len(t, decisions, 2)
	require.Equal(t, app.RoutePathDirect, decisions[0].path)
	require.Equal(t, app.RoutePathRelay, decisions[1].path)
	require.True(t, decisions[0].sourceEnabled())
	require.Equal(t, []app.RoutePath{app.RoutePathDirect}, reads)
	require.True(t, decisions[1].sourceEnabled())
	require.Equal(t, []app.RoutePath{app.RoutePathDirect, app.RoutePathRelay}, reads)
}

func TestRemoteAttemptReReadsRelayPolicyAfterDirectPreCommitFailure(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 42)
	var relayEnabled atomic.Bool
	relayEnabled.Store(true)
	var relayPolicyReads atomic.Int32
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		relayEnabled.Store(false)
		return agentproxy.AttemptTransportOutcome{
			Commit: tunnel.PreCommit, Stage: "connect", Code: agentproxy.CodeDirectConnect,
			Err: errors.New(agentproxy.CodeDirectConnect),
		}
	}}
	relay := &remoteRelayLinkStub{}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return true },
		RelayOutboundEnabled: func() bool {
			relayPolicyReads.Add(1)
			return relayEnabled.Load()
		},
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 42, validRemoteBoundAttempt())

	require.Equal(t, AttemptTransportUnavailable, outcome.Kind)
	require.Equal(t, app.RoutePathRelay, outcome.Path)
	require.Equal(t, consts.RouteErrorSourceRelayOutboundDisabled, outcome.ReasonCode)
	require.Equal(t, int32(1), relayPolicyReads.Load(), "Relay policy must be read only when Relay is reached")
	require.Equal(t, 1, direct.calls)
	require.Zero(t, relay.calls)
	require.Len(t, outcome.AgentPaths, 2)
	require.Equal(t, models.AgentPathUnavailable, outcome.AgentPaths[0].Result)
	require.Equal(t, models.AgentPathDisabled, outcome.AgentPaths[1].Result)
	require.Equal(t, models.AgentPathPolicy, outcome.AgentPaths[1].Stage)
}

func TestRemoteAttemptManagerSecondRelayGatePreservesPolicyRecord(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 42)
	var relayPolicyReads atomic.Int32
	relayEnabled := func() bool { return relayPolicyReads.Add(1) == 1 }
	manager := agenttunnel.NewManager(agenttunnel.ManagerOptions{RelayOutboundEnabled: relayEnabled})
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Relay: manager, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: relayEnabled,
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 42, validRemoteBoundAttempt())

	require.Equal(t, AttemptTransportUnavailable, outcome.Kind)
	require.Equal(t, app.RoutePathRelay, outcome.Path)
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.Equal(t, consts.RouteErrorSourceRelayOutboundDisabled, outcome.ReasonCode)
	require.Equal(t, int32(2), relayPolicyReads.Load())
	require.Len(t, outcome.AgentPaths, 2)
	relayRecord := outcome.AgentPaths[1]
	require.Equal(t, models.AgentPathDisabled, relayRecord.Result)
	require.Equal(t, models.AgentPathPolicy, relayRecord.Stage)
	require.Equal(t, models.AgentPathNotCommitted, relayRecord.CommitState)
	require.Equal(t, consts.RouteErrorSourceRelayOutboundDisabled, relayRecord.ReasonCode)
	require.Empty(t, manager.Snapshot().RecentErrors, "policy rejection must not affect Relay health")
}

func TestRemoteAttemptHardRouteIDZeroDoesNotReusePreviousRoute(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 99)
	direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		writeRemoteResponse(t, dst, http.StatusOK, []byte("ok"), attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
		})
		return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed, ResponseStarted: true}
	}}
	executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, &remoteRelayLinkStub{})

	executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 0, validRemoteBoundAttempt())

	require.Zero(t, direct.request.RouteID, "hard routes have no persisted route ID")
}

// behavior change: explicit DNS/TCP/TLS pre-commit failures may cross
// from Direct to Relay, and the business attempt is dispatched at most once.
func TestRemoteAttemptNoReplayDirectPreCommitFallsBackToRelay(t *testing.T) {
	tests := []struct {
		name  string
		stage string
		code  string
	}{
		{name: "dns", stage: "dns", code: agentproxy.CodeDirectDNS},
		{name: "tcp", stage: "connect", code: agentproxy.CodeDirectConnect},
		{name: "tls", stage: "tls", code: agentproxy.CodeDirectTLS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx, client := newRemoteAttemptContext(t, context.Background(), 41)
			targets := enabledRemoteTargets("target-a")
			direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				return agentproxy.AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: tt.stage, Code: tt.code, Err: errors.New(tt.code)}
			}}
			stream := &remoteRelayStreamStub{commit: tunnel.Committed, dispatchProvider: true, respond: func(dst http.ResponseWriter) {
				writeRemoteResponse(t, dst, http.StatusOK, []byte("relay-response"), attemptwire.AttemptProxyResult{
					Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
				})
			}}
			relay := &remoteRelayLinkStub{stream: stream}
			executor := newRemoteExecutorForTest(targets, direct, relay)

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 41, validRemoteBoundAttempt())

			require.Equal(t, AttemptSucceeded, outcome.Kind)
			require.Equal(t, app.RoutePathRelay, outcome.Path)
			require.Equal(t, "relay-response", client.Body.String())
			require.Equal(t, 1, direct.calls)
			require.Equal(t, 1, relay.calls)
			require.Equal(t, 1, stream.providerDispatches)
			require.Equal(t, []models.AgentPathKind{models.AgentPathDirect, models.AgentPathRelay}, agentPathKinds(outcome.AgentPaths))
			require.Equal(t, []string{"commit", "upload", "copy", "close"}, stream.order)
			require.Equal(t, http.MethodPost, relay.request.Method)
			require.Equal(t, attemptwire.EndpointPath, relay.request.Path)
			require.Equal(t, uint8(1), relay.request.Hop)
			require.Equal(t, uint(41), relay.request.RouteID)
			require.Equal(t, "Bearer original", relay.request.Header.Get("Authorization"))
		})
	}
}

func TestRemoteAttemptBothTransportsPreCommitUnavailable(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 7)
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		return agentproxy.AttemptTransportOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeDirectConnect, Err: errors.New("direct unavailable")}
	}}
	relay := &remoteRelayLinkStub{err: errors.New("relay unavailable")}
	executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 7, validRemoteBoundAttempt())

	require.Equal(t, AttemptTransportUnavailable, outcome.Kind)
	require.Equal(t, app.RoutePathRelay, outcome.Path)
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.Equal(t, 1, direct.calls)
	require.Equal(t, 1, relay.calls)
	require.Equal(t, []models.AgentPathKind{models.AgentPathDirect, models.AgentPathRelay}, agentPathKinds(outcome.AgentPaths))
}

func TestRemoteAttemptEndpointRejectionNeverFallsBack(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rctx, client := newRemoteAttemptContext(t, context.Background(), 11)
			direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				writeRemoteProxyRejection(t, dst, status)
				return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed, ResponseStarted: true}
			}}
			relay := &remoteRelayLinkStub{}
			executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 11, validRemoteBoundAttempt())

			require.Equal(t, AttemptProxyRejected, outcome.Kind)
			require.Zero(t, relay.calls)
			require.Empty(t, client.Body.String())
		})
	}
}

func TestRemoteAttemptProviderControlFailureNeverChangesTransport(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rctx, _ := newRemoteAttemptContext(t, context.Background(), 13)
			providerDispatches := 0
			direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				providerDispatches++
				writeRemoteControl(t, dst, attemptwire.AttemptProxyResult{
					Kind: attemptwire.ResultProviderFailed, HTTPStatus: status, ProviderDispatched: true,
					ProviderResultKnown: true, PlanAdvanceAllowed: true, ReasonCode: "provider_http_error",
				})
				return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed}
			}}
			relay := &remoteRelayLinkStub{}
			executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 13, validRemoteBoundAttempt())

			require.Equal(t, AttemptProviderFailed, outcome.Kind)
			require.True(t, outcome.PlanAdvanceAllowed)
			require.Zero(t, relay.calls)
			require.Equal(t, 1, providerDispatches)
		})
	}
}

func TestRemoteAttemptUncertainInterruptedAndCanceledNeverReplay(t *testing.T) {
	tests := []struct {
		name        string
		ctx         func() (context.Context, context.CancelFunc)
		forward     func(*testing.T, context.Context, http.ResponseWriter) agentproxy.AttemptTransportOutcome
		wantKind    AttemptOutcomeKind
		wantStarted bool
	}{
		{name: "direct commit uncertain", ctx: liveRemoteContext, wantKind: AttemptCommitUncertain, forward: func(_ *testing.T, _ context.Context, _ http.ResponseWriter) agentproxy.AttemptTransportOutcome {
			return agentproxy.AttemptTransportOutcome{Commit: tunnel.CommitUncertain, Code: agentproxy.CodeDirectRoundTrip, Err: errors.New("unknown write")}
		}},
		{name: "response interrupted", ctx: liveRemoteContext, wantKind: AttemptCommitUncertain, wantStarted: true, forward: func(t *testing.T, _ context.Context, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			dst.WriteHeader(http.StatusOK)
			_, err := dst.Write([]byte("partial"))
			require.NoError(t, err)
			return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed, ResponseStarted: true, Code: agentproxy.CodeDirectResponseInterrupted, Err: errors.New("body interrupted")}
		}},
		{name: "request canceled", ctx: canceledRemoteContext, wantKind: AttemptCanceled, forward: func(_ *testing.T, ctx context.Context, _ http.ResponseWriter) agentproxy.AttemptTransportOutcome {
			return agentproxy.AttemptTransportOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeRequestCancelled, Err: context.Cause(ctx)}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			rctx, _ := newRemoteAttemptContext(t, ctx, 17)
			direct := &remoteDirectStub{forward: func(ctx context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				return tt.forward(t, ctx, dst)
			}}
			relay := &remoteRelayLinkStub{}
			executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 17, validRemoteBoundAttempt())

			require.Equal(t, tt.wantKind, outcome.Kind)
			require.Equal(t, tt.wantStarted, outcome.ResponseStarted)
			require.LessOrEqual(t, direct.calls, 1)
			require.Zero(t, relay.calls)
			require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
				CurrentPath: app.RoutePathDirect, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
			}))
		})
	}
}

func TestRemoteAttemptRelayOnlyAndDisabledTargetBoundaries(t *testing.T) {
	t.Run("direct policy disabled skips direct", func(t *testing.T) {
		rctx, _ := newRemoteAttemptContext(t, context.Background(), 23)
		direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
			return agentproxy.AttemptTransportOutcome{}
		}}
		stream := &remoteRelayStreamStub{commit: tunnel.Committed, dispatchProvider: true, respond: func(dst http.ResponseWriter) {
			writeRemoteResponse(t, dst, http.StatusOK, nil, attemptwire.AttemptProxyResult{
				Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
			})
		}}
		relay := &remoteRelayLinkStub{stream: stream}
		executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
			SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
			DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
		})
		outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 23, validRemoteBoundAttempt())
		require.Equal(t, AttemptSucceeded, outcome.Kind)
		require.Zero(t, direct.calls)
		require.Equal(t, 1, relay.calls)
	})

	t.Run("disabled target performs no IO", func(t *testing.T) {
		rctx, _ := newRemoteAttemptContext(t, context.Background(), 29)
		targets := &remoteTargetsStub{snapshots: map[string]RemoteTargetSnapshot{"target-a": {Enabled: false}}}
		direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
			return agentproxy.AttemptTransportOutcome{}
		}}
		relay := &remoteRelayLinkStub{}
		executor := newRemoteExecutorForTest(targets, direct, relay)
		outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 29, validRemoteBoundAttempt())
		require.Equal(t, AttemptTransportUnavailable, outcome.Kind)
		require.Equal(t, app.RoutePathRelay, outcome.Path)
		require.Zero(t, direct.calls)
		require.Zero(t, relay.calls)
	})

	t.Run("missing target is distinct from disabled", func(t *testing.T) {
		rctx, _ := newRemoteAttemptContext(t, context.Background(), 31)
		targets := &remoteTargetsStub{snapshots: map[string]RemoteTargetSnapshot{}}
		executor := newRemoteExecutorForTest(targets, &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
			return agentproxy.AttemptTransportOutcome{}
		}}, &remoteRelayLinkStub{})
		outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 31, validRemoteBoundAttempt())
		require.Equal(t, agentproxy.CodeTargetNotFound, outcome.ReasonCode)
	})
}

func TestRemoteAttemptRelayUncertainInterruptedCanceledAndRejectedNeverReplay(t *testing.T) {
	tests := []struct {
		name                   string
		ctx                    func() (context.Context, context.CancelFunc)
		stream                 *remoteRelayStreamStub
		wantKind               AttemptOutcomeKind
		wantStarted            bool
		wantOpens              int
		wantProviderDispatches int
	}{
		{
			name: "commit uncertain", ctx: liveRemoteContext, wantKind: AttemptCommitUncertain, wantOpens: 1,
			stream: &remoteRelayStreamStub{commit: tunnel.CommitUncertain, commitErr: errors.New("commit ack lost")},
		},
		{
			name: "response interrupted", ctx: liveRemoteContext, wantKind: AttemptCommitUncertain, wantStarted: true, wantOpens: 1, wantProviderDispatches: 1,
			stream: &remoteRelayStreamStub{
				commit: tunnel.Committed, copyErr: errors.New("response interrupted"), dispatchProvider: true,
				respond: func(dst http.ResponseWriter) {
					dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
					dst.WriteHeader(http.StatusOK)
					_, _ = dst.Write([]byte("partial"))
				},
			},
		},
		{
			name: "canceled before open", ctx: canceledRemoteContext, wantKind: AttemptCanceled,
			stream: &remoteRelayStreamStub{commit: tunnel.PreCommit},
		},
		{
			name: "endpoint rejected", ctx: liveRemoteContext, wantKind: AttemptProxyRejected, wantOpens: 1,
			stream: &remoteRelayStreamStub{commit: tunnel.Committed, respond: func(dst http.ResponseWriter) {
				writeRemoteProxyRejection(t, dst, http.StatusUnauthorized)
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			rctx, _ := newRemoteAttemptContext(t, ctx, 37)
			direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				return agentproxy.AttemptTransportOutcome{}
			}}
			relay := &remoteRelayLinkStub{stream: tt.stream}
			executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
				SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
				DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
			})

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 37, validRemoteBoundAttempt())

			require.Equal(t, tt.wantKind, outcome.Kind)
			require.Equal(t, tt.wantStarted, outcome.ResponseStarted)
			require.Equal(t, tt.wantOpens, relay.calls)
			require.Zero(t, direct.calls)
			require.Equal(t, tt.wantProviderDispatches, tt.stream.providerDispatches)
			require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
				CurrentPath: app.RoutePathRelay, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
			}))
		})
	}
}

// behavior change: a target response may start with Flush alone. The receiver
// must preserve that fact even when the source writer has no Flusher, so no
// same-attempt target/local fallback or later business attempt is replayed.
func TestRemoteAttemptRelayFlushOnlyInterruptedWithNonFlusherNeverReplays(t *testing.T) {
	client := &plainRemoteResponseWriter{header: make(http.Header)}
	_, clientHasFlusher := any(client).(http.Flusher)
	require.False(t, clientHasFlusher)
	rctx := newRemoteAttemptContextWithWriter(t, context.Background(), 39, client)
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		return agentproxy.AttemptTransportOutcome{}
	}}
	stream := &remoteRelayStreamStub{
		commit: tunnel.Committed, copyErr: errors.New("flush-only response interrupted"), dispatchProvider: true,
		respond: func(dst http.ResponseWriter) {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			flusher, ok := dst.(http.Flusher)
			require.True(t, ok, "attempt receiver must expose flush without requiring the source writer to do so")
			flusher.Flush()
		},
	}
	relay := &remoteRelayLinkStub{stream: stream}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
		DirectOutboundEnabled: func() bool { return false }, RelayOutboundEnabled: func() bool { return true },
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 39, validRemoteBoundAttempt())

	require.Equal(t, AttemptCommitUncertain, outcome.Kind)
	require.Equal(t, app.RoutePathRelay, outcome.Path)
	require.True(t, outcome.ResponseStarted)
	require.Equal(t, http.StatusOK, client.status)
	require.Zero(t, direct.calls)
	require.Equal(t, 1, relay.calls)
	require.Equal(t, 1, stream.providerDispatches)
	require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
		CurrentPath: app.RoutePathRelay, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
	}))
}

type remoteCancellationRelayLink struct {
	stage  string
	cancel context.CancelCauseFunc
	cause  error
	stream *remoteCancellationRelayStream
	calls  int
}

func (l *remoteCancellationRelayLink) OpenAttemptStream(ctx context.Context, _ app.AttemptStreamRequest) (app.AttemptStream, error) {
	l.calls++
	if l.stage == "open" {
		l.cancel(l.cause)
		return nil, context.Cause(ctx)
	}
	return l.stream, nil
}

type remoteCancellationRelayStream struct {
	stage              string
	cancel             context.CancelCauseFunc
	cause              error
	commit             tunnel.CommitState
	providerDispatches int
}

func (s *remoteCancellationRelayStream) Commit(ctx context.Context) error {
	if s.stage == "commit" {
		s.cancel(s.cause)
		return context.Cause(ctx)
	}
	s.commit = tunnel.Committed
	return nil
}

func (s *remoteCancellationRelayStream) Upload(ctx context.Context, _ io.Reader) error {
	if s.stage == "upload" {
		s.cancel(s.cause)
		return context.Cause(ctx)
	}
	return nil
}

func (s *remoteCancellationRelayStream) CopyAttemptResponse(ctx context.Context, _ http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	if s.stage == "copy" {
		s.providerDispatches++
		s.cancel(s.cause)
		return attemptwire.AttemptProxyResult{}, context.Cause(ctx)
	}
	return attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}, nil
}

func (s *remoteCancellationRelayStream) CommitState() tunnel.CommitState { return s.commit }
func (*remoteCancellationRelayStream) Cancel(error)                      {}
func (*remoteCancellationRelayStream) Close() error                      { return nil }

// behavior change: cancellation or deadline at every transport wait is
// terminal for the whole request and cannot reach Relay/local/later attempts.
func TestRemoteAttemptCancellationAtEveryWaitNeverReplays(t *testing.T) {
	tests := []struct {
		stage                  string
		cause                  error
		wantRelay              int
		wantProviderDispatches int
		wantCode               string
		wantKind               AttemptOutcomeKind
	}{
		{stage: "direct", cause: context.Canceled, wantCode: "request_canceled"},
		{stage: "open", cause: context.DeadlineExceeded, wantRelay: 1, wantCode: "request_deadline"},
		{stage: "commit", cause: context.Canceled, wantRelay: 1, wantCode: "request_canceled"},
		{stage: "upload", cause: context.DeadlineExceeded, wantRelay: 1, wantCode: "request_deadline"},
		{stage: "copy", cause: context.Canceled, wantRelay: 1, wantProviderDispatches: 1, wantCode: "request_canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.stage, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			rctx, _ := newRemoteAttemptContext(t, ctx, 43)
			direct := &remoteDirectStub{forward: func(ctx context.Context, _ agentproxy.DirectRequest, _ http.ResponseWriter) agentproxy.AttemptTransportOutcome {
				if tt.stage == "direct" {
					cancel(tt.cause)
					return agentproxy.AttemptTransportOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeRequestCancelled, Err: context.Cause(ctx)}
				}
				return agentproxy.AttemptTransportOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeDirectConnect, Err: errors.New("direct unavailable")}
			}}
			stream := &remoteCancellationRelayStream{stage: tt.stage, cancel: cancel, cause: tt.cause}
			relay := &remoteCancellationRelayLink{stage: tt.stage, cancel: cancel, cause: tt.cause, stream: stream}
			executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 43, validRemoteBoundAttempt())

			wantKind := tt.wantKind
			if wantKind == "" {
				wantKind = AttemptCanceled
			}
			require.Equal(t, wantKind, outcome.Kind)
			require.Equal(t, tt.wantCode, outcome.ReasonCode)
			require.Equal(t, 1, direct.calls)
			require.Equal(t, tt.wantRelay, relay.calls)
			require.Equal(t, tt.wantProviderDispatches, stream.providerDispatches)
			require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
				CurrentPath: outcome.Path, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
			}))
		})
	}
}

func TestRemoteAttemptInvalidInputsPerformNoTransportIO(t *testing.T) {
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		return agentproxy.AttemptTransportOutcome{}
	}}
	relay := &remoteRelayLinkStub{}
	executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

	outcome := executor.Execute(nil, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 0, validRemoteBoundAttempt())
	require.Equal(t, AttemptExecutionRejected, outcome.Kind)

	rctx, _ := newRemoteAttemptContext(t, context.Background(), 0)
	invalid := validRemoteBoundAttempt()
	invalid.Channel.ID = 0
	outcome = executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 0, invalid)
	require.Equal(t, AttemptExecutionRejected, outcome.Kind)
	require.Zero(t, direct.calls)
	require.Zero(t, relay.calls)
}

func newRemoteExecutorForTest(targets RemoteTargetRuntime, direct agentproxy.DirectRequestForwarder, relay app.AttemptStreamOpener) RemoteAttemptExecutor {
	return NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: targets,
		DirectOutboundEnabled: func() bool { return true }, RelayOutboundEnabled: func() bool { return true },
	})
}

func enabledRemoteTargets(agentID string) *remoteTargetsStub {
	return &remoteTargetsStub{snapshots: map[string]RemoteTargetSnapshot{
		agentID: {
			Enabled: true, DirectInboundEnabled: true, RelayInboundEnabled: true,
			HTTPAddresses: `[{"url":"http://target.invalid:8139","tag":"direct"}]`,
			AddressTag:    "direct",
		},
	}}
}

func newRemoteAttemptContext(t *testing.T, ctx context.Context, routeID uint) (*state.RelayContext, *httptest.ResponseRecorder) {
	t.Helper()
	client := httptest.NewRecorder()
	return newRemoteAttemptContextWithWriter(t, ctx, routeID, client), client
}

func newRemoteAttemptContextWithWriter(t *testing.T, ctx context.Context, _ uint, client http.ResponseWriter) *state.RelayContext {
	t.Helper()
	body := &execReplayBody{data: []byte("request-body")}
	resources := &state.RequestResources{}
	require.NoError(t, resources.Replace(context.Background(), execBodyStore{body: body}, nil, app.BodyLimits{}))
	t.Cleanup(func() { require.NoError(t, resources.Close()) })
	ginContext, _ := gin.CreateTestContext(client)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses?stream=true", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer original")
	ginContext.Request = request
	return &state.RelayContext{
		Context:   ginContext,
		Input:     state.RelayInput{RequestID: "request-a"},
		State:     &state.RelayState{},
		Resources: resources,
	}
}

type plainRemoteResponseWriter struct {
	header http.Header
	status int
}

func (w *plainRemoteResponseWriter) Header() http.Header { return w.header }
func (w *plainRemoteResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *plainRemoteResponseWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return len(payload), nil
}

func validRemoteBoundAttempt() attemptwire.BoundAttempt {
	return attemptwire.BoundAttempt{
		Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
		RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
	}
}

func writeRemoteControl(t *testing.T, dst http.ResponseWriter, result attemptwire.AttemptProxyResult) {
	t.Helper()
	dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	dst.WriteHeader(http.StatusOK)
	setRemoteResult(t, dst, result)
}

func writeRemoteResponse(t *testing.T, dst http.ResponseWriter, status int, body []byte, result attemptwire.AttemptProxyResult) {
	t.Helper()
	dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	dst.WriteHeader(status)
	_, err := dst.Write(body)
	require.NoError(t, err)
	setRemoteResult(t, dst, result)
}

func writeRemoteProxyRejection(t *testing.T, dst http.ResponseWriter, status int) {
	t.Helper()
	dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	dst.WriteHeader(http.StatusOK)
	setRemoteResult(t, dst, attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProxyRejected, HTTPStatus: status,
		ProviderResultKnown: true, ReasonCode: "attempt_ingress_rejected", ErrorMessage: "attempt proxy ingress rejected",
	})
}

func setRemoteResult(t *testing.T, dst http.ResponseWriter, result attemptwire.AttemptProxyResult) {
	t.Helper()
	capture, ok := dst.(*remoteResponseCapture)
	require.True(t, ok)
	copy := result
	capture.attemptResult = &copy
}

func agentPathKinds(records []models.AgentPathRecord) []models.AgentPathKind {
	kinds := make([]models.AgentPathKind, 0, len(records))
	for _, record := range records {
		kinds = append(kinds, record.Path)
	}
	return kinds
}

func liveRemoteContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func canceledRemoteContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}
