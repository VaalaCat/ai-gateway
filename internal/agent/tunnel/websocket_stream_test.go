package tunnel

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestWebSocketTunnelSourceOpenWaitsForTargetProviderAcceptance(t *testing.T) {
	limits := apiTestLimits(8)
	id := wire.StreamID{52}
	var source *WebSocketStream
	var target *WebSocketTargetStream
	source = newWebSocketStream(id, limits, func(ctx context.Context, frame wire.Frame) error {
		return target.acceptFrame(ctx, frame)
	})
	target = newWebSocketTargetStream(id, limits, func(ctx context.Context, frame wire.Frame) error {
		return source.acceptFrame(ctx, frame)
	})
	committed := make(chan struct{}, 1)
	target.onActive = func() { committed <- struct{}{} }
	type openResult struct {
		accepted app.WebSocketAccepted
		err      error
	}
	opened := make(chan openResult, 1)
	go func() {
		accepted, err := source.Open(t.Context(), validWebSocketOpen())
		opened <- openResult{accepted: accepted, err: err}
	}()

	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("target handler did not start after Commit")
	}
	select {
	case result := <-opened:
		t.Fatalf("Source Open returned before provider acceptance: %+v", result)
	default:
	}
	require.NoError(t, target.Accept(t.Context(), app.WebSocketAccepted{
		Subprotocol: "chat.v1", ProviderStatus: http.StatusSwitchingProtocols,
	}))
	result := <-opened
	require.NoError(t, result.err)
	require.Equal(t, "chat.v1", result.accepted.Subprotocol)
	require.Equal(t, http.StatusSwitchingProtocols, result.accepted.ProviderStatus)
}

func TestWebSocketTunnelReturnsTerminalAPIExecutionResultAfterClose(t *testing.T) {
	source, target := newWebSocketTunnelPair(t, 8)
	want := apiattempt.APIExecutionResult{
		APIUpstreamID: 11, APIUpstreamName: "provider", UpstreamStatus: http.StatusSwitchingProtocols,
		ProviderDispatchKnown: true, ProviderDispatched: true, ErrorStage: "transport", ErrorCode: "closed",
		WebSocketCloseCode: websocket.CloseNormalClosure,
	}
	require.NoError(t, target.SendResult(t.Context(), want))
	got, err := source.ReceiveResult(t.Context())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestWebSocketTunnelPreservesTextBinaryAndEmptyMessages(t *testing.T) {
	source, target := newWebSocketTunnelPair(t, 4)

	for _, event := range []app.WebSocketEvent{
		{Kind: app.WebSocketMessageStartEvent, MessageID: 1, Type: app.WebSocketTextMessage},
		{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("txt")},
		{Kind: app.WebSocketMessageEndEvent, MessageID: 1},
		{Kind: app.WebSocketMessageStartEvent, MessageID: 2, Type: app.WebSocketBinaryMessage},
		{Kind: app.WebSocketMessageDataEvent, MessageID: 2, Data: []byte{0, 1, 2}},
		{Kind: app.WebSocketMessageEndEvent, MessageID: 2},
		{Kind: app.WebSocketMessageStartEvent, MessageID: 3, Type: app.WebSocketTextMessage},
		{Kind: app.WebSocketMessageEndEvent, MessageID: 3},
	} {
		require.NoError(t, source.SendEvent(t.Context(), event))
		got, err := target.ReceiveEvent(t.Context())
		require.NoError(t, err)
		require.Equal(t, event, got)
	}
}

func TestWebSocketTunnelFragmentsLargeMessageWithinFrameLimit(t *testing.T) {
	source, target := newWebSocketTunnelPair(t, 12)
	payload := []byte("0123456789")
	require.NoError(t, source.SendEvent(t.Context(), app.WebSocketEvent{Kind: app.WebSocketMessageStartEvent, MessageID: 1, Type: app.WebSocketBinaryMessage}))
	_, err := target.ReceiveEvent(t.Context())
	require.NoError(t, err)
	sent := make(chan error, 1)
	go func() {
		sent <- source.SendEvent(t.Context(), app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: payload})
	}()

	var reconstructed []byte
	for range 4 {
		event, receiveErr := target.ReceiveEvent(t.Context())
		require.NoError(t, receiveErr)
		require.Equal(t, app.WebSocketMessageDataEvent, event.Kind)
		require.LessOrEqual(t, len(event.Data), 3)
		reconstructed = append(reconstructed, event.Data...)
	}
	require.NoError(t, <-sent)
	require.Equal(t, sha256.Sum256(payload), sha256.Sum256(reconstructed))
}

