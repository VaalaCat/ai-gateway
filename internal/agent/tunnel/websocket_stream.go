package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type webSocketPhase uint8

const (
	webSocketNew webSocketPhase = iota
	webSocketOpening
	webSocketCommitting
	webSocketActive
	webSocketTerminal
)

// WebSocketStream is the source-side Generic API WebSocket transport. It
// forwards event boundaries exactly while data is split only at the tunnel
// frame limit.
type WebSocketStream struct {
	id     wire.StreamID
	limits wire.Limits
	send   apiFrameSender

	sendMu            sync.Mutex // serializes only frame sequence/write
	eventMu           sync.Mutex // serializes logical message transitions
	stateMu           sync.Mutex
	sendSequence      uint32
	receiveSeq        uint32
	phase             webSocketPhase
	terminalErr       error
	ready             chan error
	committed         chan error
	readyReceived     bool
	committedReceived bool
	commitSent        bool
	accepted          app.WebSocketAccepted
	localClosed       bool
	remoteClosed      bool
	beforeDataWrite   func()
	events            *apiEventQueue[app.WebSocketEvent]
	results           *apiEventQueue[apiattempt.APIExecutionResult]
	sendCredit        *creditWindow
	receiveCredit     *creditWindow

	outgoingMessage uint64
	incomingMessage uint64
	outgoingOpen    bool
	incomingOpen    bool
	finishOnce      sync.Once
	onDone          func()
}

var _ app.WebSocketAPIStream = (*WebSocketStream)(nil)

func newWebSocketStream(id wire.StreamID, limits wire.Limits, send apiFrameSender) *WebSocketStream {
	sendCredit := newCreditWindow(limits.InitialStreamWindow)
	_ = sendCredit.Set(0)
	return &WebSocketStream{
		id: id, limits: limits, send: send, phase: webSocketNew, ready: make(chan error, 1), committed: make(chan error, 1),
		events: newAPIEventQueue[app.WebSocketEvent](), results: newAPIEventQueue[apiattempt.APIExecutionResult](), sendCredit: sendCredit,
		receiveCredit: newCreditWindow(limits.InitialStreamWindow),
	}
}

func (st *WebSocketStream) Open(ctx context.Context, open app.WebSocketOpen) (app.WebSocketAccepted, error) {
	if ctx == nil || !isValidWebSocketOpen(open) {
		return app.WebSocketAccepted{}, st.failProtocol(ctx, "open")
	}
	st.stateMu.Lock()
	if st.phase != webSocketNew || st.id == (wire.StreamID{}) || st.send == nil {
		st.stateMu.Unlock()
		return app.WebSocketAccepted{}, st.failProtocol(ctx, "open")
	}
	st.phase = webSocketOpening
	st.stateMu.Unlock()
	api := cloneAPIAttemptMeta(open.API)
	payload, err := wire.EncodeMetadata(wire.Open{
		Method: http.MethodGet, Path: open.Path, Header: map[string][]string(open.Header.Clone()),
		RemainingNanos: durationNanos(open.Remaining), RequestID: open.RequestID, TargetAgentID: open.TargetAgentID,
		RouteID: open.RouteID, Hop: open.Hop, ResponseWindow: st.limits.InitialStreamWindow, API: &api,
		WebSocket: &wire.WebSocketOpen{ResponseWindow: st.limits.InitialStreamWindow},
	}, st.limits.MaxMetadataBytes)
	if err != nil {
		st.terminate(err, true)
		return app.WebSocketAccepted{}, err
	}
	if err = st.sendFrame(ctx, wire.FrameOpen, payload); err != nil {
		st.terminate(err, true)
		return app.WebSocketAccepted{}, err
	}
	select {
	case err = <-st.ready:
		if err != nil {
			return app.WebSocketAccepted{}, err
		}
	case <-ctx.Done():
		st.terminate(context.Cause(ctx), true)
		return app.WebSocketAccepted{}, context.Cause(ctx)
	}
	if err = st.publishCommit(ctx); err != nil {
		st.terminate(err, true)
		return app.WebSocketAccepted{}, err
	}
	select {
	case err = <-st.committed:
		if err != nil {
			return app.WebSocketAccepted{}, err
		}
	case <-ctx.Done():
		st.terminate(context.Cause(ctx), true)
		return app.WebSocketAccepted{}, context.Cause(ctx)
	}
	st.stateMu.Lock()
	accepted := st.accepted
	st.stateMu.Unlock()
	return accepted, nil
}

