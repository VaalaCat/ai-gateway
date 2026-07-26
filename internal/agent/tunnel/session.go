package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	defaultPingInterval       = 20 * time.Second
	defaultPongTimeout        = 60 * time.Second
	defaultWriteTimeout       = 15 * time.Second
	defaultOpenCommitTimeout  = 30 * time.Second
	defaultWindowStallTimeout = 60 * time.Second
	defaultTombstoneTTL       = 30 * time.Second
	defaultTombstoneLimit     = 512
	unknownDataLimit          = 8
	unknownDataWindow         = 60 * time.Second
)

var (
	errSessionClosed     = errors.New("agent tunnel: session closed")
	errSessionNotRunning = errors.New("agent tunnel: session is not running")
	errDuplicateStreamID = errors.New("agent tunnel: duplicate stream ID")
	errStreamLimit       = errors.New("agent tunnel: concurrent stream limit reached")
	errOpenAborted       = errors.New("agent tunnel: OPEN aborted before writer admission")
	errProtocol          = errors.New("agent tunnel: protocol error")
	errUnexpectedMessage = errors.New("agent tunnel: expected binary websocket message")
	errUnknownStreamData = errors.New("agent tunnel: DATA for unknown stream")
	errNilContext        = errors.New("agent tunnel: nil context")
	errNilConnection     = errors.New("agent tunnel: nil connection")
	ErrSessionDirection  = errors.New("agent tunnel: session direction violation")
	errSessionDirection  = ErrSessionDirection
	errSessionOptions    = errors.New("agent tunnel: invalid session options")
)

type SessionDirection uint8

const (
	// behavior change: zero no longer defaults to a relay session.
	SessionDirectionRelay SessionDirection = iota + 1
	SessionDirectionDirectOutgoing
	SessionDirectionDirectIncoming
)

type sessionState uint8

const (
	sessionStateNew sessionState = iota
	sessionStateRunning
	sessionStateDone
)

type SessionOptions struct {
	Logger              *zap.Logger
	Metrics             *pkgmetrics.AgentRelayMetrics
	PingInterval        time.Duration
	PongTimeout         time.Duration
	WriteTimeout        time.Duration
	OpenCommitTimeout   time.Duration
	WindowStallTimeout  time.Duration
	TombstoneTTL        time.Duration
	TombstoneLimit      int
	TargetHandler       *TargetHandler
	Direction           SessionDirection
	IngressKind         string
	BoundSourceAgentID  string
	AdmissionDeadline   time.Time
	SourceEnabled       func(string) bool
	TargetStatusEnabled func() bool
	Now                 func() time.Time
	clock               sessionClock
	directLogs          *directLogs
	directSourceAgentID string
	directTargetAgentID string
}

type sessionConn interface {
	ReadMessage() (int, []byte, error)
	SetReadLimit(int64)
	WriteMessage(int, []byte) error
	WriteControl(int, []byte, time.Time) error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	SetPongHandler(func(string) error)
	Close() error
}

type Session struct {
	conn       sessionConn
	generation uint64
	limits     wire.Limits
	opts       SessionOptions

	started         chan struct{}
	done            chan struct{}
	doneOnce        sync.Once
	startedOnce     sync.Once
	connCloseOnce   sync.Once
	preRunCloseOnce sync.Once
	connCloseDone   chan struct{}

	stateMu     sync.Mutex
	ctx         context.Context
	cancel      context.CancelCauseFunc
	cancelCause error
	writer      *fairWriter
	initErr     error
	state       sessionState

	streamsMu  sync.Mutex
	streams    map[wire.StreamID]*Stream
	targets    map[wire.StreamID]*targetStream
	tombstones *tombstoneStore

	unknownDataTimes  []time.Time
	bufferMu          sync.Mutex
	incomingBytes     int64
	queuedBytes       int64
	peakBufferedBytes int64

	admissionMu   sync.Mutex
	accepting     bool
	borrows       int
	activeStreams int
	idleSince     time.Time
	activityNow   func() time.Time
	activity      chan struct{}
	recentErrors  *diagnostics.Ring
}

