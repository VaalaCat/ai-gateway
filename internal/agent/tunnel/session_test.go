package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestAgentRelaySessionErrorRingIsBoundedAndCopyIsolated(t *testing.T) {
	session := newSessionValue(nil, 1, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	require.False(t, session.recordError(diagnostics.Event{}))
	for index := range 25 {
		require.True(t, session.recordError(diagnostics.Event{
			Code: fmt.Sprintf("relay-%02d", index), Stage: "protocol", At: time.Unix(int64(index), 0),
		}))
	}

	first := session.RecentErrors()
	require.Len(t, first, 20)
	require.Equal(t, "relay-05", first[0].Code)
	first[0].Code = "mutated"
	require.Equal(t, "relay-05", session.RecentErrors()[0].Code)
}

func TestAgentRelaySessionReplacementOwnsIndependentErrorRing(t *testing.T) {
	old := newSessionValue(nil, 1, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	current := newSessionValue(nil, 2, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	require.True(t, old.recordError(diagnostics.Event{Code: "stale", Stage: "read"}))
	require.True(t, current.recordError(diagnostics.Event{Code: "current", Stage: "read"}))
	require.Equal(t, "stale", old.RecentErrors()[0].Code)
	require.Equal(t, "current", current.RecentErrors()[0].Code)
}

func TestAgentRelayTerminalSessionRecordsInitializationError(t *testing.T) {
	session := newTerminalSession(nil, 1, testLimits(1), SessionOptions{Direction: SessionDirectionRelay}, errors.New("Authorization Bearer secret"))
	errors := session.RecentErrors()
	require.Len(t, errors, 1)
	require.Equal(t, wire.ErrorCodeRelayProtocol, errors[0].Code)
	require.Equal(t, "redacted", errors[0].Message)
}

func TestDirectSessionIdleSinceTracksAdmissionAndStreams(t *testing.T) {
	clock := newPoolTestClock()
	session := newSession(newMemorySessionConn(), 1, testLimits(2), SessionOptions{
		Direction: SessionDirectionRelay,
		Now:       clock.Now,
	})

	idleSince, idle := session.idleSinceTime()
	require.True(t, idle)
	require.Equal(t, clock.Now(), idleSince)

	clock.Advance(time.Second)
	require.True(t, session.acquireAdmission())
	_, idle = session.idleSinceTime()
	require.False(t, idle)
	session.releaseAdmission()
	idleSince, idle = session.idleSinceTime()
	require.True(t, idle)
	require.Equal(t, clock.Now(), idleSince)

	first := &Stream{session: session, id: testStreamID(1), generation: session.Generation()}
	second := &Stream{session: session, id: testStreamID(2), generation: session.Generation()}
	require.NoError(t, session.admitStream(first))
	require.NoError(t, session.admitStream(second))
	_, idle = session.idleSinceTime()
	require.False(t, idle)

	clock.Advance(time.Second)
	session.removeStream(first)
	_, idle = session.idleSinceTime()
	require.False(t, idle)
	session.removeStream(second)
	idleSince, idle = session.idleSinceTime()
	require.True(t, idle)
	require.Equal(t, clock.Now(), idleSince)
}

func TestDirectIncomingSessionRejectsTargetAdmissionAfterDrainStarts(t *testing.T) {
	session := newSessionValue(nil, 1, testLimits(2), SessionOptions{Direction: SessionDirectionDirectIncoming})
	admitted := &targetStream{session: session, id: testStreamID(1)}
	require.NoError(t, session.admitTarget(admitted))
	require.Equal(t, 1, session.StreamCount())

	session.setAccepting(false)
	rejected := &targetStream{session: session, id: testStreamID(2)}
	require.Error(t, session.admitTarget(rejected))
	require.Equal(t, 1, session.StreamCount(), "draining must preserve existing work without admitting a new target stream")

	session.removeTarget(admitted)
}

func TestV2UnsupportedProtocolVersionUsesDedicatedDiagnosticCode(t *testing.T) {
	err := fmt.Errorf("%w: %w", errProtocol, wire.ErrUnsupportedVersion)
	require.Equal(t, wire.ErrorCodeUnsupportedProtocolVersion, sessionDiagnosticCode(err))
}

func TestSessionBufferedByteSnapshotTracksCurrentAndPeak(t *testing.T) {
	limits := testLimits(1)
	session := newSessionValue(nil, 1, limits, SessionOptions{})
	current, peak := session.bufferedByteSnapshot()
	require.Zero(t, current)
	require.Zero(t, peak)

	writerCtx, cancelWriter := context.WithCancel(context.Background())
	defer cancelWriter()
	session.writer = newFairWriter(writerCtx, limits.MaxQueuedSessionBytes, time.Second, func(wire.Frame) error {
		return nil
	})
	session.writer.onQueuedBytesChanged = session.adjustQueuedBytes
	require.NoError(t, session.reserveIncoming(7))
	current, peak = session.bufferedByteSnapshot()
	require.EqualValues(t, 7, current)
	require.EqualValues(t, 7, peak)

	require.NoError(t, session.releaseIncoming(7))
	payload := []byte("queued")
	id := testStreamID(91)
	require.NoError(t, session.writer.Enqueue(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: id, Payload: payload,
	}, nil))
	queueBytes := int64(wire.HeaderSize + len(payload))
	current, peak = session.bufferedByteSnapshot()
	require.Equal(t, queueBytes, current)
	require.Equal(t, queueBytes, peak, "non-overlapping historical peaks must not be added")

	require.NoError(t, session.reserveIncoming(3))
	wantCombinedPeak := queueBytes + 3
	current, peak = session.bufferedByteSnapshot()
	require.Equal(t, wantCombinedPeak, current)
	require.Equal(t, wantCombinedPeak, peak)

	require.NoError(t, session.releaseIncoming(3))
	session.writer.discard(id)
	current, peak = session.bufferedByteSnapshot()
	require.Zero(t, current)
	require.Equal(t, wantCombinedPeak, peak)
}

func TestSessionOpenStreamClonesMetadataAndEnforcesStreamCap(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	header := http.Header{"X-Test": {"before"}}
	request := validBoundRelayRequest("/v1/chat/completions")
	request.RouteID = 7
	request.RequestID = "request"
	request.Header = header
	request.BodyLength = 12
	stream, err := session.OpenAttemptStream(t.Context(), request)
	require.NoError(t, err)
	header.Set("X-Test", "after")
	frame := readPeerFrame(t, peer, testLimits(1))
	require.Equal(t, wire.FrameOpen, frame.Type)
	var open wire.Open
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &open, testLimits(1).MaxMetadataBytes))
	require.Equal(t, "before", http.Header(open.Header).Get("X-Test"))
	require.Empty(t, open.ProbePolicy)
	require.Empty(t, open.SourceAgentID)
	require.Equal(t, testLimits(1).InitialStreamWindow, open.ResponseWindow)

	_, err = session.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.Error(t, err)
	stream.Cancel(context.Canceled)
	require.NoError(t, stream.Close())
}

