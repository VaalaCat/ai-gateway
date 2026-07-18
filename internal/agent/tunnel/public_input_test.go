package tunnel

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestStreamRejectsTypedNilInputsBeforeAdmission(t *testing.T) {
	stream := newPublicInputStream(t)
	var nilContext *panicContext
	require.NotPanics(t, func() { require.ErrorIs(t, stream.Commit(nilContext), errNilContext) })
	require.False(t, stream.commitStarted)
	var nilReader *panicReader
	require.NotPanics(t, func() { require.ErrorIs(t, stream.Upload(t.Context(), nilReader), errNilReader) })
	require.False(t, stream.uploadStarted.Load())
	response := frozenTrailerStream(t, wire.Headers{StatusCode: 200}, "")
	var nilWriter *panicResponseWriter
	require.NotPanics(t, func() { require.ErrorIs(t, response.CopyResponse(t.Context(), nilWriter), errNilResponseWriter) })
	require.False(t, response.responseOwner.claimed)
	require.NoError(t, stream.Commit(t.Context()))
	require.NoError(t, stream.Upload(t.Context(), bytes.NewReader(nil)))
	require.NoError(t, response.CopyResponse(t.Context(), httptest.NewRecorder()))
}

func TestIsNilInterfaceHandlesNilableAndValueKinds(t *testing.T) {
	var pointer *int
	var channel chan int
	require.True(t, isNilInterface(nil))
	require.True(t, isNilInterface(pointer))
	require.True(t, isNilInterface(channel))
	require.False(t, isNilInterface(0))
	require.False(t, isNilInterface(struct{}{}))
	require.False(t, isNilInterface(new(int)))
}

type panicContext struct{}

func (*panicContext) Deadline() (time.Time, bool) { panic("typed nil context called") }
func (*panicContext) Done() <-chan struct{}       { panic("typed nil context called") }
func (*panicContext) Err() error                  { panic("typed nil context called") }
func (*panicContext) Value(any) any               { panic("typed nil context called") }

type panicReader struct{}

func (*panicReader) Read([]byte) (int, error) { panic("typed nil reader called") }

type panicResponseWriter struct{}

func (*panicResponseWriter) Header() http.Header       { panic("typed nil writer called") }
func (*panicResponseWriter) Write([]byte) (int, error) { panic("typed nil writer called") }
func (*panicResponseWriter) WriteHeader(int)           { panic("typed nil writer called") }

func TestStreamPublicInputsRejectNilBeforeOneShotAdmission(t *testing.T) {
	stream := newPublicInputStream(t)
	require.ErrorIs(t, stream.Commit(nil), errNilContext)
	require.False(t, stream.commitStarted)
	require.ErrorIs(t, stream.Upload(nil, bytes.NewReader(nil)), errNilContext)
	require.ErrorIs(t, stream.Upload(t.Context(), nil), errNilReader)
	require.False(t, stream.uploadStarted.Load())
	response := frozenTrailerStream(t, wire.Headers{StatusCode: 200}, "")
	require.ErrorIs(t, response.CopyResponse(nil, httptest.NewRecorder()), errNilContext)
	require.ErrorIs(t, response.CopyResponse(t.Context(), nil), errNilResponseWriter)
	require.False(t, response.responseOwner.claimed)
	require.NoError(t, response.CopyResponse(t.Context(), httptest.NewRecorder()))
}

func TestStreamUploadRejectsInvalidReadCountsWithoutPanic(t *testing.T) {
	for _, count := range []int{-1, 4} {
		stream := newPublicInputStream(t)
		require.NotPanics(t, func() {
			err := stream.Upload(t.Context(), fixedCountReader{count: count})
			require.ErrorIs(t, err, errInvalidReadCount)
		})
	}
}

func TestStreamUploadBoundsNoProgressAndChecksContext(t *testing.T) {
	progress := newPublicInputStream(t)
	require.NoError(t, progress.Upload(t.Context(), &emptyThenEOFReader{empty: 99}))
	stalled := newPublicInputStream(t)
	require.ErrorIs(t, stalled.Upload(t.Context(), &emptyThenEOFReader{empty: 100}), io.ErrNoProgress)
	cancelled := newPublicInputStream(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, cancelled.Upload(ctx, &emptyThenEOFReader{empty: 1}), context.Canceled)
}

func newPublicInputStream(t *testing.T) *Stream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
	go w.Run()
	session := &Session{generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	stream := newStream(session, ctx, t.Context(), testStreamID(93), 0)
	stream.commitState.Store(uint32(wire.Committed))
	t.Cleanup(func() { stream.abortBeforeRun(context.Canceled); cancel(); <-w.Done() })
	return stream
}

type fixedCountReader struct{ count int }

func (r fixedCountReader) Read([]byte) (int, error) { return r.count, nil }

type emptyThenEOFReader struct{ empty int }

func (r *emptyThenEOFReader) Read([]byte) (int, error) {
	if r.empty > 0 {
		r.empty--
		return 0, nil
	}
	return 0, io.EOF
}
