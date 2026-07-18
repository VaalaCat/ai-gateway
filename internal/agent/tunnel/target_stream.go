package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type targetPhase uint8

const (
	targetWaitingCommit targetPhase = iota
	targetReceivingRequest
	targetRequestEnded
	targetTerminal
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

	sequenceMu       sync.Mutex
	sequence         uint32
	resetOnce        sync.Once
	deliveryStopOnce sync.Once
	pipeReader       *io.PipeReader
	pipeWriter       *io.PipeWriter
	stopPipe         func() bool
	workerDone       chan error
}

func (s *Session) handleTargetOpen(ctx context.Context, frame wire.Frame) {
	var open wire.Open
	if frame.Sequence != 1 || wire.DecodeMetadata(frame.Payload, &open, s.limits.MaxMetadataBytes) != nil ||
		s.opts.TargetHandler == nil || s.opts.TargetHandler.ValidateOpen(open) != nil ||
		open.ResponseWindow > wire.MaxV1StreamWindowBytes {
		s.rejectTargetOpen(ctx, frame.StreamID, "open")
		return
	}
	target := newTargetStream(s, frame.StreamID, open)
	if err := s.admitTarget(target); err != nil {
		target.stopTTL()
		target.cancel(err)
		s.rejectTargetOpen(ctx, frame.StreamID, "admission")
		return
	}
	go target.run()
}

func (s *Session) rejectTargetOpen(ctx context.Context, id wire.StreamID, stage string) {
	payload, _ := wire.EncodeMetadata(wire.Reset{Code: wire.ErrorCodeRelayProtocol, Stage: stage}, s.limits.MaxMetadataBytes)
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
				st.sendReset("handler", err)
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
	req, err := st.session.opts.TargetHandler.BuildRequest(st.ctx, st.open, st.id, reader)
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
		if result == nil {
			result = response.finish()
		}
		_ = req.Body.Close()
		done <- result
	}()
	st.session.opts.TargetHandler.ServeHTTP(response, req)
	result = response.resetError()
}

func (st *targetStream) sendResponseFrame(ctx context.Context, frameType wire.Type, payload []byte) error {
	if frameType == wire.FrameResponseData {
		if err := st.responseCredit.Take(ctx, int64(len(payload)), st.session.opts.WindowStallTimeout); err != nil {
			return err
		}
	}
	return st.enqueue(ctx, frameType, payload, false)
}

func (st *targetStream) enqueue(ctx context.Context, frameType wire.Type, payload []byte, replace bool) error {
	st.sequenceMu.Lock()
	defer st.sequenceMu.Unlock()
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
	switch {
	case errors.Is(cause, errWindowStalled):
		return wire.ErrorCodeStreamWindowTimeout
	case errors.Is(cause, context.DeadlineExceeded):
		return wire.ErrorCodeRequestDeadline
	case errors.Is(cause, context.Canceled):
		return wire.ErrorCodeRequestCancelled
	case errors.Is(cause, errStreamClosed):
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