func NewSession(conn *websocket.Conn, generation uint64, limits wire.Limits, opts SessionOptions) *Session {
	if conn == nil {
		return newTerminalSession(nil, generation, limits, opts, errNilConnection)
	}
	return newSession(conn, generation, limits, opts)
}

func newSession(conn sessionConn, generation uint64, limits wire.Limits, opts SessionOptions) *Session {
	if conn == nil {
		return newTerminalSession(nil, generation, limits, opts, errNilConnection)
	}
	normalized, err := wire.NormalizeV2Limits(limits)
	if err != nil {
		return newTerminalSession(conn, generation, limits, opts, err)
	}
	opts = defaultSessionOptions(opts)
	if generation == 0 {
		return newTerminalSession(conn, generation, normalized, opts, errSessionOptions)
	}
	opts, err = validateSessionOptions(opts)
	if err != nil {
		return newTerminalSession(conn, generation, normalized, opts, err)
	}
	return newSessionValue(conn, generation, normalized, opts)
}

func newSessionValue(conn sessionConn, generation uint64, limits wire.Limits, opts SessionOptions) *Session {
	opts = defaultSessionOptions(opts)
	now := opts.Now()
	return &Session{
		conn: conn, generation: generation, limits: limits, opts: opts,
		started: make(chan struct{}), done: make(chan struct{}), streams: make(map[wire.StreamID]*Stream),
		connCloseDone: make(chan struct{}),
		targets:       make(map[wire.StreamID]*targetStream), accepting: true, idleSince: now, activityNow: opts.Now, activity: make(chan struct{}, 1),
		tombstones:   newTombstoneStore(opts.TombstoneLimit, opts.TombstoneTTL, opts.clock.Now),
		recentErrors: diagnostics.NewRing(diagnostics.DefaultRingCapacity),
	}
}

func (s *Session) Generation() uint64 { return s.generation }

func (s *Session) StreamCount() int {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return len(s.streams) + len(s.targets)
}

func (s *Session) setAccepting(accepting bool) {
	s.admissionMu.Lock()
	s.accepting = accepting
	s.admissionMu.Unlock()
}

func (s *Session) isRunning() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state == sessionStateRunning && s.ctx != nil
}

func (s *Session) tryActivate() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	// behavior change: ingress may publish an active Session between Run's
	// running-state transition and its context initialization.
	if s.state != sessionStateRunning || s.cancelCause != nil || s.ctx == nil || context.Cause(s.ctx) != nil {
		return false
	}
	s.admissionMu.Lock()
	s.accepting = true
	s.admissionMu.Unlock()
	return true
}

func (s *Session) acquireAdmission() bool {
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if !s.accepting {
		return false
	}
	s.borrows++
	s.idleSince = time.Time{}
	return true
}

func (s *Session) acceptsNew() bool {
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	return s.accepting
}

func (s *Session) releaseAdmission() {
	s.admissionMu.Lock()
	if s.borrows > 0 {
		s.borrows--
	}
	s.startIdleLocked()
	s.admissionMu.Unlock()
	s.signalActivity()
}

func (s *Session) idle() bool {
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	return s.borrows == 0 && s.activeStreams == 0
}

func (s *Session) idleSinceTime() (time.Time, bool) {
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if s.borrows != 0 || s.activeStreams != 0 {
		return time.Time{}, false
	}
	if s.idleSince.IsZero() {
		s.idleSince = s.activityTimeLocked()
	}
	return s.idleSince, true
}

func (s *Session) setActivityNow(now func() time.Time, current time.Time) {
	if now == nil {
		now = time.Now
	}
	s.admissionMu.Lock()
	s.activityNow = now
	if s.borrows == 0 && s.activeStreams == 0 {
		s.idleSince = current
	}
	s.admissionMu.Unlock()
}