func TestWebSocketTunnelForwardsPingPongAndCloseCodeReason(t *testing.T) {
	source, target := newWebSocketTunnelPair(t, 8)
	ping := app.WebSocketEvent{Kind: app.WebSocketPingEvent, Data: []byte("probe")}
	require.NoError(t, source.SendEvent(t.Context(), ping))
	got, err := target.ReceiveEvent(t.Context())
	require.NoError(t, err)
	require.Equal(t, ping, got)

	pong := app.WebSocketEvent{Kind: app.WebSocketPongEvent, Data: []byte("ack")}
	require.NoError(t, target.SendEvent(t.Context(), pong))
	got, err = source.ReceiveEvent(t.Context())
	require.NoError(t, err)
	require.Equal(t, pong, got)

	closeEvent := app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: 1000, Reason: "done"}
	require.NoError(t, target.SendEvent(t.Context(), closeEvent))
	got, err = source.ReceiveEvent(t.Context())
	require.NoError(t, err)
	require.Equal(t, closeEvent, got)
}

func TestWebSocketTunnelUsesIndependentDirectionWindows(t *testing.T) {
	source, target := newWebSocketTunnelPair(t, 4)
	require.NoError(t, target.SendEvent(t.Context(), app.WebSocketEvent{Kind: app.WebSocketMessageStartEvent, MessageID: 1, Type: app.WebSocketBinaryMessage}))
	_, err := source.ReceiveEvent(t.Context())
	require.NoError(t, err)

	blocked := make(chan error, 1)
	go func() {
		blocked <- target.SendEvent(context.Background(), app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("12345")})
	}()
	select {
	case err := <-blocked:
		t.Fatalf("downstream data unexpectedly bypassed its window: %v", err)
	default:
	}

	ping := app.WebSocketEvent{Kind: app.WebSocketPingEvent, Data: []byte("upstream-control")}
	require.NoError(t, source.SendEvent(t.Context(), ping))
	got, err := target.ReceiveEvent(t.Context())
	require.NoError(t, err)
	require.Equal(t, ping, got)
	// Draining the first downlink data frame acknowledges only target -> source.
	_, err = source.ReceiveEvent(t.Context())
	require.NoError(t, err)
	select {
	case err := <-blocked:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("downstream data did not resume after its own acknowledgement")
	}
}

func TestWebSocketTunnelProtocolCloseReclaimsSessionRegistries(t *testing.T) {
	limits := apiTestLimits(8)
	sourceConn, targetConn := websocketPair(t)
	source := NewSession(sourceConn, 31, limits, SessionOptions{Direction: SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour})
	target := NewSession(targetConn, 32, limits, SessionOptions{
		Direction: SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour,
		TargetHandler: NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target", DirectInboundEnabled: func() bool { return true }, RelayInboundEnabled: func() bool { return true }}),
		WebSocketTargetHandler: WebSocketTargetHandlerFunc(func(ctx context.Context, stream *WebSocketTargetStream) error {
			if err := stream.Accept(ctx, app.WebSocketAccepted{ProviderStatus: http.StatusSwitchingProtocols}); err != nil {
				return err
			}
			_, err := stream.ReceiveEvent(ctx)
			if err != nil {
				return err
			}
			return stream.SendResult(ctx, apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
		}),
	})
	go func() { _ = source.Run(t.Context()) }()
	go func() { _ = target.Run(t.Context()) }()
	t.Cleanup(func() { source.Cancel(context.Canceled); target.Cancel(context.Canceled) })

	stream, err := source.OpenWebSocketAPIStream(t.Context(), validWebSocketOpen())
	require.NoError(t, err)
	require.Error(t, stream.SendEvent(t.Context(), app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("orphan")}))
	require.Eventually(t, func() bool { return source.StreamCount() == 0 && target.StreamCount() == 0 }, time.Second, time.Millisecond)
}

