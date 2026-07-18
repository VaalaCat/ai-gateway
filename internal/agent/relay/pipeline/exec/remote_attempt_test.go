package exec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type remoteTargetsStub struct {
	snapshots map[string]RemoteTargetSnapshot
	calls     []string
}

func (s *remoteTargetsStub) SnapshotRemoteTarget(agentID string) (RemoteTargetSnapshot, bool) {
	s.calls = append(s.calls, agentID)
	snapshot, ok := s.snapshots[agentID]
	return snapshot, ok
}

type remoteDirectStub struct {
	calls   int
	request agentproxy.DirectRequest
	forward func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome
}

func (s *remoteDirectStub) Forward(ctx context.Context, request agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
	s.calls++
	s.request = request
	return s.forward(ctx, request, dst)
}

type remoteRelayLinkStub struct {
	calls   int
	request app.RelayRequest
	stream  *remoteRelayStreamStub
	err     error
}

func (s *remoteRelayLinkStub) OpenStream(_ context.Context, request app.RelayRequest) (app.RelayStream, error) {
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
	respond            func(http.ResponseWriter)
	dispatchProvider   bool
	providerDispatches int
	order              []string
	uploaded           string
	canceled           error
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

func (s *remoteRelayStreamStub) CopyResponse(_ context.Context, dst http.ResponseWriter) error {
	s.order = append(s.order, "copy")
	if s.dispatchProvider {
		s.providerDispatches++
	}
	if s.respond != nil {
		s.respond(dst)
	}
	return s.copyErr
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
	direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
		providerDispatches++
		writeRemoteResponse(t, dst, http.StatusCreated, []byte("direct-response"), attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
		})
		return agentproxy.DirectOutcome{Commit: tunnel.Committed, ResponseStarted: true}
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
	require.Equal(t, attemptwire.AttemptProxyMeta{Attempt: validRemoteBoundAttempt(), RequestPath: "/v1/responses"}, *direct.request.Attempt)
	require.Equal(t, []string{"target-a"}, targets.calls)
	require.Equal(t, []models.AgentPathKind{models.AgentPathDirect}, agentPathKinds(outcome.AgentPaths))
}

func TestRemoteAttemptHardRouteIDZeroDoesNotReusePreviousRoute(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 99)
	direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
		writeRemoteResponse(t, dst, http.StatusOK, []byte("ok"), attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
		})
		return agentproxy.DirectOutcome{Commit: tunnel.Committed, ResponseStarted: true}
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
			direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
				return agentproxy.DirectOutcome{Commit: tunnel.PreCommit, Stage: tt.stage, Code: tt.code, Err: errors.New(tt.code)}
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
			require.Empty(t, relay.request.Header.Get(attemptwire.HeaderMeta))
		})
	}
}

