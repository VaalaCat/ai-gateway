package tunnel

import (
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type apiFrameSender func(context.Context, wire.Frame) error

type apiSourceRequestPhase uint8

const (
	apiSourceNew apiSourceRequestPhase = iota
	apiSourceOpening
	apiSourceRequestStreaming
	apiSourceRequestEnded
	apiSourceTerminal
)

type apiSourceResponsePhase uint8

const (
	apiSourceWaitingHeaders apiSourceResponsePhase = iota
	apiSourceResponseStreaming
	apiSourceResponseEnded
	apiSourceResponseResult
)

// APIStream is the Source-side Generic API state machine. Session wiring is
// deliberately supplied as a frame sender so API and LLM retain independent
// typed lifecycles while sharing the Tunnel frame and window leaves.
type APIStream struct {
	id     wire.StreamID
	limits wire.Limits
	send   apiFrameSender
	ctx    context.Context
	cancel context.CancelCauseFunc

	sendMu          sync.Mutex
	sendSequence    uint32
	requestMu       sync.Mutex
	responseAckMu   sync.Mutex
	stateMu         sync.Mutex
	requestPhase    apiSourceRequestPhase
	responsePhase   apiSourceResponsePhase
	terminalErr     error
	receiveSequence uint32
	readyReceived   bool
	responseHeaders wire.Headers
	openAPI         apiattempt.APIAttemptMeta

	requestWindow   *creditWindow
	responseCredit  *creditWindow
	responses       *apiEventQueue[app.APIResponseEvent]
	ready           chan error
	committed       chan error
	resetOnce       sync.Once
	done            chan struct{}
	finishOnce      sync.Once
	onDone          func()
	controlContext  func() (context.Context, func())
	reserveIncoming func(int64) error
	releaseIncoming func(int64) error
}

var _ app.HTTPAPIStream = (*APIStream)(nil)

func newAPIStream(id wire.StreamID, limits wire.Limits, send apiFrameSender) *APIStream {
	requestWindow := newCreditWindow(limits.InitialStreamWindow)
	_ = requestWindow.Set(0)
	ctx, cancel := context.WithCancelCause(context.Background())
	return &APIStream{
		id: id, limits: limits, send: send, ctx: ctx, cancel: cancel,
		requestPhase: apiSourceNew, responsePhase: apiSourceWaitingHeaders,
		requestWindow: requestWindow, responseCredit: newCreditWindow(limits.InitialStreamWindow),
		responses: newAPIEventQueue[app.APIResponseEvent](), ready: make(chan error, 1), committed: make(chan error, 1),
		done: make(chan struct{}),
	}
}

func (st *APIStream) Open(ctx context.Context, open app.APIOpen) error {
	if isNilInterface(ctx) {
		return st.localProtocolError("open")
	}
	normalized, err := normalizeAPIOpen(open)
	if err != nil {
		return st.localProtocolError("open")
	}
	open = normalized
	if err := st.beginOpen(open); err != nil {
		return err
	}
	payload, err := wire.EncodeMetadata(apiOpenWire(open, st.limits.InitialStreamWindow), st.limits.MaxMetadataBytes)
	if err != nil {
		st.terminate(err, true)
		return err
	}
	if err = st.sendFrame(ctx, wire.FrameOpen, payload); err != nil {
		st.terminate(err, true)
		return err
	}
	if err = st.waitHandshake(ctx, st.ready); err != nil {
		return err
	}
	if err = st.sendFrame(ctx, wire.FrameCommit, nil); err != nil {
		st.terminate(err, true)
		return err
	}
	return st.waitHandshake(ctx, st.committed)
}

func (st *APIStream) beginOpen(open app.APIOpen) error {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	if st.requestPhase != apiSourceNew || !validateAPIOpen(open) || st.id == (wire.StreamID{}) || st.send == nil {
		return newHTTPAPIProtocolError("open")
	}
	st.requestPhase = apiSourceOpening
	st.openAPI = cloneAPIAttemptMeta(open.API)
	return nil
}

func validateAPIOpen(open app.APIOpen) bool {
	_, err := normalizeAPIOpen(open)
	return err == nil
}

func apiOpenWire(open app.APIOpen, responseWindow int64) wire.Open {
	meta := cloneAPIAttemptMeta(open.API)
	header := open.Header.Clone()
	return wire.Open{
		Method: open.Method, Path: open.Path, Header: map[string][]string(header), BodyLength: open.BodyLength,
		RemainingNanos: durationNanos(open.Remaining), RequestID: open.RequestID, TargetAgentID: open.TargetAgentID,
		RouteID: open.RouteID, Hop: open.Hop, ResponseWindow: responseWindow, API: &meta,
	}
}

func cloneAPIAttemptMeta(meta apiattempt.APIAttemptMeta) apiattempt.APIAttemptMeta {
	meta.RequestTrailerKeys = append([]string(nil), meta.RequestTrailerKeys...)
	return meta
}

func (st *APIStream) waitHandshake(ctx context.Context, signal <-chan error) error {
	select {
	case err := <-signal:
		return err
	case <-ctx.Done():
		err := context.Cause(ctx)
		st.Cancel(err)
		return err
	}
}

func (st *APIStream) SendRequestData(ctx context.Context, data []byte) error {
	if isNilInterface(ctx) || len(data) == 0 {
		return st.protocolViolation(ctx, "request_data")
	}
	st.requestMu.Lock()
	defer st.requestMu.Unlock()
	if !st.sourceRequestPhaseIs(apiSourceRequestStreaming) {
		return st.protocolViolation(ctx, "request_data")
	}
	for len(data) > 0 {
		requested := min(int64(len(data)), st.limits.MaxDataBytes)
		credit, err := st.requestWindow.TakeUpTo(ctx, requested, 0)
		if err != nil {
			return err
		}
		chunk := append([]byte(nil), data[:credit]...)
		if err = st.sendFrame(ctx, wire.FrameRequestData, chunk); err != nil {
			st.terminate(err, true)
			return err
		}
		data = data[credit:]
	}
	return nil
}

func (st *APIStream) EndRequest(ctx context.Context, trailers wire.Trailers) error {
	if isNilInterface(ctx) {
		return st.protocolViolation(ctx, "request_end")
	}
	// behavior change: request serialization always precedes the response
	// acknowledgement barrier, so a failed data publish cannot form an ABBA.
	st.requestMu.Lock()
	defer st.requestMu.Unlock()
	st.responseAckMu.Lock()
	defer st.responseAckMu.Unlock()
	if !st.sourceRequestPhaseIs(apiSourceRequestStreaming) {
		if cause := st.terminalCause(); cause != nil {
			return cause
		}
		return st.protocolViolationLocked(ctx, "request_end")
	}
	st.stateMu.Lock()
	declared := append([]string(nil), st.requestTrailerKeysLocked()...)
	st.stateMu.Unlock()
	normalized, err := normalizeAPIRequestTrailers(trailers, declared)
	if err != nil {
		return st.protocolViolationLocked(ctx, "request_end")
	}
	payload, err := wire.EncodeMetadata(normalized, st.limits.MaxMetadataBytes)
	if err != nil {
		return st.protocolViolationLocked(ctx, "request_end")
	}
	if err = st.sendFrame(ctx, wire.FrameRequestEnd, payload); err != nil {
		st.terminateLocked(err, true)
		return err
	}
	st.stateMu.Lock()
	st.requestPhase = apiSourceRequestEnded
	st.stateMu.Unlock()
	return nil
}

func (st *APIStream) requestTrailerKeysLocked() []string {
	return st.openAPI.RequestTrailerKeys
}

func (st *APIStream) Receive(ctx context.Context) (app.APIResponseEvent, error) {
	if isNilInterface(ctx) {
		return app.APIResponseEvent{}, st.localProtocolError("receive")
	}
	event, err := st.responses.Pop(ctx)
	if err != nil {
		return app.APIResponseEvent{}, err
	}
	if event.Kind != app.APIResponseData {
		return event, nil
	}
	if st.releaseIncoming != nil {
		if err = st.releaseIncoming(int64(len(event.Data))); err != nil {
			st.terminate(err, true)
			return app.APIResponseEvent{}, err
		}
	}
	st.responseAckMu.Lock()
	defer st.responseAckMu.Unlock()
	if st.sourceResponsePhaseIs(apiSourceResponseResult) {
		return event, nil
	}
	if cause := st.terminalCause(); cause != nil {
		return app.APIResponseEvent{}, cause
	}
	bytes := int64(len(event.Data))
	if err = st.responseCredit.Add(bytes); err != nil {
		st.terminateLocked(err, true)
		return app.APIResponseEvent{}, err
	}
	payload, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: bytes}, st.limits.MaxMetadataBytes)
	if err == nil {
		err = st.sendFrame(ctx, wire.FrameWindowUpdate, payload)
	}
	if err != nil {
		st.terminateLocked(err, true)
		return app.APIResponseEvent{}, err
	}
	return event, nil
}