func TestWebSocketTunnelSourceControlBypassesStalledSameDirectionData(t *testing.T) {
	for _, control := range []struct {
		name     string
		event    app.WebSocketEvent
		frame    wire.Type
		terminal bool
	}{
		{"Ping", app.WebSocketEvent{Kind: app.WebSocketPingEvent, Data: []byte("ping")}, wire.FrameWebSocketPing, false},
		{"Pong", app.WebSocketEvent{Kind: app.WebSocketPongEvent, Data: []byte("pong")}, wire.FrameWebSocketPong, false},
		{"Close", app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: 1000, Reason: "done"}, wire.FrameWebSocketClose, true},
	} {
		t.Run(control.name, func(t *testing.T) {
			frames := make(chan wire.Frame, 4)
			stream := newWebSocketStream(wire.StreamID{41}, apiTestLimits(4), func(_ context.Context, frame wire.Frame) error { frames <- frame; return nil })
			stream.stateMu.Lock()
			stream.phase, stream.outgoingMessage, stream.outgoingOpen = webSocketActive, 1, true
			stream.stateMu.Unlock()
			dataCtx, cancelData := context.WithCancel(context.Background())
			defer cancelData()
			dataDone := make(chan error, 1)
			go func() {
				dataDone <- stream.SendEvent(dataCtx, app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("data")})
			}()
			controlDone := make(chan error, 1)
			go func() { controlDone <- stream.SendEvent(t.Context(), control.event) }()
			select {
			case frame := <-frames:
				require.Equal(t, control.frame, frame.Type)
			case <-time.After(time.Second):
				cancelData()
				<-dataDone
				t.Fatalf("%s was blocked behind same-direction stalled Data", control.name)
			}
			require.NoError(t, <-controlDone)
			if control.terminal {
				require.Error(t, <-dataDone)
				return
			}
			require.NoError(t, stream.sendCredit.Add(4))
			require.NoError(t, <-dataDone)
		})
	}
}

func TestWebSocketTunnelSourceWaitsForCommittedBeforeOpenReturns(t *testing.T) {
	limits := apiTestLimits(8)
	id := wire.StreamID{42}
	var stream *WebSocketStream
	stream = newWebSocketStream(id, limits, func(ctx context.Context, frame wire.Frame) error {
		switch frame.Type {
		case wire.FrameOpen:
			payload, _ := wire.EncodeMetadata(wire.WebSocketAccepted{RequestWindow: limits.InitialStreamWindow}, limits.MaxMetadataBytes)
			return stream.acceptFrame(ctx, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1, Payload: payload})
		case wire.FrameCommit:
			return nil
		}
		return nil
	})
	opened := make(chan error, 1)
	go func() { _, err := stream.Open(t.Context(), validWebSocketOpen()); opened <- err }()
	select {
	case err := <-opened:
		t.Fatalf("Open returned before Committed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	acceptedPayload, err := wire.EncodeMetadata(wire.WebSocketAccepted{
		RequestWindow: limits.InitialStreamWindow, ProviderStatus: http.StatusSwitchingProtocols,
	}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, stream.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2, Payload: acceptedPayload,
	}))
	require.NoError(t, <-opened)
}

func TestWebSocketTunnelRejectsCommittedBeforeCommitPublishSucceeds(t *testing.T) {
	stream := newWebSocketStream(wire.StreamID{49}, apiTestLimits(8), func(context.Context, wire.Frame) error { return nil })
	stream.stateMu.Lock()
	stream.phase, stream.readyReceived, stream.receiveSeq = webSocketOpening, true, 1
	stream.stateMu.Unlock()

	require.Error(t, stream.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: stream.id, Sequence: 2}))
	require.True(t, stream.isTerminal(), "Committed before Commit publish must terminate the stream")
}