func isValidWebSocketOpen(open app.WebSocketOpen) bool {
	return open.TargetAgentID != "" && open.RequestID != "" && open.Path != "" &&
		open.API.Protocol == "websocket" && open.Remaining >= 0
}

func (st *WebSocketStream) ProviderAcceptance() app.WebSocketAccepted {
	if st == nil {
		return app.WebSocketAccepted{}
	}
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.accepted
}

func (st *WebSocketStream) SendEvent(ctx context.Context, event app.WebSocketEvent) error {
	if ctx == nil {
		return st.failProtocol(context.Background(), "send")
	}
	if !st.active() {
		return st.failProtocol(ctx, "send")
	}
	st.stateMu.Lock()
	localClosed := st.localClosed
	st.stateMu.Unlock()
	if localClosed {
		return io.EOF
	}
	switch event.Kind {
	case app.WebSocketMessageStartEvent:
		st.eventMu.Lock()
		defer st.eventMu.Unlock()
		if event.MessageID == 0 || event.MessageID <= st.outgoingMessage || st.outgoingOpen || wire.ValidateWebSocketMessageType(event.Type) != nil {
			return st.failProtocol(ctx, "message_start")
		}
		payload, err := wire.EncodeMetadata(wire.WebSocketMessageStart{MessageID: event.MessageID, Type: event.Type}, st.limits.MaxMetadataBytes)
		if err != nil {
			return st.failProtocol(ctx, "message_start")
		}
		if err = st.sendFrame(ctx, wire.FrameWebSocketMessageStart, payload); err != nil {
			st.terminate(err, true)
			return err
		}
		st.outgoingMessage, st.outgoingOpen = event.MessageID, true
		return nil
	case app.WebSocketMessageDataEvent:
		st.eventMu.Lock()
		defer st.eventMu.Unlock()
		if !st.outgoingOpen || event.MessageID != st.outgoingMessage || len(event.Data) == 0 {
			return st.failProtocol(ctx, "message_data")
		}
		return st.sendDataLocked(ctx, event.Data)
	case app.WebSocketMessageEndEvent:
		st.eventMu.Lock()
		defer st.eventMu.Unlock()
		if !st.outgoingOpen || event.MessageID != st.outgoingMessage {
			return st.failProtocol(ctx, "message_end")
		}
		if err := st.sendFrame(ctx, wire.FrameWebSocketMessageEnd, nil); err != nil {
			st.terminate(err, true)
			return err
		}
		st.outgoingOpen = false
		return nil
	case app.WebSocketPingEvent, app.WebSocketPongEvent:
		if len(event.Data) > wire.MaxWebSocketControlPayloadBytes {
			return st.failProtocol(ctx, "control")
		}
		frameType := wire.FrameWebSocketPing
		if event.Kind == app.WebSocketPongEvent {
			frameType = wire.FrameWebSocketPong
		}
		return st.sendFrame(ctx, frameType, append([]byte(nil), event.Data...))
	case app.WebSocketCloseEvent:
		payload, err := wire.EncodeWebSocketClose(event.Code, event.Reason)
		if err != nil {
			return st.failProtocol(ctx, "close")
		}
		// behavior change: Close is forwarded, while FrameAPIResult remains the
		// unique terminal so settlement facts cannot be lost.
		st.stateMu.Lock()
		st.localClosed = true
		st.stateMu.Unlock()
		st.sendCredit.Close(io.EOF)
		return st.sendFrame(ctx, wire.FrameWebSocketClose, payload)
	default:
		return st.failProtocol(ctx, "event")
	}
}