func (s *Session) startIdleLocked() {
	if s.borrows == 0 && s.activeStreams == 0 && s.idleSince.IsZero() {
		s.idleSince = s.activityTimeLocked()
	}
}

func (s *Session) activityTimeLocked() time.Time {
	if s.activityNow != nil {
		return s.activityNow()
	}
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

func (s *Session) Activity() <-chan struct{} { return s.activity }

func (s *Session) signalActivity() {
	select {
	case s.activity <- struct{}{}:
	default:
	}
}

func newTerminalSession(conn sessionConn, generation uint64, limits wire.Limits, opts SessionOptions, initErr error) *Session {
	s := newSessionValue(conn, generation, limits, opts)
	s.initErr = initErr
	s.recordError(diagnostics.Event{Code: wire.ErrorCodeRelayProtocol, Stage: "init", Message: initErr.Error(), At: s.opts.Now()})
	s.state = sessionStateDone
	s.startedOnce.Do(func() { close(s.started) })
	s.doneOnce.Do(func() { close(s.done) })
	if conn != nil {
		_ = conn.Close()
	}
	s.connCloseOnce.Do(func() { close(s.connCloseDone) })
	return s
}

func defaultSessionOptions(opts SessionOptions) SessionOptions {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = defaultPingInterval
	}
	if opts.PongTimeout <= 0 {
		opts.PongTimeout = defaultPongTimeout
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = defaultWriteTimeout
	}
	if opts.OpenCommitTimeout <= 0 {
		opts.OpenCommitTimeout = defaultOpenCommitTimeout
	}
	if opts.WindowStallTimeout <= 0 {
		opts.WindowStallTimeout = defaultWindowStallTimeout
	}
	if opts.TombstoneTTL <= 0 {
		opts.TombstoneTTL = defaultTombstoneTTL
	}
	if opts.TombstoneLimit <= 0 {
		opts.TombstoneLimit = defaultTombstoneLimit
	}
	if opts.TombstoneLimit > defaultTombstoneLimit {
		opts.TombstoneLimit = defaultTombstoneLimit
	}
	if opts.Now == nil {
		if opts.clock != nil {
			opts.Now = opts.clock.Now
		} else {
			opts.Now = time.Now
		}
	}
	if opts.clock == nil {
		opts.clock = realSessionClock{now: opts.Now}
	}
	return opts
}

func validateSessionOptions(opts SessionOptions) (SessionOptions, error) {
	switch opts.Direction {
	case SessionDirectionRelay:
		if opts.IngressKind == "" {
			opts.IngressKind = agentproxy.IngressKindRelayTunnel
		}
		if opts.IngressKind != agentproxy.IngressKindRelayTunnel || opts.BoundSourceAgentID != "" ||
			!opts.AdmissionDeadline.IsZero() || opts.SourceEnabled != nil || opts.TargetStatusEnabled != nil {
			return opts, errSessionOptions
		}
	case SessionDirectionDirectOutgoing:
		if opts.IngressKind == "" {
			opts.IngressKind = agentproxy.IngressKindDirectTunnel
		}
		if opts.IngressKind != agentproxy.IngressKindDirectTunnel || opts.TargetHandler != nil ||
			opts.BoundSourceAgentID != "" || !opts.AdmissionDeadline.IsZero() || opts.SourceEnabled != nil ||
			opts.TargetStatusEnabled != nil {
			return opts, errSessionOptions
		}
	case SessionDirectionDirectIncoming:
		if opts.IngressKind == "" {
			opts.IngressKind = agentproxy.IngressKindDirectTunnel
		}
		boundSource := strings.TrimSpace(opts.BoundSourceAgentID)
		if opts.IngressKind != agentproxy.IngressKindDirectTunnel || opts.TargetHandler == nil ||
			boundSource == "" || boundSource != opts.BoundSourceAgentID || opts.AdmissionDeadline.IsZero() ||
			opts.SourceEnabled == nil || opts.TargetStatusEnabled == nil {
			return opts, errSessionOptions
		}
	default:
		return opts, errSessionOptions
	}
	return opts, nil
}

