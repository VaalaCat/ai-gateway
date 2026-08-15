package tunnel

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"golang.org/x/net/http/httpguts"
)

type apiTargetPhase uint8

const (
	apiTargetWaitingOpen apiTargetPhase = iota
	apiTargetWaitingCommit
	apiTargetReceivingRequest
	apiTargetRequestEnded
	apiTargetTerminal
)

type apiTargetResponsePhase uint8

const (
	apiTargetWaitingHeaders apiTargetResponsePhase = iota
	apiTargetStreamingResponse
	apiTargetResponseEnded
	apiTargetResultSent
)

type apiTargetTerminalOwner uint8

const (
	apiTargetTerminalUnclaimed apiTargetTerminalOwner = iota
	apiTargetTerminalResult
	apiTargetTerminalReset
	apiTargetTerminalLocalFailure
)

type APIRequestEventKind uint8

const (
	APIRequestData APIRequestEventKind = iota + 1
	APIRequestEnd
)

type APIRequestEvent struct {
	Kind     APIRequestEventKind
	Data     []byte
	Trailers wire.Trailers
}

type APITargetStream struct {
	limits          wire.Limits
	send            apiFrameSender
	session         *Session
	ctx             context.Context
	cancel          context.CancelCauseFunc
	stopTTL         context.CancelFunc
	handler         APITargetHandler
	done            chan struct{}
	onDone          func()
	controlContext  func() (context.Context, func())
	reserveIncoming func(int64) error
	releaseIncoming func(int64) error

	sendMu          sync.Mutex
	sendSequence    uint32
	responseOpMu    sync.Mutex // serializes handler response calls across credit waits
	responseMu      sync.Mutex // terminal and response-window linearization barrier
	requestAckMu    sync.Mutex
	stateMu         sync.Mutex
	id              wire.StreamID
	open            wire.Open
	phase           apiTargetPhase
	responsePhase   apiTargetResponsePhase
	receiveSequence uint32
	requestBytes    int64
	responseHeaders wire.Headers
	terminalErr     error
	terminalOwner   apiTargetTerminalOwner
	localCancel     bool

	requestCredit  *creditWindow
	responseCredit *creditWindow
	requests       *apiEventQueue[APIRequestEvent]
	finishOnce     sync.Once
	handlerDone    chan error
	handlerStarted bool
}

type apiTargetStream = APITargetStream
type apiRequestEventKind = APIRequestEventKind
type apiRequestEvent = APIRequestEvent

const (
	apiRequestData = APIRequestData
	apiRequestEnd  = APIRequestEnd
)

func newAPITargetStream(limits wire.Limits, send apiFrameSender) *apiTargetStream {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &apiTargetStream{
		limits: limits, send: send, phase: apiTargetWaitingOpen, responsePhase: apiTargetWaitingHeaders,
		requestCredit: newCreditWindow(limits.InitialStreamWindow), requests: newAPIEventQueue[APIRequestEvent](),
		ctx: ctx, cancel: cancel, stopTTL: func() {}, done: make(chan struct{}), handlerDone: make(chan error, 1),
	}
}

var apiTargetFrameHandlers = map[wire.Type]func(*apiTargetStream, context.Context, wire.Frame) error{
	wire.FrameOpen:         (*apiTargetStream).acceptOpen,
	wire.FrameCommit:       (*apiTargetStream).acceptCommit,
	wire.FrameRequestData:  (*apiTargetStream).acceptRequestData,
	wire.FrameRequestEnd:   (*apiTargetStream).acceptRequestEnd,
	wire.FrameWindowUpdate: (*apiTargetStream).acceptResponseWindow,
	wire.FrameCancel:       (*apiTargetStream).acceptCancellation,
	wire.FrameReset:        (*apiTargetStream).acceptCancellation,
}

