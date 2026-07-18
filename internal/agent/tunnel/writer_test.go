package tunnel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestWriterRoundRobinPreservesPerStreamFIFO(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var got []byte
	w := newFairWriter(ctx, 1<<20, time.Second, func(frame wire.Frame) error {
		once.Do(func() { close(firstStarted); <-release })
		mu.Lock()
		got = append(got, frame.Payload[0])
		mu.Unlock()
		return nil
	})
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })

	require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'a'), nil))
	<-firstStarted
	require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'b'), nil))
	require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'c'), nil))
	require.NoError(t, w.Enqueue(t.Context(), testFrame(2, 'x'), nil))
	require.NoError(t, w.Enqueue(t.Context(), testFrame(2, 'y'), nil))
	close(release)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 5
	}, time.Second, time.Millisecond)
	mu.Lock()
	require.Equal(t, []byte{'a', 'x', 'b', 'y', 'c'}, got)
	mu.Unlock()
	queuedBytes, queuedStreams := w.stats()
	require.Zero(t, queuedBytes)
	require.Zero(t, queuedStreams)
}

func TestWriterQueueByteCapWaitsAndCanBeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	w := newFairWriter(ctx, wire.HeaderSize+1, time.Second, func(wire.Frame) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil
	})
	go w.Run()
	t.Cleanup(func() { close(release); cancel(); <-w.Done() })
	require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'a'), nil))
	<-started

	waitCtx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	err := w.Enqueue(waitCtx, testFrame(2, 'x'), nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWriterCommitAcceptLinearizesBeforeSocketFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	socketErr := errors.New("socket failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	w := newFairWriter(ctx, 1<<20, time.Second, func(wire.Frame) error {
		close(entered)
		<-release
		return socketErr
	})
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })
	var state atomic.Uint32
	state.Store(uint32(wire.PreCommit))
	require.NoError(t, w.Enqueue(t.Context(), testFrameOfType(1, wire.FrameCommit), func() {
		state.Store(uint32(wire.CommitUncertain))
	}))
	require.Equal(t, wire.CommitUncertain, wire.CommitState(state.Load()))
	<-entered
	close(release)
	require.ErrorIs(t, <-w.Err(), socketErr)
	require.Equal(t, wire.CommitUncertain, wire.CommitState(state.Load()))
	queuedBytes, queuedStreams := w.stats()
	require.Zero(t, queuedBytes)
	require.Zero(t, queuedStreams)
}

func TestWriterPingIsNotStarvedByBusyQueues(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	var writes atomic.Int32
	pingAt := make(chan int32, 1)
	w := newFairWriter(ctx, 1<<20, time.Second, func(wire.Frame) error {
		first.Do(func() { close(firstStarted); <-release })
		writes.Add(1)
		time.Sleep(2 * time.Millisecond)
		return nil
	})
	w.pingInterval = time.Millisecond
	w.ping = func() error {
		select {
		case pingAt <- writes.Load():
		default:
		}
		return nil
	}
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })
	require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'a'), nil))
	<-firstStarted
	for i := byte(0); i < 10; i++ {
		require.NoError(t, w.Enqueue(t.Context(), testFrame(1+i%2, 'b'+i), nil))
	}
	close(release)
	select {
	case count := <-pingAt:
		require.Less(t, count, int32(11))
	case <-time.After(time.Second):
		t.Fatal("writer never sent ping")
	}
}

func TestWriterReplaceAtomicallyDiscardsOnlyTargetNormalQueue(t *testing.T) {
	for _, terminalType := range []wire.Type{wire.FrameCancel, wire.FrameReset} {
		t.Run(fmt.Sprintf("terminal_%d", terminalType), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var mu sync.Mutex
			var written []wire.Frame
			w := newFairWriter(ctx, 2*(wire.HeaderSize+1), time.Second, func(frame wire.Frame) error {
				mu.Lock()
				written = append(written, frame)
				mu.Unlock()
				return nil
			})
			target := testStreamID(1)
			other := testStreamID(2)
			require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'a'), nil))
			require.NoError(t, w.Enqueue(t.Context(), testFrame(2, 'x'), nil))
			terminal := wire.Frame{
				Version: wire.ProtocolVersion, Type: terminalType, StreamID: target,
				Payload: make([]byte, 12),
			}
			replaceDone := make(chan error, 1)
			go func() { replaceDone <- w.Replace(t.Context(), terminal, nil) }()
			require.Eventually(t, func() bool {
				w.mu.Lock()
				defer w.mu.Unlock()
				return w.replacing[target]
			}, time.Second, time.Millisecond)
			require.ErrorIs(t, w.Enqueue(t.Context(), testFrame(1, 'b'), nil), errStreamQueueClosing)
			go w.Run()
			require.NoError(t, <-replaceDone)
			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(written) == 2
			}, time.Second, time.Millisecond)
			cancel()
			<-w.Done()
			mu.Lock()
			require.Equal(t, other, written[0].StreamID)
			require.Equal(t, byte('x'), written[0].Payload[0])
			require.Equal(t, target, written[1].StreamID)
			require.Equal(t, terminalType, written[1].Type)
			mu.Unlock()
		})
	}
}

