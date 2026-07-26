package tunnel

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type targetPhase uint8

const (
	targetWaitingCommit targetPhase = iota
	targetReceivingRequest
	targetRequestEnded
	targetTerminal
)

type targetResponsePhase uint8

const (
	targetResponseWaitingHeaders targetResponsePhase = iota
	targetResponseStreaming
	targetResponseResultEnqueued
	targetResponseFailed
	targetResponseEnded
)

type targetFrame struct {
	frame    wire.Frame
	reserved int64
}

type targetStream struct {
	session *Session
	id      wire.StreamID
	open    wire.Open
	ctx     context.Context
	cancel  context.CancelCauseFunc
	stopTTL context.CancelFunc

	inbound      chan targetFrame
	done         chan struct{}
	deliveryStop chan struct{}
	closed       atomic.Bool
	deliveries   operationTracker

	phase          targetPhase
	receiveSeq     uint32
	requestBytes   int64
	requestCredit  int64
	responseCredit *creditWindow
	committed      bool

	sequenceMu    sync.Mutex
	sequence      uint32
	responsePhase targetResponsePhase
	resultWritten bool
	resultErr     error

	resetOnce        sync.Once
	deliveryStopOnce sync.Once
	pipeReader       *io.PipeReader
	pipeWriter       *io.PipeWriter
	stopPipe         func() bool
	workerDone       chan error
}

func (s *Session) handleTargetOpen(ctx context.Context, frame wire.Frame) {
	var open wire.Open
	if frame.Sequence != 1 || wire.DecodeMetadata(frame.Payload, &open, s.limits.MaxMetadataBytes) != nil || open.ResponseWindow > wire.MaxV2StreamWindowBytes {
		s.rejectTargetOpen(ctx, frame.StreamID, "open", errStreamProtocol)
		return
	}
	if err := s.admitTargetOpen(&open); err != nil {
		s.rejectTargetOpen(ctx, frame.StreamID, pathFailureStage(err, "open"), err)
		return
	}
	target := newTargetStream(s, frame.StreamID, open)
	if err := s.admitTarget(target); err != nil {
		target.stopTTL()
		target.cancel(err)
		s.rejectTargetOpen(ctx, frame.StreamID, pathFailureStage(err, "admission"), err)
		return
	}
	go target.run()
}

func (s *Session) admitTargetOpen(open *wire.Open) error {
	if s == nil || open == nil || s.opts.TargetHandler == nil || s.opts.Direction == SessionDirectionDirectOutgoing {
		return errStreamProtocol
	}
	if s.opts.Direction == SessionDirectionDirectIncoming {
		open.SourceAgentID = s.opts.BoundSourceAgentID
		// behavior change: each new DirectIncoming OPEN rechecks local target status.
		if open.SourceAgentID == "" || !s.opts.Now().Before(s.opts.AdmissionDeadline) ||
			s.opts.SourceEnabled == nil || !s.opts.SourceEnabled(open.SourceAgentID) ||
			s.opts.TargetStatusEnabled == nil || !s.opts.TargetStatusEnabled() {
			return errTargetUnavailable
		}
	}
	return s.opts.TargetHandler.ValidateOpen(*open, s.opts.IngressKind)
}

func (s *Session) rejectTargetOpen(ctx context.Context, id wire.StreamID, stage string, cause error) {
	payload, _ := wire.EncodeMetadata(wire.Reset{Code: targetResetCode(cause), Stage: stage}, s.limits.MaxMetadataBytes)
	_ = s.writer.Replace(ctx, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 1, Payload: payload,
	}, nil)
	s.tombstones.Add(id)
	s.writer.Forget(id)
}

func newTargetStream(session *Session, id wire.StreamID, open wire.Open) *targetStream {
	parent := session.ctx
	stopTTL := context.CancelFunc(func() {})
	if open.RemainingNanos > 0 {
		parent, stopTTL = context.WithDeadlineCause(parent, session.opts.Now().Add(time.Duration(open.RemainingNanos)), context.DeadlineExceeded)
	}
	ctx, cancel := context.WithCancelCause(parent)
	return &targetStream{
		session: session, id: id, open: open, ctx: ctx, cancel: cancel, stopTTL: stopTTL,
		inbound: make(chan targetFrame, 16), done: make(chan struct{}), deliveryStop: make(chan struct{}), phase: targetWaitingCommit,
		receiveSeq: 1, requestCredit: session.limits.InitialStreamWindow,
		responseCredit: newCreditWindowWithClock(open.ResponseWindow, session.opts.clock), deliveries: newOperationTracker(),
	}
}

