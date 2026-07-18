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
	session := newSessionValue(nil, 1, testLimits(1), SessionOptions{})
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
	old := newSessionValue(nil, 1, testLimits(1), SessionOptions{})
	current := newSessionValue(nil, 2, testLimits(1), SessionOptions{})
	require.True(t, old.recordError(diagnostics.Event{Code: "stale", Stage: "read"}))
	require.True(t, current.recordError(diagnostics.Event{Code: "current", Stage: "read"}))
	require.Equal(t, "stale", old.RecentErrors()[0].Code)
	require.Equal(t, "current", current.RecentErrors()[0].Code)
}

func TestAgentRelayTerminalSessionRecordsInitializationError(t *testing.T) {
	session := newTerminalSession(nil, 1, testLimits(1), SessionOptions{}, errors.New("Authorization Bearer secret"))
	errors := session.RecentErrors()
	require.Len(t, errors, 1)
	require.Equal(t, wire.ErrorCodeRelayProtocol, errors[0].Code)
	require.Equal(t, "redacted", errors[0].Message)
}

func TestSessionOpenStreamClonesMetadataAndEnforcesStreamCap(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{})
	header := http.Header{"X-Test": {"before"}}
	request := validBoundRelayRequest("/v1/chat/completions")
	request.RouteID = 7
	request.RequestID = "request"
	request.Header = header
	request.BodyLength = 12
	stream, err := session.OpenStream(t.Context(), request)
	require.NoError(t, err)
	header.Set("X-Test", "after")
	frame := readPeerFrame(t, peer, testLimits(1))
	require.Equal(t, wire.FrameOpen, frame.Type)
	var open wire.Open
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &open, testLimits(1).MaxMetadataBytes))
	require.Equal(t, "before", http.Header(open.Header).Get("X-Test"))
	require.Empty(t, open.Purpose)
	require.Empty(t, open.SourceAgentID)
	require.Equal(t, testLimits(1).InitialStreamWindow, open.ResponseWindow)

	_, err = session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.Error(t, err)
	stream.Cancel(context.Canceled)
	require.NoError(t, stream.Close())
}

func TestSessionBoundAttemptOpenUsesDedicatedEndpointAndDefensiveCopy(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	meta := validTunnelAttemptMeta()
	want := meta
	stream, err := session.OpenStream(t.Context(), agentproxy.RelayRequest{
		TargetAgentID: "target-a", RouteID: 0, Method: http.MethodPost, Hop: 1,
		Path: attemptwire.EndpointPath, Attempt: &meta,
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
		req  agentproxy.RelayRequest
	}{
		{
			name: "bound business open",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: &meta,
			},
		},
		{
			name: "strict connectivity probe",
			req: agentproxy.RelayRequest{
				Purpose: wire.StreamPurposeConnectivityProbe, TargetAgentID: "target-a",
				Method: http.MethodGet, Path: "/ping", Header: make(http.Header),
				Remaining: time.Second,
			},
		},
	}
	invalidRequests := []struct {
		name string
		req  agentproxy.RelayRequest
	}{
		{
			name: "business open without attempt",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: "/v1/responses", Hop: 1,
			},
		},
		{
			name: "probe shape without purpose",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodGet, Path: "/ping", Remaining: time.Second,
			},
		},
		{
			name: "invalid attempt metadata",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: &invalidMeta,
			},
		},
		{
			name: "invalid provider request path",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: &invalidProviderPath,
			},
		},
		{
			name: "business open with probe purpose",
			req: agentproxy.RelayRequest{
				Purpose: wire.StreamPurposeConnectivityProbe, TargetAgentID: "target-a",
				Method: http.MethodPost, Path: attemptwire.EndpointPath, Hop: 1, Attempt: &meta,
			},
		},
		{
			name: "business open with wrong hop",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
				Hop: 2, Attempt: &meta,
			},
		},
		{
			name: "bound attempt with wrong method",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodGet, Path: attemptwire.EndpointPath,
				Hop: 1, Attempt: &meta,
			},
		},
		{
			name: "bound attempt with provider wire path",
			req: agentproxy.RelayRequest{
				TargetAgentID: "target-a", Method: http.MethodPost, Path: meta.RequestPath,
				Hop: 1, Attempt: &meta,
			},
		},
	}

	for _, test := range invalidRequests {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(1)
			conn := newMemorySessionConn()
			session := newSession(conn, 1, limits, SessionOptions{PingInterval: time.Hour, PongTimeout: time.Hour})
			runDone := make(chan error, 1)
			go func() { runDone <- session.Run(t.Context()) }()
			<-session.started
			t.Cleanup(func() {
				session.Cancel(context.Canceled)
				<-runDone
			})

			_, err := session.OpenStream(t.Context(), test.req)
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
			session := newSession(conn, 1, limits, SessionOptions{PingInterval: time.Hour, PongTimeout: time.Hour})
			runDone := make(chan error, 1)
			go func() { runDone <- session.Run(t.Context()) }()
			<-session.started
			t.Cleanup(func() {
				session.Cancel(context.Canceled)
				<-runDone
			})

			stream, err := session.OpenStream(t.Context(), test.req)
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

func TestSessionTargetBoundAttemptPreservesTrailerAndFlowControl(t *testing.T) {
	limits := testLimits(1)
	wantMeta := validTunnelAttemptMeta()
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		w.WriteHeader(http.StatusNoContent)
		w.Header().Set("X-Usage", "tokens=3")
	}))
	_, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	session, _ := startTestSession(t, testLimits(2), SessionOptions{})
	id := testStreamID(8)
	_, err := session.openStream(t.Context(), id, validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	_, err = session.openStream(t.Context(), id, validBoundRelayRequest("/v1/responses"))
	require.Error(t, err)
}

