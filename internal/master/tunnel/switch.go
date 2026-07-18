package tunnel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

var errInvalidDirection = errors.New("master tunnel: invalid frame direction")

const defaultTerminalTimeout = 15 * time.Second

type terminalContextFactory func(context.Context) (context.Context, context.CancelFunc)

type routeDirection uint8

const (
	routeSourceToTarget routeDirection = 1 << iota
	routeTargetToSource
)

var switchFrameRoutes = map[wire.Type]routeDirection{
	wire.FrameOpen:         routeSourceToTarget,
	wire.FrameReady:        routeTargetToSource,
	wire.FrameCommit:       routeSourceToTarget,
	wire.FrameCommitted:    routeTargetToSource,
	wire.FrameRequestData:  routeSourceToTarget,
	wire.FrameRequestEnd:   routeSourceToTarget,
	wire.FrameHeaders:      routeTargetToSource,
	wire.FrameResponseData: routeTargetToSource,
	wire.FrameEnd:          routeTargetToSource,
	wire.FrameCancel:       routeSourceToTarget | routeTargetToSource,
	wire.FrameReset:        routeSourceToTarget | routeTargetToSource,
	wire.FrameWindowUpdate: routeSourceToTarget | routeTargetToSource,
}

var terminalFrames = map[wire.Type]struct{}{
	wire.FrameEnd: {}, wire.FrameCancel: {}, wire.FrameReset: {},
}

type switchQueue struct {
	destination *Session
	frames      chan queuedFrame
}

type Switch struct {
	hub    *Hub
	source *Session
	target *Session
	id     wire.StreamID

	sourceGeneration uint64
	targetGeneration uint64
	openedAt         time.Time
	limits           wire.Limits

	ctx               context.Context
	cancel            context.CancelCauseFunc
	done              chan struct{}
	once              sync.Once
	started           atomic.Bool
	lifecycleMu       sync.Mutex
	coordinatorOnce   sync.Once
	closing           bool
	terminalIntent    bool
	terminalForwarded bool
	protocolOffender  *Session
	failedSource      bool
	failedTarget      bool
	producers         sync.WaitGroup
	attachments       sync.WaitGroup

	sourceQueue        *switchQueue
	targetQueue        *switchQueue
	sequenceMu         sync.Mutex
	observedSequence   map[*Session]uint32
	deliveredSequence  map[*Session]uint32
	openSeen           bool
	openDelivered      bool
	deliveredCommitted bool
	workers            sync.WaitGroup
	terminalContext    terminalContextFactory

	finalizations atomic.Int32
}

func newSwitch(h *Hub, source, target *Session, id wire.StreamID, openedAt time.Time, limits wire.Limits) *Switch {
	ctx, cancel := context.WithCancelCause(context.Background())
	if h != nil {
		ctx, cancel = context.WithCancelCause(h.ctx)
	}
	queueSize := limits.MaxConcurrentStreams
	if queueSize <= 0 || queueSize > 256 {
		queueSize = 256
	}
	if queueSize < 1 {
		queueSize = 1
	}
	return &Switch{hub: h, source: source, target: target, id: id, sourceGeneration: source.generation,
		targetGeneration: target.generation, openedAt: openedAt, limits: limits, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), observedSequence: make(map[*Session]uint32), deliveredSequence: make(map[*Session]uint32),
		sourceQueue: &switchQueue{destination: source, frames: make(chan queuedFrame, queueSize)},
		targetQueue: &switchQueue{destination: target, frames: make(chan queuedFrame, queueSize)},
		terminalContext: func(parent context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(parent, defaultTerminalTimeout)
		}}
}

func (s *Switch) start() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started.Load() || s.closing || s.ctx.Err() != nil {
		s.startCoordinatorLocked()
		return
	}
	s.started.Store(true)
	s.workers.Add(2)
	s.startCoordinatorLocked()
	go s.forward(s.sourceQueue)
	go s.forward(s.targetQueue)
}

func (s *Switch) startCoordinatorLocked() {
	s.coordinatorOnce.Do(func() { go s.finishCoordinator() })
}

func (s *Switch) finishCoordinator() {
	<-s.ctx.Done()
	s.lifecycleMu.Lock()
	s.closing = true
	s.lifecycleMu.Unlock()
	s.workers.Wait()
	s.producers.Wait()
	s.attachments.Wait()
	s.releaseQueue(s.sourceQueue)
	s.releaseQueue(s.targetQueue)
	s.sendSyntheticTerminals()
	s.finish()
}

func (s *Switch) accept(from *Session, generation uint64, frame wire.Frame) error {
	if from == nil {
		return errInvalidDirection
	}
	if from == s.source && generation != s.sourceGeneration || from == s.target && generation != s.targetGeneration {
		return errOldGeneration
	}
	if from != s.source && from != s.target {
		return errInvalidDirection
	}
	if frame.Type == wire.FrameOpen {
		s.sequenceMu.Lock()
		if from != s.source || s.openSeen {
			s.sequenceMu.Unlock()
			return errDuplicateStreamID
		}
		s.openSeen = true
		s.sequenceMu.Unlock()
	}
	if err := s.checkSequence(from, frame.Sequence); err != nil {
		return err
	}
	destination := s.destination(from, frame.Type)
	if destination == nil {
		return errInvalidDirection
	}
	if frame.Type == wire.FrameOpen {
		var err error
		frame, err = s.prepareOpen(frame)
		if err != nil {
			return err
		}
	}
	s.recordFrameMetrics(from, frame)
	if !s.started.Load() {
		s.start()
	}
	if err := s.enqueue(s.ctx, destination, frame); err != nil {
		return err
	}
	return nil
}