func TestWriterReplaceReusesFirstDiscardedSequence(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	written := make(chan wire.Frame, 1)
	w := newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error {
		written <- frame
		return nil
	})
	id := testStreamID(41)
	for _, item := range []struct {
		sequence  uint32
		frameType wire.Type
	}{{2, wire.FrameCommitted}, {3, wire.FrameHeaders}} {
		frame := wire.Frame{Version: wire.ProtocolVersion, Type: item.frameType, StreamID: id, Sequence: item.sequence}
		require.NoError(t, w.Enqueue(t.Context(), frame, nil))
	}
	terminal := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 4}
	acceptedSequence := uint32(0)
	require.NoError(t, w.Replace(t.Context(), terminal, func(sequence uint32) { acceptedSequence = sequence }))
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })

	got := <-written
	require.Equal(t, wire.FrameReset, got.Type)
	require.EqualValues(t, 2, got.Sequence)
	require.EqualValues(t, 2, acceptedSequence)
}

func TestWriterReplaceContinuesAfterInFlightAndReusesQueuedSequence(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan wire.Frame, 1)
	release := make(chan struct{})
	written := make(chan wire.Frame, 2)
	w := newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error {
		if frame.Sequence == 2 {
			started <- frame
			<-release
		}
		written <- frame
		return nil
	})
	id := testStreamID(42)
	require.NoError(t, w.Enqueue(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2,
	}, nil))
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })
	<-started
	require.NoError(t, w.Enqueue(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: id, Sequence: 3,
	}, nil))
	terminal := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 4}
	acceptedSequence := uint32(0)
	require.NoError(t, w.Replace(t.Context(), terminal, func(sequence uint32) { acceptedSequence = sequence }))
	close(release)

	require.EqualValues(t, 2, (<-written).Sequence)
	got := <-written
	require.Equal(t, wire.FrameReset, got.Type)
	require.EqualValues(t, 3, got.Sequence)
	require.EqualValues(t, 3, acceptedSequence)
}

func TestWriterReplaceWithoutQueuedFrameKeepsSequenceAfterInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	written := make(chan wire.Frame, 2)
	w := newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error {
		if frame.Sequence == 2 {
			close(started)
			<-release
		}
		written <- frame
		return nil
	})
	id := testStreamID(44)
	require.NoError(t, w.Enqueue(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2,
	}, nil))
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })
	<-started
	acceptedSequence := uint32(0)
	require.NoError(t, w.Replace(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 3,
	}, func(sequence uint32) { acceptedSequence = sequence }))
	close(release)

	require.EqualValues(t, 2, (<-written).Sequence)
	got := <-written
	require.Equal(t, wire.FrameReset, got.Type)
	require.EqualValues(t, 3, got.Sequence)
	require.EqualValues(t, 3, acceptedSequence)
}

func TestWriterReplaceRejectsPreCancelledCallerBeforeAdmission(t *testing.T) {
	writerCtx, cancelWriter := context.WithCancel(t.Context())
	defer cancelWriter()
	w := newFairWriter(writerCtx, 4096, time.Second, func(wire.Frame) error { return nil })
	id := testStreamID(45)
	original := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2, Payload: []byte("queued")}
	require.NoError(t, w.Enqueue(t.Context(), original, nil))
	queuedBytes, _ := w.stats()

	cause := errors.New("caller cancelled")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	callbacks := 0
	err := w.Replace(ctx, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 3,
	}, func(uint32) { callbacks++ })

	require.ErrorIs(t, err, cause)
	require.Zero(t, callbacks)
	requireWriterReplacementUnchanged(t, w, id, original, queuedBytes)
}

func TestWriterReplaceRejectsPreCancelledWriterBeforeAdmission(t *testing.T) {
	writerCtx, cancelWriter := context.WithCancelCause(t.Context())
	w := newFairWriter(writerCtx, 4096, time.Second, func(wire.Frame) error { return nil })
	id := testStreamID(46)
	original := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2, Payload: []byte("queued")}
	require.NoError(t, w.Enqueue(t.Context(), original, nil))
	queuedBytes, _ := w.stats()
	cause := errors.New("writer cancelled")
	cancelWriter(cause)

	callbacks := 0
	err := w.Replace(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 3,
	}, func(uint32) { callbacks++ })

	require.ErrorIs(t, err, cause)
	require.Zero(t, callbacks)
	requireWriterReplacementUnchanged(t, w, id, original, queuedBytes)
}

