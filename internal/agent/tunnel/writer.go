package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

var (
	errWriterClosed       = errors.New("agent tunnel: writer closed")
	errQueueFrameTooLarge = errors.New("agent tunnel: frame exceeds session queue cap")
	errStreamQueueClosing = errors.New("agent tunnel: stream queue is closing")
	errWindowStalled      = errors.New("agent tunnel: stream window stalled")
	errInvalidCredit      = errors.New("agent tunnel: invalid stream credit")
)

type queuedFrame struct {
	frame wire.Frame
	cost  int64
}

type fairWriter struct {
	ctx          context.Context
	byteCap      int64
	writeTimeout time.Duration
	write        func(wire.Frame) error
	clock        sessionClock

	mu          sync.Mutex
	queues      map[wire.StreamID][]queuedFrame
	active      []wire.StreamID
	inFlight    map[wire.StreamID]bool
	replacing   map[wire.StreamID]bool
	terminal    map[wire.StreamID]bool
	forgotten   map[wire.StreamID]bool
	queuedBytes int64
	closed      bool
	wake        chan struct{}
	space       chan struct{}
	done        chan struct{}
	err         chan error

	pingInterval time.Duration
	ping         func() error
	onError      func(error)
}

func newFairWriter(ctx context.Context, byteCap int64, writeTimeout time.Duration, write func(wire.Frame) error) *fairWriter {
	return &fairWriter{
		ctx: ctx, byteCap: byteCap, writeTimeout: writeTimeout, write: write,
		clock:  realSessionClock{now: time.Now},
		queues: make(map[wire.StreamID][]queuedFrame), inFlight: make(map[wire.StreamID]bool),
		replacing: make(map[wire.StreamID]bool), terminal: make(map[wire.StreamID]bool),
		forgotten: make(map[wire.StreamID]bool),
		wake:      make(chan struct{}, 1), space: make(chan struct{}), done: make(chan struct{}), err: make(chan error, 1),
	}
}

func (w *fairWriter) Enqueue(ctx context.Context, frame wire.Frame, onAccept func()) error {
	cost := int64(wire.HeaderSize + len(frame.Payload))
	if cost > w.byteCap || w.byteCap <= 0 {
		return errQueueFrameTooLarge
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if err := context.Cause(w.ctx); err != nil {
			return err
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return errWriterClosed
		}
		if w.replacing[frame.StreamID] || w.terminal[frame.StreamID] {
			w.mu.Unlock()
			return errStreamQueueClosing
		}
		if err := context.Cause(ctx); err != nil {
			w.mu.Unlock()
			return err
		}
		if w.queuedBytes+cost <= w.byteCap {
			w.acceptLocked(frame, cost, onAccept)
			w.mu.Unlock()
			w.signalWake()
			return nil
		}
		space := w.space
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-w.ctx.Done():
			return context.Cause(w.ctx)
		case <-space:
		}
	}
}

func (w *fairWriter) Replace(ctx context.Context, frame wire.Frame, onAccept func(uint32)) error {
	cost := int64(wire.HeaderSize + len(frame.Payload))
	if cost > w.byteCap || w.byteCap <= 0 {
		return errQueueFrameTooLarge
	}
	replacing := false
	for {
		if err := context.Cause(ctx); err != nil {
			if replacing {
				w.stopReplacing(frame.StreamID)
			}
			return err
		}
		if err := context.Cause(w.ctx); err != nil {
			if replacing {
				w.stopReplacing(frame.StreamID)
			}
			return err
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return errWriterClosed
		}
		if w.terminal[frame.StreamID] {
			w.mu.Unlock()
			return nil
		}
		if err := context.Cause(ctx); err != nil {
			w.mu.Unlock()
			if replacing {
				w.stopReplacing(frame.StreamID)
			}
			return err
		}
		if err := context.Cause(w.ctx); err != nil {
			w.mu.Unlock()
			if replacing {
				w.stopReplacing(frame.StreamID)
			}
			return err
		}
		w.replacing[frame.StreamID] = true
		replacing = true
		discardBytes := w.queueBytesLocked(frame.StreamID)
		if w.queuedBytes-discardBytes+cost <= w.byteCap {
			if queue := w.queues[frame.StreamID]; len(queue) > 0 {
				frame.Sequence = queue[0].frame.Sequence
			}
			w.discardLocked(frame.StreamID)
			delete(w.replacing, frame.StreamID)
			w.terminal[frame.StreamID] = true
			w.acceptLocked(frame, cost, func() {
				if onAccept != nil {
					onAccept(frame.Sequence)
				}
			})
			w.mu.Unlock()
			w.signalWake()
			return nil
		}
		space := w.space
		w.mu.Unlock()
		w.signalWake()
		select {
		case <-ctx.Done():
			w.stopReplacing(frame.StreamID)
			return context.Cause(ctx)
		case <-w.ctx.Done():
			w.stopReplacing(frame.StreamID)
			return context.Cause(w.ctx)
		case <-space:
		}
	}
}