func (st *WebSocketStream) sendDataLocked(ctx context.Context, data []byte) error {
	for len(data) > 0 {
		want := min(int64(len(data)), st.limits.MaxDataBytes)
		bytes, err := st.sendCredit.TakeUpTo(ctx, want, 0)
		if err != nil {
			return err
		}
		if st.beforeDataWrite != nil {
			st.beforeDataWrite()
		}
		st.stateMu.Lock()
		localClosed := st.localClosed
		st.stateMu.Unlock()
		if localClosed {
			return io.EOF
		}
		if err = st.sendFrame(ctx, wire.FrameWebSocketMessageData, append([]byte(nil), data[:bytes]...)); err != nil {
			st.terminate(err, true)
			return err
		}
		data = data[bytes:]
	}
	return nil
}

func (st *WebSocketStream) ReceiveEvent(ctx context.Context) (app.WebSocketEvent, error) {
	event, err := st.events.Pop(ctx)
	if err != nil || event.Kind != app.WebSocketMessageDataEvent {
		return event, err
	}
	bytes := int64(len(event.Data))
	if err := st.receiveCredit.Add(bytes); err != nil {
		return app.WebSocketEvent{}, st.failProtocol(ctx, "window")
	}
	payload, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: bytes}, st.limits.MaxMetadataBytes)
	if err != nil {
		return app.WebSocketEvent{}, st.failProtocol(ctx, "window")
	}
	if err = st.sendFrame(ctx, wire.FrameWindowUpdate, payload); err != nil {
		st.terminate(err, true)
		return app.WebSocketEvent{}, err
	}
	return event, nil
}

func (st *WebSocketStream) ReceiveResult(ctx context.Context) (apiattempt.APIExecutionResult, error) {
	return st.results.Pop(ctx)
}

func (st *WebSocketStream) Close() error {
	st.terminate(io.EOF, true)
	return nil
}