func (st *targetStream) run() {
	defer st.finalize()
	if err := st.sendReady(); err != nil {
		return
	}
	timer := st.session.opts.clock.NewTimer(st.session.opts.OpenCommitTimeout)
	defer timer.Stop()
	for {
		select {
		case <-st.ctx.Done():
			st.sendReset("cancel", context.Cause(st.ctx))
			return
		case <-timer.Chan():
			if !st.committed {
				st.Cancel(errOpenCommitTimeout)
			}
		case incoming := <-st.inbound:
			if st.handleFrame(incoming) {
				return
			}
		case err := <-st.workerDone:
			st.workerDone = nil
			if err != nil {
				stage := "handler"
				if st.attemptResultFailed() {
					stage = "attempt_result"
				}
				st.sendReset(stage, err)
			}
			return
		}
	}
}

func (st *targetStream) sendReady() error {
	payload, err := wire.EncodeMetadata(wire.Ready{RequestWindow: st.requestCredit}, st.session.limits.MaxMetadataBytes)
	if err != nil {
		return err
	}
	return st.enqueue(st.ctx, wire.FrameReady, payload, false)
}

func (st *targetStream) handleFrame(incoming targetFrame) bool {
	if incoming.reserved > 0 {
		defer func() { _ = st.session.releaseIncoming(incoming.reserved) }()
	}
	frame := incoming.frame
	next, err := wire.NextSequence(st.receiveSeq)
	if err != nil || frame.Sequence != next {
		return st.protocolViolation("sequence")
	}
	st.receiveSeq = frame.Sequence
	switch frame.Type {
	case wire.FrameCommit:
		return st.handleCommit()
	case wire.FrameRequestData:
		return st.handleRequestData(frame.Payload)
	case wire.FrameRequestEnd:
		return st.handleRequestEnd()
	case wire.FrameWindowUpdate:
		return st.handleWindowUpdate(frame.Payload)
	case wire.FrameCancel, wire.FrameReset:
		st.phase = targetTerminal
		st.Cancel(errStreamClosed)
		return true
	default:
		return st.protocolViolation("phase")
	}
}

func (st *targetStream) handleCommit() bool {
	if st.phase != targetWaitingCommit || st.committed {
		return st.protocolViolation("phase")
	}
	if err := st.enqueue(st.ctx, wire.FrameCommitted, nil, false); err != nil {
		st.Cancel(err)
		return true
	}
	st.committed = true
	st.phase = targetReceivingRequest
	if err := st.startWorker(); err != nil {
		st.sendReset("request", err)
		return true
	}
	return false
}

func (st *targetStream) handleRequestData(payload []byte) bool {
	if st.phase != targetReceivingRequest || len(payload) == 0 || int64(len(payload)) > st.requestCredit {
		return st.protocolViolation("request_data")
	}
	nextBytes := st.requestBytes + int64(len(payload))
	if st.open.BodyLength >= 0 && nextBytes > st.open.BodyLength {
		return st.protocolViolation("body_length")
	}
	st.requestCredit -= int64(len(payload))
	n, err := st.pipeWriter.Write(payload)
	if err != nil || n != len(payload) {
		st.sendReset("request_body", err)
		return true
	}
	st.requestBytes = nextBytes
	payload, err = wire.EncodeMetadata(wire.WindowUpdate{Bytes: int64(n)}, st.session.limits.MaxMetadataBytes)
	if err != nil || st.enqueue(st.ctx, wire.FrameWindowUpdate, payload, false) != nil {
		st.Cancel(err)
		return true
	}
	st.requestCredit += int64(n)
	return false
}

func (st *targetStream) handleRequestEnd() bool {
	if st.phase != targetReceivingRequest || (st.open.BodyLength >= 0 && st.requestBytes != st.open.BodyLength) {
		return st.protocolViolation("body_length")
	}
	st.phase = targetRequestEnded
	if err := st.pipeWriter.Close(); err != nil {
		st.sendReset("request_body", err)
		return true
	}
	return false
}

func (st *targetStream) handleWindowUpdate(payload []byte) bool {
	if !st.committed || st.phase == targetTerminal {
		return st.protocolViolation("phase")
	}
	var update wire.WindowUpdate
	if wire.DecodeMetadata(payload, &update, st.session.limits.MaxMetadataBytes) != nil || st.responseCredit.Add(update.Bytes) != nil {
		return st.protocolViolation("window")
	}
	return false
}

func (st *targetStream) startWorker() error {
	reader, writer := io.Pipe()
	req, err := st.session.opts.TargetHandler.BuildRequest(st.ctx, st.open, st.id, reader, st.session.opts.IngressKind)
	if err != nil {
		_ = reader.CloseWithError(err)
		_ = writer.CloseWithError(err)
		return err
	}
	st.pipeReader, st.pipeWriter = reader, writer
	st.stopPipe = context.AfterFunc(st.ctx, func() {
		cause := context.Cause(st.ctx)
		_ = reader.CloseWithError(cause)
		_ = writer.CloseWithError(cause)
	})
	if st.open.Attempt != nil {
		req = req.WithContext(attemptwire.WithAttemptResultWriter(req.Context(), st))
	}
	response := newTunnelResponseWriter(st.ctx, st.session.limits.MaxMetadataBytes, st.session.limits.MaxDataBytes, st.sendResponseFrame)
	done := make(chan error, 1)
	st.workerDone = done
	go st.executeHandler(response, req, done)
	return nil
}

