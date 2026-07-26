package common

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadBoundedErrorBodyKeepsHeadAndTail(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error"}}`)
	got, err := ReadBoundedErrorBody(context.Background(), io.NopCloser(bytes.NewReader(body)), ErrorBodyLimits{
		Head: 64, Tail: 8, MaxRead: 64,
	})
	require.NoError(t, err)
	require.Equal(t, body, got.Head)
	require.Equal(t, body[len(body)-8:], got.Tail)
	require.Equal(t, int64(len(body)), got.TotalSeen)
	require.False(t, got.Truncated)
}

func TestReadBoundedErrorBodyStopsPastAbsoluteLimit(t *testing.T) {
	body := []byte("HEAD" + strings.Repeat("x", 64) + "physical-tail")
	got, err := ReadBoundedErrorBody(context.Background(), io.NopCloser(bytes.NewReader(body)), ErrorBodyLimits{
		Head: 4, Tail: 8, MaxRead: 20,
	})
	require.NoError(t, err)
	require.Equal(t, []byte("HEAD"), got.Head)
	require.Len(t, got.Tail, 8)
	require.Equal(t, int64(21), got.TotalSeen)
	require.True(t, got.Truncated)
}

func TestReadBoundedErrorBodyPropagatesReaderError(t *testing.T) {
	wantErr := errors.New("provider body failed")
	_, err := ReadBoundedErrorBody(context.Background(), &errorReadCloser{data: []byte("partial"), err: wantErr}, ErrorBodyLimits{
		Head: 8, Tail: 8, MaxRead: 64,
	})
	require.ErrorIs(t, err, wantErr)
}

func TestReadBoundedErrorBodyCancellationClosesBlockedReader(t *testing.T) {
	body := newBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ReadBoundedErrorBody(ctx, body, ErrorBodyLimits{Head: 8, Tail: 8, MaxRead: 64})
		done <- err
	}()
	<-body.entered
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock body read")
	}
}

type errorReadCloser struct {
	data []byte
	err  error
}

func (r *errorReadCloser) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
func (*errorReadCloser) Close() error { return nil }

type blockingReadCloser struct {
	entered chan struct{}
	closed  chan struct{}
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{entered: make(chan struct{}), closed: make(chan struct{})}
}
func (r *blockingReadCloser) Read([]byte) (int, error) {
	select {
	case <-r.entered:
	default:
		close(r.entered)
	}
	<-r.closed
	return 0, errors.New("closed")
}
func (r *blockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}