func TestSessionRejectsTextMessages(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{})
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
	session := newSession(conn, 1, limits, SessionOptions{})
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
	session, peer := startTestSession(t, testLimits(1), SessionOptions{})
	readLimit := sessionMessageReadLimit(session.limits)
	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, make([]byte, readLimit+1)))
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("oversized websocket DATA did not terminate Session")
	}
}

func TestSessionUnknownDataClosesOnlyAtThreshold(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{})
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
	session, _ := startTestSession(t, testLimits(2), SessionOptions{})
	stream, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
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
	session, peer := startTestSession(t, limits, SessionOptions{})
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
	opts := defaultSessionOptions(SessionOptions{})
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
		opts:    defaultSessionOptions(SessionOptions{OpenCommitTimeout: 20 * time.Millisecond}),
		started: started, done: make(chan struct{}), ctx: ctx, writer: w,
		streams:    make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	openDone := make(chan error, 1)
	go func() {
		_, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), body)
		meta, ok := agentproxy.IngressMetaFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, "source-a", meta.SourceAgentID)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, err = w.Write([]byte{0, 1, 0xff})
		require.NoError(t, err)
	}))
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	require.Equal(t, wire.FrameEnd, readPeerFrame(t, peer, limits).Type)
	require.EqualValues(t, 1, calls.Load())

	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Never(t, func() bool { return calls.Load() > 1 }, 30*time.Millisecond, 5*time.Millisecond)
	requireSessionRunning(t, session)
}

func TestSessionTargetSwitchingProtocolsReturnsResetWithoutHeaders(t *testing.T) {
	limits := testLimits(1)
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.router = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		canceled <- context.Cause(r.Context())
	})
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		remaining <- time.Until(deadline)
		w.WriteHeader(http.StatusNoContent)
	}))
	_, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
	}))
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(targetStarted)
		<-r.Context().Done()
		close(targetCanceled)
	}))
	session, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
	source, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	require.Equal(t, wire.FrameOpen, readPeerFrame(t, peer, limits).Type)
	require.True(t, source.responseOwner.Claim())
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
	handler := NewTargetHandler("target-a", enabled.Load, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(called)
		w.WriteHeader(http.StatusNoContent)
	}))
	_, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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
		session, peer := startTestSession(t, limits, SessionOptions{})
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
		handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		_, peer := startTestSession(t, limits, SessionOptions{TargetHandler: handler})
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

func validBoundRelayRequest(requestPath string) agentproxy.RelayRequest {
	meta := validTunnelAttemptMeta()
	meta.RequestPath = requestPath
	return agentproxy.RelayRequest{
		TargetAgentID: "target-a", Method: http.MethodPost, Path: attemptwire.EndpointPath,
		Hop: 1, Attempt: &meta,
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
	peerSequences[key] = frame.Sequence
	peerSequenceMu.Unlock()
	message, err := wire.Encode(frame, limits)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, message))
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