func (s *Session) Run(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	s.stateMu.Lock()
	if s.state != sessionStateNew {
		err := s.sessionErrorLocked()
		s.stateMu.Unlock()
		return err
	}
	s.state = sessionStateRunning
	s.stateMu.Unlock()
	s.run(ctx)
	return s.cause()
}

func (s *Session) run(parent context.Context) {
	startedAt := s.opts.Now()
	if s.isDirect() {
		defer func() { s.opts.Metrics.ObserveDirectSessionDuration(s.opts.Now().Sub(startedAt)) }()
	}
	ctx, cancel := context.WithCancelCause(parent)
	s.stateMu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	priorCause := s.cancelCause
	w := newFairWriter(ctx, s.limits.MaxQueuedSessionBytes, s.opts.WriteTimeout, s.writeFrame)
	w.clock = s.opts.clock
	w.pingInterval = s.opts.PingInterval
	w.ping = s.writePing
	w.onError = s.Cancel
	w.onQueuedBytesChanged = s.adjustQueuedBytes
	s.writer = w
	s.stateMu.Unlock()

	if priorCause != nil {
		cancel(priorCause)
	}
	s.configureReader()
	s.startedOnce.Do(func() { close(s.started) })
	go w.Run()
	err := s.readLoop(ctx)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			s.recordError(diagnostics.Event{
				Code: sessionDiagnosticCode(err), Stage: "read", Message: err.Error(), At: s.opts.Now(),
			})
		}
		s.Cancel(err)
	}
	s.finalize(w)
}

func (s *Session) configureReader() {
	s.conn.SetReadLimit(sessionMessageReadLimit(s.limits))
	_ = s.conn.SetReadDeadline(s.opts.clock.Now().Add(s.opts.PongTimeout))
	s.conn.SetPongHandler(func(string) error {
		return s.conn.SetReadDeadline(s.opts.clock.Now().Add(s.opts.PongTimeout))
	})
}

func sessionMessageReadLimit(limits wire.Limits) int64 {
	payloadLimit := int64(0)
	for _, frameType := range []wire.Type{wire.FrameOpen, wire.FrameRequestData, wire.FrameAttemptResult} {
		limit, err := wire.FramePayloadLimit(frameType, limits)
		if err == nil && limit > payloadLimit {
			payloadLimit = limit
		}
	}
	return int64(wire.HeaderSize) + payloadLimit
}

func (s *Session) readLoop(ctx context.Context) error {
	for {
		messageType, message, err := s.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		}
		if messageType != websocket.BinaryMessage {
			return errUnexpectedMessage
		}
		frame, err := wire.Decode(message, s.limits)
		if err != nil {
			return fmt.Errorf("%w: %w", errProtocol, err)
		}
		if err := s.dispatch(ctx, frame); err != nil {
			return err
		}
	}
}

