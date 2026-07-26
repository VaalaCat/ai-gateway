package legacy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	backendcommon "github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
)

func TestReadLegacy5xxTraceKeepsTailAndSeenAcrossShortReads(t *testing.T) {
	body := strings.Repeat("h", MaxTraceBodySize) + "real-legacy-tail"
	tc := newTraceCollector(true)
	resp := &http.Response{Body: io.NopCloser(&shortChunkReader{data: []byte(body), chunk: 13})}

	trace, err := readLegacyErrorResponse(context.Background(), tc, resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(trace.ResponseBody), "real-legacy-tail") {
		t.Fatalf("response tail = %q", trace.ResponseBody[len(trace.ResponseBody)-32:])
	}
	if trace.ResponseBodySeen != int64(len(body)) {
		t.Fatalf("seen = %d, want %d", trace.ResponseBodySeen, len(body))
	}
}

func TestReadLegacy5xxTraceDisabledDoesNotReadBody(t *testing.T) {
	body := &countingReadCloser{}
	tc := newTraceCollector(false)
	trace, err := readLegacyErrorResponse(context.Background(), tc, &http.Response{Body: body})
	if err != nil || trace != nil {
		t.Fatalf("trace=%v err=%v", trace, err)
	}
	if body.reads != 0 || !body.closed {
		t.Fatalf("reads=%d closed=%v, want no reads and closed", body.reads, body.closed)
	}
}

func TestReadLegacy5xxTracePropagatesReadError(t *testing.T) {
	wantErr := errors.New("legacy body failed")
	tc := newTraceCollector(true)
	_, err := readLegacyErrorResponse(context.Background(), tc, &http.Response{Body: &legacyErrorReader{err: wantErr}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
}

func TestReadLegacy5xxTraceStopsAtAbsoluteLimit(t *testing.T) {
	body := strings.Repeat("x", backendcommon.DefaultErrorBodyMaxRead+128) + "unread-physical-tail"
	tc := newTraceCollector(true)
	trace, err := readLegacyErrorResponse(context.Background(), tc, &http.Response{
		Body: io.NopCloser(&shortChunkReader{data: []byte(body), chunk: 29}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if trace.ResponseBodySeen != backendcommon.DefaultErrorBodyMaxRead+1 {
		t.Fatalf("seen=%d, want lower bound %d", trace.ResponseBodySeen, backendcommon.DefaultErrorBodyMaxRead+1)
	}
	if strings.Contains(string(trace.ResponseBody), "unread-physical-tail") {
		t.Fatal("capture claimed a physical EOF tail beyond the absolute drain cap")
	}
}

func TestReadLegacy5xxTraceCancellationUnblocksRead(t *testing.T) {
	body := newLegacyBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := readLegacyErrorResponse(ctx, newTraceCollector(true), &http.Response{Body: body})
		done <- err
	}()
	<-body.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want canceled", err)
		}
	case <-time.After(time.Second):
		_ = body.Close()
		t.Fatal("legacy 5xx read ignored cancellation")
	}
}

type countingReadCloser struct {
	reads  int
	closed bool
}

func (r *countingReadCloser) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }
func (r *countingReadCloser) Close() error             { r.closed = true; return nil }

type legacyErrorReader struct{ err error }

func (r *legacyErrorReader) Read([]byte) (int, error) { return 0, r.err }
func (r *legacyErrorReader) Close() error             { return nil }

type legacyBlockingReadCloser struct {
	entered chan struct{}
	closed  chan struct{}
}

func newLegacyBlockingReadCloser() *legacyBlockingReadCloser {
	return &legacyBlockingReadCloser{entered: make(chan struct{}), closed: make(chan struct{})}
}
func (r *legacyBlockingReadCloser) Read([]byte) (int, error) {
	select {
	case <-r.entered:
	default:
		close(r.entered)
	}
	<-r.closed
	return 0, errors.New("closed")
}
func (r *legacyBlockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func TestTraceCollectorStreamingResponseKeepsRealTail(t *testing.T) {
	tc := newTraceCollector(true)
	payload := strings.Repeat("a", MaxTraceBodySize) + "real-tail"
	resp := &http.Response{Body: io.NopCloser(&shortChunkReader{data: []byte(payload), chunk: 17})}
	tc.setResponse(resp)
	tc.wrapBody(resp)

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatal("trace capture changed streamed response")
	}
	trace := tc.build()
	if len(trace.ResponseBody) != MaxTraceBodySize {
		t.Fatalf("captured len = %d, want %d", len(trace.ResponseBody), MaxTraceBodySize)
	}
	if !strings.HasSuffix(string(trace.ResponseBody), "real-tail") {
		t.Fatalf("captured response does not contain real tail: %q", trace.ResponseBody[len(trace.ResponseBody)-20:])
	}
}

type shortChunkReader struct {
	data  []byte
	chunk int
}

func (r *shortChunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestInstallLegacyRequestBodyClosesPreviousReader(t *testing.T) {
	previous := &legacyCloseSpy{Reader: strings.NewReader("old")}
	req := &http.Request{Body: previous}

	installLegacyRequestBody(req, []byte("new"))
	if previous.closes.Load() != 1 {
		t.Fatalf("previous reader closes = %d, want 1", previous.closes.Load())
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("installed body = %q, want new", got)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

type legacyCloseSpy struct {
	io.Reader
	closes atomic.Int32
}

func (r *legacyCloseSpy) Close() error {
	r.closes.Add(1)
	return nil
}