func TestRemoteAttemptBothTransportsPreCommitUnavailable(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 7)
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
		return agentproxy.DirectOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeDirectConnect, Err: errors.New("direct unavailable")}
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
			direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
				writeRemoteProxyRejection(t, dst, status)
				return agentproxy.DirectOutcome{Commit: tunnel.Committed, ResponseStarted: true}
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
			direct := &remoteDirectStub{forward: func(_ context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
				providerDispatches++
				writeRemoteControl(t, dst, attemptwire.AttemptProxyResult{
					Kind: attemptwire.ResultProviderFailed, HTTPStatus: status, ProviderDispatched: true,
					ProviderResultKnown: true, PlanAdvanceAllowed: true, ReasonCode: "provider_http_error",
				})
				return agentproxy.DirectOutcome{Commit: tunnel.Committed}
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
		forward     func(*testing.T, context.Context, http.ResponseWriter) agentproxy.DirectOutcome
		wantKind    AttemptOutcomeKind
		wantStarted bool
	}{
		{name: "direct commit uncertain", ctx: liveRemoteContext, wantKind: AttemptCommitUncertain, forward: func(_ *testing.T, _ context.Context, _ http.ResponseWriter) agentproxy.DirectOutcome {
			return agentproxy.DirectOutcome{Commit: tunnel.CommitUncertain, Code: agentproxy.CodeDirectRoundTrip, Err: errors.New("unknown write")}
		}},
		{name: "response interrupted", ctx: liveRemoteContext, wantKind: AttemptCommitUncertain, wantStarted: true, forward: func(t *testing.T, _ context.Context, dst http.ResponseWriter) agentproxy.DirectOutcome {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			dst.Header().Add("Trailer", attemptwire.TrailerResult)
			dst.WriteHeader(http.StatusOK)
			_, err := dst.Write([]byte("partial"))
			require.NoError(t, err)
			return agentproxy.DirectOutcome{Commit: tunnel.Committed, ResponseStarted: true, Code: agentproxy.CodeDirectResponseInterrupted, Err: errors.New("body interrupted")}
		}},
		{name: "request canceled", ctx: canceledRemoteContext, wantKind: AttemptCanceled, forward: func(_ *testing.T, ctx context.Context, _ http.ResponseWriter) agentproxy.DirectOutcome {
			return agentproxy.DirectOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeRequestCancelled, Err: context.Cause(ctx)}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			rctx, _ := newRemoteAttemptContext(t, ctx, 17)
			direct := &remoteDirectStub{forward: func(ctx context.Context, _ agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
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

func TestRemoteAttemptForwardTicketCacheMissAllowsRelay(t *testing.T) {
	rctx, _ := newRemoteAttemptContext(t, context.Background(), 19)
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
		return agentproxy.DirectOutcome{}
	}}
	stream := &remoteRelayStreamStub{commit: tunnel.Committed, respond: func(dst http.ResponseWriter) {
		writeRemoteControl(t, dst, attemptwire.AttemptProxyResult{Kind: attemptwire.ResultExecutionRejected, ProviderResultKnown: true, PlanAdvanceAllowed: true})
	}}
	relay := &remoteRelayLinkStub{stream: stream}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) { return "", errors.New("cache miss") },
		RelayEnabled:        func() bool { return true },
	})

	outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 19, validRemoteBoundAttempt())

	require.Equal(t, AttemptExecutionRejected, outcome.Kind)
	require.Equal(t, app.RoutePathRelay, outcome.Path)
	require.Zero(t, direct.calls)
	require.Equal(t, 1, relay.calls)
	require.Zero(t, stream.providerDispatches)
}

func TestRemoteAttemptRelayOnlyAndDisabledTargetBoundaries(t *testing.T) {
	t.Run("relay only skips direct", func(t *testing.T) {
		rctx, _ := newRemoteAttemptContext(t, context.Background(), 23)
		direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
			return agentproxy.DirectOutcome{}
		}}
		stream := &remoteRelayStreamStub{commit: tunnel.Committed, dispatchProvider: true, respond: func(dst http.ResponseWriter) {
			writeRemoteResponse(t, dst, http.StatusOK, nil, attemptwire.AttemptProxyResult{
				Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
			})
		}}
		relay := &remoteRelayLinkStub{stream: stream}
		executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
			SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
			CachedForwardTicket: func() (agentauth.ForwardTicket, error) { return "ticket", nil },
			RelayEnabled:        func() bool { return true }, PeerRouteMode: func() string { return consts.PeerRouteModeRelayOnly },
		})
		outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 23, validRemoteBoundAttempt())
		require.Equal(t, AttemptSucceeded, outcome.Kind)
		require.Zero(t, direct.calls)
		require.Equal(t, 1, relay.calls)
	})

	t.Run("disabled target performs no IO", func(t *testing.T) {
		rctx, _ := newRemoteAttemptContext(t, context.Background(), 29)
		targets := &remoteTargetsStub{snapshots: map[string]RemoteTargetSnapshot{"target-a": {Enabled: false}}}
		direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
			return agentproxy.DirectOutcome{}
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
		executor := newRemoteExecutorForTest(targets, &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
			return agentproxy.DirectOutcome{}
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
					dst.Header().Add("Trailer", attemptwire.TrailerResult)
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
			direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
				return agentproxy.DirectOutcome{}
			}}
			relay := &remoteRelayLinkStub{stream: tt.stream}
			executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
				SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
				CachedForwardTicket: func() (agentauth.ForwardTicket, error) { return "ticket", nil },
				RelayEnabled:        func() bool { return true }, PeerRouteMode: func() string { return consts.PeerRouteModeRelayOnly },
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
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
		return agentproxy.DirectOutcome{}
	}}
	stream := &remoteRelayStreamStub{
		commit: tunnel.Committed, copyErr: errors.New("flush-only response interrupted"), dispatchProvider: true,
		respond: func(dst http.ResponseWriter) {
			dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			dst.Header().Add("Trailer", attemptwire.TrailerResult)
			flusher, ok := dst.(http.Flusher)
			require.True(t, ok, "attempt receiver must expose flush without requiring the source writer to do so")
			flusher.Flush()
		},
	}
	relay := &remoteRelayLinkStub{stream: stream}
	executor := NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: enabledRemoteTargets("target-a"),
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) { return "ticket", nil },
		RelayEnabled:        func() bool { return true }, PeerRouteMode: func() string { return consts.PeerRouteModeRelayOnly },
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

