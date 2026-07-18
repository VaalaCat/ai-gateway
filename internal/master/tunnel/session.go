package tunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
)

const (
	tombstoneLimit      = 512
	tombstoneTTL        = 30 * time.Second
	protocolErrorLimit  = 8
	protocolErrorTTL    = 60 * time.Second
	defaultWriteTimeout = 15 * time.Second
)

var (
	errSessionClosed     = errors.New("master tunnel: session closed")
	errDuplicateSession  = errors.New("master tunnel: candidate session already exists")
	errDuplicateStreamID = errors.New("master tunnel: duplicate stream ID")
	errStreamCapacity    = errors.New("master tunnel: stream capacity exhausted")
	errProtocolThreshold = errors.New("master tunnel: too many stream protocol errors")
	errUnexpectedMessage = errors.New("master tunnel: expected binary websocket message")
	errProtocol          = errors.New("master tunnel: protocol error")
	errOldGeneration     = errors.New("master tunnel: old session generation")
	errDrainTimeout      = errors.New("master tunnel: drain timeout")
	errQueueFull         = errors.New("master tunnel: forwarding queue is full")
)

type sessionConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	SetWriteDeadline(time.Time) error
	Close() error
}

type tombstone struct {
	id wire.StreamID
	at time.Time
}

type unknownFrameHandler func(*Session, wire.Frame) error

var unknownFrameHandlers = map[wire.Type]unknownFrameHandler{
	wire.FrameOpen:         func(s *Session, frame wire.Frame) error { return s.handleOpen(frame) },
	wire.FrameRequestData:  handleUnknownDataFrame,
	wire.FrameResponseData: handleUnknownDataFrame,
}

func handleUnknownDataFrame(s *Session, frame wire.Frame) error {
	return s.rejectStream(frame.StreamID, nil, "data", errProtocol)
}

type Session struct {
	hub               *Hub
	conn              sessionConn
	agentID           string
	generation        uint64
	desiredGeneration uint64
	limits            wire.Limits
	connectedAt       int64
	now               func() time.Time

	ctx             context.Context
	cancel          context.CancelCauseFunc
	done            chan struct{}
	closeOnce       sync.Once
	readerDone      chan struct{}
	readerOnce      sync.Once
	coordinatorOnce sync.Once
	drainOnce       sync.Once
	running         atomic.Bool
	writerStarted   atomic.Bool

	availability atomic.Value
	accepting    atomic.Bool
	drainingAt   atomic.Int64

	mu             sync.Mutex
	closing        bool
	legs           map[wire.StreamID]*Switch
	tombstones     map[wire.StreamID]time.Time
	tombOrder      []tombstone
	protocolErrors []time.Time
	lateData       atomic.Uint64
	recentErrors   *diagnostics.Ring
	metricState    sessionMetricState // owned exclusively by Hub.mu

	writer *sessionWriter
	budget *sessionBudget
}

func newSession(h *Hub, conn sessionConn, agentID string, generation, desired uint64, limits wire.Limits, ctx context.Context, cancel context.CancelCauseFunc) *Session {
	if ctx == nil || cancel == nil {
		ctx, cancel = context.WithCancelCause(context.Background())
	}
	now := time.Now
	s := &Session{hub: h, conn: conn, agentID: agentID, generation: generation, desiredGeneration: desired, limits: limits,
		connectedAt: now().Unix(), now: now, ctx: ctx, cancel: cancel, done: make(chan struct{}), readerDone: make(chan struct{}),
		legs: make(map[wire.StreamID]*Switch), tombstones: make(map[wire.StreamID]time.Time), budget: newSessionBudget(limits.MaxQueuedSessionBytes),
		recentErrors: diagnostics.NewRing(diagnostics.DefaultRingCapacity)}
	s.availability.Store("candidate")
	s.accepting.Store(true)
	if conn != nil {
		s.writer = newSessionWriter(ctx, s.budget, conn)
		s.writer.onError = s.Cancel
	}
	return s
}

func (s *Session) run() {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	s.startCloseCoordinator()
	if s.writer == nil || s.ctx.Err() != nil {
		s.readerOnce.Do(func() { close(s.readerDone) })
		s.Cancel(errSessionClosed)
		<-s.done
		return
	}
	s.writerStarted.Store(true)
	go s.writer.run()
	err := s.readLoop()
	s.readerOnce.Do(func() { close(s.readerDone) })
	s.Cancel(err)
	<-s.done
}