func (st *apiTargetStream) acceptFrame(ctx context.Context, frame wire.Frame) error {
	if st.isTargetTerminal() {
		return nil
	}
	st.captureResetStreamID(frame.StreamID)
	if st.targetPhaseIs(apiTargetWaitingOpen) && frame.Type != wire.FrameOpen {
		return st.protocolViolation(ctx, "open")
	}
	if err := st.validateIncomingFrame(frame); err != nil {
		return st.protocolViolation(ctx, err.Stage)
	}
	handler := apiTargetFrameHandlers[frame.Type]
	if handler == nil {
		return st.protocolViolation(ctx, "direction")
	}
	return handler(st, ctx, frame)
}

func (st *apiTargetStream) captureResetStreamID(id wire.StreamID) {
	st.stateMu.Lock()
	if st.id == (wire.StreamID{}) {
		st.id = id
	}
	st.stateMu.Unlock()
}

func (st *apiTargetStream) validateIncomingFrame(frame wire.Frame) *app.HTTPAPIProtocolError {
	st.stateMu.Lock()
	id := st.id
	st.stateMu.Unlock()
	if frame.Version != wire.ProtocolVersion || frame.StreamID == (wire.StreamID{}) || id != frame.StreamID {
		return newHTTPAPIProtocolError("frame")
	}
	limit, err := wire.FramePayloadLimit(frame.Type, st.limits)
	if err != nil || int64(len(frame.Payload)) > limit {
		return newHTTPAPIProtocolError(apiTargetPayloadStage(frame.Type))
	}
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	next, err := wire.NextSequence(st.receiveSequence)
	if err != nil || frame.Sequence != next {
		return newHTTPAPIProtocolError("sequence")
	}
	st.receiveSequence = frame.Sequence
	return nil
}

func (st *apiTargetStream) acceptOpen(ctx context.Context, frame wire.Frame) error {
	if !st.targetPhaseIs(apiTargetWaitingOpen) {
		return st.protocolViolation(ctx, "open")
	}
	var open wire.Open
	if wire.DecodeMetadata(frame.Payload, &open, st.limits.MaxMetadataBytes) != nil {
		return st.protocolViolation(ctx, "open")
	}
	open, err := normalizeAPIWireOpen(open, st.limits.InitialStreamWindow)
	if err != nil {
		return st.protocolViolation(ctx, "open")
	}
	st.stateMu.Lock()
	st.open = open
	st.phase = apiTargetWaitingCommit
	st.responseCredit = newCreditWindow(open.ResponseWindow)
	st.stateMu.Unlock()
	payload, err := wire.EncodeMetadata(wire.Ready{RequestWindow: st.limits.InitialStreamWindow}, st.limits.MaxMetadataBytes)
	if err == nil {
		err = st.sendFrame(ctx, wire.FrameReady, payload)
	}
	if err != nil {
		st.terminate(err)
	}
	return err
}

func (st *apiTargetStream) acceptCommit(ctx context.Context, frame wire.Frame) error {
	if !st.targetPhaseIs(apiTargetWaitingCommit) || len(frame.Payload) != 0 {
		return st.protocolViolation(ctx, "commit")
	}
	st.stateMu.Lock()
	st.phase = apiTargetReceivingRequest
	st.stateMu.Unlock()
	if err := st.sendFrame(ctx, wire.FrameCommitted, nil); err != nil {
		st.terminate(err)
		return err
	}
	st.startHandler()
	return nil
}

func (st *apiTargetStream) acceptRequestData(ctx context.Context, frame wire.Frame) error {
	if !st.targetPhaseIs(apiTargetReceivingRequest) || len(frame.Payload) == 0 {
		return st.protocolViolation(ctx, "request_data")
	}
	size := int64(len(frame.Payload))
	accepted, err := st.requestCredit.TryTake(size)
	if err != nil || !accepted {
		return st.protocolViolation(ctx, "window")
	}
	st.stateMu.Lock()
	if st.requestBytes > math.MaxInt64-size || st.open.BodyLength >= 0 && st.requestBytes+size > st.open.BodyLength {
		st.stateMu.Unlock()
		return st.protocolViolation(ctx, "request_data")
	}
	st.requestBytes += size
	st.stateMu.Unlock()
	if st.reserveIncoming != nil {
		if err = st.reserveIncoming(size); err != nil {
			return st.protocolViolation(ctx, "request_data")
		}
	}
	if !st.requests.Push(apiRequestEvent{Kind: apiRequestData, Data: append([]byte(nil), frame.Payload...)}) && st.releaseIncoming != nil {
		_ = st.releaseIncoming(size)
	}
	return nil
}

