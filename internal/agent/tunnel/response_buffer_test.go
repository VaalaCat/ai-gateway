package tunnel

import (
	"context"
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestResponseBufferCoalescesBytesAndEnforcesBoundary(t *testing.T) {
	buffer := newResponseBuffer(1000)
	for range 1000 {
		require.NoError(t, buffer.Push([]byte("x")))
	}
	require.Equal(t, 1000, buffer.Len())
	require.Error(t, buffer.Push([]byte("y")))
	chunk, err := buffer.ReadChunk(t.Context(), 1000)
	require.NoError(t, err)
	require.Len(t, chunk, 1000)
	require.Zero(t, buffer.Len())
}

func TestSessionIncomingBudgetAcrossStreamsAndCancelRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	limits := testLimits(3)
	limits.InitialStreamWindow = 4
	limits.MaxQueuedSessionBytes = 4
	w := newFairWriter(ctx, 4, time.Second, func(wire.Frame) error { return nil })
	session := &Session{generation: 1, limits: limits, ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	first := respondingBudgetStream(session, ctx, testStreamID(1))
	second := respondingBudgetStream(session, ctx, testStreamID(2))
	require.False(t, first.handleFrame(wire.Frame{Type: wire.FrameResponseData, Sequence: 4, StreamID: first.id, Payload: []byte("abc")}))
	require.False(t, second.handleFrame(wire.Frame{Type: wire.FrameResponseData, Sequence: 4, StreamID: second.id, Payload: []byte("d")}))
	require.EqualValues(t, 4, session.incomingSize())
	offending := respondingBudgetStream(session, ctx, testStreamID(3))
	require.True(t, offending.handleFrame(wire.Frame{Type: wire.FrameResponseData, Sequence: 4, StreamID: offending.id, Payload: []byte("e")}))
	require.EqualValues(t, 4, session.incomingSize())
	go first.run()
	first.Cancel(context.Canceled)
	<-first.Done()
	require.EqualValues(t, 1, session.incomingSize())
	require.NoError(t, session.reserveIncoming(3))
	require.NoError(t, session.releaseIncoming(3))
	go second.run()
	second.Cancel(context.Canceled)
	<-second.Done()
	require.Zero(t, session.incomingSize())
}

func respondingBudgetStream(session *Session, ctx context.Context, id wire.StreamID) *Stream {
	stream := newStream(session, ctx, ctx, id, 0)
	stream.receivePhase = receiveResponding
	stream.receiveSeq = 3
	stream.commitState.Store(uint32(wire.Committed))
	return stream
}

func TestResponseBufferCloseDiscardAndCancel(t *testing.T) {
	buffer := newResponseBuffer(4)
	require.NoError(t, buffer.Push([]byte("abc")))
	require.Equal(t, int64(3), buffer.Discard())
	require.Zero(t, buffer.Discard())
	buffer.Close()
	_, err := buffer.ReadChunk(t.Context(), 4)
	require.ErrorIs(t, err, errResponseBufferClosed)

	waiting := newResponseBuffer(4)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = waiting.ReadChunk(ctx, 4)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSessionIncomingBudgetCheckedReserveRelease(t *testing.T) {
	session := &Session{limits: testLimits(1)}
	session.limits.MaxQueuedSessionBytes = 4
	require.NoError(t, session.reserveIncoming(4))
	require.Error(t, session.reserveIncoming(1))
	require.NoError(t, session.releaseIncoming(3))
	require.NoError(t, session.reserveIncoming(3))
	require.NoError(t, session.releaseIncoming(4))
	require.Error(t, session.releaseIncoming(1))
	require.Zero(t, session.incomingSize())
}
