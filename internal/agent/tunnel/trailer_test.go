package tunnel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestCopyResponseDeliversHTTPResponseTrailers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		stream := frozenTrailerStream(t, wire.Headers{StatusCode: http.StatusCreated,
			Header: http.Header{"X-Regular": {"value"}}, Trailer: http.Header{"x-checksum": {"lower"}, "X-Checksum": {"upper"}}}, "body")
		require.NoError(t, stream.CopyResponse(t.Context(), w))
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, http.StatusCreated, response.StatusCode)
	require.Equal(t, "body", string(body))
	require.Equal(t, "value", response.Header.Get("X-Regular"))
	require.ElementsMatch(t, []string{"lower", "upper"}, response.Trailer.Values("X-Checksum"))
}

func TestCopyResponseRejectsForbiddenTrailerBeforeWrite(t *testing.T) {
	for _, key := range []string{"Trailer", "transfer-encoding", "Content-Length"} {
		stream := frozenTrailerStream(t, wire.Headers{StatusCode: http.StatusOK, Trailer: http.Header{key: {"bad"}}}, "body")
		recorder := httptest.NewRecorder()
		require.ErrorIs(t, stream.CopyResponse(t.Context(), recorder), errStreamProtocol)
		require.Zero(t, recorder.Body.Len())
		require.False(t, recorder.Flushed)
	}
}

func TestCopyResponseCanonicalizesAndMergesTrailerValues(t *testing.T) {
	stream := frozenTrailerStream(t, wire.Headers{StatusCode: 200, Trailer: http.Header{
		"x-checksum": {"lower"}, "X-Checksum": {"upper"},
	}}, "")
	recorder := httptest.NewRecorder()
	require.NoError(t, stream.CopyResponse(t.Context(), recorder))
	result := recorder.Result()
	require.ElementsMatch(t, []string{"lower", "upper"}, result.Trailer.Values("X-Checksum"))
}

func TestCopyResponseStripsReservedResponseHeadersAndDeclaredTrailers(t *testing.T) {
	stream := frozenTrailerStream(t, wire.Headers{StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Business": {"ok"}, consts.HeaderXAgentForwardTicket: {"header-secret"}, consts.HeaderXAgentRouteID: {"999"},
		},
		Trailer: http.Header{"X-Usage": {"tokens=7"}, consts.HeaderXAgentForwardTicket: {"trailer-secret"}},
	}, "")
	recorder := httptest.NewRecorder()
	require.NoError(t, stream.CopyResponse(t.Context(), recorder))
	result := recorder.Result()
	require.Equal(t, "ok", result.Header.Get("X-Business"))
	require.Empty(t, result.Header.Get(consts.HeaderXAgentForwardTicket))
	require.Empty(t, result.Header.Get(consts.HeaderXAgentRouteID))
	require.Equal(t, "tokens=7", result.Trailer.Get("X-Usage"))
	require.Empty(t, result.Trailer.Get(consts.HeaderXAgentForwardTicket))
}

func TestNormalizeFinalTrailersStripsDynamicReservedNames(t *testing.T) {
	normalized, dynamic, err := normalizeFinalTrailers(wire.Trailers{
		Header: http.Header{
			"X-Dynamic": {"ok"}, consts.HeaderXAgentRouteID: {"dynamic-secret"},
		},
		Dynamic: []string{"X-Dynamic", consts.HeaderXAgentRouteID},
	}, http.Header{})
	require.NoError(t, err)
	require.Equal(t, "ok", normalized.Get("X-Dynamic"))
	require.Empty(t, normalized.Get(consts.HeaderXAgentRouteID))
	require.Equal(t, []string{"X-Dynamic"}, dynamic)
}

func TestCopyResponseRejectsInvalidTrailerWithoutResponseWriterMutation(t *testing.T) {
	tests := []http.Header{
		{"Bad Key": {"value"}}, {"Bad:Key": {"value"}}, {"\x01bad": {"value"}},
		{"X-Test": {"bad\r\nvalue"}},
	}
	for _, trailer := range tests {
		stream := frozenTrailerStream(t, wire.Headers{StatusCode: 200, Trailer: trailer}, "")
		writer := &mutationTrackingWriter{header: make(http.Header)}
		require.ErrorIs(t, stream.CopyResponse(t.Context(), writer), errStreamProtocol)
		require.Zero(t, writer.headerCalls)
		require.Zero(t, writer.writeCalls)
	}
}

type mutationTrackingWriter struct {
	header      http.Header
	headerCalls int
	writeCalls  int
}

func (w *mutationTrackingWriter) Header() http.Header { w.headerCalls++; return w.header }
func (w *mutationTrackingWriter) Write(payload []byte) (int, error) {
	w.writeCalls++
	return len(payload), nil
}
func (w *mutationTrackingWriter) WriteHeader(int) { w.writeCalls++ }

func frozenTrailerStream(t *testing.T, headers wire.Headers, body string) *Stream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
	limits := testLimits(1)
	limits.InitialStreamWindow = int64(max(1, len(body)))
	session := &Session{generation: 1, limits: limits, ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	stream := newStream(session, ctx, t.Context(), testStreamID(92), 0)
	stream.signalHeaders(headersResult{headers: headers})
	if body != "" {
		require.NoError(t, session.reserveIncoming(int64(len(body))))
		require.NoError(t, stream.responseData.Push([]byte(body)))
	}
	stream.responseData.Close()
	stream.setTerminal(nil)
	return stream
}