func (st *apiTargetStream) acceptRequestEnd(ctx context.Context, frame wire.Frame) error {
	if !st.targetPhaseIs(apiTargetReceivingRequest) {
		return st.protocolViolation(ctx, "request_end")
	}
	var final wire.Trailers
	if wire.DecodeMetadata(frame.Payload, &final, st.limits.MaxMetadataBytes) != nil {
		return st.protocolViolation(ctx, "request_end")
	}
	st.stateMu.Lock()
	declared := append([]string(nil), st.open.API.RequestTrailerKeys...)
	lengthValid := st.open.BodyLength < 0 || st.open.BodyLength == st.requestBytes
	st.stateMu.Unlock()
	normalized, err := normalizeAPIRequestTrailers(final, declared)
	if err != nil || !lengthValid {
		return st.protocolViolation(ctx, "request_end")
	}
	st.stateMu.Lock()
	st.phase = apiTargetRequestEnded
	st.stateMu.Unlock()
	st.requests.Push(apiRequestEvent{Kind: apiRequestEnd, Trailers: normalized})
	return nil
}

func (st *apiTargetStream) acceptResponseWindow(ctx context.Context, frame wire.Frame) error {
	st.responseMu.Lock()
	defer st.responseMu.Unlock()
	if st.isTargetTerminal() {
		return nil
	}
	if !st.targetCommitted() {
		return st.protocolViolationResponseLocked(ctx, "window")
	}
	var update wire.WindowUpdate
	st.stateMu.Lock()
	credit := st.responseCredit
	st.stateMu.Unlock()
	if credit == nil || wire.DecodeMetadata(frame.Payload, &update, st.limits.MaxMetadataBytes) != nil || credit.Add(update.Bytes) != nil {
		return st.protocolViolationResponseLocked(ctx, "window")
	}
	return nil
}

func (st *apiTargetStream) acceptCancellation(ctx context.Context, frame wire.Frame) error {
	cause := error(errStreamClosed)
	if frame.Type == wire.FrameReset {
		var reset wire.Reset
		if wire.DecodeMetadata(frame.Payload, &reset, st.limits.MaxMetadataBytes) != nil || reset.Stage == "" || reset.Code == "" {
			return st.protocolViolation(ctx, "reset")
		}
		cause = &app.HTTPAPIProtocolError{Stage: reset.Stage, Code: reset.Code}
	}
	if st.cancel != nil {
		st.cancel(cause)
	}
	// behavior change: lifetime cancellation may unblock an ordinary publish,
	// but only the terminal-barrier winner may publish the Reset terminal.
	st.responseMu.Lock()
	st.requestAckMu.Lock()
	st.terminateLocked(cause)
	st.requestAckMu.Unlock()
	st.responseMu.Unlock()
	return nil
}

func (st *apiTargetStream) ReceiveRequest(ctx context.Context) (APIRequestEvent, error) {
	if isNilInterface(ctx) {
		return APIRequestEvent{}, newHTTPAPIProtocolError("request_receive")
	}
	event, err := st.requests.Pop(ctx)
	if err != nil || event.Kind != apiRequestData {
		return event, err
	}
	st.requestAckMu.Lock()
	if cause := st.targetTerminalCause(); cause != nil {
		if st.releaseIncoming != nil {
			_ = st.releaseIncoming(int64(len(event.Data)))
		}
		st.requestAckMu.Unlock()
		return APIRequestEvent{}, cause
	}
	bytes := int64(len(event.Data))
	if st.releaseIncoming != nil {
		if err = st.releaseIncoming(bytes); err != nil {
			st.requestAckMu.Unlock()
			st.terminate(err)
			return APIRequestEvent{}, err
		}
	}
	payload, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: bytes}, st.limits.MaxMetadataBytes)
	if err == nil {
		err = st.requestCredit.Add(bytes)
	}
	if err == nil {
		err = st.sendFrame(ctx, wire.FrameWindowUpdate, payload)
	}
	if err != nil {
		st.requestAckMu.Unlock()
		st.terminate(err)
		return APIRequestEvent{}, err
	}
	st.requestAckMu.Unlock()
	return event, nil
}