func (s *Switch) checkSequence(from *Session, sequence uint32) error {
	s.sequenceMu.Lock()
	defer s.sequenceMu.Unlock()
	want, err := wire.NextSequence(s.observedSequence[from])
	if err != nil {
		return err
	}
	if sequence != want {
		return errProtocol
	}
	s.observedSequence[from] = sequence
	return nil
}

func (s *Switch) prepareOpen(frame wire.Frame) (wire.Frame, error) {
	var open wire.Open
	if err := wire.DecodeMetadata(frame.Payload, &open, s.limits.MaxMetadataBytes); err != nil {
		return wire.Frame{}, err
	}
	open.SourceAgentID = s.source.agentID
	if open.RemainingNanos > 0 {
		remaining := time.Duration(open.RemainingNanos) - time.Since(s.openedAt)
		if remaining <= 0 {
			return wire.Frame{}, context.DeadlineExceeded
		}
		open.RemainingNanos = int64(remaining)
	}
	if open.Attempt != nil {
		copied := *open.Attempt
		open.Attempt = &copied
	}
	payload, err := wire.EncodeMetadata(open, s.limits.MaxMetadataBytes)
	if err != nil {
		return wire.Frame{}, err
	}
	frame.Payload = payload
	return frame, nil
}

func (s *Switch) destination(from *Session, frameType wire.Type) *Session {
	route := switchFrameRoutes[frameType]
	if from == s.source && route&routeSourceToTarget != 0 {
		return s.target
	}
	if from == s.target && route&routeTargetToSource != 0 {
		return s.source
	}
	return nil
}

func (s *Switch) enqueue(ctx context.Context, destination *Session, frame wire.Frame) error {
	if !s.beginProducer() {
		if err := context.Cause(s.ctx); err != nil {
			return err
		}
		return errSessionClosed
	}
	defer s.producers.Done()
	queue := s.targetQueue
	if destination == s.source {
		queue = s.sourceQueue
	}
	if destination != queue.destination {
		return errInvalidDirection
	}
	cost := int64(wire.HeaderSize + len(frame.Payload))
	if cost > s.limits.MaxQueuedSessionBytes || cost <= 0 {
		return errQueueFull
	}
	if err := destination.budget.reserve(ctx, cost); err != nil {
		return err
	}
	select {
	case queue.frames <- queuedFrame{frame: frame, cost: cost}:
		return nil
	case <-ctx.Done():
		destination.budget.release(cost)
		return context.Cause(ctx)
	case <-s.ctx.Done():
		destination.budget.release(cost)
		return context.Cause(s.ctx)
	}
}

func (s *Switch) beginProducer() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return false
	}
	s.producers.Add(1)
	return true
}

func (s *Switch) beginAttachment() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return false
	}
	s.attachments.Add(1)
	return true
}

func (s *Switch) attachmentStatus() error {
	s.lifecycleMu.Lock()
	closing := s.closing || s.ctx.Err() != nil
	s.lifecycleMu.Unlock()
	if closing || !s.source.canAttach() || !s.target.canAttach() {
		return errSessionClosed
	}
	return nil
}

func (s *Switch) forward(queue *switchQueue) {
	defer s.workers.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case item := <-queue.frames:
			err := queue.destination.enqueueReserved(s.ctx, item.frame, item.cost)
			if err != nil {
				s.Terminate(queue.destination, err)
				return
			}
			s.markDelivered(queue.destination, item.frame)
			if terminalFrame(item.frame.Type) {
				s.markTerminalForwarded()
				s.Cancel(errSessionClosed)
				return
			}
		}
	}
}

func (s *Switch) releaseQueue(queue *switchQueue) {
	for {
		select {
		case item := <-queue.frames:
			queue.destination.budget.release(item.cost)
		default:
			return
		}
	}
}

func terminalFrame(frameType wire.Type) bool {
	_, terminal := terminalFrames[frameType]
	return terminal
}

func (s *Switch) Cancel(cause error) {
	if cause == nil {
		cause = errSessionClosed
	}
	s.lifecycleMu.Lock()
	s.closing = true
	s.startCoordinatorLocked()
	s.lifecycleMu.Unlock()
	s.cancel(cause)
}