func (s *Session) readLoop() error {
	for {
		messageType, raw, err := s.conn.ReadMessage()
		if err != nil {
			if s.ctx.Err() != nil {
				return context.Cause(s.ctx)
			}
			return err
		}
		if messageType != websocket.BinaryMessage {
			return errUnexpectedMessage
		}
		frame, err := wire.Decode(raw, s.limits)
		if err != nil {
			return fmt.Errorf("%w: %v", errProtocol, err)
		}
		if err := s.dispatch(frame); err != nil {
			return err
		}
	}
}

func (s *Session) dispatch(frame wire.Frame) error {
	if sw := s.lookupLeg(frame.StreamID); sw != nil {
		if frame.Type == wire.FrameOpen {
			return s.rejectStream(frame.StreamID, sw, "duplicate", errDuplicateStreamID)
		}
		if err := sw.accept(s, s.generation, frame); err != nil {
			return s.rejectStream(frame.StreamID, sw, "protocol", err)
		}
		return nil
	}
	if s.isTombstoned(frame.StreamID, s.now()) {
		if frame.Type == wire.FrameRequestData || frame.Type == wire.FrameResponseData {
			s.lateData.Add(1)
		}
		return nil
	}
	if handler := unknownFrameHandlers[frame.Type]; handler != nil {
		return handler(s, frame)
	}
	return s.rejectStream(frame.StreamID, nil, "unknown", errProtocol)
}

func (s *Session) handleOpen(frame wire.Frame) error {
	if !s.accepting.Load() || s.hub == nil {
		s.sendAdmissionReset(frame.StreamID)
		return nil
	}
	var open wire.Open
	if err := wire.DecodeMetadata(frame.Payload, &open, s.limits.MaxMetadataBytes); err != nil {
		return s.rejectStream(frame.StreamID, nil, "open", err)
	}
	// behavior change: a bounded diagnostic ping may verify Relay while business fallback admission is disabled.
	if s.hub.opts.Admission == nil || !s.hub.opts.Admission.AllowNew() && !open.IsConnectivityProbe() {
		s.sendAdmissionReset(frame.StreamID)
		return nil
	}
	target, err := s.hub.activeTarget(s.ctx, open.TargetAgentID)
	if err != nil {
		s.sendResetCode(frame.StreamID, masterResetCode(err), "target")
		return nil
	}
	sw := newSwitch(s.hub, s, target, frame.StreamID, s.now(), s.limits)
	if err := s.hub.attachSwitch(sw); err != nil {
		if code := masterResetCode(err); code != wire.ErrorCodeRelayProtocol {
			s.sendResetCode(frame.StreamID, code, "admission")
			return nil
		}
		return s.rejectStream(frame.StreamID, nil, "duplicate", err)
	}
	sw.start()
	if err := sw.accept(s, s.generation, frame); err != nil {
		return s.rejectStream(frame.StreamID, sw, "open", err)
	}
	return nil
}

func (s *Session) sendAdmissionReset(id wire.StreamID) {
	code := wire.ErrorCodeRelayProtocol
	if s.hub != nil && s.hub.opts.Admission != nil {
		if rejectionCode := s.hub.opts.Admission.RejectionCode(); rejectionCode != "" {
			code = rejectionCode
		}
	}
	_ = s.sendResetCode(id, code, "admission")
}

func (s *Session) rejectStream(id wire.StreamID, sw *Switch, stage string, cause error) error {
	now := s.now()
	s.recordError(diagnostics.Event{Code: wire.ErrorCodeRelayProtocol, Stage: stage, Message: cause.Error(), At: now})
	if sw != nil {
		// behavior change: a bound-stream protocol failure terminates both legs.
		sw.TerminateProtocol(s, cause)
		return s.noteProtocolError(now)
	}
	if err := s.sendReset(id, stage); err != nil {
		return err
	}
	return s.noteProtocolError(now)
}

func (s *Session) sendReset(id wire.StreamID, stage string) error {
	return s.sendResetCode(id, wire.ErrorCodeRelayProtocol, stage)
}