func TestWriterReplaceRechecksCancellationAfterSpaceWake(t *testing.T) {
	writerCtx, cancelWriter := context.WithCancel(t.Context())
	defer cancelWriter()
	unit := int64(wire.HeaderSize + 1)
	w := newFairWriter(writerCtx, 2*unit, time.Second, func(wire.Frame) error { return nil })
	id := testStreamID(47)
	otherID := testStreamID(48)
	original := wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2, Payload: []byte{'a'},
	}
	require.NoError(t, w.Enqueue(t.Context(), original, nil))
	require.NoError(t, w.Enqueue(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: otherID, Sequence: 1, Payload: []byte{'b'},
	}, nil))

	caller := &mutableCauseContext{parent: t.Context()}
	callbacks := 0
	replaceDone := make(chan error, 1)
	go func() {
		replaceDone <- w.Replace(caller, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 3, Payload: []byte{'x', 'y'},
		}, func(uint32) { callbacks++ })
	}()
	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.replacing[id]
	}, time.Second, time.Millisecond)

	w.mu.Lock()
	w.discardLocked(otherID)
	caller.cancel()
	w.mu.Unlock()
	require.ErrorIs(t, <-replaceDone, context.Canceled)
	require.Zero(t, callbacks)
	requireWriterReplacementUnchanged(t, w, id, original, unit)
}

func TestTargetResetReplacementRemainsTerminalForSource(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	written := make(chan wire.Frame, 1)
	limits := testLimits(2)
	w := newFairWriter(ctx, limits.MaxQueuedSessionBytes, time.Second, func(frame wire.Frame) error {
		written <- frame
		return nil
	})
	session := &Session{
		generation: 1, limits: limits, opts: defaultSessionOptions(SessionOptions{}), ctx: ctx, writer: w,
		streams: make(map[wire.StreamID]*Stream), targets: make(map[wire.StreamID]*targetStream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	id := testStreamID(43)
	target := newTargetStream(session, id, wire.Open{ResponseWindow: limits.InitialStreamWindow})
	target.sequence = 1 // READY was already written to the peer.
	target.committed = true
	require.NoError(t, target.enqueue(t.Context(), wire.FrameCommitted, nil, false))
	require.NoError(t, target.enqueue(t.Context(), wire.FrameHeaders, mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits), false))
	target.sendReset("handler", errTargetPanic)
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })

	resetFrame := <-written
	require.Equal(t, wire.FrameReset, resetFrame.Type)
	require.EqualValues(t, 2, resetFrame.Sequence)
	var reset wire.Reset
	require.NoError(t, wire.DecodeMetadata(resetFrame.Payload, &reset, limits.MaxMetadataBytes))
	require.True(t, reset.Committed)

	source := newReceiveTestStream(t)
	source.id = id
	ready := mustMetadata(t, wire.Ready{RequestWindow: limits.InitialStreamWindow}, limits)
	require.False(t, source.handleFrame(wire.Frame{Type: wire.FrameReady, StreamID: id, Sequence: 1, Payload: ready}))
	require.True(t, source.handleFrame(resetFrame))
	terminalSet, terminalErr := source.getTerminal()
	require.True(t, terminalSet)
	require.ErrorIs(t, terminalErr, errStreamClosed)
	require.NotErrorIs(t, terminalErr, errStreamProtocol)
}

func TestTargetStreamResetUsesStableCauseCode(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		want  string
	}{
		{name: "window stall", cause: errWindowStalled, want: wire.ErrorCodeStreamWindowTimeout},
		{name: "deadline", cause: context.DeadlineExceeded, want: wire.ErrorCodeRequestDeadline},
		{name: "cancelled", cause: context.Canceled, want: wire.ErrorCodeRequestCancelled},
		{name: "session closed", cause: errStreamClosed, want: wire.ErrorCodeSessionClosed},
		{name: "unknown internal fails closed", cause: errors.New("internal stack"), want: wire.ErrorCodeRelayProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			written := make(chan wire.Frame, 1)
			limits := testLimits(1)
			writer := newFairWriter(ctx, limits.MaxQueuedSessionBytes, time.Second, func(frame wire.Frame) error {
				written <- frame
				return nil
			})
			session := &Session{
				generation: 1, limits: limits, opts: defaultSessionOptions(SessionOptions{}), ctx: ctx, writer: writer,
				streams: make(map[wire.StreamID]*Stream), targets: make(map[wire.StreamID]*targetStream),
				tombstones: newTombstoneStore(8, time.Second, time.Now),
			}
			target := newTargetStream(session, testStreamID(44), wire.Open{ResponseWindow: limits.InitialStreamWindow})
			target.sendReset("response", test.cause)
			go writer.Run()
			t.Cleanup(func() { cancel(); <-writer.Done() })

			frame := <-written
			var reset wire.Reset
			require.NoError(t, wire.DecodeMetadata(frame.Payload, &reset, limits.MaxMetadataBytes))
			require.Equal(t, test.want, reset.Code)
		})
	}
}