func (st *apiTargetStream) SendHeaders(ctx context.Context, headers wire.Headers) error {
	st.responseOpMu.Lock()
	defer st.responseOpMu.Unlock()
	st.responseMu.Lock()
	defer st.responseMu.Unlock()
	if cause := st.targetTerminalCause(); cause != nil {
		return cause
	}
	if !st.targetCommitted() || !st.targetResponsePhaseIs(apiTargetWaitingHeaders) {
		return st.protocolViolationResponseLocked(ctx, "headers")
	}
	normalized, err := normalizeAPIResponseHeaders(headers)
	if err != nil {
		return st.protocolViolationResponseLocked(ctx, "headers")
	}
	payload, err := wire.EncodeMetadata(normalized, st.limits.MaxMetadataBytes)
	if err != nil {
		return st.protocolViolationResponseLocked(ctx, "headers")
	}
	st.stateMu.Lock()
	st.responseHeaders = normalized
	st.responsePhase = apiTargetStreamingResponse
	st.stateMu.Unlock()
	// behavior change: commit the phase before the frame becomes peer-visible.
	if err = st.sendFrame(ctx, wire.FrameHeaders, payload); err != nil {
		st.terminateOrdinaryResponsePublishFailureLocked(err)
		return err
	}
	return nil
}

func (st *apiTargetStream) SendResponseData(ctx context.Context, data []byte) error {
	if isNilInterface(ctx) || len(data) == 0 {
		return st.protocolViolation(ctx, "response_data")
	}
	operationCtx, cancel := st.streamContext(ctx)
	defer cancel()
	st.responseOpMu.Lock()
	defer st.responseOpMu.Unlock()
	st.responseMu.Lock()
	if cause := st.targetTerminalCause(); cause != nil {
		st.responseMu.Unlock()
		return cause
	}
	if !st.targetCommitted() || !st.targetResponsePhaseIs(apiTargetStreamingResponse) {
		err := st.protocolViolationResponseLocked(ctx, "response_data")
		st.responseMu.Unlock()
		return err
	}
	st.stateMu.Lock()
	credit := st.responseCredit
	st.stateMu.Unlock()
	st.responseMu.Unlock()
	for len(data) > 0 {
		requested := min(int64(len(data)), st.limits.MaxDataBytes)
		available, err := credit.TakeUpTo(operationCtx, requested, 0)
		if err != nil {
			return err
		}
		chunk := append([]byte(nil), data[:available]...)
		st.responseMu.Lock()
		if cause := st.targetTerminalCause(); cause != nil {
			st.responseMu.Unlock()
			return cause
		}
		if err = st.sendFrame(operationCtx, wire.FrameResponseData, chunk); err != nil {
			st.terminateOrdinaryResponsePublishFailureLocked(err)
			st.responseMu.Unlock()
			return err
		}
		st.responseMu.Unlock()
		data = data[available:]
	}
	return nil
}

func apiTargetPayloadStage(frameType wire.Type) string {
	if frameType == wire.FrameOpen {
		return "open"
	}
	if frameType == wire.FrameRequestData {
		return "request_data"
	}
	return "payload"
}