func TestSessionBoundAttemptOpenUsesDedicatedEndpointAndDefensiveCopy(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay})
	meta := validTunnelAttemptMeta()
	want := meta
	stream, err := session.OpenAttemptStream(t.Context(), agentproxy.AttemptStreamRequest{
		TargetAgentID: "target-a", RouteID: 0, Method: http.MethodPost, Hop: 1,
		Path: attemptwire.EndpointPath, Attempt: meta,
	})
	require.NoError(t, err)
	meta.Attempt.RealModel = "mutated-after-open"
	meta.RequestPath = "/v1/messages"

	frame := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameOpen, frame.Type)
	var open wire.Open
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &open, limits.MaxMetadataBytes))
	require.Equal(t, http.MethodPost, open.Method)
	require.Equal(t, attemptwire.EndpointPath, open.Path)
	require.Zero(t, open.RouteID)
	require.NotNil(t, open.Attempt)
	require.Equal(t, want, *open.Attempt)
	require.Equal(t, "/v1/responses", open.Attempt.RequestPath)

	stream.Cancel(context.Canceled)
	require.NoError(t, stream.Close())
}

func TestSessionRejectsInvalidEnvelopeBeforeSourceAdmission(t *testing.T) {
	meta := validTunnelAttemptMeta()
	invalidMeta := attemptwire.AttemptProxyMeta{}
	invalidProviderPath := meta
	invalidProviderPath.RequestPath = "/internal/agent/attempt"
	validRequests := []struct {
		name string
		req  agentproxy.AttemptStreamRequest
	}{
		{
			name: "bound business open",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: meta,
			},
		},
	}
	invalidRequests := []struct {
		name string
		req  agentproxy.AttemptStreamRequest
	}{
		{
			name: "business open without attempt",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: "/v1/responses", Hop: 1,
			},
		},
		{
			name: "invalid attempt metadata",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: invalidMeta,
			},
		},
		{
			name: "invalid provider request path",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: invalidProviderPath,
			},
		},
		{
			name: "business open with wrong hop",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 2, Attempt: meta,
			},
		},
		{
			name: "bound attempt with wrong method",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodGet, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: meta,
			},
		},
		{
			name: "bound attempt with provider wire path",
			req: agentproxy.AttemptStreamRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: meta.RequestPath,
				Hop: 1, Attempt: meta,
			},
		},
	}

	for _, test := range invalidRequests {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(1)
			conn := newMemorySessionConn()
			session := newSession(conn, 1, limits, SessionOptions{Direction: SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour})
			runDone := make(chan error, 1)
			go func() { runDone <- session.Run(t.Context()) }()
			<-session.started
			t.Cleanup(func() {
				session.Cancel(context.Canceled)
				<-runDone
			})

			_, err := session.OpenAttemptStream(t.Context(), test.req)
			if !errors.Is(err, errProtocol) {
				t.Errorf("invalid OPEN error = %v, want %v", err, errProtocol)
			}
			if count := session.StreamCount(); count != 0 {
				t.Errorf("invalid OPEN consumed %d stream admission slots", count)
			}

			timer := time.NewTimer(30 * time.Millisecond)
			defer timer.Stop()
			select {
			case message := <-conn.writes:
				frame := decodeMemoryWrite(t, message, limits)
				t.Fatalf("invalid OPEN emitted frame %d without an OPEN", frame.Type)
			case <-session.Done():
				t.Fatal("invalid OPEN attempts closed the Session")
			case <-timer.C:
			}

		})
	}

	for _, test := range validRequests {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(1)
			conn := newMemorySessionConn()
			session := newSession(conn, 1, limits, SessionOptions{Direction: SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour})
			runDone := make(chan error, 1)
			go func() { runDone <- session.Run(t.Context()) }()
			<-session.started
			t.Cleanup(func() {
				session.Cancel(context.Canceled)
				<-runDone
			})

			stream, err := session.OpenAttemptStream(t.Context(), test.req)
			require.NoError(t, err)
			require.Equal(t, 1, session.StreamCount())
			select {
			case message := <-conn.writes:
				require.Equal(t, wire.FrameOpen, decodeMemoryWrite(t, message, limits).Type)
			case <-time.After(time.Second):
				t.Fatal("valid OPEN did not reach the peer")
			}
			stream.Cancel(context.Canceled)
			select {
			case <-stream.Done():
			case <-time.After(time.Second):
				t.Fatal("valid stream did not stop")
			}
		})
	}
}