type mutableCauseContext struct {
	parent   context.Context
	canceled atomic.Bool
}

func (c *mutableCauseContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (*mutableCauseContext) Done() <-chan struct{}         { return nil }
func (c *mutableCauseContext) Err() error {
	if c.canceled.Load() {
		return context.Canceled
	}
	return nil
}
func (*mutableCauseContext) Value(any) any { return nil }
func (c *mutableCauseContext) cancel()     { c.canceled.Store(true) }

func requireWriterReplacementUnchanged(t *testing.T, w *fairWriter, id wire.StreamID, want wire.Frame, wantBytes int64) {
	t.Helper()
	w.mu.Lock()
	queue := append([]queuedFrame(nil), w.queues[id]...)
	terminal := w.terminal[id]
	replacing := w.replacing[id]
	queuedBytes := w.queuedBytes
	w.mu.Unlock()
	require.Len(t, queue, 1)
	require.Equal(t, want, queue[0].frame)
	require.False(t, terminal)
	require.False(t, replacing)
	require.Equal(t, wantBytes, queuedBytes)
}

func TestWriterRejectsCancelledContextBeforeImmediateAdmission(t *testing.T) {
	writerCtx, cancelWriter := context.WithCancel(t.Context())
	defer cancelWriter()
	w := newFairWriter(writerCtx, 4096, time.Second, func(wire.Frame) error { return nil })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, w.Enqueue(ctx, testFrame(1, 'x'), nil), context.Canceled)
	queuedBytes, queuedStreams := w.stats()
	require.Zero(t, queuedBytes)
	require.Zero(t, queuedStreams)
}

func TestWriterTerminalMarkerLivesUntilStreamForget(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	written := make(chan wire.Frame, 2)
	w := newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error {
		written <- frame
		return nil
	})
	go w.Run()
	t.Cleanup(func() { cancel(); <-w.Done() })
	id := testStreamID(1)
	terminal := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCancel, StreamID: id}
	require.NoError(t, w.Replace(t.Context(), terminal, nil))
	<-written
	require.Eventually(t, func() bool {
		queuedBytes, _ := w.stats()
		return queuedBytes == 0
	}, time.Second, time.Millisecond)
	require.ErrorIs(t, w.Enqueue(t.Context(), testFrame(1, 'x'), nil), errStreamQueueClosing)
	w.Forget(id)
	require.NoError(t, w.Enqueue(t.Context(), testFrame(1, 'y'), nil))
}

func TestWindowIndependentCreditAndDeadline(t *testing.T) {
	request := newCreditWindow(2)
	response := newCreditWindow(3)
	require.NoError(t, request.Take(t.Context(), 2, time.Second))
	require.EqualValues(t, 3, response.Available())

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, request.Take(ctx, 1, time.Second), context.DeadlineExceeded)
	request.Add(1)
	require.NoError(t, request.Take(t.Context(), 1, time.Second))
}

func TestWindowNoDeadlineUsesStallTimeout(t *testing.T) {
	w := newCreditWindow(0)
	started := time.Now()
	err := w.Take(t.Context(), 1, 20*time.Millisecond)
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(started), 15*time.Millisecond)
}

func TestWindowRejectsInvalidAndOverflowCredit(t *testing.T) {
	w := newCreditWindow(3)
	require.Error(t, w.Set(-1))
	require.Error(t, w.Set(4))
	require.Error(t, w.Add(0))
	require.Error(t, w.Add(-1))
	require.Error(t, w.Add(math.MaxInt64))
	require.Error(t, w.Take(t.Context(), 0, time.Second))
	_, err := w.TakeUpTo(t.Context(), -1, time.Second)
	require.Error(t, err)
	_, err = w.TryTake(0)
	require.Error(t, err)
	require.EqualValues(t, 3, w.Available())
}

func TestWindowConsumeAndReplenishBoundary(t *testing.T) {
	w := newCreditWindow(3)
	require.NoError(t, w.Take(t.Context(), 3, time.Second))
	require.NoError(t, w.Add(3))
	require.Error(t, w.Add(1))
	require.EqualValues(t, 3, w.Available())
	require.NoError(t, w.Set(0))
	require.Zero(t, w.Available())
}