func (s *Session) sendResetCode(id wire.StreamID, code, stage string) error {
	if s.writer == nil {
		return errSessionClosed
	}
	payload, _ := wire.EncodeMetadata(wire.Reset{Code: code, Stage: stage}, s.limits.MaxMetadataBytes)
	err := s.writer.enqueueAndWait(s.ctx, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 1, Payload: payload})
	if err == nil && s.hub != nil && s.hub.opts.Metrics != nil {
		s.hub.opts.Metrics.AddTunnelBytes(pkgmetrics.DirectionInbound, float64(wire.HeaderSize+len(payload)))
		s.hub.opts.Metrics.IncTunnelReset(tunnelMetricStage(stage), false)
	}
	return err
}

func masterResetCode(err error) string {
	switch {
	case errors.Is(err, errTargetNotFound):
		return consts.RouteErrorTargetNotFound
	case errors.Is(err, errTargetDisabled):
		return consts.RouteErrorTargetDisabled
	case errors.Is(err, errTargetCapability):
		return consts.RouteErrorRelayUnsupported
	case errors.Is(err, errRelayNotReady), errors.Is(err, errHubDraining), errors.Is(err, errSessionClosed):
		return consts.RouteErrorRelayNotReady
	case errors.Is(err, errStreamCapacity), errors.Is(err, errQueueFull):
		return wire.ErrorCodeRelayOverloaded
	default:
		return wire.ErrorCodeRelayProtocol
	}
}

func (s *Session) sendSwitchReset(ctx context.Context, id wire.StreamID, sequence uint32, code, stage string, committed bool) error {
	if s.writer == nil || !s.writerStarted.Load() {
		return errSessionClosed
	}
	payload, err := wire.EncodeMetadata(wire.Reset{
		Code: code, Stage: stage, Committed: committed,
	}, s.limits.MaxMetadataBytes)
	if err != nil {
		return err
	}
	return s.writer.enqueueAndWait(ctx, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: sequence, Payload: payload,
	})
}

func (s *Session) enqueue(ctx context.Context, frame wire.Frame) error {
	if s.writer == nil {
		return errSessionClosed
	}
	return s.writer.enqueue(ctx, frame)
}

func (s *Session) enqueueReserved(ctx context.Context, frame wire.Frame, cost int64) error {
	if s.writer == nil {
		s.budget.release(cost)
		return errSessionClosed
	}
	return s.writer.enqueueReserved(ctx, frame, cost)
}

func (s *Session) addLeg(id wire.StreamID, sw *Switch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canAttachLocked() {
		return errSessionClosed
	}
	if s.legs[id] != nil {
		return errDuplicateStreamID
	}
	if len(s.legs) >= s.limits.MaxConcurrentStreams {
		return errStreamCapacity
	}
	s.legs[id] = sw
	return nil
}

func (s *Session) canAttach() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canAttachLocked()
}

// behavior change: draining sessions reject new Switch attachments.
func (s *Session) canAttachLocked() bool {
	return s.accepting.Load() && !s.closing && s.ctx.Err() == nil
}

func (s *Session) lookupLeg(id wire.StreamID) *Switch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.legs[id]
}

func (s *Session) removeLeg(id wire.StreamID, sw *Switch, generation uint64) {
	removed := false
	s.mu.Lock()
	if s.generation == generation && s.legs[id] == sw {
		delete(s.legs, id)
		removed = true
	}
	s.mu.Unlock()
	if removed {
		s.addTombstone(id, s.now())
	}
}

func (s *Session) noteUnknownData(now time.Time) error {
	return s.noteProtocolError(now)
}

