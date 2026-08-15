package tunnel

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestWebSocketTunnelRejectsInvalidContinuationAndOversizedControl(t *testing.T) {
	t.Run("continuation without start closes and reclaims", func(t *testing.T) {
		var sent []wire.Frame
		target := newWebSocketTargetStream(wire.StreamID{1}, apiTestLimits(8), func(_ context.Context, frame wire.Frame) error {
			sent = append(sent, frame)
			return nil
		})
		target.markAcceptedForTest()
		err := target.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWebSocketMessageData, StreamID: wire.StreamID{1}, Sequence: 1, Payload: []byte("orphan")})
		require.Error(t, err)
		require.True(t, target.isTerminal())
		require.Len(t, sent, 1)
		require.Equal(t, wire.FrameWebSocketClose, sent[0].Type)
		closeEvent, err := wire.DecodeWebSocketClose(sent[0].Payload)
		require.NoError(t, err)
		require.Equal(t, app.WebSocketCloseProtocolError, closeEvent.Code)
	})

	t.Run("oversized control closes and reclaims", func(t *testing.T) {
		var sent []wire.Frame
		target := newWebSocketTargetStream(wire.StreamID{2}, apiTestLimits(8), func(_ context.Context, frame wire.Frame) error {
			sent = append(sent, frame)
			return nil
		})
		target.markAcceptedForTest()
		err := target.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWebSocketPing, StreamID: wire.StreamID{2}, Sequence: 1, Payload: make([]byte, wire.MaxWebSocketControlPayloadBytes+1)})
		require.Error(t, err)
		require.True(t, target.isTerminal())
		require.Len(t, sent, 1)
		require.Equal(t, wire.FrameWebSocketClose, sent[0].Type)
	})
}

func TestWebSocketTunnelTargetControlBypassesStalledSameDirectionData(t *testing.T) {
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
			target := newWebSocketTargetStream(wire.StreamID{44}, apiTestLimits(4), func(_ context.Context, frame wire.Frame) error { frames <- frame; return nil })
			target.markAcceptedForTest()
			require.NoError(t, target.sendCredit.Set(0))
			target.outgoingMessage, target.outgoingOpen = 1, true
			dataCtx, cancelData := context.WithCancel(context.Background())
			defer cancelData()
			dataDone := make(chan error, 1)
			go func() {
				dataDone <- target.SendEvent(dataCtx, app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("data")})
			}()
			controlDone := make(chan error, 1)
			go func() { controlDone <- target.SendEvent(t.Context(), control.event) }()
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
			require.NoError(t, target.sendCredit.Add(4))
			require.NoError(t, <-dataDone)
		})
	}
}

func TestWebSocketTunnelTargetRejectsFramesBeforeCommit(t *testing.T) {
	target := newWebSocketTargetStream(wire.StreamID{45}, apiTestLimits(8), func(context.Context, wire.Frame) error { return nil })
	target.stateMu.Lock()
	target.phase = webSocketOpening
	target.stateMu.Unlock()
	err := target.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWebSocketPing, StreamID: target.id, Sequence: 1, Payload: []byte("early")})
	require.Error(t, err)
	require.True(t, target.isTerminal())
}

func TestWebSocketTunnelTargetTerminalTransitionsAreRaceSafe(t *testing.T) {
	for range 100 {
		target := newWebSocketTargetStream(wire.StreamID{46}, apiTestLimits(8), func(context.Context, wire.Frame) error { return nil })
		target.markAcceptedForTest()
		closePayload, err := wire.EncodeWebSocketClose(1000, "peer")
		require.NoError(t, err)
		var group sync.WaitGroup
		group.Add(3)
		go func() {
			defer group.Done()
			_ = target.SendEvent(context.Background(), app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: 1000, Reason: "local"})
		}()
		go func() {
			defer group.Done()
			_ = target.acceptFrame(context.Background(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWebSocketClose, StreamID: target.id, Sequence: 1, Payload: closePayload})
		}()
		go func() { defer group.Done(); target.terminate(io.EOF, true) }()
		group.Wait()
		require.True(t, target.isTerminal())
	}
}

func TestWebSocketTunnelTargetStartsHandlerOnlyAfterCommit(t *testing.T) {
	limits := apiTestLimits(8)
	target := newWebSocketTargetStream(wire.StreamID{48}, limits, func(context.Context, wire.Frame) error { return nil })
	started := make(chan struct{}, 1)
	target.onActive = func() { started <- struct{}{} }
	openPayload, err := wire.EncodeMetadata(wire.Open{
		Method: http.MethodGet, Path: "/ws", RequestID: "request", TargetAgentID: "target", ResponseWindow: 8,
		API: &apiattempt.APIAttemptMeta{Protocol: apiattempt.APIProtocolWebSocket}, WebSocket: &wire.WebSocketOpen{ResponseWindow: 8},
	}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, target.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: target.id, Sequence: 1, Payload: openPayload}))
	select {
	case <-started:
		t.Fatal("handler started before Commit")
	default:
	}
	require.NoError(t, target.acceptFrame(t.Context(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: target.id, Sequence: 2}))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start after Commit")
	}
}

func TestWebSocketTunnelTargetCloseWinsAfterDataCreditBeforeWrite(t *testing.T) {
	frames := make(chan wire.Frame, 2)
	target := newWebSocketTargetStream(wire.StreamID{51}, apiTestLimits(8), func(_ context.Context, frame wire.Frame) error { frames <- frame; return nil })
	target.markAcceptedForTest()
	target.outgoingMessage, target.outgoingOpen = 1, true
	credited, release := make(chan struct{}), make(chan struct{})
	target.beforeDataWrite = func() { close(credited); <-release }
	dataDone := make(chan error, 1)
	go func() {
		dataDone <- target.SendEvent(context.Background(), app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("data")})
	}()
	<-credited
	require.NoError(t, target.SendEvent(t.Context(), app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: 1000, Reason: "close"}))
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
