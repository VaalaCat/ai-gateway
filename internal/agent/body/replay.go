package body

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
)

var ErrReplayBodyClosed = errors.New("replay body closed")
var ErrReplayLimitExceeded = errors.New("replay body exceeds bytes limit")
var ErrStoreClosed = errors.New("body store closed")

type replayBody struct {
	mu          sync.Mutex
	store       *Store
	data        []byte
	path        string
	pendingFile *os.File
	size        int64
	closed      bool
	cleaned     bool
	capturing   bool
	readers     int
	cleanup     *cleanupAttempt
}

type cleanupAttempt struct {
	done chan struct{}
	err  error
}

func (b *replayBody) Size() int64 { return b.size }

func (b *replayBody) Open() (io.ReadCloser, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrReplayBodyClosed
	}
	if b.store != nil && b.store.isClosed() {
		b.mu.Unlock()
		return nil, ErrStoreClosed
	}
	path := b.path
	data := b.data
	b.readers++
	b.mu.Unlock()

	var reader io.ReadCloser
	if path != "" {
		file, err := b.store.openFile(path)
		if err != nil {
			return nil, combineStoreFailures(err, b.releaseReader())
		}
		reader = file
	} else {
		reader = io.NopCloser(bytes.NewReader(data))
	}
	return &trackedReader{body: b, reader: reader}, nil
}

func (b *replayBody) Bytes(limit int64) ([]byte, error) {
	if b.size > limit {
		return nil, ErrReplayLimitExceeded
	}
	r, err := b.Open()
	if err != nil {
		return nil, err
	}
	out := make([]byte, b.size)
	_, readErr := io.ReadFull(r, out)
	closeErr := r.Close()
	if err := combineStoreFailures(readErr, closeErr); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *replayBody) Close() error {
	attempt, start := b.beginClose()
	if attempt == nil {
		return nil
	}
	if start {
		b.runCleanup(attempt)
	} else {
		<-attempt.done
	}
	return attempt.err
}

// CloseAndWait starts cleanup under Store ownership and lets ctx interrupt only
// the caller's wait. The cleanup attempt continues until its file operations exit.
func (b *replayBody) CloseAndWait(ctx context.Context) error {
	attempt := b.startCloseAsync()
	if attempt == nil {
		return nil
	}
	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *replayBody) startCloseAsync() *cleanupAttempt {
	attempt, start := b.beginClose()
	if start {
		go b.runCleanup(attempt)
	}
	return attempt
}

func (b *replayBody) beginClose() (*cleanupAttempt, bool) {
	b.mu.Lock()
	b.closed = true
	attempt, start := b.beginCleanupLocked()
	b.mu.Unlock()
	b.store.notifyState()
	return attempt, start
}

func (b *replayBody) beginCleanupLocked() (*cleanupAttempt, bool) {
	if b.cleaned || b.capturing || b.readers > 0 {
		return nil, false
	}
	if b.cleanup != nil {
		return b.cleanup, false
	}
	b.cleanup = &cleanupAttempt{done: make(chan struct{})}
	return b.cleanup, true
}

func (b *replayBody) finishCapture(size int64, fileClosed bool) bool {
	b.mu.Lock()
	b.store.mu.Lock()
	storeClosed := b.store.closed
	b.store.mu.Unlock()
	if fileClosed {
		b.pendingFile = nil
	}
	b.size = size
	b.capturing = false
	if storeClosed {
		b.closed = true
	}
	closed := b.closed
	b.mu.Unlock()
	b.store.notifyState()
	return closed
}

func (b *replayBody) releaseReader() error {
	b.mu.Lock()
	if b.readers > 0 {
		b.readers--
	}
	var attempt *cleanupAttempt
	var start bool
	if b.closed {
		attempt, start = b.beginCleanupLocked()
	}
	b.mu.Unlock()
	b.store.notifyState()
	if start {
		b.runCleanup(attempt)
		return attempt.err
	}
	return nil
}

func (b *replayBody) runCleanup(attempt *cleanupAttempt) {
	b.mu.Lock()
	b.data = nil
	pendingFile := b.pendingFile
	path := b.path
	b.mu.Unlock()

	var closeErr error
	if pendingFile != nil {
		closeErr = b.store.closeSpill(pendingFile)
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil
		}
	}
	var removeErr error
	if path != "" {
		if err := b.store.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = err
		}
	}

	b.mu.Lock()
	if closeErr == nil && b.pendingFile == pendingFile {
		b.pendingFile = nil
	}
	if removeErr == nil && b.path == path {
		b.path = ""
	}
	complete := b.pendingFile == nil && b.path == ""
	if complete {
		b.cleaned = true
	}
	if closeErr != nil || removeErr != nil {
		attempt.err = storeFailure(errors.Join(closeErr, removeErr))
	}
	if b.cleanup == attempt {
		b.cleanup = nil
	}
	close(attempt.done)
	b.mu.Unlock()

	if b.store.afterCleanupSignal != nil {
		b.store.afterCleanupSignal()
	}
	if complete {
		b.store.unregister(b)
	} else {
		b.store.notifyState()
	}
	b.store.notifyState()
}

type trackedReader struct {
	body   *replayBody
	reader io.ReadCloser
	once   sync.Once
	err    error
}

func (r *trackedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = storeFailure(err)
	}
	return n, err
}

func (r *trackedReader) Close() error {
	r.once.Do(func() {
		r.err = combineStoreFailures(r.reader.Close(), r.body.releaseReader())
	})
	return r.err
}

func combineStoreFailures(errs ...error) error {
	var joined error
	for _, err := range errs {
		joined = errors.Join(joined, err)
	}
	if joined == nil {
		return nil
	}
	return storeFailure(joined)
}