func (s *Session) noteProtocolError(now time.Time) error {
	s.mu.Lock()
	cutoff := now.Add(-protocolErrorTTL)
	kept := s.protocolErrors[:0]
	for _, at := range s.protocolErrors {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	s.protocolErrors = append(kept, now)
	if len(s.protocolErrors) > protocolErrorLimit {
		s.protocolErrors = s.protocolErrors[len(s.protocolErrors)-protocolErrorLimit:]
	}
	count := len(s.protocolErrors)
	s.mu.Unlock()
	if count >= protocolErrorLimit {
		return errProtocolThreshold
	}
	return nil
}

func (s *Session) addTombstone(id wire.StreamID, now time.Time) {
	s.mu.Lock()
	s.pruneTombstonesLocked(now)
	if _, exists := s.tombstones[id]; !exists {
		s.tombOrder = append(s.tombOrder, tombstone{id: id, at: now})
	}
	s.tombstones[id] = now
	for len(s.tombOrder) > tombstoneLimit {
		old := s.tombOrder[0]
		s.tombOrder = s.tombOrder[1:]
		if s.tombstones[old.id] == old.at {
			delete(s.tombstones, old.id)
		}
	}
	s.mu.Unlock()
}

func (s *Session) pruneTombstonesLocked(now time.Time) {
	cutoff := now.Add(-tombstoneTTL)
	for len(s.tombOrder) > 0 && !s.tombOrder[0].at.After(cutoff) {
		old := s.tombOrder[0]
		s.tombOrder = s.tombOrder[1:]
		if s.tombstones[old.id] == old.at {
			delete(s.tombstones, old.id)
		}
	}
}

func (s *Session) isTombstoned(id wire.StreamID, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneTombstonesLocked(now)
	_, ok := s.tombstones[id]
	return ok
}

func (s *Session) tombstoneCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.tombstones) }

func (s *Session) Cancel(cause error) {
	if cause == nil {
		cause = errSessionClosed
	}
	s.accepting.Store(false)
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.startCloseCoordinator()
	s.cancel(cause)
}

func (s *Session) startCloseCoordinator() {
	s.coordinatorOnce.Do(func() { go s.closeCoordinator() })
}

func (s *Session) closeCoordinator() {
	<-s.ctx.Done()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if !s.running.Load() {
		s.readerOnce.Do(func() { close(s.readerDone) })
	}
	s.closeOnce.Do(func() {
		var switches []*Switch
		s.mu.Lock()
		for _, sw := range s.legs {
			switches = append(switches, sw)
		}
		clear(s.legs)
		s.mu.Unlock()
		for _, sw := range switches {
			sw.Terminate(s, context.Cause(s.ctx))
		}
		<-s.readerDone
		for _, sw := range switches {
			<-sw.Done()
		}
		if s.writer != nil && s.writerStarted.Load() {
			<-s.writer.done
		}
		if s.hub != nil {
			s.hub.unregister(s)
		}
		close(s.done)
	})
}

func (s *Session) snapshot() SessionSnapshot {
	s.mu.Lock()
	streams := len(s.legs)
	s.mu.Unlock()
	availability, _ := s.availability.Load().(string)
	return SessionSnapshot{Generation: s.generation, DesiredGeneration: s.desiredGeneration, Availability: availability, AcceptingNewStreams: s.accepting.Load(), Streams: streams, ConnectedAt: s.connectedAt, DrainingAt: s.drainingAt.Load(), RecentErrors: relayRecentErrors(s.recentErrors.Snapshot())}
}

func (s *Session) recordError(event diagnostics.Event) bool {
	return s != nil && s.recentErrors.Record(event)
}

func relayRecentErrors(events []diagnostics.Event) []connectivity.RecentError {
	result := make([]connectivity.RecentError, 0, len(events))
	for _, event := range events {
		result = append(result, connectivity.RecentError{
			Code: event.Code, Stage: event.Stage, Message: event.Message, OccurredAt: event.At.Unix(), Count: 1,
		})
	}
	return result
}

func (s *Session) Done() <-chan struct{} { return s.done }

type queuedFrame struct {
	frame      wire.Frame
	cost       int64
	completion chan error
}

type sessionBudget struct {
	cap   int64
	mu    sync.Mutex
	used  int64
	space chan struct{}
}

func newSessionBudget(capacity int64) *sessionBudget {
	return &sessionBudget{cap: capacity, space: make(chan struct{})}
}

func (b *sessionBudget) reserve(ctx context.Context, cost int64) error {
	if cost <= 0 || cost > b.cap || b.cap <= 0 {
		return errQueueFull
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		b.mu.Lock()
		if b.used <= b.cap-cost {
			b.used += cost
			b.mu.Unlock()
			return nil
		}
		space := b.space
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-space:
		}
	}
}

func (b *sessionBudget) release(cost int64) {
	if cost <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= cost
	if b.used < 0 {
		b.used = 0
	}
	close(b.space)
	b.space = make(chan struct{})
	b.mu.Unlock()
}