var apiSourceFrameHandlers = map[wire.Type]func(*APIStream, context.Context, wire.Frame) error{
	wire.FrameReady:        (*APIStream).acceptReady,
	wire.FrameCommitted:    (*APIStream).acceptCommitted,
	wire.FrameWindowUpdate: (*APIStream).acceptRequestWindow,
	wire.FrameHeaders:      (*APIStream).acceptHeaders,
	wire.FrameResponseData: (*APIStream).acceptResponseData,
	wire.FrameEnd:          (*APIStream).acceptResponseEnd,
	wire.FrameAPIResult:    (*APIStream).acceptResult,
	wire.FrameCancel:       (*APIStream).acceptCancellation,
	wire.FrameReset:        (*APIStream).acceptCancellation,
}

func (st *APIStream) acceptFrame(ctx context.Context, frame wire.Frame) error {
	if st.isTerminal() {
		return nil
	}
	if err := st.validateIncomingFrame(frame); err != nil {
		return st.protocolViolation(ctx, err.Stage)
	}
	handler := apiSourceFrameHandlers[frame.Type]
	if handler == nil {
		return st.protocolViolation(ctx, "direction")
	}
	return handler(st, ctx, frame)
}

func (st *APIStream) validateIncomingFrame(frame wire.Frame) *app.HTTPAPIProtocolError {
	if frame.Version != wire.ProtocolVersion || frame.StreamID != st.id {
		return newHTTPAPIProtocolError("frame")
	}
	limit, err := wire.FramePayloadLimit(frame.Type, st.limits)
	if err != nil || int64(len(frame.Payload)) > limit {
		return newHTTPAPIProtocolError("payload")
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

func (st *APIStream) acceptReady(_ context.Context, frame wire.Frame) error {
	var ready wire.Ready
	if wire.DecodeMetadata(frame.Payload, &ready, st.limits.MaxMetadataBytes) != nil ||
		ready.RequestWindow <= 0 || ready.RequestWindow > st.limits.InitialStreamWindow || st.requestWindow.Set(ready.RequestWindow) != nil {
		return st.protocolViolation(context.Background(), "window")
	}
	st.stateMu.Lock()
	valid := st.requestPhase == apiSourceOpening && !st.readyReceived
	if valid {
		st.readyReceived = true
	}
	st.stateMu.Unlock()
	if !valid {
		return st.protocolViolation(context.Background(), "ready")
	}
	signalAPIHandshake(st.ready, nil)
	return nil
}

func (st *APIStream) acceptCommitted(_ context.Context, _ wire.Frame) error {
	st.stateMu.Lock()
	valid := st.requestPhase == apiSourceOpening && st.readyReceived
	if valid {
		st.requestPhase = apiSourceRequestStreaming
	}
	st.stateMu.Unlock()
	if !valid {
		return st.protocolViolation(context.Background(), "committed")
	}
	signalAPIHandshake(st.committed, nil)
	return nil
}

func (st *APIStream) acceptRequestWindow(_ context.Context, frame wire.Frame) error {
	var update wire.WindowUpdate
	if !st.sourceRequestActive() || wire.DecodeMetadata(frame.Payload, &update, st.limits.MaxMetadataBytes) != nil ||
		st.requestWindow.Add(update.Bytes) != nil {
		return st.protocolViolation(context.Background(), "window")
	}
	return nil
}

func (st *APIStream) acceptHeaders(_ context.Context, frame wire.Frame) error {
	var headers wire.Headers
	if wire.DecodeMetadata(frame.Payload, &headers, st.limits.MaxMetadataBytes) != nil {
		return st.protocolViolation(context.Background(), "headers")
	}
	normalized, err := normalizeAPIResponseHeaders(headers)
	if err != nil {
		return st.protocolViolation(context.Background(), "headers")
	}
	st.stateMu.Lock()
	valid := st.requestPhase >= apiSourceRequestStreaming && st.requestPhase < apiSourceTerminal &&
		st.responsePhase == apiSourceWaitingHeaders
	if valid {
		st.responsePhase = apiSourceResponseStreaming
		st.responseHeaders = cloneAPIHeaders(normalized)
	}
	st.stateMu.Unlock()
	if !valid {
		return st.protocolViolation(context.Background(), "headers")
	}
	copy := cloneAPIHeaders(normalized)
	st.responses.Push(app.APIResponseEvent{Kind: app.APIResponseHeaders, Headers: &copy})
	return nil
}

func (st *APIStream) acceptResponseData(_ context.Context, frame wire.Frame) error {
	if len(frame.Payload) == 0 || !st.sourceResponsePhaseIs(apiSourceResponseStreaming) {
		return st.protocolViolation(context.Background(), "response_data")
	}
	accepted, err := st.responseCredit.TryTake(int64(len(frame.Payload)))
	if err != nil || !accepted {
		return st.protocolViolation(context.Background(), "window")
	}
	reserved := int64(len(frame.Payload))
	if st.reserveIncoming != nil {
		if err = st.reserveIncoming(reserved); err != nil {
			return st.protocolViolation(context.Background(), "response_data")
		}
	}
	if !st.responses.Push(app.APIResponseEvent{Kind: app.APIResponseData, Data: append([]byte(nil), frame.Payload...)}) && st.releaseIncoming != nil {
		_ = st.releaseIncoming(reserved)
	}
	return nil
}

func (st *APIStream) acceptResponseEnd(_ context.Context, frame wire.Frame) error {
	if !st.sourceResponsePhaseIs(apiSourceResponseStreaming) {
		return st.protocolViolation(context.Background(), "response_end")
	}
	trailers, err := decodeAPIResponseEnd(frame.Payload, st.limits.MaxMetadataBytes, st.declaredResponseTrailers())
	if err != nil {
		return st.protocolViolation(context.Background(), "response_end")
	}
	st.stateMu.Lock()
	st.responsePhase = apiSourceResponseEnded
	st.stateMu.Unlock()
	st.responses.Push(app.APIResponseEvent{Kind: app.APIResponseEnd, Trailers: &trailers})
	return nil
}

func (st *APIStream) acceptResult(_ context.Context, frame wire.Frame) error {
	limit, err := wire.FramePayloadLimit(wire.FrameAPIResult, st.limits)
	if err != nil {
		return st.protocolViolation(context.Background(), "result")
	}
	result, err := apiattempt.DecodeResultJSONWithin(frame.Payload, int(limit))
	if err != nil {
		return st.protocolViolation(context.Background(), "result")
	}
	st.responseAckMu.Lock()
	defer st.responseAckMu.Unlock()
	if !st.sourceResponsePhaseIs(apiSourceResponseEnded) {
		return st.protocolViolationLocked(context.Background(), "result")
	}
	st.stateMu.Lock()
	st.responsePhase = apiSourceResponseResult
	st.stateMu.Unlock()
	st.responses.Push(app.APIResponseEvent{Kind: app.APIResponseResult, Result: &result})
	st.terminateLocked(io.EOF, false)
	return nil
}

func (st *APIStream) acceptCancellation(_ context.Context, frame wire.Frame) error {
	cause := error(errStreamClosed)
	if frame.Type == wire.FrameReset {
		var reset wire.Reset
		if wire.DecodeMetadata(frame.Payload, &reset, st.limits.MaxMetadataBytes) != nil {
			return st.protocolViolation(context.Background(), "reset")
		}
		cause = &app.HTTPAPIProtocolError{Stage: reset.Stage, Code: reset.Code}
	}
	st.terminate(cause, true)
	return cause
}

func (st *APIStream) protocolViolation(ctx context.Context, stage string) error {
	st.responseAckMu.Lock()
	defer st.responseAckMu.Unlock()
	return st.protocolViolationLocked(ctx, stage)
}

func (st *APIStream) protocolViolationLocked(ctx context.Context, stage string) error {
	err := newHTTPAPIProtocolError(stage)
	st.terminateLocked(err, true)
	st.resetOnce.Do(func() {
		payload, encodeErr := wire.EncodeMetadata(wire.Reset{Code: err.Code, Stage: stage}, st.limits.MaxMetadataBytes)
		if encodeErr == nil {
			_ = st.sendFrame(nonNilContext(ctx), wire.FrameReset, payload)
		}
	})
	return err
}

func (st *APIStream) localProtocolError(stage string) error {
	return newHTTPAPIProtocolError(stage)
}

func (st *APIStream) terminate(cause error, discard bool) {
	st.responseAckMu.Lock()
	defer st.responseAckMu.Unlock()
	st.terminateLocked(cause, discard)
}

func (st *APIStream) terminateLocked(cause error, discard bool) {
	if cause == nil {
		cause = errStreamClosed
	}
	st.stateMu.Lock()
	if st.requestPhase == apiSourceTerminal {
		terminalErr := st.terminalErr
		st.stateMu.Unlock()
		if discard {
			st.releaseDiscardedResponses(st.responses.CloseAndDrain(terminalErr, true))
		}
		return
	}
	st.requestPhase = apiSourceTerminal
	st.terminalErr = cause
	st.stateMu.Unlock()
	if st.cancel != nil {
		st.cancel(cause)
	}
	st.requestWindow.Close(cause)
	st.responseCredit.Close(cause)
	st.releaseDiscardedResponses(st.responses.CloseAndDrain(cause, discard))
	signalAPIHandshake(st.ready, cause)
	signalAPIHandshake(st.committed, cause)
	st.finishOnce.Do(func() {
		if st.onDone != nil {
			st.onDone()
		}
		close(st.done)
	})
}

func (st *APIStream) releaseDiscardedResponses(discarded []app.APIResponseEvent) {
	if st.releaseIncoming == nil {
		return
	}
	for _, event := range discarded {
		if event.Kind == app.APIResponseData {
			_ = st.releaseIncoming(int64(len(event.Data)))
		}
	}
}

func (st *APIStream) sendCancel(cause error) {
	payload, _ := wire.EncodeMetadata(wire.Reset{Code: targetResetCode(cause), Stage: "cancel"}, st.limits.MaxMetadataBytes)
	ctx, cancel := st.terminalContext()
	_ = st.sendFrame(ctx, wire.FrameCancel, payload)
	cancel()
}

func (st *APIStream) terminalContext() (context.Context, func()) {
	if st.controlContext != nil {
		return st.controlContext()
	}
	return context.Background(), func() {}
}

func (st *APIStream) Cancel(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	if st.isTerminal() {
		// behavior change: Close after Result still discards retained events and
		// releases their Session incoming budget.
		st.responseAckMu.Lock()
		st.terminateLocked(cause, true)
		st.responseAckMu.Unlock()
		return
	}
	if st.cancel != nil {
		st.cancel(cause)
	}
	// behavior change: cancel the stream lifetime before waiting for the
	// response acknowledgement barrier so an ordinary blocked send can unwind.
	st.resetOnce.Do(func() { st.sendCancel(cause) })
	st.responseAckMu.Lock()
	defer st.responseAckMu.Unlock()
	st.terminateLocked(cause, true)
}

func (st *APIStream) Close() error {
	if st == nil {
		return nil
	}
	st.Cancel(errStreamClosed)
	<-st.done
	return nil
}

func (st *APIStream) Done() <-chan struct{} {
	if st == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return st.done
}

func (st *APIStream) sendFrame(ctx context.Context, frameType wire.Type, payload []byte) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if st.send == nil {
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

func (st *APIStream) frameContext(ctx context.Context, frameType wire.Type) (context.Context, func()) {
	if frameType == wire.FrameCancel || frameType == wire.FrameReset {
		return nonNilContext(ctx), func() {}
	}
	operationCtx, cancel := context.WithCancelCause(nonNilContext(ctx))
	stop := context.AfterFunc(st.ctx, func() { cancel(context.Cause(st.ctx)) })
	return operationCtx, func() {
		stop()
		cancel(context.Canceled)
	}
}

func (st *APIStream) sourceRequestPhaseIs(want apiSourceRequestPhase) bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.requestPhase == want
}

func (st *APIStream) sourceRequestActive() bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.requestPhase == apiSourceRequestStreaming || st.requestPhase == apiSourceRequestEnded
}

func (st *APIStream) sourceResponsePhaseIs(want apiSourceResponsePhase) bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.responsePhase == want
}