func (st *apiTargetStream) EndResponse(ctx context.Context, trailers wire.Trailers) error {
	st.responseOpMu.Lock()
	defer st.responseOpMu.Unlock()
	st.responseMu.Lock()
	defer st.responseMu.Unlock()
	if cause := st.targetTerminalCause(); cause != nil {
		return cause
	}
	if !st.targetCommitted() || !st.targetResponsePhaseIs(apiTargetStreamingResponse) {
		return st.protocolViolationResponseLocked(ctx, "response_end")
	}
	normalized, err := normalizeAPIResponseTrailers(trailers, st.declaredResponseTrailers())
	if err != nil {
		return st.protocolViolationResponseLocked(ctx, "response_end")
	}
	payload, err := wire.EncodeMetadata(normalized, st.limits.MaxMetadataBytes)
	if err != nil {
		return st.protocolViolationResponseLocked(ctx, "response_end")
	}
	st.stateMu.Lock()
	st.responsePhase = apiTargetResponseEnded
	st.stateMu.Unlock()
	// behavior change: commit the phase before the frame becomes peer-visible.
	if err = st.sendFrame(ctx, wire.FrameEnd, payload); err != nil {
		st.terminateOrdinaryResponsePublishFailureLocked(err)
		return err
	}
	return nil
}

func (st *apiTargetStream) SendResult(ctx context.Context, result apiattempt.APIExecutionResult) error {
	st.responseOpMu.Lock()
	defer st.responseOpMu.Unlock()
	st.responseMu.Lock()
	defer st.responseMu.Unlock()
	st.requestAckMu.Lock()
	defer st.requestAckMu.Unlock()
	if cause := st.targetTerminalCause(); cause != nil {
		if st.targetResponsePhaseIs(apiTargetResultSent) {
			return newHTTPAPIProtocolError("result")
		}
		return cause
	}
	if !st.targetResponsePhaseIs(apiTargetResponseEnded) {
		return st.protocolViolationLocked(ctx, "result")
	}
	limit, err := wire.FramePayloadLimit(wire.FrameAPIResult, st.limits)
	if err != nil {
		return err
	}
	payload, err := apiattempt.EncodeResultJSONWithin(result, int(limit))
	if err != nil {
		return errors.Join(err, st.protocolViolationLocked(ctx, "result"))
	}
	if !st.claimTerminalLocked(apiTargetTerminalResult) {
		return st.targetTerminalCause()
	}
	st.stateMu.Lock()
	st.responsePhase = apiTargetResultSent
	st.stateMu.Unlock()
	if err = st.sendFrame(ctx, wire.FrameAPIResult, payload); err != nil {
		st.rollbackResultTerminalLocked()
		if !st.ordinaryPublishFailedFromLocalCancel(err) {
			st.terminateLocked(err)
		}
		return err
	}
	st.finishTerminalLocked(apiTargetTerminalResult, errStreamClosed)
	return nil
}

func (st *apiTargetStream) protocolViolation(ctx context.Context, stage string) error {
	st.responseMu.Lock()
	defer st.responseMu.Unlock()
	return st.protocolViolationResponseLocked(ctx, stage)
}

func (st *apiTargetStream) protocolViolationResponseLocked(ctx context.Context, stage string) error {
	st.requestAckMu.Lock()
	defer st.requestAckMu.Unlock()
	return st.protocolViolationLocked(ctx, stage)
}

func (st *apiTargetStream) protocolViolationLocked(_ context.Context, stage string) error {
	err := newHTTPAPIProtocolError(stage)
	st.terminateWithResetLocked(stage, err)
	return err
}

func (st *apiTargetStream) terminate(cause error) {
	st.responseMu.Lock()
	defer st.responseMu.Unlock()
	st.terminateResponseLocked(cause)
}

func (st *apiTargetStream) terminateResponseLocked(cause error) {
	st.requestAckMu.Lock()
	defer st.requestAckMu.Unlock()
	st.terminateLocked(cause)
}

func (st *apiTargetStream) terminateLocked(cause error) bool {
	if !st.claimTerminalLocked(apiTargetTerminalLocalFailure) {
		return false
	}
	return st.finishTerminalLocked(apiTargetTerminalLocalFailure, cause)
}

func (st *apiTargetStream) claimTerminalLocked(owner apiTargetTerminalOwner) bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	if st.phase == apiTargetTerminal || st.terminalOwner != apiTargetTerminalUnclaimed {
		return false
	}
	st.terminalOwner = owner
	return true
}

func (st *apiTargetStream) rollbackResultTerminalLocked() {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	if st.phase == apiTargetTerminal || st.terminalOwner != apiTargetTerminalResult {
		return
	}
	st.terminalOwner = apiTargetTerminalUnclaimed
	st.responsePhase = apiTargetResponseEnded
}