func (b *sessionBudget) usage() int64 { b.mu.Lock(); defer b.mu.Unlock(); return b.used }

type sessionWriter struct {
	ctx          context.Context
	budget       *sessionBudget
	conn         sessionConn
	mu           sync.Mutex
	queues       map[wire.StreamID][]queuedFrame
	active       []wire.StreamID
	wake         chan struct{}
	done         chan struct{}
	onError      func(error)
	writeTimeout time.Duration
	now          func() time.Time
}

func newSessionWriter(ctx context.Context, budget *sessionBudget, conn sessionConn) *sessionWriter {
	return &sessionWriter{ctx: ctx, budget: budget, conn: conn, queues: make(map[wire.StreamID][]queuedFrame), wake: make(chan struct{}, 1), done: make(chan struct{}), writeTimeout: defaultWriteTimeout, now: time.Now}
}

func (w *sessionWriter) enqueue(ctx context.Context, frame wire.Frame) error {
	cost := int64(wire.HeaderSize + len(frame.Payload))
	if err := w.budget.reserve(ctx, cost); err != nil {
		return err
	}
	return w.enqueueReserved(ctx, frame, cost)
}

func (w *sessionWriter) enqueueAndWait(ctx context.Context, frame wire.Frame) error {
	cost := int64(wire.HeaderSize + len(frame.Payload))
	if err := w.budget.reserve(ctx, cost); err != nil {
		return err
	}
	completion := make(chan error, 1)
	if err := w.enqueueItem(ctx, queuedFrame{frame: frame, cost: cost, completion: completion}); err != nil {
		return err
	}
	select {
	case err := <-completion:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-w.ctx.Done():
		return context.Cause(w.ctx)
	}
}

func (w *sessionWriter) enqueueReserved(ctx context.Context, frame wire.Frame, cost int64) error {
	return w.enqueueItem(ctx, queuedFrame{frame: frame, cost: cost})
}

func (w *sessionWriter) enqueueItem(ctx context.Context, item queuedFrame) error {
	if err := context.Cause(ctx); err != nil {
		w.budget.release(item.cost)
		return err
	}
	if err := context.Cause(w.ctx); err != nil {
		w.budget.release(item.cost)
		return err
	}
	w.mu.Lock()
	if err := context.Cause(w.ctx); err != nil {
		w.mu.Unlock()
		w.budget.release(item.cost)
		return err
	}
	if len(w.queues[item.frame.StreamID]) == 0 {
		w.active = append(w.active, item.frame.StreamID)
	}
	w.queues[item.frame.StreamID] = append(w.queues[item.frame.StreamID], item)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

func (w *sessionWriter) run() {
	defer func() { w.releaseQueued(); close(w.done) }()
	for {
		item, ok := w.next()
		if ok {
			raw, err := wire.Encode(item.frame, wire.Limits{MaxMetadataBytes: wire.MaxV1PayloadBytes, MaxDataBytes: wire.MaxV1PayloadBytes})
			if err == nil {
				err = w.conn.SetWriteDeadline(w.now().Add(w.writeTimeout))
			}
			if err == nil {
				err = w.conn.WriteMessage(websocket.BinaryMessage, raw)
			}
			w.budget.release(item.cost)
			if item.completion != nil {
				item.completion <- err
			}
			if err != nil {
				if w.onError != nil {
					w.onError(err)
				}
				return
			}
			continue
		}
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
		}
	}
}

func (w *sessionWriter) next() (queuedFrame, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.active) == 0 {
		return queuedFrame{}, false
	}
	id := w.active[0]
	w.active = w.active[1:]
	queue := w.queues[id]
	item := queue[0]
	queue = queue[1:]
	if len(queue) > 0 {
		w.queues[id] = queue
		w.active = append(w.active, id)
	} else {
		delete(w.queues, id)
	}
	return item, true
}

func (w *sessionWriter) releaseQueued() {
	w.mu.Lock()
	var items []queuedFrame
	for _, queue := range w.queues {
		items = append(items, queue...)
	}
	clear(w.queues)
	w.active = nil
	w.mu.Unlock()
	for _, item := range items {
		w.budget.release(item.cost)
		if item.completion != nil {
			item.completion <- context.Cause(w.ctx)
		}
	}
}