func TestSessionOpenProbeStreamBuildsFixedPing(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay})
	stream, err := session.OpenProbeStream(t.Context(), agentproxy.ProbeStreamRequest{
		TargetAgentID: "target-a", RequestID: "probe", Remaining: time.Second,
		Policy: agentproxy.ProbeBypassBusinessPolicy,
	})
	require.NoError(t, err)
	frame := readPeerFrame(t, peer, limits)
	var open wire.Open
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &open, limits.MaxMetadataBytes))
	require.True(t, open.IsConnectivityProbe())
	require.Nil(t, open.Attempt)
	stream.Cancel(context.Canceled)
	require.NoError(t, stream.Close())
}

func TestSessionOpenProbeStreamRejectsInvalidPolicyAndBoundaryBeforeAdmission(t *testing.T) {
	for _, test := range []struct {
		name string
		req  agentproxy.ProbeStreamRequest
	}{
		{name: "empty target", req: agentproxy.ProbeStreamRequest{
			Remaining: time.Second, Policy: agentproxy.ProbeRespectBusinessPolicy,
		}},
		{name: "zero remaining", req: agentproxy.ProbeStreamRequest{
			TargetAgentID: "target-a", Policy: agentproxy.ProbeRespectBusinessPolicy,
		}},
		{name: "invalid policy", req: agentproxy.ProbeStreamRequest{
			TargetAgentID: "target-a", Remaining: time.Second, Policy: agentproxy.ProbePolicy("invalid"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(1)
			conn := newMemorySessionConn()
			session := newSession(conn, 1, limits, SessionOptions{Direction: SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour})
			runDone := make(chan error, 1)
			go func() { runDone <- session.Run(t.Context()) }()
			<-session.started
			t.Cleanup(func() { session.Cancel(context.Canceled); <-runDone })

			_, err := session.OpenProbeStream(t.Context(), test.req)
			require.ErrorIs(t, err, errProtocol)
			require.Zero(t, session.StreamCount())
			select {
			case frame := <-conn.writes:
				t.Fatalf("invalid Probe emitted frame %d", decodeMemoryWrite(t, frame, limits).Type)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

func TestSessionTargetBoundAttemptPreservesTrailerAndFlowControl(t *testing.T) {
	limits := testLimits(1)
	wantMeta := validTunnelAttemptMeta()
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ingress, ok := agentproxy.IngressMetaFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, wantMeta, *ingress.Attempt)
		contextMeta, ok := attemptwire.MetaFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, wantMeta, contextMeta)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), body)
		w.Header().Set("Trailer", "X-Usage")
		writeExplicitAttemptResponse(t, w, r, http.StatusNoContent, nil, http.Header{"X-Usage": {"tokens=3"}})
	})})

	_, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(93)
	writeTargetOpen(t, peer, limits, id, wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath, BodyLength: 3,
		SourceAgentID: "source-a", TargetAgentID: "target-a", RouteID: 0, Hop: 1,
		ResponseWindow: limits.InitialStreamWindow, Attempt: &wantMeta,
	})
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Payload: []byte("abc")})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id})

	var sawWindow bool
	for {
		frame := readPeerFrame(t, peer, limits)
		switch frame.Type {
		case wire.FrameWindowUpdate:
			var update wire.WindowUpdate
			require.NoError(t, wire.DecodeMetadata(frame.Payload, &update, limits.MaxMetadataBytes))
			require.EqualValues(t, 3, update.Bytes)
			sawWindow = true
		case wire.FrameHeaders:
			var headers wire.Headers
			require.NoError(t, wire.DecodeMetadata(frame.Payload, &headers, limits.MaxMetadataBytes))
			require.Equal(t, http.StatusNoContent, headers.StatusCode)
			require.Contains(t, http.Header(headers.Trailer), "X-Usage")
		case wire.FrameAttemptResult:
			result, err := attemptwire.DecodeResultJSON(frame.Payload)
			require.NoError(t, err)
			require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
		case wire.FrameEnd:
			var trailers wire.Trailers
			require.NoError(t, wire.DecodeMetadata(frame.Payload, &trailers, limits.MaxMetadataBytes))
			require.Equal(t, "tokens=3", http.Header(trailers.Header).Get("X-Usage"))
			require.True(t, sawWindow)
			return
		default:
			t.Fatalf("unexpected bound attempt response frame: %v", frame.Type)
		}
	}
}