func TestWebSocketTunnelAcceptsCommittedDeliveredByAsyncWriterBeforeOpenContinues(t *testing.T) {
	limits := apiTestLimits(8)
	id := wire.StreamID{51}
	writerCtx, cancelWriter := context.WithCancel(t.Context())
	defer cancelWriter()
	commitWritten := make(chan struct{})
	releaseCommitReturn := make(chan struct{})
	var stream *WebSocketStream
	writer := newFairWriter(writerCtx, 1024, time.Second, func(frame wire.Frame) error {
		switch frame.Type {
		case wire.FrameOpen:
			payload, _ := wire.EncodeMetadata(wire.WebSocketAccepted{RequestWindow: limits.InitialStreamWindow}, limits.MaxMetadataBytes)
			return stream.acceptFrame(writerCtx, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1, Payload: payload})
		case wire.FrameCommit:
			close(commitWritten)
			payload, _ := wire.EncodeMetadata(wire.WebSocketAccepted{
				RequestWindow: limits.InitialStreamWindow, ProviderStatus: http.StatusSwitchingProtocols,
			}, limits.MaxMetadataBytes)
			return stream.acceptFrame(writerCtx, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2, Payload: payload,
			})
		}
		return nil
	})
	go writer.Run()
	stream = newWebSocketStream(id, limits, func(ctx context.Context, frame wire.Frame) error {
		if err := writer.Enqueue(ctx, frame, nil); err != nil {
			return err
		}
		if frame.Type == wire.FrameCommit {
			<-commitWritten
			<-releaseCommitReturn
		}
		return nil
	})

	opened := make(chan error, 1)
	go func() { _, err := stream.Open(t.Context(), validWebSocketOpen()); opened <- err }()
	<-commitWritten
	require.Eventually(t, func() bool {
		stream.stateMu.Lock()
		defer stream.stateMu.Unlock()
		return stream.receiveSeq == 2
	}, time.Second, time.Millisecond, "async peer must deliver Committed before Open continues")
	close(releaseCommitReturn)
	require.NoError(t, <-opened)
}

func TestWebSocketTunnelSourceCloseWinsAfterDataCreditBeforeWrite(t *testing.T) {
	frames := make(chan wire.Frame, 2)
	stream := newWebSocketStream(wire.StreamID{50}, apiTestLimits(8), func(_ context.Context, frame wire.Frame) error { frames <- frame; return nil })
	stream.stateMu.Lock()
	stream.phase, stream.outgoingMessage, stream.outgoingOpen = webSocketActive, 1, true
	stream.stateMu.Unlock()
	require.NoError(t, stream.sendCredit.Set(8))
	credited, release := make(chan struct{}), make(chan struct{})
	stream.beforeDataWrite = func() { close(credited); <-release }
	dataDone := make(chan error, 1)
	go func() {
		dataDone <- stream.SendEvent(context.Background(), app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("data")})
	}()
	<-credited
	require.NoError(t, stream.SendEvent(t.Context(), app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: 1000, Reason: "close"}))
	close(release)
	require.ErrorIs(t, <-dataDone, io.EOF)
	frame := <-frames
	require.Equal(t, wire.FrameWebSocketClose, frame.Type)
	select {
	case late := <-frames:
		t.Fatalf("frame %d was emitted after Close", late.Type)
	default:
	}
}

func TestWebSocketTunnelSourceRejectsHandshakeDuplicatesAndOrderViolations(t *testing.T) {
	newOpening := func() *WebSocketStream {
		stream := newWebSocketStream(wire.StreamID{47}, apiTestLimits(8), func(context.Context, wire.Frame) error { return nil })
		stream.stateMu.Lock()
		stream.phase = webSocketOpening
		stream.stateMu.Unlock()
		return stream
	}
	readyPayload, err := wire.EncodeMetadata(wire.WebSocketAccepted{RequestWindow: 8}, apiTestLimits(8).MaxMetadataBytes)
	require.NoError(t, err)
	t.Run("Committed before Ready", func(t *testing.T) {
		stream := newOpening()
		require.Error(t, stream.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: stream.id, Sequence: 1}))
		require.True(t, stream.isTerminal())
	})
	t.Run("duplicate Ready", func(t *testing.T) {
		stream := newOpening()
		require.NoError(t, stream.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: stream.id, Sequence: 1, Payload: readyPayload}))
		require.Error(t, stream.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: stream.id, Sequence: 2, Payload: readyPayload}))
		require.True(t, stream.isTerminal())
	})
}