func (w *fairWriter) acceptLocked(frame wire.Frame, cost int64, onAccept func()) {
	id := frame.StreamID
	if onAccept != nil {
		onAccept()
	}
	if len(w.queues[id]) == 0 && !w.inFlight[id] {
		w.active = append(w.active, id)
	}
	w.queues[id] = append(w.queues[id], queuedFrame{frame: frame, cost: cost})
	w.queuedBytes += cost
}

func (w *fairWriter) discard(id wire.StreamID) {
	w.mu.Lock()
	w.discardLocked(id)
	w.mu.Unlock()
}

func (w *fairWriter) discardLocked(id wire.StreamID) {
	queue := w.queues[id]
	for _, item := range queue {
		w.queuedBytes -= item.cost
	}
	delete(w.queues, id)
	w.removeActiveLocked(id)
	w.notifySpaceLocked()
}

func (w *fairWriter) queueBytesLocked(id wire.StreamID) int64 {
	var bytes int64
	for _, item := range w.queues[id] {
		bytes += item.cost
	}
	return bytes

}

func (w *fairWriter) stopReplacing(id wire.StreamID) {
	w.mu.Lock()
	delete(w.replacing, id)
	w.notifySpaceLocked()
	w.mu.Unlock()
	w.signalWake()
}

func (w *fairWriter) Forget(id wire.StreamID) {
	w.mu.Lock()
	w.forgotten[id] = true
	if len(w.queues[id]) == 0 && !w.inFlight[id] {
		delete(w.terminal, id)
		delete(w.forgotten, id)
	}
	w.mu.Unlock()
}

func (w *fairWriter) Run() {
	defer close(w.done)
	var ticker sessionTicker
	if w.ping != nil && w.pingInterval > 0 {
		ticker = w.clock.NewTicker(w.pingInterval)
		defer ticker.Stop()
	}
	for {
		if err := w.poll(ticker); err != nil {
			w.fail(err)
			return
		}
		item, ok := w.next()
		if ok {
			if err := w.write(item.frame); err != nil {
				w.fail(err)
				return
			}
			w.finish(item)
			continue
		}
		if err := w.wait(ticker); err != nil {
			w.fail(err)
			return
		}
	}
}

func (w *fairWriter) poll(ticker sessionTicker) error {
	if ticker == nil {
		select {
		case <-w.ctx.Done():
			return context.Cause(w.ctx)
		default:
			return nil
		}
	}
	select {
	case <-w.ctx.Done():
		return context.Cause(w.ctx)
	case <-ticker.Chan():
		return w.ping()
	default:
		return nil
	}
}

func (w *fairWriter) next() (queuedFrame, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	activeCount := len(w.active)
	for range activeCount {
		id := w.active[0]
		w.active = w.active[1:]
		if w.replacing[id] {
			w.active = append(w.active, id)
			continue
		}
		queue := w.queues[id]
		item := queue[0]
		w.queues[id] = queue[1:]
		w.inFlight[id] = true
		return item, true
	}
	return queuedFrame{}, false
}

func (w *fairWriter) finish(item queuedFrame) {
	w.mu.Lock()
	id := item.frame.StreamID
	w.inFlight[id] = false
	w.queuedBytes -= item.cost
	if len(w.queues[id]) > 0 {
		w.active = append(w.active, id)
	} else {
		delete(w.queues, id)
		delete(w.inFlight, id)
		if w.forgotten[id] {
			delete(w.terminal, id)
			delete(w.forgotten, id)
		}
	}
	w.notifySpaceLocked()
	w.mu.Unlock()
	w.signalWake()
}

