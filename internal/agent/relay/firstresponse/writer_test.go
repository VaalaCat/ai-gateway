package firstresponse

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type partialWriter struct {
	gin.ResponseWriter
	written int
	err     error
}

func (writer *partialWriter) Write(body []byte) (int, error) {
	written := min(writer.written, len(body))
	if written > 0 {
		_, _ = writer.ResponseWriter.Write(body[:written])
	}
	return writer.written, writer.err
}

func (writer *partialWriter) WriteString(body string) (int, error) {
	return writer.Write([]byte(body))
}

type capabilityWriter struct {
	gin.ResponseWriter
	flushes     int
	hijackErr   error
	closeNotify chan bool
	pusher      http.Pusher
}

func (writer *capabilityWriter) Flush() {
	writer.flushes++
}

func (writer *capabilityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, writer.hijackErr
}

func (writer *capabilityWriter) CloseNotify() <-chan bool {
	return writer.closeNotify
}

func (writer *capabilityWriter) Pusher() http.Pusher {
	return writer.pusher
}

type testPusher struct{}

func (testPusher) Push(string, *http.PushOptions) error {
	return nil
}

func testGinWriter() (*httptest.ResponseRecorder, gin.ResponseWriter) {
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(base)
	return base, context.Writer
}

func TestWriterObservesOnlySuccessfulPrefix(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	base, ginWriter := testGinWriter()
	disconnected := errors.New("client disconnected")
	writer := newWriter(
		&partialWriter{ResponseWriter: ginWriter, written: 8, err: disconnected},
		tracker,
		func() time.Time { return startedAt.Add(120 * time.Millisecond) },
	)

	written, err := writer.Write([]byte("event: response.created\ndata: {}\n\n"))

	require.ErrorIs(t, err, disconnected)
	require.Equal(t, 8, written)
	require.Equal(t, 120, tracker.Milliseconds())
	require.Equal(t, "event: r", base.Body.String())
	require.NotContains(t, base.Body.String(), "\n\n")
}

func TestWriterDoesNotObserveZeroByteWrite(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	_, ginWriter := testGinWriter()
	disconnected := errors.New("client disconnected")
	writer := newWriter(
		&partialWriter{ResponseWriter: ginWriter, written: 0, err: disconnected},
		tracker,
		func() time.Time { return startedAt.Add(120 * time.Millisecond) },
	)

	written, err := writer.Write([]byte("data: ignored\n\n"))

	require.ErrorIs(t, err, disconnected)
	require.Zero(t, written)
	require.Zero(t, tracker.Milliseconds())
}

func TestWriterPrefersCompleteEventAfterEarlierHeartbeatWrite(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	_, ginWriter := testGinWriter()
	observedAt := startedAt.Add(40 * time.Millisecond)
	writer := newWriter(ginWriter, tracker, func() time.Time { return observedAt })

	_, err := writer.Write([]byte(": keepalive\n\n"))
	require.NoError(t, err)
	require.Equal(t, 40, tracker.Milliseconds())

	observedAt = startedAt.Add(120 * time.Millisecond)
	_, err = writer.Write([]byte("event: response.created\ndata: {}\n\n"))
	require.NoError(t, err)
	require.Equal(t, 120, tracker.Milliseconds())
}

func TestWriterWriteStringUsesSuccessfulPrefix(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	base, ginWriter := testGinWriter()
	disconnected := errors.New("client disconnected")
	writer := newWriter(
		&partialWriter{ResponseWriter: ginWriter, written: 12, err: disconnected},
		tracker,
		func() time.Time { return startedAt.Add(120 * time.Millisecond) },
	)

	written, err := writer.WriteString("event: response.created\ndata: {}\n\n")

	require.ErrorIs(t, err, disconnected)
	require.Equal(t, 12, written)
	require.Equal(t, 120, tracker.Milliseconds())
	require.Equal(t, "event: respo", base.Body.String())
}

func TestWriterPreservesGinResponseWriterCapabilities(t *testing.T) {
	_, ginWriter := testGinWriter()
	hijackErr := errors.New("hijack called")
	closeNotify := make(chan bool)
	pusher := testPusher{}
	base := &capabilityWriter{
		ResponseWriter: ginWriter,
		hijackErr:      hijackErr,
		closeNotify:    closeNotify,
		pusher:         pusher,
	}
	writer := Wrap(base, NewTracker(time.Now()))

	writer.Flush()
	_, _, err := writer.Hijack()

	require.Equal(t, 1, base.flushes)
	require.ErrorIs(t, err, hijackErr)
	require.Equal(t, (<-chan bool)(closeNotify), writer.CloseNotify())
	require.Equal(t, pusher, writer.Pusher())
}

func TestWriterComposesWithTraceRecorder(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	base, ginWriter := testGinWriter()
	firstResponseWriter := newWriter(
		ginWriter,
		tracker,
		func() time.Time { return startedAt.Add(120 * time.Millisecond) },
	)
	recorder := trace.NewRecorderAt(trace.CaptureOff, 1024, startedAt)
	writer := recorder.WrapClientWriter(firstResponseWriter)
	payload := "event: response.created\ndata: {}\n\n"

	written, err := writer.WriteString(payload)

	require.NoError(t, err)
	require.Equal(t, len(payload), written)
	require.Equal(t, payload, base.Body.String())
	require.Equal(t, 120, tracker.Milliseconds())
}

func TestWriterComposesWithTraceRecorderAfterPartialWrite(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	base, ginWriter := testGinWriter()
	disconnected := errors.New("client disconnected")
	firstResponseWriter := newWriter(
		&partialWriter{ResponseWriter: ginWriter, written: 8, err: disconnected},
		tracker,
		func() time.Time { return startedAt.Add(120 * time.Millisecond) },
	)
	recorder := trace.NewRecorderAt(trace.CaptureOff, 1024, startedAt)
	writer := recorder.WrapClientWriter(firstResponseWriter)

	written, err := writer.Write([]byte("event: response.created\ndata: {}\n\n"))

	require.ErrorIs(t, err, disconnected)
	require.Equal(t, 8, written)
	require.Equal(t, "event: r", base.Body.String())
	require.Equal(t, 120, tracker.Milliseconds())
}

func TestWriterDoesNotObserveResponseHeaders(t *testing.T) {
	tracker := NewTracker(time.Now().Add(-100 * time.Millisecond))
	_, ginWriter := testGinWriter()
	writer := Wrap(ginWriter, tracker)

	writer.WriteHeader(http.StatusNoContent)
	writer.WriteHeaderNow()

	require.Zero(t, tracker.Milliseconds())
}