func (st *targetStream) executeHandler(response *TunnelResponseWriter, req *http.Request, done chan<- error) {
	var result error
	defer func() {
		if recover() != nil {
			result = errTargetPanic
		}
		state := st.attemptResultState()
		if result != nil {
			if resultErr := state.validate(); resultErr != nil {
				result = resultErr
			}
		} else {
			result = response.finish(state)
		}
		_ = req.Body.Close()
		done <- result
	}()
	st.session.opts.TargetHandler.ServeHTTP(response, req)
	result = response.resetError()
}

var (
	errAttemptResultAlreadyWritten = errors.New("agent tunnel: attempt result already written")
	errAttemptResultBeforeHeaders  = errors.New("agent tunnel: attempt result written before response headers")
	errAttemptResultMissing        = errors.New("agent tunnel: attempt result missing")
)

var _ attemptwire.AttemptResultWriter = (*targetStream)(nil)

func (st *targetStream) WriteAttemptResult(result attemptwire.AttemptProxyResult) error {
	if st == nil {
		return errAttemptResultMissing
	}
	st.sequenceMu.Lock()
	defer st.sequenceMu.Unlock()
	if st.resultWritten {
		if st.resultErr == nil {
			st.resultErr = errAttemptResultAlreadyWritten
		}
		st.responsePhase = targetResponseFailed
		return errAttemptResultAlreadyWritten
	}
	st.resultWritten = true
	if st.responsePhase != targetResponseStreaming {
		st.resultErr = errAttemptResultBeforeHeaders
		st.responsePhase = targetResponseFailed
		return st.resultErr
	}
	resultLimit, limitErr := wire.FramePayloadLimit(wire.FrameAttemptResult, st.session.limits)
	if limitErr != nil {
		st.resultErr = limitErr
		st.responsePhase = targetResponseFailed
		st.observeDirectResultEncodeFailure(limitErr)
		return limitErr
	}
	payload, encodeErr := attemptwire.EncodeResultJSONWithin(result, int(resultLimit))
	if encodeErr != nil {
		st.resultErr = encodeErr
		st.responsePhase = targetResponseFailed
		st.observeDirectResultEncodeFailure(encodeErr)
		return encodeErr
	}
	enqueueErr := st.enqueueLocked(st.ctx, wire.FrameAttemptResult, payload, false)
	st.resultErr = enqueueErr
	if enqueueErr != nil {
		st.responsePhase = targetResponseFailed
		return enqueueErr
	}
	st.responsePhase = targetResponseResultEnqueued
	return nil
}

func (st *targetStream) observeDirectResultEncodeFailure(err error) {
	if st.session == nil || st.session.opts.Direction != SessionDirectionDirectIncoming {
		return
	}
	kind := pkgmetrics.ResultInvalid
	if errors.Is(err, attemptwire.ErrResultTooLarge) {
		kind = pkgmetrics.ResultTooLarge
	}
	st.session.opts.Metrics.ObserveDirectResultFrame(kind, pkgmetrics.DirectReasonProtocol)
	st.session.opts.directLogs.ResultProtocolFailed(directLogEvent{
		SourceAgentID: st.session.opts.directSourceAgentID, TargetAgentID: st.session.opts.directTargetAgentID,
		Stage: "result", ReasonCode: string(kind), SessionGeneration: st.session.generation,
		StreamID: hex.EncodeToString(st.id[:]), TargetInbound: true, ResultKind: string(kind),
	}, "", err)
}

func (st *targetStream) attemptResultState() attemptResultState {
	if st == nil {
		return attemptResultState{required: true, err: errAttemptResultMissing}
	}
	st.sequenceMu.Lock()
	defer st.sequenceMu.Unlock()
	return attemptResultState{
		required: st.open.Attempt != nil,
		written:  st.resultWritten,
		err:      st.resultErr,
	}
}

func (st *targetStream) attemptResultFailed() bool {
	state := st.attemptResultState()
	return state.required && (!state.written || state.err != nil)
}

func (st *targetStream) sendResponseFrame(ctx context.Context, frameType wire.Type, payload []byte) error {
	if frameType == wire.FrameResponseData {
		if err := st.responseCredit.Take(ctx, int64(len(payload)), st.session.opts.WindowStallTimeout); err != nil {
			return err
		}
	}
	st.sequenceMu.Lock()
	defer st.sequenceMu.Unlock()
	if !st.responseFrameAllowedLocked(frameType) {
		return errStreamProtocol
	}
	if err := st.enqueueLocked(ctx, frameType, payload, false); err != nil {
		if frameType == wire.FrameHeaders {
			st.responsePhase = targetResponseWaitingHeaders
		}
		return err
	}
	st.advanceResponsePhaseLocked(frameType)
	return nil
}