func TestSessionDuplicateStreamIDIsRejected(t *testing.T) {
	session, _ := startTestSession(t, testLimits(2), SessionOptions{Direction: SessionDirectionRelay})
	id := testStreamID(8)
	_, err := session.openStream(t.Context(), id, validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	_, err = session.openStream(t.Context(), id, validBoundRelayRequest("/v1/responses"))
	require.Error(t, err)
}

func TestSessionRejectsTextMessages(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	require.NoError(t, peer.WriteMessage(websocket.TextMessage, []byte("not binary")))
	require.Eventually(t, func() bool {
		select {
		case <-session.Done():
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestSessionSetsFrameReadLimitBeforeReading(t *testing.T) {
	conn := newMemorySessionConn()
	limits := testLimits(1)
	session := newSession(conn, 1, limits, SessionOptions{Direction: SessionDirectionRelay})
	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(t.Context()) }()
	<-session.started
	conn.mu.Lock()
	readLimit := conn.readLimit
	conn.mu.Unlock()
	require.Equal(t, sessionMessageReadLimit(session.limits), readLimit)
	conn.inbound <- memoryMessage{messageType: websocket.BinaryMessage, payload: make([]byte, readLimit+1)}
	require.ErrorContains(t, <-runDone, "read limit exceeded")
}

func TestSessionOversizedDataMessageClosesRealWebSocket(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	readLimit := sessionMessageReadLimit(session.limits)
	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, make([]byte, readLimit+1)))
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("oversized websocket DATA did not terminate Session")
	}
}

func TestSessionUnknownDataClosesOnlyAtThreshold(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{Direction: SessionDirectionRelay})
	for i := byte(1); i <= 7; i++ {
		writePeerFrame(t, peer, testLimits(1), wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: testStreamID(i), Payload: []byte("x"),
		})
	}
	require.Never(t, func() bool {
		select {
		case <-session.Done():
			return true
		default:
			return false
		}
	}, 30*time.Millisecond, 5*time.Millisecond)
	writePeerFrame(t, peer, testLimits(1), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: testStreamID(9), Payload: []byte("x"),
	})
	require.Eventually(t, func() bool {
		select {
		case <-session.Done():
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestSessionCloseCancelsStreamsAndWaitsForOwners(t *testing.T) {
	session, _ := startTestSession(t, testLimits(2), SessionOptions{Direction: SessionDirectionRelay})
	stream, err := session.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, session.Close(ctx))
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("stream owner did not exit")
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("session did not close done")
	}
}

func TestSessionCloseAbandonsUnclaimedSuccessfulResponse(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay})
	stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits)})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID})
	require.Eventually(t, stream.isTerminalSuccess, time.Second, time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, session.Close(ctx))
	select {
	case <-stream.Done():
	default:
		t.Fatal("Session Close did not join response reservation")
	}
}