func (s *Switch) Terminate(failedLeg *Session, cause error) {
	if cause == nil {
		cause = errSessionClosed
	}
	s.lifecycleMu.Lock()
	switch failedLeg {
	case s.source:
		s.failedSource = true
	case s.target:
		s.failedTarget = true
	default:
		s.closing = true
		s.startCoordinatorLocked()
		s.lifecycleMu.Unlock()
		s.cancel(cause)
		return
	}
	if !s.terminalForwarded {
		s.terminalIntent = true
	}
	s.closing = true
	s.startCoordinatorLocked()
	s.lifecycleMu.Unlock()
	s.cancel(cause)
}

func (s *Switch) TerminateProtocol(offender *Session, cause error) {
	if cause == nil {
		cause = errProtocol
	}
	s.lifecycleMu.Lock()
	if (offender == s.source || offender == s.target) && s.protocolOffender == nil {
		s.protocolOffender = offender
		if !s.terminalForwarded {
			s.terminalIntent = true
		}
	}
	s.closing = true
	s.startCoordinatorLocked()
	s.lifecycleMu.Unlock()
	s.cancel(cause)
}

func (s *Switch) markTerminalForwarded() {
	s.lifecycleMu.Lock()
	s.terminalForwarded = true
	s.terminalIntent = false
	s.lifecycleMu.Unlock()
}

func (s *Switch) markDelivered(destination *Session, frame wire.Frame) {
	sender := s.source
	if destination == s.source {
		sender = s.target
	}
	s.sequenceMu.Lock()
	s.deliveredSequence[sender] = frame.Sequence
	if destination == s.target && sender == s.source && frame.Type == wire.FrameOpen {
		s.openDelivered = true
	}
	if frame.Type == wire.FrameCommitted {
		s.deliveredCommitted = true
	}
	s.sequenceMu.Unlock()
}

type terminalDelivery struct {
	destination *Session
	sender      *Session
	code        string
	stage       string
}

func (s *Switch) sendSyntheticTerminals() {
	parent := context.Background()
	if s.hub != nil {
		parent = s.hub.ctx
	}
	terminalContext := s.terminalContext
	if terminalContext == nil {
		terminalContext = func(parent context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(parent, defaultTerminalTimeout)
		}
	}
	for _, delivery := range s.terminalDeliveries() {
		if delivery.destination.ctx.Err() != nil {
			continue
		}
		sequence, committed, deliver, err := s.prepareTerminalDelivery(delivery)
		if !deliver {
			continue
		}
		if err != nil {
			delivery.destination.Cancel(errProtocol)
			continue
		}
		// behavior change: each destination gets a fixed deadline covering admission and completion.
		ctx, cancel := terminalContext(parent)
		err = delivery.destination.sendSwitchReset(ctx, s.id, sequence, delivery.code, delivery.stage, committed)
		cancel()
		if err != nil {
			delivery.destination.Cancel(err)
		} else if s.hub != nil && s.hub.opts.Metrics != nil {
			direction := pkgmetrics.DirectionInbound
			if delivery.sender == s.source {
				direction = pkgmetrics.DirectionOutbound
			}
			payload, _ := wire.EncodeMetadata(wire.Reset{
				Code: delivery.code, Stage: delivery.stage, Committed: committed,
			}, s.limits.MaxMetadataBytes)
			s.hub.opts.Metrics.AddTunnelBytes(direction, float64(wire.HeaderSize+len(payload)))
			s.hub.opts.Metrics.IncTunnelReset(tunnelMetricStage(delivery.stage), committed)
		}
	}
}

func (s *Switch) prepareTerminalDelivery(delivery terminalDelivery) (uint32, bool, bool, error) {
	s.sequenceMu.Lock()
	defer s.sequenceMu.Unlock()
	if delivery.destination == s.target && !s.openDelivered {
		return 0, false, false, nil
	}
	sequence, err := wire.NextSequence(s.deliveredSequence[delivery.sender])
	return sequence, s.deliveredCommitted, true, err
}

func (s *Switch) terminalDeliveries() []terminalDelivery {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.terminalIntent || s.terminalForwarded {
		return nil
	}
	if s.protocolOffender != nil {
		peer := s.source
		if s.protocolOffender == s.source {
			peer = s.target
		}
		return []terminalDelivery{
			{destination: peer, sender: s.protocolOffender, code: wire.ErrorCodeSessionClosed, stage: "peer"},
			{destination: s.protocolOffender, sender: peer, code: wire.ErrorCodeRelayProtocol, stage: "protocol"},
		}
	}
	if s.failedSource == s.failedTarget {
		return nil
	}
	if s.failedSource {
		return []terminalDelivery{{destination: s.target, sender: s.source, code: wire.ErrorCodeSessionClosed, stage: "peer"}}
	}
	return []terminalDelivery{{destination: s.source, sender: s.target, code: wire.ErrorCodeSessionClosed, stage: "peer"}}
}

func (s *Switch) finish() {
	s.once.Do(func() {
		s.finalizations.Add(1)
		if s.source != nil {
			s.source.removeLeg(s.id, s, s.sourceGeneration)
		}
		if s.target != nil {
			s.target.removeLeg(s.id, s, s.targetGeneration)
		}
		if s.hub != nil {
			s.hub.removeSwitch(s)
		}
		close(s.done)
	})
}

func (s *Switch) Done() <-chan struct{} { return s.done }