func (st *apiTargetStream) finishTerminalLocked(owner apiTargetTerminalOwner, cause error) bool {
	if cause == nil {
		cause = errStreamClosed
	}
	st.stateMu.Lock()
	if st.phase == apiTargetTerminal || st.terminalOwner != owner {
		st.stateMu.Unlock()
		return false
	}
	st.phase = apiTargetTerminal
	st.terminalErr = cause
	requestCredit := st.requestCredit
	responseCredit := st.responseCredit
	st.stateMu.Unlock()
	if requestCredit != nil {
		requestCredit.Close(cause)
	}
	if responseCredit != nil {
		responseCredit.Close(cause)
	}
	discarded := st.requests.CloseAndDrain(cause, true)
	if st.releaseIncoming != nil {
		for _, event := range discarded {
			if event.Kind == apiRequestData {
				_ = st.releaseIncoming(int64(len(event.Data)))
			}
		}
	}
	if st.cancel != nil {
		st.cancel(cause)
	}
	st.finishOnce.Do(func() { go st.finalize() })
	return true
}

func (st *apiTargetStream) terminateOrdinaryResponsePublishFailureLocked(err error) {
	if st.ordinaryPublishFailedFromLocalCancel(err) {
		return
	}
	st.terminateResponseLocked(err)
}

func (st *apiTargetStream) ordinaryPublishFailedFromLocalCancel(err error) bool {
	st.stateMu.Lock()
	localCancel := st.localCancel
	st.stateMu.Unlock()
	if localCancel {
		cause := context.Cause(st.ctx)
		if cause != nil && errors.Is(err, cause) {
			return true
		}
	}
	return false
}

func (st *apiTargetStream) terminateWithReset(stage string, cause error) {
	st.responseMu.Lock()
	st.requestAckMu.Lock()
	st.terminateWithResetLocked(stage, cause)
	st.requestAckMu.Unlock()
	st.responseMu.Unlock()
}

func (st *apiTargetStream) terminateWithResetLocked(stage string, cause error) bool {
	if !st.claimTerminalLocked(apiTargetTerminalReset) {
		return false
	}
	if st.cancel != nil {
		st.cancel(cause)
	}
	payload, err := wire.EncodeMetadata(wire.Reset{Code: targetResetCode(cause), Stage: stage}, st.limits.MaxMetadataBytes)
	if err == nil {
		ctx, cancel := st.terminalContext()
		_ = st.sendFrame(ctx, wire.FrameReset, payload)
		cancel()
	}
	return st.finishTerminalLocked(apiTargetTerminalReset, cause)
}

func (st *apiTargetStream) startHandler() {
	st.stateMu.Lock()
	if st.handler == nil || st.handlerStarted || st.phase == apiTargetTerminal {
		st.stateMu.Unlock()
		return
	}
	st.handlerStarted = true
	handler := st.handler
	ctx := st.ctx
	st.stateMu.Unlock()
	go func() {
		var err error
		defer func() {
			if recover() != nil {
				err = errTargetPanic
			}
			if err != nil {
				st.terminateWithReset("handler", err)
			} else if !st.targetResponsePhaseIs(apiTargetResultSent) {
				err = newHTTPAPIProtocolError("result")
				st.terminateWithReset("result", err)
			}
			st.handlerDone <- err
		}()
		err = handler.ServeHTTPAPI(ctx, st)
	}()
}

func (st *apiTargetStream) startCommitTimer() {
	if st.session == nil {
		return
	}
	go func() {
		timer := st.session.opts.clock.NewTimer(st.session.opts.OpenCommitTimeout)
		defer timer.Stop()
		select {
		case <-st.ctx.Done():
		case <-timer.Chan():
			if st.targetPhaseIs(apiTargetWaitingCommit) {
				st.Cancel(errOpenCommitTimeout)
			}
		}
	}()
}