func TestSessionOptionsDefaultsBoundLiveness(t *testing.T) {
	opts := defaultSessionOptions(SessionOptions{Direction: SessionDirectionRelay})
	require.Equal(t, 20*time.Second, opts.PingInterval)
	require.Equal(t, 60*time.Second, opts.PongTimeout)
	require.Equal(t, 15*time.Second, opts.WriteTimeout)
	require.Equal(t, 30*time.Second, opts.OpenCommitTimeout)
	require.Equal(t, 60*time.Second, opts.WindowStallTimeout)
	require.Equal(t, 30*time.Second, opts.TombstoneTTL)
	require.Equal(t, 512, opts.TombstoneLimit)
}

func TestSessionOpenQueueWaitStopsWithStreamAndNeverWritesLateOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	written := make(chan wire.Frame, 4)
	var first sync.Once
	w := newFairWriter(ctx, 512, time.Second, func(frame wire.Frame) error {
		first.Do(func() {
			close(writeStarted)
			<-releaseWrite
		})
		written <- frame
		return nil
	})
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })
	fill := wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
		StreamID: testStreamID(99), Payload: make([]byte, 512-wire.HeaderSize),
	}
	require.NoError(t, w.Enqueue(t.Context(), fill, nil))
	<-writeStarted

	started := make(chan struct{})
	close(started)
	session := &Session{
		generation: 1, limits: testLimits(1),
		opts:    defaultSessionOptions(SessionOptions{Direction: SessionDirectionRelay, OpenCommitTimeout: 20 * time.Millisecond}),
		started: started, done: make(chan struct{}), ctx: ctx, writer: w,
		streams:    make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	openDone := make(chan error, 1)
	go func() {
		_, err := session.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
		openDone <- err
	}()
	select {
	case err := <-openDone:
		require.Error(t, err)
	case <-time.After(100 * time.Millisecond):
		close(releaseWrite)
		err := <-openDone
		require.Fail(t, "OpenStream outlived its stream timeout", "later result: %v", err)
		return
	}
	close(releaseWrite)
	require.Eventually(t, func() bool {
		queuedBytes, _ := w.stats()
		session.streamsMu.Lock()
		streamCount := len(session.streams)
		session.streamsMu.Unlock()
		return queuedBytes == 0 && streamCount == 0
	}, time.Second, time.Millisecond)
	for {
		select {
		case frame := <-written:
			require.NotEqual(t, wire.FrameOpen, frame.Type)
		default:
			goto drained
		}
	}
drained:
	session.streamsMu.Lock()
	require.Empty(t, session.streams)
	session.streamsMu.Unlock()
}

func TestSessionPingsAndPongExtendsReadDeadline(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{
		Direction:    SessionDirectionRelay,
		PingInterval: 10 * time.Millisecond,
		PongTimeout:  80 * time.Millisecond,
		WriteTimeout: 30 * time.Millisecond,
	})
	ping := make(chan struct{}, 1)
	peer.SetPingHandler(func(payload string) error {
		select {
		case ping <- struct{}{}:
		default:
		}
		return peer.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(30*time.Millisecond))
	})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _, _ = peer.ReadMessage()
	}()
	select {
	case <-ping:
	case <-time.After(time.Second):
		t.Fatal("peer did not receive ping")
	}
	require.Never(t, func() bool {
		select {
		case <-session.Done():
			return true
		default:
			return false
		}
	}, 30*time.Millisecond, 5*time.Millisecond)
	_ = peer.Close()
	<-readDone
}

