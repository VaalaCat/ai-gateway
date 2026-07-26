package tunnel

import (
	"context"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestSessionDirectionRejectsZeroAndUnknownValuesAtConstruction(t *testing.T) {
	for _, direction := range []SessionDirection{0, SessionDirection(math.MaxUint8)} {
		session := newSession(newMemorySessionConn(), 1, testLimits(1), SessionOptions{Direction: direction})
		require.ErrorIs(t, session.initErr, errSessionOptions)
		select {
		case <-session.Done():
		default:
			t.Fatalf("direction %d did not create a terminal session", direction)
		}
	}
}

func TestSessionDirectionAcceptsEachExplicitDirection(t *testing.T) {
	handler := NewTargetHandler(TargetHandlerOptions{
		TargetAgentID:       "target-a",
		RelayInboundEnabled: func() bool { return true },
		Router:              http.NotFoundHandler(),
	})
	tests := []SessionOptions{
		{Direction: SessionDirectionRelay},
		{Direction: SessionDirectionDirectOutgoing},
		{
			Direction:           SessionDirectionDirectIncoming,
			TargetHandler:       handler,
			BoundSourceAgentID:  "source-a",
			AdmissionDeadline:   time.Now().Add(time.Hour),
			SourceEnabled:       func(string) bool { return true },
			TargetStatusEnabled: func() bool { return true },
		},
	}
	for _, opts := range tests {
		_, err := validateSessionOptions(defaultSessionOptions(opts))
		require.NoError(t, err, "direction %d", opts.Direction)
	}
}

func TestSessionDirectionOutgoingCanOpenButRejectsReverseOpen(t *testing.T) {
	limits := testLimits(2)
	session, peer := startTestSession(t, limits, SessionOptions{
		Direction:   SessionDirectionDirectOutgoing,
		IngressKind: agentproxy.IngressKindDirectTunnel,
	})

	stream, err := session.OpenProbeStream(t.Context(), agentproxy.ProbeStreamRequest{
		Policy: wire.ProbeBypassBusinessPolicy, TargetAgentID: "target-a", Remaining: time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, stream)
	require.Equal(t, wire.FrameOpen, readPeerFrame(t, peer, limits).Type)

	writeTargetOpen(t, peer, limits, testStreamID(91), validTargetBoundOpen(limits, "/v1/responses"))
	reset := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameReset, reset.Type)
}

func TestSessionDirectionIncomingFailsClosedForActiveOpenAPIs(t *testing.T) {
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.NotFoundHandler()})
	session, _ := startTestSession(t, testLimits(1), SessionOptions{
		Direction:           SessionDirectionDirectIncoming,
		IngressKind:         agentproxy.IngressKindDirectTunnel,
		BoundSourceAgentID:  "source-a",
		AdmissionDeadline:   time.Now().Add(time.Hour),
		SourceEnabled:       func(sourceID string) bool { return sourceID == "source-a" },
		TargetStatusEnabled: func() bool { return true },
		TargetHandler:       handler,
	})

	_, err := session.OpenProbeStream(t.Context(), agentproxy.ProbeStreamRequest{
		Policy: wire.ProbeBypassBusinessPolicy, TargetAgentID: "target-a", Remaining: time.Second,
	})
	require.ErrorIs(t, err, errSessionDirection)
	_, err = session.OpenAttemptStream(t.Context(), agentproxy.AttemptStreamRequest{})
	require.ErrorIs(t, err, errSessionDirection)
}

func TestTargetRelayPolicyResetKeepsSessionHealthy(t *testing.T) {
	limits := testLimits(2)
	var relayInboundEnabled atomic.Bool
	source, target := startTargetPolicySessionPair(t, limits, relayInboundEnabled.Load)
	sourceErrors := len(source.RecentErrors())
	targetErrors := len(target.RecentErrors())

	rejected, err := source.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	commitCtx, cancelCommit := context.WithTimeout(t.Context(), time.Second)
	err = rejected.Commit(commitCtx)
	cancelCommit()
	require.Error(t, err)
	var resetErr *StreamResetError
	require.ErrorAs(t, err, &resetErr)
	require.Equal(t, wire.Reset{
		Code: consts.RouteErrorTargetRelayInboundDisabled, Stage: "policy", Committed: false,
	}, resetErr.reset)
	require.Equal(t, wire.PreCommit, rejected.CommitState())
	requireSessionRunning(t, source)
	requireSessionRunning(t, target)
	require.Len(t, source.RecentErrors(), sourceErrors)
	require.Len(t, target.RecentErrors(), targetErrors)

	relayInboundEnabled.Store(true)
	allowed, err := source.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	commitCtx, cancelCommit = context.WithTimeout(t.Context(), time.Second)
	require.NoError(t, allowed.Commit(commitCtx))
	cancelCommit()
	require.Equal(t, wire.Committed, allowed.CommitState())
	requireSessionRunning(t, source)
	requireSessionRunning(t, target)
	require.Len(t, source.RecentErrors(), sourceErrors)
	require.Len(t, target.RecentErrors(), targetErrors)
	require.NoError(t, allowed.Close())
}