func (st *apiTargetStream) terminalContext() (context.Context, func()) {
	if st.controlContext != nil {
		return st.controlContext()
	}
	return context.Background(), func() {}
}

func (st *apiTargetStream) finalize() {
	st.stateMu.Lock()
	started := st.handlerStarted
	st.stateMu.Unlock()
	if started {
		<-st.handlerDone
	}
	if st.stopTTL != nil {
		st.stopTTL()
	}
	if st.onDone != nil {
		st.onDone()
	}
	close(st.done)
}

func (st *apiTargetStream) OpenMetadata() wire.Open {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	open := st.open
	open.Header = map[string][]string(http.Header(open.Header).Clone())
	if open.API != nil {
		meta := cloneAPIAttemptMeta(*open.API)
		open.API = &meta
	}
	return open
}

func (st *apiTargetStream) Cancel(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	st.stateMu.Lock()
	if st.phase != apiTargetTerminal {
		st.localCancel = true
	}
	st.stateMu.Unlock()
	if st.cancel != nil {
		st.cancel(cause)
	}
	st.terminateWithReset("cancel", cause)
}

func (st *apiTargetStream) Done() <-chan struct{} {
	if st == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return st.done
}

func (st *apiTargetStream) sendFrame(ctx context.Context, frameType wire.Type, payload []byte) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if st.send == nil || st.id == (wire.StreamID{}) {
		return errStreamClosed
	}
	next, err := wire.NextSequence(st.sendSequence)
	if err != nil {
		return err
	}
	frame := wire.Frame{Version: wire.ProtocolVersion, Type: frameType, StreamID: st.id, Sequence: next, Payload: payload}
	frameCtx, cancel := st.frameContext(ctx, frameType)
	defer cancel()
	if err = st.send(frameCtx, frame); err != nil {
		return err
	}
	st.sendSequence = next
	return nil
}

func (st *apiTargetStream) frameContext(ctx context.Context, frameType wire.Type) (context.Context, func()) {
	if frameType == wire.FrameCancel || frameType == wire.FrameReset {
		return nonNilContext(ctx), func() {}
	}
	return st.streamContext(ctx)
}

func (st *apiTargetStream) streamContext(ctx context.Context) (context.Context, func()) {
	operationCtx, cancel := context.WithCancelCause(nonNilContext(ctx))
	stop := context.AfterFunc(st.ctx, func() { cancel(context.Cause(st.ctx)) })
	return operationCtx, func() {
		stop()
		cancel(context.Canceled)
	}
}

func (st *apiTargetStream) targetPhaseIs(want apiTargetPhase) bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.phase == want
}

func (st *apiTargetStream) targetCommitted() bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.phase == apiTargetReceivingRequest || st.phase == apiTargetRequestEnded
}

func (st *apiTargetStream) targetResponsePhaseIs(want apiTargetResponsePhase) bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.responsePhase == want
}

func (st *apiTargetStream) isTargetTerminal() bool {
	return st.targetPhaseIs(apiTargetTerminal)
}

func (st *apiTargetStream) targetTerminalCause() error {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	if st.phase != apiTargetTerminal {
		return nil
	}
	return st.terminalErr
}

func (st *apiTargetStream) declaredResponseTrailers() http.Header {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return http.Header(st.responseHeaders.Trailer).Clone()
}

func normalizeAPIRequestTrailers(final wire.Trailers, declared []string) (wire.Trailers, error) {
	if len(final.Dynamic) != 0 {
		return wire.Trailers{}, errStreamProtocol
	}
	keys, err := normalizeAPITrailerKeys(declared)
	if err != nil || validateAPIMetadataHeader(final.Header, true) != nil {
		return wire.Trailers{}, errStreamProtocol
	}
	normalized, names, err := normalizeTrailers(http.Header(final.Header))
	if err != nil {
		return wire.Trailers{}, err
	}
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	for _, name := range names {
		if _, ok := allowed[name]; !ok {
			return wire.Trailers{}, errStreamProtocol
		}
	}
	return wire.Trailers{Header: map[string][]string(normalized)}, nil
}