func TestSessionPongTimeoutClosesSilentPeer(t *testing.T) {
	session, _ := startTestSession(t, testLimits(1), SessionOptions{
		Direction:    SessionDirectionRelay,
		PingInterval: time.Second,
		PongTimeout:  20 * time.Millisecond,
	})
	require.Eventually(t, func() bool {
		select {
		case <-session.Done():
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestSessionTargetReadyDoesNotExecuteAndCommittedPipelineRunsOnce(t *testing.T) {
	limits := testLimits(2)
	var calls atomic.Int32
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), body)
		meta, ok := agentproxy.IngressMetaFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, "source-a", meta.SourceAgentID)
		w.Header().Set("Content-Type", "application/octet-stream")
		writeExplicitAttemptResponse(t, w, r, http.StatusOK, []byte{0, 1, 0xff}, nil)
	})})

	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(71)
	open := validTargetBoundOpen(limits, "/v1/responses")
	open.BodyLength = 3
	writeTargetOpen(t, peer, limits, id, open)
	ready := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameReady, ready.Type)
	require.Zero(t, calls.Load(), "READY must not execute the router")

	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Payload: []byte("abc")})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id})

	window := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameWindowUpdate, window.Type)
	var update wire.WindowUpdate
	require.NoError(t, wire.DecodeMetadata(window.Payload, &update, limits.MaxMetadataBytes))
	require.EqualValues(t, 3, update.Bytes)
	headers := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameHeaders, headers.Type)
	data := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameResponseData, data.Type)
	require.Equal(t, []byte{0, 1, 0xff}, data.Payload)
	require.Equal(t, wire.FrameAttemptResult, readPeerFrame(t, peer, limits).Type)
	require.Equal(t, wire.FrameEnd, readPeerFrame(t, peer, limits).Type)
	require.EqualValues(t, 1, calls.Load())

	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Never(t, func() bool { return calls.Load() > 1 }, 30*time.Millisecond, 5*time.Millisecond)
	requireSessionRunning(t, session)
}

func TestSessionTargetSwitchingProtocolsReturnsResetWithoutHeaders(t *testing.T) {
	limits := testLimits(1)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})})

	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(88)
	writeTargetOpen(t, peer, limits, id, validTargetBoundOpen(limits, "/v1/responses"))
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	frame := readPeerFrame(t, peer, limits)
	if frame.Type == wire.FrameCommitted {
		frame = readPeerFrame(t, peer, limits)
	}
	require.Equal(t, wire.FrameReset, frame.Type)
	var reset wire.Reset
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &reset, limits.MaxMetadataBytes))
	require.True(t, reset.Committed)
	requireSessionRunning(t, session)
}

func TestSessionTargetRejectsInvalidOpenWithoutClosingSession(t *testing.T) {
	limits := testLimits(2)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.NotFoundHandler()})
	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	invalidID := testStreamID(72)
	writeTargetOpen(t, peer, limits, invalidID, validTargetBoundOpen(limits, "http://127.0.0.1/v1/responses"))
	require.Equal(t, wire.FrameReset, readPeerFrame(t, peer, limits).Type)
	requireSessionRunning(t, session)

	validID := testStreamID(73)
	writeTargetOpen(t, peer, limits, validID, validTargetBoundOpen(limits, "/v1/responses"))
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCancel, StreamID: validID})
	requireSessionRunning(t, session)
}

func TestSessionTargetInvalidHeadersNeverReachReadyOrRouter(t *testing.T) {
	limits := testLimits(3)
	var calls atomic.Int32
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	})})

	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	invalid := []map[string][]string{
		{"Bad Header": {"value"}},
		{"X-Test": {"bad\r\nvalue"}},
	}
	for i, header := range invalid {
		id := testStreamID(byte(84 + i))
		open := validTargetBoundOpen(limits, "/v1/responses")
		open.Header = header
		writeTargetOpen(t, peer, limits, id, open)
		frame := readPeerFrame(t, peer, limits)
		require.Equal(t, wire.FrameReset, frame.Type)
		require.Equal(t, id, frame.StreamID)
	}
	require.Zero(t, calls.Load())
	requireSessionRunning(t, session)
}

func TestSessionTargetCancelPropagatesToRouterContext(t *testing.T) {
	limits := testLimits(1)
	started := make(chan struct{})
	canceled := make(chan error, 1)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})})
	handler.router = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		canceled <- context.Cause(r.Context())
	})
	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(74)
	open := validTargetBoundOpen(limits, "/v1/responses")
	open.BodyLength = -1
	writeTargetOpen(t, peer, limits, id, open)
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("router did not start after COMMIT")
	}
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCancel, StreamID: id})
	select {
	case cause := <-canceled:
		require.Error(t, cause)
	case <-time.After(time.Second):
		t.Fatal("CANCEL did not reach router request context")
	}
	requireSessionRunning(t, session)
}