func (st *WebSocketStream) acceptFrame(ctx context.Context, frame wire.Frame) error {
	if err := st.validateFrame(frame); err != nil {
		return st.failProtocol(ctx, "frame")
	}
	switch frame.Type {
	case wire.FrameReady:
		var accepted wire.WebSocketAccepted
		if wire.DecodeMetadata(frame.Payload, &accepted, st.limits.MaxMetadataBytes) != nil || accepted.RequestWindow <= 0 || accepted.RequestWindow > st.limits.InitialStreamWindow || st.sendCredit.Set(accepted.RequestWindow) != nil {
			return st.failProtocol(ctx, "ready")
		}
		st.stateMu.Lock()
		valid := st.phase == webSocketOpening && !st.readyReceived
		if valid {
			st.readyReceived = true
		}
		st.stateMu.Unlock()
		if !valid {
			return st.failProtocol(ctx, "ready")
		}
		select {
		case st.ready <- nil:
		default:
		}
		return nil
	case wire.FrameCommitted:
		var accepted wire.WebSocketAccepted
		if wire.DecodeMetadata(frame.Payload, &accepted, st.limits.MaxMetadataBytes) != nil ||
			accepted.RequestWindow <= 0 || accepted.RequestWindow > st.limits.InitialStreamWindow {
			return st.failProtocol(ctx, "committed")
		}
		success := accepted.ProviderStatus == http.StatusSwitchingProtocols && accepted.Rejection == nil
		rejection := wire.ValidWebSocketRejectionStatus(accepted.ProviderStatus) &&
			accepted.Subprotocol == "" && accepted.Rejection != nil &&
			accepted.Rejection.StatusCode == accepted.ProviderStatus
		if !success && !rejection {
			return st.failProtocol(ctx, "committed")
		}
		st.sendMu.Lock()
		st.stateMu.Lock()
		valid := st.phase == webSocketOpening && st.readyReceived && st.commitSent && !st.committedReceived
		if valid {
			st.committedReceived = true
			if success {
				st.phase = webSocketActive
			}
			st.accepted = app.WebSocketAccepted(accepted)
		}
		st.stateMu.Unlock()
		st.sendMu.Unlock()
		if !valid {
			return st.failProtocol(ctx, "committed")
		}
		select {
		case st.committed <- nil:
		default:
		}
		return nil
	case wire.FrameWindowUpdate:
		if !st.active() {
			return st.failProtocol(ctx, "window")
		}
		var update wire.WindowUpdate
		if wire.DecodeMetadata(frame.Payload, &update, st.limits.MaxMetadataBytes) != nil || st.sendCredit.Add(update.Bytes) != nil {
			return st.failProtocol(ctx, "window")
		}
		return nil
	case wire.FrameWebSocketMessageStart:
		if !st.active() {
			return st.failProtocol(ctx, "message_start")
		}
		var start wire.WebSocketMessageStart
		if wire.DecodeMetadata(frame.Payload, &start, st.limits.MaxMetadataBytes) != nil || start.MessageID == 0 || start.MessageID <= st.incomingMessage || st.incomingOpen || wire.ValidateWebSocketMessageType(start.Type) != nil {
			return st.failProtocol(ctx, "message_start")
		}
		st.incomingMessage, st.incomingOpen = start.MessageID, true
		st.events.Push(app.WebSocketEvent{Kind: app.WebSocketMessageStartEvent, MessageID: start.MessageID, Type: start.Type})
		return nil
	case wire.FrameWebSocketMessageData:
		if !st.active() {
			return st.failProtocol(ctx, "message_data")
		}
		if !st.incomingOpen || len(frame.Payload) == 0 {
			return st.failProtocol(ctx, "message_data")
		}
		accepted, err := st.receiveCredit.TryTake(int64(len(frame.Payload)))
		if err != nil || !accepted {
			return st.failProtocol(ctx, "window")
		}
		st.events.Push(app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: st.incomingMessage, Data: append([]byte(nil), frame.Payload...)})
		return nil
	case wire.FrameWebSocketMessageEnd:
		if !st.active() {
			return st.failProtocol(ctx, "message_end")
		}
		if !st.incomingOpen || len(frame.Payload) != 0 {
			return st.failProtocol(ctx, "message_end")
		}
		st.incomingOpen = false
		st.events.Push(app.WebSocketEvent{Kind: app.WebSocketMessageEndEvent, MessageID: st.incomingMessage})
		return nil
	case wire.FrameWebSocketPing, wire.FrameWebSocketPong:
		if !st.active() {
			return st.failProtocol(ctx, "control")
		}
		if len(frame.Payload) > wire.MaxWebSocketControlPayloadBytes {
			return st.failProtocol(ctx, "control")
		}
		kind := app.WebSocketPingEvent
		if frame.Type == wire.FrameWebSocketPong {
			kind = app.WebSocketPongEvent
		}
		st.events.Push(app.WebSocketEvent{Kind: kind, Data: append([]byte(nil), frame.Payload...)})
		return nil
	case wire.FrameWebSocketClose:
		if !st.active() {
			return st.failProtocol(ctx, "close")
		}
		event, err := wire.DecodeWebSocketClose(frame.Payload)
		if err != nil {
			return st.failProtocol(ctx, "close")
		}
		st.events.Push(event)
		// behavior change: the close event is observable but does not remove the
		// stream before its terminal execution result arrives.
		st.stateMu.Lock()
		st.remoteClosed = true
		st.stateMu.Unlock()
		return nil
	case wire.FrameAPIResult:
		limit, err := wire.FramePayloadLimit(wire.FrameAPIResult, st.limits)
		if err != nil {
			return st.failProtocol(ctx, "result")
		}
		result, err := apiattempt.DecodeResultJSONWithin(frame.Payload, int(limit))
		if err != nil {
			return st.failProtocol(ctx, "result")
		}
		st.results.Push(result)
		st.stateMu.Lock()
		opening := st.phase == webSocketOpening
		st.stateMu.Unlock()
		cause := error(io.EOF)
		if opening {
			cause = &app.WebSocketExecutionError{Result: result, Err: errors.New("remote WebSocket provider rejected execution")}
		}
		st.terminate(cause, false)
		return nil
	default:
		return st.failProtocol(ctx, "direction")
	}
}