func normalizeAPIResponseHeaders(headers wire.Headers) (wire.Headers, error) {
	if headers.StatusCode < 200 || headers.StatusCode > 599 || validateAPIMetadataHeader(headers.Header, false) != nil ||
		validateAPIMetadataHeader(headers.Trailer, true) != nil {
		return wire.Headers{}, errStreamProtocol
	}
	ordinary, err := canonicalResponseHeaders(http.Header(headers.Header))
	if err != nil {
		return wire.Headers{}, err
	}
	normalizedTrailers, trailerKeys, err := normalizeTrailers(http.Header(headers.Trailer))
	if err != nil {
		return wire.Headers{}, err
	}
	// behavior change: validate declarations against the unfiltered ordinary
	// response Header so overlap and Connection-nominated names cannot reappear.
	if _, err = normalizeAPITrailerKeysForHeader(
		trailerKeys, ordinary, apiConnectionHeaderNames(ordinary),
	); err != nil {
		return wire.Headers{}, err
	}
	normalizedHeaders, err := normalizeResponseHeaders(ordinary)
	if err != nil {
		return wire.Headers{}, err
	}
	return wire.Headers{
		StatusCode: headers.StatusCode, Header: map[string][]string(normalizedHeaders), Trailer: map[string][]string(normalizedTrailers),
	}, nil
}

func normalizeAPIResponseTrailers(final wire.Trailers, declared http.Header) (wire.Trailers, error) {
	if validateAPIMetadataHeader(final.Header, true) != nil {
		return wire.Trailers{}, errStreamProtocol
	}
	normalized, dynamic, err := normalizeFinalTrailers(final, declared)
	if err != nil {
		return wire.Trailers{}, err
	}
	if len(dynamic) == 0 {
		dynamic = nil
	}
	return wire.Trailers{Header: map[string][]string(normalized), Dynamic: dynamic}, nil
}

func decodeAPIResponseEnd(payload []byte, limit int64, declared http.Header) (wire.Trailers, error) {
	var final wire.Trailers
	if len(payload) > 0 && wire.DecodeMetadata(payload, &final, limit) != nil {
		return wire.Trailers{}, errStreamProtocol
	}
	return normalizeAPIResponseTrailers(final, declared)
}

func normalizeAPITrailerKeys(source []string) ([]string, error) {
	return normalizeAPITrailerKeysForHeader(source, nil, nil)
}

func normalizeAPITrailerKeysForHeader(
	source []string,
	ordinary http.Header,
	connectionNames map[string]struct{},
) ([]string, error) {
	seen := make(map[string]struct{}, len(source))
	keys := make([]string, 0, len(source))
	for _, raw := range source {
		if !httpguts.ValidHeaderFieldName(raw) {
			return nil, errStreamProtocol
		}
		key := http.CanonicalHeaderKey(raw)
		if unsafeAPITrailerKey(key, connectionNames) {
			return nil, errStreamProtocol
		}
		if _, ok := ordinary[key]; ok {
			return nil, errStreamProtocol
		}
		if _, ok := seen[key]; ok {
			return nil, errStreamProtocol
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func validateAPIMetadataHeader(header map[string][]string, trailer bool) error {
	connectionNames := apiConnectionHeaderNames(http.Header(header))
	for name, values := range header {
		if !httpguts.ValidHeaderFieldName(name) || apiInternalHeader(name) || trailer && unsafeAPITrailerKey(name, connectionNames) {
			return errStreamProtocol
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return errStreamProtocol
			}
		}
	}
	return nil
}

func unsafeAPITrailerKey(name string, connectionNames map[string]struct{}) bool {
	return forbiddenTrailerKey(name) || apiInternalHeader(name) || unsafeAPIOpenHeader(name, connectionNames)
}

func apiInternalHeader(name string) bool {
	for _, internal := range []string{
		consts.HeaderXAgentID, consts.HeaderXAgentSecret, consts.HeaderXAgentTag,
		consts.HeaderXAgentAddressTag, consts.HeaderXAgentHop,
		consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID,
	} {
		if strings.EqualFold(name, internal) {
			return true
		}
	}
	return false
}