func (w *fairWriter) wait(ticker sessionTicker) error {
	if ticker == nil {
		select {
		case <-w.ctx.Done():
			return context.Cause(w.ctx)
		case <-w.wake:
			return nil
		}
	}
	select {
	case <-w.ctx.Done():
		return context.Cause(w.ctx)
	case <-w.wake:
		return nil
	case <-ticker.Chan():
		return w.ping()
	}
}

func (w *fairWriter) fail(err error) {
	if err == nil {
		err = errWriterClosed
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		clear(w.queues)
		clear(w.inFlight)
		clear(w.replacing)
		clear(w.terminal)
		clear(w.forgotten)
		w.active = nil
		w.queuedBytes = 0
		w.notifySpaceLocked()
	}
	w.mu.Unlock()
	select {
	case w.err <- err:
	default:
	}
	if w.onError != nil {
		w.onError(err)
	}
}

func (w *fairWriter) stats() (int64, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.queuedBytes, len(w.queues)
}

func (w *fairWriter) removeActiveLocked(id wire.StreamID) {
	kept := w.active[:0]
	for _, activeID := range w.active {
		if activeID != id {
			kept = append(kept, activeID)
		}
	}
	w.active = kept
}

func (w *fairWriter) notifySpaceLocked() {
	close(w.space)
	w.space = make(chan struct{})
}

func (w *fairWriter) signalWake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *fairWriter) Done() <-chan struct{} { return w.done }

func (w *fairWriter) Err() <-chan error { return w.err }

type creditWindow struct {
	mu        sync.Mutex
	available int64
	max       int64
	closed    error
	changed   chan struct{}
	clock     sessionClock
}

func newCreditWindow(initial int64) *creditWindow {
	return newCreditWindowWithClock(initial, realSessionClock{now: time.Now})
}

func newCreditWindowWithClock(initial int64, clock sessionClock) *creditWindow {
	return &creditWindow{available: initial, max: initial, changed: make(chan struct{}), clock: clock}
}

func (w *creditWindow) Take(ctx context.Context, bytes int64, stall time.Duration) error {
	_, err := w.take(ctx, bytes, false, stall)
	return err
}

func (w *creditWindow) TakeUpTo(ctx context.Context, bytes int64, stall time.Duration) (int64, error) {
	return w.take(ctx, bytes, true, stall)
}

func (w *creditWindow) take(ctx context.Context, bytes int64, partial bool, stall time.Duration) (int64, error) {
	if bytes <= 0 {
		return 0, errInvalidCredit
	}
	for {
		w.mu.Lock()
		if w.closed != nil {
			err := w.closed
			w.mu.Unlock()
			return 0, err
		}
		if w.available >= bytes || partial && w.available > 0 {
			taken := bytes
			if taken > w.available {
				taken = w.available
			}
			w.available -= taken
			w.mu.Unlock()
			return taken, nil
		}
		changed := w.changed
		w.mu.Unlock()
		if err := waitForCredit(ctx, changed, stall, w.clock); err != nil {
			return 0, err
		}
	}
}

func waitForCredit(ctx context.Context, changed <-chan struct{}, stall time.Duration, clock sessionClock) error {
	if stall <= 0 {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-changed:
			return nil
		}
	}
	timer := clock.NewTimer(stall)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-changed:
		return nil
	case <-timer.Chan():
		return errWindowStalled
	}
}

func (w *creditWindow) Add(bytes int64) error {
	if bytes <= 0 {
		return errInvalidCredit
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if bytes > w.max-w.available {
		return errInvalidCredit
	}
	if w.closed == nil {
		w.available += bytes
		w.notifyLocked()
	}
	return nil
}

func (w *creditWindow) Set(bytes int64) error {
	if bytes < 0 || bytes > w.max {
		return errInvalidCredit
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed == nil {
		w.available = bytes
		w.notifyLocked()
	}
	return nil
}

func (w *creditWindow) TryTake(bytes int64) (bool, error) {
	if bytes <= 0 {
		return false, errInvalidCredit
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed != nil || w.available < bytes {
		return false, nil
	}
	w.available -= bytes
	return true, nil
}

func (w *creditWindow) Close(cause error) {
	if cause == nil {
		cause = errWriterClosed
	}
	w.mu.Lock()
	if w.closed == nil {
		w.closed = cause
		w.notifyLocked()
	}
	w.mu.Unlock()
}

func (w *creditWindow) Available() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.available
}

func (w *creditWindow) notifyLocked() {
	close(w.changed)
	w.changed = make(chan struct{})
}