func (st *WebSocketStream) validateFrame(frame wire.Frame) error {
	if frame.Version != wire.ProtocolVersion || frame.StreamID != st.id {
		return errors.New("invalid websocket frame")
	}
	if _, err := wire.FramePayloadLimit(frame.Type, st.limits); err != nil {
		return err
	}
	limit, _ := wire.FramePayloadLimit(frame.Type, st.limits)
	if int64(len(frame.Payload)) > limit {
		return errors.New("websocket frame payload too large")
	}
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	next, err := wire.NextSequence(st.receiveSeq)
	if err != nil || frame.Sequence != next {
		return errors.New("invalid websocket sequence")
	}
	st.receiveSeq = frame.Sequence
	return nil
}

func (st *WebSocketStream) active() bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.phase == webSocketActive
}
func (st *WebSocketStream) isTerminal() bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.phase == webSocketTerminal
}

func (st *WebSocketStream) sendFrame(ctx context.Context, typ wire.Type, payload []byte) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if requiresActiveWebSocketOutbound(typ) && !st.active() {
		return st.terminalCause()
	}
	return st.sendFrameLocked(ctx, typ, payload)
}

func (st *WebSocketStream) publishCommit(ctx context.Context) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if err := st.sendFrameLocked(ctx, wire.FrameCommit, nil); err != nil {
		return err
	}
	st.stateMu.Lock()
	commitSent := st.phase == webSocketOpening && st.readyReceived
	if commitSent {
		st.commitSent = true
	}
	st.stateMu.Unlock()
	if !commitSent {
		return st.terminalCause()
	}
	return nil
}

func requiresActiveWebSocketOutbound(typ wire.Type) bool {
	switch typ {
	case wire.FrameWindowUpdate, wire.FrameWebSocketMessageStart, wire.FrameWebSocketMessageData,
		wire.FrameWebSocketMessageEnd, wire.FrameWebSocketPing, wire.FrameWebSocketPong:
		return true
	default:
		return false
	}
}
func (st *WebSocketStream) sendFrameLocked(ctx context.Context, typ wire.Type, payload []byte) error {
	next, err := wire.NextSequence(st.sendSequence)
	if err != nil {
		return err
	}
	if err = st.send(ctx, wire.Frame{Version: wire.ProtocolVersion, Type: typ, StreamID: st.id, Sequence: next, Payload: payload}); err != nil {
		return err
	}
	st.sendSequence = next
	return nil
}
func (st *WebSocketStream) failProtocol(ctx context.Context, _ string) error {
	err := errors.New("websocket tunnel protocol error")
	payload, _ := wire.EncodeWebSocketClose(wire.WebSocketCloseProtocolError, "tunnel protocol error")
	if sendErr := st.sendTerminal(nonNilContext(ctx), payload, err, true); sendErr != nil {
		return sendErr
	}
	return err
}
func (st *WebSocketStream) terminalCause() error {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.terminalErr
}
func (st *WebSocketStream) terminate(cause error, discard bool) {
	if !st.claimTerminal(cause) {
		return
	}
	st.finishTerminal(cause, discard)
}
func (st *WebSocketStream) claimTerminal(cause error) bool {
	st.stateMu.Lock()
	if st.phase == webSocketTerminal {
		st.stateMu.Unlock()
		return false
	}
	if cause == nil {
		cause = io.EOF
	}
	st.phase, st.terminalErr = webSocketTerminal, cause
	st.stateMu.Unlock()
	return true
}
func (st *WebSocketStream) finishTerminal(cause error, discard bool) {
	if cause == nil {
		cause = io.EOF
	}
	st.sendCredit.Close(cause)
	st.receiveCredit.Close(cause)
	st.events.Close(cause, discard)
	st.results.Close(cause, discard)
	select {
	case st.ready <- cause:
	default:
	}
	select {
	case st.committed <- cause:
	default:
	}
	st.finishOnce.Do(func() {
		if st.onDone != nil {
			st.onDone()
		}
	})
}

func (st *WebSocketStream) sendTerminal(ctx context.Context, payload []byte, cause error, discard bool) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if !st.claimTerminal(cause) {
		return st.terminalCause()
	}
	err := st.sendFrameLocked(ctx, wire.FrameWebSocketClose, payload)
	st.finishTerminal(cause, discard)
	return err
}