func (st *APIStream) isTerminal() bool {
	return st.sourceRequestPhaseIs(apiSourceTerminal)
}

func (st *APIStream) terminalCause() error {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	if st.requestPhase != apiSourceTerminal {
		return nil
	}
	return st.terminalErr
}

func (st *APIStream) declaredResponseTrailers() http.Header {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return http.Header(st.responseHeaders.Trailer).Clone()
}

func signalAPIHandshake(signal chan<- error, err error) {
	select {
	case signal <- err:
	default:
	}
}

func newHTTPAPIProtocolError(stage string) *app.HTTPAPIProtocolError {
	return &app.HTTPAPIProtocolError{Stage: stage, Code: wire.ErrorCodeRelayProtocol}
}

func nonNilContext(ctx context.Context) context.Context {
	if isNilInterface(ctx) {
		return context.Background()
	}
	return ctx
}

type apiEventQueue[T any] struct {
	mu       sync.Mutex
	items    []T
	closed   bool
	closeErr error
	notify   chan struct{}
}

func newAPIEventQueue[T any]() *apiEventQueue[T] {
	return &apiEventQueue[T]{notify: make(chan struct{})}
}

func (q *apiEventQueue[T]) Push(event T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, event)
	q.signalLocked()
	return true
}

func (q *apiEventQueue[T]) Pop(ctx context.Context) (T, error) {
	var zero T
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			event := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return event, nil
		}
		if q.closed {
			err := q.closeErr
			q.mu.Unlock()
			return zero, err
		}
		notify := q.notify
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return zero, context.Cause(ctx)
		case <-notify:
		}
	}
}

func (q *apiEventQueue[T]) Close(cause error, discard bool) {
	_ = q.CloseAndDrain(cause, discard)
}

func (q *apiEventQueue[T]) CloseAndDrain(cause error, discard bool) []T {
	q.mu.Lock()
	var discarded []T
	if !q.closed {
		q.closed = true
		q.closeErr = cause
		q.signalLocked()
	}
	if discard {
		discarded = q.items
		q.items = nil
	}
	q.mu.Unlock()
	return discarded
}

func (q *apiEventQueue[T]) signalLocked() {
	close(q.notify)
	q.notify = make(chan struct{})
}
