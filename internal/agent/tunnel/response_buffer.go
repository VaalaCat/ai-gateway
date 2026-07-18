package tunnel

import (
	"bytes"
	"context"
	"errors"
	"sync"
)

var (
	errResponseBufferFull   = errors.New("agent tunnel: response buffer full")
	errResponseBufferClosed = errors.New("agent tunnel: response buffer closed")
	errIncomingBudget       = errors.New("agent tunnel: incoming byte budget exceeded")
)

type responseBuffer struct {
	mu     sync.Mutex
	data   bytes.Buffer
	max    int64
	closed bool
	notify chan struct{}
}

func newResponseBuffer(max int64) *responseBuffer {
	return &responseBuffer{max: max, notify: make(chan struct{})}
}

func (b *responseBuffer) Push(payload []byte) error {
	if len(payload) == 0 {
		return errInvalidCredit
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errResponseBufferClosed
	}
	if int64(len(payload)) > b.max-int64(b.data.Len()) {
		return errResponseBufferFull
	}
	_, _ = b.data.Write(payload)
	b.signalLocked()
	return nil
}

func (b *responseBuffer) ReadChunk(ctx context.Context, max int) ([]byte, error) {
	for {
		b.mu.Lock()
		if b.data.Len() > 0 {
			size := min(max, b.data.Len())
			chunk := make([]byte, size)
			_, _ = b.data.Read(chunk)
			b.mu.Unlock()
			return chunk, nil
		}
		if b.closed {
			b.mu.Unlock()
			return nil, errResponseBufferClosed
		}
		notify := b.notify
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-notify:
		}
	}
}

func (b *responseBuffer) Close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		b.signalLocked()
	}
	b.mu.Unlock()
}

func (b *responseBuffer) Discard() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	bytes := int64(b.data.Len())
	b.data.Reset()
	return bytes
}

func (b *responseBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Len()
}

func (b *responseBuffer) signalLocked() {
	close(b.notify)
	b.notify = make(chan struct{})
}