func (s *Session) dispatch(ctx context.Context, frame wire.Frame) error {
	stream := s.lookupStream(frame.StreamID)
	if stream != nil {
		if frame.Type == wire.FrameOpen {
			return nil
		}
		if stream.closed.Load() {
			return nil
		}
		select {
		case stream.inbound <- frame:
			return nil
		case <-stream.done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	if target := s.lookupTarget(frame.StreamID); target != nil {
		if frame.Type == wire.FrameOpen {
			return nil
		}
		if !target.deliveries.Begin() {
			return nil
		}
		defer target.deliveries.End()
		if target.closed.Load() {
			return nil
		}
		if frame.Type == wire.FrameCancel || frame.Type == wire.FrameReset {
			target.Cancel(errStreamClosed)
		}
		reserved := int64(0)
		if frame.Type == wire.FrameRequestData {
			reserved = int64(len(frame.Payload))
			if err := s.reserveIncoming(reserved); err != nil {
				target.Cancel(err)
				return nil
			}
		}
		select {
		case target.inbound <- targetFrame{frame: frame, reserved: reserved}:
			return nil
		case <-target.ctx.Done():
			if reserved > 0 {
				_ = s.releaseIncoming(reserved)
			}
			return nil
		case <-target.deliveryStop:
			if reserved > 0 {
				_ = s.releaseIncoming(reserved)
			}
			return nil
		case <-target.done:
			if reserved > 0 {
				_ = s.releaseIncoming(reserved)
			}
			return nil
		case <-ctx.Done():
			if reserved > 0 {
				_ = s.releaseIncoming(reserved)
			}
			return context.Cause(ctx)
		}
	}
	if s.tombstones.Contains(frame.StreamID) {
		return nil
	}
	if frame.Type == wire.FrameOpen {
		s.handleTargetOpen(ctx, frame)
		return nil
	}
	if frame.Type == wire.FrameResponseData || frame.Type == wire.FrameRequestData {
		return s.handleUnknownData(ctx, frame.StreamID)
	}
	return fmt.Errorf("%w: frame %d for unknown stream", errProtocol, frame.Type)
}

func (s *Session) handleUnknownData(ctx context.Context, id wire.StreamID) error {
	payload, _ := wire.EncodeMetadata(wire.Reset{Code: wire.ErrorCodeRelayProtocol, Stage: "data"}, s.limits.MaxMetadataBytes)
	_ = s.writer.Enqueue(ctx, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Payload: payload,
	}, nil)
	now := s.opts.clock.Now()
	s.recordError(diagnostics.Event{Code: wire.ErrorCodeRelayProtocol, Stage: "data", Message: errUnknownStreamData.Error(), At: now})
	cutoff := now.Add(-unknownDataWindow)
	kept := s.unknownDataTimes[:0]
	for _, observed := range s.unknownDataTimes {
		if observed.After(cutoff) {
			kept = append(kept, observed)
		}
	}
	s.unknownDataTimes = append(kept, now)
	if len(s.unknownDataTimes) >= unknownDataLimit {
		return errUnknownStreamData
	}
	return nil
}

func (s *Session) recordError(event diagnostics.Event) bool {
	return s != nil && s.recentErrors.Record(event)
}

func (s *Session) RecentErrors() []diagnostics.Event {
	if s == nil {
		return nil
	}
	return s.recentErrors.Snapshot()
}

func sessionDiagnosticCode(err error) string {
	if errors.Is(err, wire.ErrUnsupportedVersion) {
		return wire.ErrorCodeUnsupportedProtocolVersion
	}
	if errors.Is(err, errProtocol) || errors.Is(err, errUnexpectedMessage) || errors.Is(err, errUnknownStreamData) {
		return wire.ErrorCodeRelayProtocol
	}
	return wire.ErrorCodeSessionClosed
}

func (s *Session) OpenAttemptStream(ctx context.Context, req agentproxy.AttemptStreamRequest) (*Stream, error) {
	if s != nil && s.opts.Direction == SessionDirectionDirectIncoming {
		return nil, errSessionDirection
	}
	return s.openAttemptStream(ctx, req)
}

func (s *Session) openAttemptStream(ctx context.Context, req agentproxy.AttemptStreamRequest) (*Stream, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if err := s.initializationError(); err != nil {
		return nil, err
	}
	id, err := wire.NewStreamID()
	if err != nil {
		return nil, err
	}
	return s.openStream(ctx, id, req)
}

func (s *Session) OpenProbeStream(ctx context.Context, req agentproxy.ProbeStreamRequest) (*Stream, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if err := s.initializationError(); err != nil {
		return nil, err
	}
	if s.opts.Direction == SessionDirectionDirectIncoming {
		return nil, errSessionDirection
	}
	open := wire.Open{
		ProbePolicy: req.Policy, Method: http.MethodGet, Path: "/ping", Header: map[string][]string{},
		RemainingNanos: durationNanos(req.Remaining), RequestID: req.RequestID, TargetAgentID: req.TargetAgentID,
		ResponseWindow: s.limits.InitialStreamWindow,
	}
	if !open.IsConnectivityProbe() {
		return nil, errProtocol
	}
	id, err := wire.NewStreamID()
	if err != nil {
		return nil, err
	}
	return s.openPreparedStream(ctx, id, open, req.Remaining, streamKindProbe)
}

func (s *Session) openStream(ctx context.Context, id wire.StreamID, req agentproxy.AttemptStreamRequest) (*Stream, error) {
	frame, err := s.openFrame(id, req)
	if err != nil {
		return nil, err
	}
	var open wire.Open
	if err := wire.DecodeMetadata(frame.Payload, &open, s.limits.MaxMetadataBytes); err != nil {
		return nil, err
	}
	return s.openPreparedStream(ctx, id, open, req.Remaining, streamKindAttempt)
}

func (s *Session) openPreparedStream(ctx context.Context, id wire.StreamID, open wire.Open, remaining time.Duration, kind streamKind) (*Stream, error) {
	if err := s.waitStarted(ctx); err != nil {
		return nil, err
	}
	s.stateMu.Lock()
	sessionCtx := s.ctx
	w := s.writer
	s.stateMu.Unlock()
	if sessionCtx == nil || sessionCtx.Err() != nil {
		return nil, errSessionClosed
	}
	payload, err := wire.EncodeMetadata(open, s.limits.MaxMetadataBytes)
	if err != nil {
		return nil, err
	}
	frame := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload}
	stream := newStream(s, sessionCtx, id, remaining, open.RequestID)
	stream.kind = kind
	if err := s.admitStream(stream); err != nil {
		stream.abortBeforeRun(err)
		return nil, err
	}
	go stream.run()
	enqueueCtx, stopEnqueue := stream.operationContext(ctx)
	err = w.Enqueue(enqueueCtx, frame, nil)
	stopEnqueue()
	if err == nil && (!s.isCurrentStream(stream) || stream.closed.Load()) {
		err = errOpenAborted
	}
	if err != nil {
		stream.Cancel(err)
		return nil, err
	}
	return stream, nil
}