func TestSessionTargetDeadlineShrinksWhileWaitingForCommit(t *testing.T) {
	limits := testLimits(1)
	remaining := make(chan time.Duration, 1)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		remaining <- time.Until(deadline)
		writeExplicitAttemptResponse(t, w, r, http.StatusNoContent, nil, nil)
	})})

	_, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(75)
	open := validTargetBoundOpen(limits, "/v1/responses")
	open.RemainingNanos = int64(150 * time.Millisecond)
	writeTargetOpen(t, peer, limits, id, open)
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	time.Sleep(40 * time.Millisecond)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	select {
	case got := <-remaining:
		require.Positive(t, got)
		require.Less(t, got, 130*time.Millisecond)
	case <-time.After(time.Second):
		t.Fatal("router did not observe target deadline")
	}
}

func TestSessionTargetRejectsRequestEndBeforeDeclaredBody(t *testing.T) {
	limits := testLimits(1)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
	})})

	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(76)
	open := validTargetBoundOpen(limits, "/v1/responses")
	open.BodyLength = 3
	writeTargetOpen(t, peer, limits, id, open)
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id})
	require.Equal(t, wire.FrameReset, readPeerFrame(t, peer, limits).Type)
	requireSessionRunning(t, session)
}

func TestSessionShutdownCancelsTargetsBeforeJoiningBlockedSource(t *testing.T) {
	limits := testLimits(2)
	targetStarted := make(chan struct{})
	targetCanceled := make(chan struct{})
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(targetStarted)
		<-r.Context().Done()
		close(targetCanceled)
	})})

	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	source, err := session.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	require.Equal(t, wire.FrameOpen, readPeerFrame(t, peer, limits).Type)
	require.Equal(t, responseClaimAcquired, source.responseOwner.Claim())
	t.Cleanup(source.responseOwner.Finish)

	id := testStreamID(77)
	open := validTargetBoundOpen(limits, "/v1/responses")
	open.BodyLength = -1
	writeTargetOpen(t, peer, limits, id, open)
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	select {
	case <-targetStarted:
	case <-time.After(time.Second):
		t.Fatal("target router did not start")
	}

	session.Cancel(context.Canceled)
	select {
	case <-targetCanceled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("target cancellation waited for blocked source owner")
	}
	source.responseOwner.Finish()
}

func TestSessionTargetGateOnlyAppliesAtOpenAdmission(t *testing.T) {
	limits := testLimits(1)
	var enabled atomic.Bool
	enabled.Store(true)
	called := make(chan struct{})
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: enabled.Load, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(called)
		writeExplicitAttemptResponse(t, w, r, http.StatusNoContent, nil, nil)
	})})

	_, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(79)
	writeTargetOpen(t, peer, limits, id, validTargetBoundOpen(limits, "/v1/responses"))
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	enabled.Store(false)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("admitted target did not drain after gate closed")
	}
}

func TestSessionDuplicateOpenDoesNotReplaceExistingLeg(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		limits := testLimits(1)
		session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay})
		id := testStreamID(80)
		stream, err := session.openStream(t.Context(), id, validBoundRelayRequest("/v1/responses"))
		require.NoError(t, err)
		require.Equal(t, wire.FrameOpen, readPeerFrame(t, peer, limits).Type)
		writeTargetOpen(t, peer, limits, id, validTargetBoundOpen(limits, "/v1/responses"))
		commitDone := make(chan error, 1)
		go func() { commitDone <- stream.Commit(t.Context()) }()
		writePeerFrame(t, peer, limits, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1,
			Payload: mustMetadata(t, wire.Ready{RequestWindow: limits.InitialStreamWindow}, limits),
		})
		require.Equal(t, wire.FrameCommit, readPeerFrame(t, peer, limits).Type)
		writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2})
		require.NoError(t, <-commitDone)
	})

	t.Run("target", func(t *testing.T) {
		limits := testLimits(1)
		var calls atomic.Int32
		handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			writeExplicitAttemptResponse(t, w, r, http.StatusNoContent, nil, nil)
		})})

		_, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
		id := testStreamID(81)
		open := validTargetBoundOpen(limits, "/v1/responses")
		writeTargetOpen(t, peer, limits, id, open)
		require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
		writeTargetOpen(t, peer, limits, id, open)
		writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id, Sequence: 2})
		require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
		require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	})
}

func validBoundRelayRequest(requestPath string) agentproxy.AttemptStreamRequest {
	meta := validTunnelAttemptMeta()
	meta.RequestPath = requestPath
	return agentproxy.AttemptStreamRequest{
		TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
		Hop: 1, Attempt: meta,
	}
}