func startTargetPolicySessionPair(t *testing.T, limits wire.Limits, relayInboundEnabled func() bool) (*Session, *Session) {
	t.Helper()
	source, targetConn := startTestSession(t, limits, SessionOptions{
		Direction: SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour,
	})
	handler := NewTargetHandler(TargetHandlerOptions{
		TargetAgentID: "target-a", RelayInboundEnabled: relayInboundEnabled, Router: http.NotFoundHandler(),
	})
	target := NewSession(targetConn, 11, limits, SessionOptions{
		Direction: SessionDirectionRelay, TargetHandler: handler, PingInterval: time.Hour, PongTimeout: time.Hour,
	})
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = target.Run(t.Context())
	}()
	<-target.started
	t.Cleanup(func() {
		target.Cancel(context.Canceled)
		select {
		case <-runDone:
		case <-time.After(time.Second):
			t.Error("target Session.Run did not exit")
		}
	})
	return source, target
}

func TestBoundSourceIdentityOverridesFrameAndAdmissionIsCheckedOnce(t *testing.T) {
	limits := testLimits(1)
	var enabled atomic.Bool
	enabled.Store(true)
	captured := make(chan agentproxy.IngressMeta, 1)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta, ok := agentproxy.IngressMetaFromContext(r.Context())
		require.True(t, ok)
		captured <- meta
		writeExplicitAttemptResponse(t, w, r, http.StatusOK, nil, nil)
	})})

	session, peer := startTestSession(t, limits, SessionOptions{
		Direction:           SessionDirectionDirectIncoming,
		IngressKind:         agentproxy.IngressKindDirectTunnel,
		BoundSourceAgentID:  "source-ticket",
		AdmissionDeadline:   time.Now().Add(time.Hour),
		SourceEnabled:       func(sourceID string) bool { return sourceID == "source-ticket" && enabled.Load() },
		TargetStatusEnabled: func() bool { return true },
		TargetHandler:       handler,
	})

	open := validTargetBoundOpen(limits, "/v1/responses")
	open.SourceAgentID = "source-forged"
	id := testStreamID(92)
	writeTargetOpen(t, peer, limits, id, open)
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	enabled.Store(false)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id, Sequence: 2})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id, Sequence: 3})

	select {
	case meta := <-captured:
		require.Equal(t, "source-ticket", meta.SourceAgentID)
		require.Equal(t, agentproxy.IngressKindDirectTunnel, meta.Kind)
	case <-time.After(time.Second):
		t.Fatal("target handler was not called")
	}
	require.NotNil(t, session)
}

func TestBoundSourceRejectsExpiredDisabledAndMissingBindings(t *testing.T) {
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.NotFoundHandler()})
	tests := []SessionOptions{
		{Direction: SessionDirectionDirectIncoming, IngressKind: agentproxy.IngressKindDirectTunnel, AdmissionDeadline: time.Now().Add(time.Hour), SourceEnabled: func(string) bool { return true }, TargetStatusEnabled: func() bool { return true }, TargetHandler: handler},
		{Direction: SessionDirectionDirectIncoming, IngressKind: agentproxy.IngressKindDirectTunnel, BoundSourceAgentID: "source-a", AdmissionDeadline: time.Now().Add(time.Hour), TargetStatusEnabled: func() bool { return true }, TargetHandler: handler},
		{Direction: SessionDirectionDirectIncoming, IngressKind: agentproxy.IngressKindDirectTunnel, BoundSourceAgentID: "source-a", AdmissionDeadline: time.Now().Add(time.Hour), SourceEnabled: func(string) bool { return true }, TargetStatusEnabled: func() bool { return true }},
		{Direction: SessionDirectionDirectIncoming, IngressKind: agentproxy.IngressKindDirectTunnel, BoundSourceAgentID: "source-a", AdmissionDeadline: time.Now().Add(time.Hour), SourceEnabled: func(string) bool { return true }, TargetHandler: handler},
	}
	for index, opts := range tests {
		conn, _ := websocketPair(t)
		session := NewSession(conn, uint64(index+1), testLimits(1), opts)
		err := session.Run(context.Background())
		require.Error(t, err)
		select {
		case <-session.Done():
		case <-time.After(time.Second):
			t.Fatal("invalid direct incoming session did not terminate")
		}
	}
}

func TestBoundSourceRejectsNewOpenAtExpiryOrAfterDisable(t *testing.T) {
	now := time.Now()
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.NotFoundHandler()})
	tests := []struct {
		name     string
		deadline time.Time
		enabled  bool
	}{
		{name: "exact expiry", deadline: now, enabled: true},
		{name: "source disabled", deadline: now.Add(time.Hour)},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(1)
			session, peer := startTestSession(t, limits, SessionOptions{
				Direction: SessionDirectionDirectIncoming, IngressKind: agentproxy.IngressKindDirectTunnel,
				BoundSourceAgentID: "source-a", AdmissionDeadline: test.deadline,
				SourceEnabled:       func(string) bool { return test.enabled },
				TargetStatusEnabled: func() bool { return true }, TargetHandler: handler,
				Now: func() time.Time { return now },
			})
			writeTargetOpen(t, peer, limits, testStreamID(byte(93+index)), validTargetBoundOpen(limits, "/v1/responses"))
			require.Equal(t, wire.FrameReset, readPeerFrame(t, peer, limits).Type)
			requireSessionRunning(t, session)
		})
	}
}
