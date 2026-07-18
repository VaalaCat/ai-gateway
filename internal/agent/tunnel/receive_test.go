package tunnel

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestReceiveRejectsEarlyCommittedBeforeSideEffects(t *testing.T) {
	stream := newReceiveTestStream(t)
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameCommitted, Sequence: 1, StreamID: stream.id}))
	require.Equal(t, wire.PreCommit, stream.CommitState())
}

func TestReceiveRejectsDuplicateReadyWithoutResettingCredit(t *testing.T) {
	stream := newReceiveTestStream(t)
	ready := mustMetadata(t, wire.Ready{RequestWindow: 0}, stream.session.limits)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameReady, Sequence: 1, StreamID: stream.id, Payload: ready}))
	require.Zero(t, stream.requestWindow.Available())
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameReady, Sequence: 2, StreamID: stream.id,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: 3}, stream.session.limits)}))
	require.Zero(t, stream.requestWindow.Available())
}

func TestReceiveRejectsSkippedSequenceAndDataBeforeHeaders(t *testing.T) {
	stream := newReceiveTestStream(t)
	ready := mustMetadata(t, wire.Ready{RequestWindow: 3}, stream.session.limits)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameReady, Sequence: 1, StreamID: stream.id, Payload: ready}))
	stream.commitState.Store(uint32(wire.CommitUncertain))
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameCommitted, Sequence: 2, StreamID: stream.id}))
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameResponseData, Sequence: 4, StreamID: stream.id, Payload: []byte("x")}))
	require.Zero(t, stream.responseData.Len())
	beforeHeaders := committedReceiveTestStream(t)
	require.True(t, beforeHeaders.handleFrame(wire.Frame{Type: wire.FrameResponseData, Sequence: 3, StreamID: beforeHeaders.id, Payload: []byte("x")}))
}

func TestReceiveRejectsDuplicateRegressOverflowAndDuplicateHeaders(t *testing.T) {
	for _, sequence := range []uint32{1, math.MaxUint32} {
		stream := newReceiveTestStream(t)
		stream.receiveSeq = sequence
		frameSequence := sequence
		if sequence == math.MaxUint32 {
			frameSequence = math.MaxUint32
		}
		require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameCancel, Sequence: frameSequence, StreamID: stream.id}))
	}
	stream := committedReceiveTestStream(t)
	headers := mustMetadata(t, wire.Headers{StatusCode: 200}, stream.session.limits)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameHeaders, Sequence: 3, StreamID: stream.id, Payload: headers}))
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameHeaders, Sequence: 4, StreamID: stream.id, Payload: headers}))
}

func TestReceiveCanonicalizesAndStripsResponseHeadersBeforeSignaling(t *testing.T) {
	stream := committedReceiveTestStream(t)
	payload := mustMetadata(t, wire.Headers{StatusCode: http.StatusCreated, Header: map[string][]string{
		"connection":     {"x-hop"},
		"x-hop":          {"remove"},
		"content-length": {"99"},
		"X-Business":     {"canonical"},
		"X-bUsInEsS":     {"mixed"},
		"x-business":     {"lowercase"},
	}}, stream.session.limits)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameHeaders, Sequence: 3, StreamID: stream.id, Payload: payload}))
	header := http.Header(stream.responseHeaders.Header)
	require.Equal(t, []string{"canonical", "mixed", "lowercase"}, header.Values("X-Business"))
	for _, key := range []string{"Connection", "X-Hop", "Content-Length"} {
		require.Empty(t, header.Values(key), key)
	}
}

func TestReceiveWindowReplayResetsOnlyBadStreamAndHealthyStreamContinues(t *testing.T) {
	bad := committedReceiveTestStream(t)
	before := bad.requestWindow.Available()
	update := mustMetadata(t, wire.WindowUpdate{Bytes: bad.session.limits.InitialStreamWindow}, bad.session.limits)
	require.True(t, bad.handleFrame(wire.Frame{Type: wire.FrameWindowUpdate, Sequence: 3, StreamID: bad.id, Payload: update}))
	require.Equal(t, before, bad.requestWindow.Available())
	require.NoError(t, bad.session.ctx.Err())
	healthy := newStream(bad.session, bad.session.ctx, t.Context(), testStreamID(86), 0)
	ready := mustMetadata(t, wire.Ready{RequestWindow: 0}, bad.session.limits)
	require.False(t, healthy.handleFrame(wire.Frame{Type: wire.FrameReady, Sequence: 1, StreamID: healthy.id, Payload: ready}))
	require.Equal(t, receiveReady, healthy.receivePhase)
}

func committedReceiveTestStream(t *testing.T) *Stream {
	stream := newReceiveTestStream(t)
	ready := mustMetadata(t, wire.Ready{RequestWindow: stream.session.limits.InitialStreamWindow}, stream.session.limits)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameReady, Sequence: 1, StreamID: stream.id, Payload: ready}))
	stream.commitState.Store(uint32(wire.CommitUncertain))
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameCommitted, Sequence: 2, StreamID: stream.id}))
	return stream
}

func newReceiveTestStream(t *testing.T) *Stream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
	session := &Session{generation: 1, limits: testLimits(2), ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	return newStream(session, ctx, t.Context(), testStreamID(85), 0)
}