func writeExplicitAttemptResponse(t *testing.T, writer http.ResponseWriter, request *http.Request, status int, body []byte, late http.Header) {
	t.Helper()
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
	}
	header := writer.Header()
	header.Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	writer.WriteHeader(status)
	if len(body) > 0 {
		_, err := writer.Write(body)
		require.NoError(t, err)
	}
	for key, values := range late {
		header[key] = append([]string(nil), values...)
	}
	resultWriter, ok := attemptwire.AttemptResultWriterFromContext(request.Context())
	if ok {
		_ = resultWriter.WriteAttemptResult(result)
	}
}

func validTargetBoundOpen(limits wire.Limits, requestPath string) wire.Open {
	open := validBoundTunnelOpen(requestPath)
	open.ResponseWindow = limits.InitialStreamWindow
	return open
}

func writeTargetOpen(t *testing.T, peer *websocket.Conn, limits wire.Limits, id wire.StreamID, open wire.Open) {
	t.Helper()
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1,
		Payload: mustMetadata(t, open, limits),
	})
}

func requireSessionRunning(t *testing.T, session *Session) {
	t.Helper()
	select {
	case <-session.Done():
		t.Fatal("target stream failure closed the session")
	default:
	}
}

func websocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	peer := <-accepted
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
		server.Close()
	})
	return client, peer
}

func startTestSession(t *testing.T, limits wire.Limits, opts SessionOptions) (*Session, *websocket.Conn) {
	t.Helper()
	conn, peer := websocketPair(t)
	session := NewSession(conn, 11, limits, opts)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = session.Run(t.Context())
	}()
	t.Cleanup(func() {
		session.Cancel(context.Canceled)
		select {
		case <-runDone:
		case <-time.After(time.Second):
			t.Error("session Run did not exit")
		}
	})
	return session, peer
}

func testLimits(streams int) wire.Limits {
	return wire.Limits{
		MaxMetadataBytes: 4096, MaxDataBytes: 3, InitialStreamWindow: 3,
		MaxQueuedSessionBytes: 4096, MaxConcurrentStreams: streams,
	}
}

func TestV2SessionReadLimitAllowsAttemptResult(t *testing.T) {
	limits := testLimits(1)
	limits.MaxMetadataBytes = 128
	limits.MaxDataBytes = 256
	require.Equal(t, int64(wire.HeaderSize)+limits.MaxDataBytes, sessionMessageReadLimit(limits))

	limits.MaxDataBytes = 64
	require.Equal(t, int64(wire.HeaderSize)+limits.MaxMetadataBytes, sessionMessageReadLimit(limits))
}

func testStreamID(value byte) wire.StreamID {
	var id wire.StreamID
	id[0] = value
	return id
}

func readPeerFrame(t *testing.T, conn *websocket.Conn, limits wire.Limits) wire.Frame {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	frame, err := wire.Decode(message, limits)
	require.NoError(t, err)
	return frame
}

func writePeerFrame(t *testing.T, conn *websocket.Conn, limits wire.Limits, frame wire.Frame) {
	t.Helper()
	peerSequenceMu.Lock()
	key := peerSequenceKey{conn: conn, id: frame.StreamID}
	if frame.Sequence == 0 {
		frame.Sequence = peerSequences[key] + 1
	}
	frames := []wire.Frame{frame}
	if frame.Type == wire.FrameEnd {
		payload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
		require.NoError(t, err)
		frames = []wire.Frame{
			{Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: frame.StreamID, Sequence: frame.Sequence, Payload: payload},
			frame,
		}
		frames[1].Sequence++
	}
	peerSequences[key] = frames[len(frames)-1].Sequence
	peerSequenceMu.Unlock()
	for _, outgoing := range frames {
		message, err := wire.Encode(outgoing, limits)
		require.NoError(t, err)
		require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, message))
	}
}

func copyAttemptResponse(stream *Stream, ctx context.Context, dst http.ResponseWriter) error {
	_, err := stream.CopyAttemptResponse(ctx, dst)
	return err
}

type peerSequenceKey struct {
	conn *websocket.Conn
	id   wire.StreamID
}

var (
	peerSequenceMu sync.Mutex
	peerSequences  = make(map[peerSequenceKey]uint32)
)

func testFrame(stream byte, payload byte) wire.Frame {
	return wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: testStreamID(stream), Payload: []byte{payload}}
}

func testFrameOfType(stream byte, frameType wire.Type) wire.Frame {
	return wire.Frame{Version: wire.ProtocolVersion, Type: frameType, StreamID: testStreamID(stream)}
}