func (s *Session) isCurrentStream(stream *Stream) bool {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	current := s.streams[stream.id]
	return current == stream && current.generation == stream.generation
}

func (s *Session) openFrame(id wire.StreamID, req agentproxy.AttemptStreamRequest) (wire.Frame, error) {
	header := req.Header.Clone()
	if req.Method != http.MethodPost || req.Path != attemptwire.EndpointPath || req.Attempt.Validate() != nil {
		return wire.Frame{}, errProtocol
	}
	attempt := req.Attempt
	open := wire.Open{
		Method: req.Method, Path: req.Path, Header: map[string][]string(header), BodyLength: req.BodyLength,
		RemainingNanos: durationNanos(req.Remaining), RequestID: req.RequestID, SourceAgentID: "", TargetAgentID: req.TargetAgentID,
		RouteID: req.RouteID, Hop: req.Hop, ResponseWindow: s.limits.InitialStreamWindow, Attempt: &attempt,
	}
	if err := validateAttemptOrProbeOpen(open); err != nil {
		return wire.Frame{}, fmt.Errorf("%w: %v", errProtocol, err)
	}
	payload, err := wire.EncodeMetadata(open, s.limits.MaxMetadataBytes)
	if err != nil {
		return wire.Frame{}, err
	}
	return wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload}, nil
}

func durationNanos(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Nanoseconds()
}

func (s *Session) admitStream(stream *Stream) error {
	s.admissionMu.Lock()
	s.streamsMu.Lock()
	if _, exists := s.streams[stream.id]; exists || s.targets[stream.id] != nil {
		s.streamsMu.Unlock()
		s.admissionMu.Unlock()
		return errDuplicateStreamID
	}
	if s.limits.MaxConcurrentStreams <= 0 || len(s.streams)+len(s.targets) >= s.limits.MaxConcurrentStreams {
		s.streamsMu.Unlock()
		s.admissionMu.Unlock()
		return errStreamLimit
	}
	s.streams[stream.id] = stream
	s.activeStreams++
	s.idleSince = time.Time{}
	s.streamsMu.Unlock()
	s.admissionMu.Unlock()
	if s.isDirect() {
		s.opts.Metrics.AddDirectStreams(1)
	}
	return nil
}