func TestWebSocketTunnelTerminalTransitionsAreRaceSafe(t *testing.T) {
	for range 100 {
		stream := newWebSocketStream(wire.StreamID{43}, apiTestLimits(8), func(context.Context, wire.Frame) error { return nil })
		stream.stateMu.Lock()
		stream.phase = webSocketActive
		stream.stateMu.Unlock()
		closePayload, err := wire.EncodeWebSocketClose(1000, "peer")
		require.NoError(t, err)
		var group sync.WaitGroup
		group.Add(3)
		go func() {
			defer group.Done()
			_ = stream.SendEvent(context.Background(), app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: 1000, Reason: "local"})
		}()
		go func() {
			defer group.Done()
			_ = stream.acceptFrame(context.Background(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWebSocketClose, StreamID: stream.id, Sequence: 1, Payload: closePayload})
		}()
		go func() { defer group.Done(); stream.terminate(io.EOF, true) }()
		group.Wait()
		require.True(t, stream.isTerminal())
	}
}

func validWebSocketOpen() app.WebSocketOpen {
	return app.WebSocketOpen{TargetAgentID: "target", RequestID: "request", Path: "/ws", API: apiattempt.APIAttemptMeta{
		APIServiceID: 1, APIRouteID: 2, Protocol: apiattempt.APIProtocolWebSocket,
	}}
}

func newWebSocketTunnelPair(t *testing.T, window int64) (*WebSocketStream, *WebSocketTargetStream) {
	t.Helper()
	limits := apiTestLimits(window)
	id := wire.StreamID{9}
	pairCtx, cancelPair := context.WithCancel(t.Context())
	type delivery struct {
		frame wire.Frame
		done  chan error
	}
	sourceToTarget := make(chan delivery, 64)
	targetToSource := make(chan delivery, 64)
	var deliveryMu sync.Mutex
	var deliveryErr error
	recordDeliveryError := func(err error) {
		if err == nil {
			return
		}
		deliveryMu.Lock()
		if deliveryErr == nil {
			deliveryErr = err
			cancelPair()
		}
		deliveryMu.Unlock()
	}
	send := func(queue chan<- delivery) apiFrameSender {
		return func(ctx context.Context, frame wire.Frame) error {
			frame.Payload = append([]byte(nil), frame.Payload...)
			item := delivery{frame: frame}
			if !isAsyncWebSocketPairFrame(frame.Type) {
				item.done = make(chan error, 1)
			}
			select {
			case queue <- item:
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-pairCtx.Done():
				return context.Cause(pairCtx)
			}
			if item.done == nil {
				return nil
			}
			select {
			case err := <-item.done:
				return err
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-pairCtx.Done():
				return context.Cause(pairCtx)
			}
		}
	}
	var source *WebSocketStream
	target := newWebSocketTargetStream(id, limits, send(targetToSource))
	target.onActive = func() {
		_ = target.Accept(pairCtx, app.WebSocketAccepted{ProviderStatus: http.StatusSwitchingProtocols})
	}
	source = newWebSocketStream(id, limits, send(sourceToTarget))
	var deliveryWG sync.WaitGroup
	deliver := func(queue <-chan delivery, accept func(context.Context, wire.Frame) error) {
		deliveryWG.Add(1)
		go func() {
			defer deliveryWG.Done()
			for {
				select {
				case <-pairCtx.Done():
					return
				case item := <-queue:
					err := accept(pairCtx, item.frame)
					if item.done != nil {
						item.done <- err
					}
					if err != nil && pairCtx.Err() == nil {
						recordDeliveryError(err)
						return
					}
				}
			}
		}()
	}
	deliver(sourceToTarget, target.acceptFrame)
	deliver(targetToSource, source.acceptFrame)
	t.Cleanup(func() {
		cancelPair()
		deliveryWG.Wait()
		deliveryMu.Lock()
		defer deliveryMu.Unlock()
		require.NoError(t, deliveryErr)
	})
	_, err := source.Open(t.Context(), validWebSocketOpen())
	require.NoError(t, err)
	return source, target
}

func isAsyncWebSocketPairFrame(typ wire.Type) bool {
	switch typ {
	case wire.FrameOpen, wire.FrameReady, wire.FrameCommit, wire.FrameCommitted, wire.FrameWebSocketClose:
		return true
	default:
		return false
	}
}