func (st *targetStream) enqueue(ctx context.Context, frameType wire.Type, payload []byte, replace bool) error {
	st.sequenceMu.Lock()
	defer st.sequenceMu.Unlock()
	return st.enqueueLocked(ctx, frameType, payload, replace)
}

func (st *targetStream) enqueueLocked(ctx context.Context, frameType wire.Type, payload []byte, replace bool) error {
	next, err := wire.NextSequence(st.sequence)
	if err != nil {
		return err
	}
	frame := wire.Frame{Version: wire.ProtocolVersion, Type: frameType, StreamID: st.id, Sequence: next, Payload: payload}
	if replace {
		return st.session.writer.Replace(ctx, frame, func(sequence uint32) { st.sequence = sequence })
	}
	return st.session.writer.Enqueue(ctx, frame, func() { st.sequence = next })
}

func (st *targetStream) responseFrameAllowedLocked(frameType wire.Type) bool {
	switch frameType {
	case wire.FrameHeaders:
		return st.responsePhase == targetResponseWaitingHeaders
	case wire.FrameResponseData:
		return st.responsePhase == targetResponseStreaming
	case wire.FrameEnd:
		if st.open.Attempt != nil {
			return st.responsePhase == targetResponseResultEnqueued
		}
		return st.responsePhase == targetResponseStreaming
	default:
		return false
	}
}

func (st *targetStream) advanceResponsePhaseLocked(frameType wire.Type) {
	switch frameType {
	case wire.FrameHeaders:
		st.responsePhase = targetResponseStreaming
	case wire.FrameEnd:
		st.responsePhase = targetResponseEnded
	}
}

func (st *targetStream) protocolViolation(stage string) bool {
	st.sendReset(stage, errStreamProtocol)
	st.Cancel(errStreamProtocol)
	return true
}

func (st *targetStream) sendReset(stage string, cause error) {
	st.resetOnce.Do(func() {
		code := targetResetCode(cause)
		payload, _ := wire.EncodeMetadata(wire.Reset{Code: code, Stage: stage, Committed: st.committed}, st.session.limits.MaxMetadataBytes)
		controlCtx, cancel := withClockTimeoutCause(st.session.ctx, st.session.opts.clock, st.session.opts.WriteTimeout, errControlSendTimeout)
		_ = st.enqueue(controlCtx, wire.FrameReset, payload, true)
		cancel()
	})
}

func targetResetCode(cause error) string {
	var failure interface{ ReasonCode() string }
	if errors.As(cause, &failure) && consts.IsPublicRouteErrorCode(failure.ReasonCode()) {
		return failure.ReasonCode()
	}
	switch {
	case errors.Is(cause, errWindowStalled):
		return wire.ErrorCodeStreamWindowTimeout
	case errors.Is(cause, context.DeadlineExceeded):
		return wire.ErrorCodeRequestDeadline
	case errors.Is(cause, context.Canceled):
		return wire.ErrorCodeRequestCancelled
	case errors.Is(cause, errStreamClosed), errors.Is(cause, errSessionClosed):
		return wire.ErrorCodeSessionClosed
	default:
		return wire.ErrorCodeRelayProtocol
	}
}

func (st *targetStream) Cancel(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	st.cancel(cause)
}

func (st *targetStream) finalize() {
	waitDeliveries := st.deliveries.Stop()
	st.closed.Store(true)
	st.deliveryStopOnce.Do(func() { close(st.deliveryStop) })
	st.cancel(errStreamClosed)
	<-waitDeliveries
	st.releaseBufferedInbound()
	st.responseCredit.Close(context.Cause(st.ctx))
	if st.stopPipe != nil {
		st.stopPipe()
	}
	if st.pipeWriter != nil {
		_ = st.pipeWriter.CloseWithError(context.Cause(st.ctx))
	}
	if st.pipeReader != nil {
		_ = st.pipeReader.CloseWithError(context.Cause(st.ctx))
	}
	if st.workerDone != nil {
		<-st.workerDone
	}
	st.stopTTL()
	st.session.writer.Forget(st.id)
	st.session.removeTarget(st)
	close(st.done)
}

func (st *targetStream) releaseBufferedInbound() {
	for {
		select {
		case incoming := <-st.inbound:
			if incoming.reserved > 0 {
				_ = st.session.releaseIncoming(incoming.reserved)
			}
		default:
			return
		}
	}
}

func (st *targetStream) Done() <-chan struct{} { return st.done }