func (s *Session) lookupStream(id wire.StreamID) *Stream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.streams[id]
}

func (s *Session) lookupTarget(id wire.StreamID) *targetStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.targets[id]
}

func (s *Session) admitTarget(target *targetStream) error {
	s.admissionMu.Lock()
	// behavior change: incoming OPEN admission is atomic with replacement/drain shutdown.
	if !s.accepting {
		s.admissionMu.Unlock()
		return errSessionClosed
	}
	s.streamsMu.Lock()
	if s.streams[target.id] != nil || s.targets[target.id] != nil {
		s.streamsMu.Unlock()
		s.admissionMu.Unlock()
		return errDuplicateStreamID
	}
	if s.limits.MaxConcurrentStreams <= 0 || len(s.streams)+len(s.targets) >= s.limits.MaxConcurrentStreams {
		s.streamsMu.Unlock()
		s.admissionMu.Unlock()
		return errStreamLimit
	}
	s.targets[target.id] = target
	s.activeStreams++
	s.idleSince = time.Time{}
	s.streamsMu.Unlock()
	s.admissionMu.Unlock()
	if s.isDirect() {
		s.opts.Metrics.AddDirectStreams(1)
	}
	return nil
}

func (s *Session) removeTarget(target *targetStream) {
	removed := false
	s.admissionMu.Lock()
	s.streamsMu.Lock()
	if current := s.targets[target.id]; current == target {
		delete(s.targets, target.id)
		removed = true
		s.activeStreams--
		s.startIdleLocked()
	}
	s.streamsMu.Unlock()
	s.admissionMu.Unlock()
	if removed {
		if s.isDirect() {
			s.opts.Metrics.AddDirectStreams(-1)
		}
		s.tombstones.Add(target.id)
		s.signalActivity()
	}
}

func (s *Session) removeStream(stream *Stream) {
	removed := false
	s.admissionMu.Lock()
	s.streamsMu.Lock()
	if current := s.streams[stream.id]; current == stream && current.generation == stream.generation {
		delete(s.streams, stream.id)
		removed = true
		s.activeStreams--
		s.startIdleLocked()
	}
	s.streamsMu.Unlock()
	s.admissionMu.Unlock()
	if removed {
		if s.isDirect() {
			s.opts.Metrics.AddDirectStreams(-1)
		}
		s.tombstones.Add(stream.id)
		s.signalActivity()
	}
}

func (s *Session) waitStarted(ctx context.Context) error {
	select {
	case <-s.started:
		return nil
	case <-s.done:
		return errSessionClosed
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Session) Cancel(cause error) {
	if cause == nil {
		cause = errSessionClosed
	}
	s.stateMu.Lock()
	s.admissionMu.Lock()
	s.accepting = false
	s.admissionMu.Unlock()
	if s.cancelCause == nil {
		s.cancelCause = cause
	}
	cancel := s.cancel
	preRun := s.state == sessionStateNew
	if preRun {
		s.state = sessionStateDone
	}
	s.stateMu.Unlock()
	if cancel != nil {
		cancel(cause)
	}
	s.startConnClose()
	if preRun {
		s.preRunCloseOnce.Do(func() {
			go func() {
				<-s.connCloseDone
				s.startedOnce.Do(func() { close(s.started) })
				s.doneOnce.Do(func() { close(s.done) })
			}()
		})
	}
}

func (s *Session) startConnClose() {
	s.connCloseOnce.Do(func() {
		go func() {
			if s.conn != nil {
				_ = s.conn.Close()
			}
			close(s.connCloseDone)
		}()
	})
}

func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	s.Cancel(errSessionClosed)
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) finalize(w *fairWriter) {
	s.Cancel(s.cause())
	<-w.Done()
	streams := s.clearStreams()
	targets := s.clearTargets()
	for _, stream := range streams {
		stream.Cancel(s.cause())
	}
	for _, target := range targets {
		target.Cancel(s.cause())
	}
	for _, stream := range streams {
		<-stream.Done()
	}
	for _, target := range targets {
		<-target.Done()
	}
	<-s.connCloseDone
	s.stateMu.Lock()
	s.state = sessionStateDone
	s.stateMu.Unlock()
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *Session) clearTargets() []*targetStream {
	s.admissionMu.Lock()
	s.streamsMu.Lock()
	targets := make([]*targetStream, 0, len(s.targets))
	for _, target := range s.targets {
		targets = append(targets, target)
	}
	clear(s.targets)
	s.activeStreams -= len(targets)
	s.startIdleLocked()
	s.streamsMu.Unlock()
	s.admissionMu.Unlock()
	if s.isDirect() && len(targets) > 0 {
		s.opts.Metrics.AddDirectStreams(-float64(len(targets)))
	}
	return targets
}