func (l *remoteCancellationRelayLink) OpenStream(ctx context.Context, _ app.RelayRequest) (app.RelayStream, error) {
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

func (s *remoteCancellationRelayStream) CopyResponse(ctx context.Context, _ http.ResponseWriter) error {
	if s.stage == "copy" {
		s.providerDispatches++
		s.cancel(s.cause)
		return context.Cause(ctx)
	}
	return nil
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
			direct := &remoteDirectStub{forward: func(ctx context.Context, _ agentproxy.DirectRequest, _ http.ResponseWriter) agentproxy.DirectOutcome {
				if tt.stage == "direct" {
					cancel(tt.cause)
					return agentproxy.DirectOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeRequestCancelled, Err: context.Cause(ctx)}
				}
				return agentproxy.DirectOutcome{Commit: tunnel.PreCommit, Code: agentproxy.CodeDirectConnect, Err: errors.New("direct unavailable")}
			}}
			stream := &remoteCancellationRelayStream{stage: tt.stage, cancel: cancel, cause: tt.cause}
			relay := &remoteCancellationRelayLink{stage: tt.stage, cancel: cancel, cause: tt.cause, stream: stream}
			executor := newRemoteExecutorForTest(enabledRemoteTargets("target-a"), direct, relay)

			outcome := executor.Execute(rctx, AttemptTarget{AgentID: "target-a", Kind: AttemptTargetRemote}, 43, validRemoteBoundAttempt())

			require.Equal(t, AttemptCanceled, outcome.Kind)
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
	direct := &remoteDirectStub{forward: func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome {
		return agentproxy.DirectOutcome{}
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

func newRemoteExecutorForTest(targets RemoteTargetRuntime, direct agentproxy.DirectRequestForwarder, relay app.RelayLink) RemoteAttemptExecutor {
	return NewRemoteAttemptExecutor(RemoteAttemptExecutorOptions{
		SourceAgentID: "source-a", Direct: direct, Relay: relay, Targets: targets,
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) { return "forward-ticket", nil },
		RelayEnabled:        func() bool { return true },
	})
}

func enabledRemoteTargets(agentID string) *remoteTargetsStub {
	return &remoteTargetsStub{snapshots: map[string]RemoteTargetSnapshot{
		agentID: {
			Enabled: true, HTTPAddresses: `[{"url":"http://target.invalid:8139","tag":"direct"}]`,
			AddressTag: "direct",
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
	request.Header.Set(attemptwire.HeaderMeta, "forged")
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
	require.NoError(t, json.NewEncoder(dst).Encode(result))
}

func writeRemoteResponse(t *testing.T, dst http.ResponseWriter, status int, body []byte, result attemptwire.AttemptProxyResult) {
	t.Helper()
	encoded, err := attemptwire.EncodeResult(result)
	require.NoError(t, err)
	dst.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	dst.Header().Add("Trailer", attemptwire.TrailerResult)
	dst.WriteHeader(status)
	_, err = dst.Write(body)
	require.NoError(t, err)
	dst.Header().Set(attemptwire.TrailerResult, encoded)
}

func writeRemoteProxyRejection(t *testing.T, dst http.ResponseWriter, status int) {
	t.Helper()
	dst.WriteHeader(status)
	require.NoError(t, json.NewEncoder(dst).Encode(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProxyRejected, HTTPStatus: status,
		ReasonCode: "attempt_ingress_rejected", ErrorMessage: "attempt proxy ingress rejected",
	}))
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