func (s *Session) clearStreams() []*Stream {
	s.admissionMu.Lock()
	s.streamsMu.Lock()
	streams := make([]*Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		streams = append(streams, stream)
	}
	clear(s.streams)
	s.activeStreams -= len(streams)
	s.startIdleLocked()
	s.streamsMu.Unlock()
	s.admissionMu.Unlock()
	if s.isDirect() && len(streams) > 0 {
		s.opts.Metrics.AddDirectStreams(-float64(len(streams)))
	}
	return streams
}

func (s *Session) isDirect() bool {
	return s != nil && (s.opts.Direction == SessionDirectionDirectOutgoing || s.opts.Direction == SessionDirectionDirectIncoming)
}

func (s *Session) cause() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.cancelCause != nil {
		return s.cancelCause
	}
	if s.ctx != nil && context.Cause(s.ctx) != nil {
		return context.Cause(s.ctx)
	}
	return errSessionClosed
}

func (s *Session) initializationError() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.initErr
}

func (s *Session) sessionErrorLocked() error {
	if s.initErr != nil {
		return s.initErr
	}
	if s.cancelCause != nil {
		return s.cancelCause
	}
	return errSessionClosed
}

func (s *Session) reserveIncoming(bytes int64) error {
	if bytes <= 0 {
		return errIncomingBudget
	}
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	if bytes > s.limits.MaxQueuedSessionBytes-s.incomingBytes {
		return errIncomingBudget
	}
	s.incomingBytes += bytes
	s.recordBufferedPeakLocked()
	return nil
}

func (s *Session) releaseIncoming(bytes int64) error {
	if bytes < 0 {
		return errIncomingBudget
	}
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	if bytes > s.incomingBytes {
		return errIncomingBudget
	}
	s.incomingBytes -= bytes
	return nil
}

func (s *Session) incomingSize() int64 {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	return s.incomingBytes
}

func (s *Session) bufferedByteSnapshot() (current, peak int64) {
	if s == nil {
		return 0, 0
	}
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	return s.incomingBytes + s.queuedBytes, s.peakBufferedBytes
}

func (s *Session) adjustQueuedBytes(delta int64) {
	s.bufferMu.Lock()
	s.queuedBytes += delta
	s.recordBufferedPeakLocked()
	s.bufferMu.Unlock()
}

func (s *Session) recordBufferedPeakLocked() {
	s.peakBufferedBytes = max(s.peakBufferedBytes, s.incomingBytes+s.queuedBytes)
}

func (s *Session) writeFrame(frame wire.Frame) error {
	message, err := wire.Encode(frame, s.limits)
	if err != nil {
		return err
	}
	if err := s.conn.SetWriteDeadline(s.opts.clock.Now().Add(s.opts.WriteTimeout)); err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, message)
}

func (s *Session) writePing() error {
	deadline := s.opts.clock.Now().Add(s.opts.WriteTimeout)
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return s.conn.WriteControl(websocket.PingMessage, nil, deadline)
}
