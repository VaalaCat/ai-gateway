package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

// WebSocketTargetHandler is the application bridge contract implemented by
// Task 16. This transport deliberately does not interpret WebSocket payloads.
type WebSocketTargetHandler interface {
	ServeWebSocketAPI(context.Context, *WebSocketTargetStream) error
}

type WebSocketTargetHandlerFunc func(context.Context, *WebSocketTargetStream) error

func (f WebSocketTargetHandlerFunc) ServeWebSocketAPI(ctx context.Context, stream *WebSocketTargetStream) error {
	return f(ctx, stream)
}

// WebSocketTargetStream is the target-side counterpart to WebSocketStream.
// Task 16 owns its application bridge; this type owns only wire validation,
// event boundaries and the target-facing data window.
type WebSocketTargetStream struct {
	id     wire.StreamID
	limits wire.Limits
	send   apiFrameSender

	sendMu        sync.Mutex // serializes only frame sequence/write
	eventMu       sync.Mutex // serializes logical message transitions
	stateMu       sync.Mutex
	sendSequence  uint32
	receiveSeq    uint32
	phase         webSocketPhase
	terminalErr   error
	events        *apiEventQueue[app.WebSocketEvent]
	sendCredit    *creditWindow
	receiveCredit *creditWindow

	outgoingMessage uint64
	incomingMessage uint64
	outgoingOpen    bool
	incomingOpen    bool
	finishOnce      sync.Once
	onDone          func()
	onActive        func()
	beforeDataWrite func()
	open            app.WebSocketOpen
	localClosed     bool
	remoteClosed    bool
	committedSent   bool
	resultSent      bool
}

func newWebSocketTargetStream(id wire.StreamID, limits wire.Limits, send apiFrameSender) *WebSocketTargetStream {
	sendCredit := newCreditWindow(limits.InitialStreamWindow)
	_ = sendCredit.Set(0)
	return &WebSocketTargetStream{id: id, limits: limits, send: send, phase: webSocketNew,
		events: newAPIEventQueue[app.WebSocketEvent](), sendCredit: sendCredit,
		receiveCredit: newCreditWindow(limits.InitialStreamWindow)}
}

func (st *WebSocketTargetStream) acceptFrame(ctx context.Context, frame wire.Frame) error {
	if err := st.validateFrame(frame); err != nil {
		return st.failProtocol(ctx, "frame")
	}
	switch frame.Type {
	case wire.FrameOpen:
		if !st.openFrame(ctx, frame.Payload) {
			return st.failProtocol(ctx, "open")
		}
		return nil
	case wire.FrameCommit:
		st.stateMu.Lock()
		valid := st.phase == webSocketOpening && len(frame.Payload) == 0
		if valid {
			st.phase = webSocketCommitting
		}
		st.stateMu.Unlock()
		if !valid {
			return st.failProtocol(ctx, "commit")
		}
		if st.onActive != nil {
			st.onActive()
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
		// behavior change: Close does not own the terminal; SendResult does.
		st.stateMu.Lock()
		st.remoteClosed = true
		st.stateMu.Unlock()
		return nil
	default:
		return st.failProtocol(ctx, "direction")
	}
}

// Accept publishes the provider's terminal handshake decision. A 101 moves the
// stream active; an ordinary HTTP rejection stays pre-upgrade until SendResult.
func (st *WebSocketTargetStream) Accept(ctx context.Context, accepted app.WebSocketAccepted) error {
	if ctx == nil || accepted.RequestWindow < 0 || accepted.RequestWindow > st.limits.InitialStreamWindow {
		return st.failProtocol(nonNilContext(ctx), "accept")
	}
	if accepted.RequestWindow == 0 {
		accepted.RequestWindow = st.limits.InitialStreamWindow
	}
	success := accepted.ProviderStatus == http.StatusSwitchingProtocols && accepted.Rejection == nil
	rejection := wire.ValidWebSocketRejectionStatus(accepted.ProviderStatus) &&
		accepted.Subprotocol == "" && accepted.Rejection != nil &&
		accepted.Rejection.StatusCode == accepted.ProviderStatus
	if !success && !rejection {
		return st.failProtocol(ctx, "accept")
	}
	payload, err := encodeWebSocketAcceptedWithin(accepted, st.limits.MaxMetadataBytes)
	if err != nil {
		return st.failProtocol(ctx, "accept")
	}
	st.stateMu.Lock()
	valid := st.phase == webSocketCommitting && !st.committedSent && !st.resultSent
	if valid {
		st.committedSent = true
		if success {
			st.phase = webSocketActive
		}
	}
	st.stateMu.Unlock()
	if !valid {
		return st.failProtocol(ctx, "accept")
	}
	if err = st.sendFrame(ctx, wire.FrameCommitted, payload); err != nil {
		st.terminate(err, true)
		return err
	}
	return nil
}

func encodeWebSocketAcceptedWithin(accepted app.WebSocketAccepted, limit int64) ([]byte, error) {
	accepted = cloneWebSocketAccepted(accepted)
	if accepted.Rejection == nil {
		return wire.EncodeMetadata(wire.WebSocketAccepted(accepted), limit)
	}
	if accepted.Rejection.BodyTruncated {
		deleteHeaderFold(accepted.Rejection.Header, "Content-Encoding")
	}
	payload, err := wire.EncodeMetadata(wire.WebSocketAccepted(accepted), limit)
	if err == nil || !errors.Is(err, wire.ErrMetadataTooLarge) {
		return payload, err
	}

	originalBody := accepted.Rejection.Body
	bodyMustTruncate := accepted.Rejection.BodyTruncated || len(originalBody) > 0
	accepted.Rejection.Body = nil
	accepted.Rejection.BodyTruncated = bodyMustTruncate
	if bodyMustTruncate {
		deleteHeaderFold(accepted.Rejection.Header, "Content-Encoding")
	}

	// Headers have first claim on the metadata budget. A full header set is
	// tested once; otherwise binary search chooses the largest deterministic
	// key/value prefix whose final JSON metadata still fits.
	payload, err = wire.EncodeMetadata(wire.WebSocketAccepted(accepted), limit)
	if err != nil {
		keys := sortedHeaderKeys(accepted.Rejection.Header)
		sourceHeader := accepted.Rejection.Header
		accepted.Rejection.HeaderTruncated = true
		accepted.Rejection.Header = nil
		payload, err = wire.EncodeMetadata(wire.WebSocketAccepted(accepted), limit)
		if err != nil {
			return nil, err
		}
		bestHeader, bestPayload := http.Header(nil), payload
		low, high := int64(1), min(limit, int64(^uint(0)>>1))
		for low <= high {
			mid := low + (high-low)/2
			candidateHeader := webSocketHeaderPrefix(sourceHeader, keys, mid)
			accepted.Rejection.Header = candidateHeader
			candidatePayload, encodeErr := wire.EncodeMetadata(wire.WebSocketAccepted(accepted), limit)
			if encodeErr == nil {
				bestHeader, bestPayload = candidateHeader, candidatePayload
				low = mid + 1
				continue
			}
			high = mid - 1
		}
		accepted.Rejection.Header, payload = bestHeader, bestPayload
	}

	if len(originalBody) == 0 {
		return payload, nil
	}
	bestPayload := payload
	low, high := 1, len(originalBody)-1
	for low <= high {
		mid := low + (high-low)/2
		accepted.Rejection.Body = originalBody[:mid]
		candidatePayload, encodeErr := wire.EncodeMetadata(wire.WebSocketAccepted(accepted), limit)
		if encodeErr == nil {
			bestPayload = candidatePayload
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	return bestPayload, nil
}

func cloneWebSocketAccepted(accepted app.WebSocketAccepted) app.WebSocketAccepted {
	if accepted.Rejection == nil {
		return accepted
	}
	rejection := *accepted.Rejection
	rejection.Header = http.Header(rejection.Header).Clone()
	rejection.Body = append([]byte(nil), rejection.Body...)
	accepted.Rejection = &rejection
	return accepted
}

func sortedHeaderKeys(header http.Header) []string {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func webSocketHeaderPrefix(source http.Header, keys []string, budget int64) http.Header {
	if budget <= 0 {
		return nil
	}
	result := make(http.Header)
	remaining := budget
	for _, key := range keys {
		for _, value := range source[key] {
			entryCost := int64(len(key) + 4)
			if remaining <= entryCost {
				return result
			}
			valueBytes := int64(len(value))
			if valueBytes > remaining-entryCost {
				valueBytes = remaining - entryCost
				result[key] = append(result[key], value[:valueBytes])
				return result
			}
			result[key] = append(result[key], value)
			remaining -= entryCost + valueBytes
		}
	}
	return result
}

func deleteHeaderFold(header http.Header, target string) {
	for key := range header {
		if strings.EqualFold(key, target) {
			delete(header, key)
		}
	}
}

// SendResult publishes the exactly-once terminal execution result. It is valid
// both before Accept (provider rejection) and after the WebSocket closes.
func (st *WebSocketTargetStream) SendResult(ctx context.Context, result apiattempt.APIExecutionResult) error {
	limit, err := wire.FramePayloadLimit(wire.FrameAPIResult, st.limits)
	if err != nil {
		return err
	}
	payload, err := apiattempt.EncodeResultJSONWithin(result, int(limit))
	if err != nil {
		return err
	}
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	st.stateMu.Lock()
	valid := (st.phase == webSocketCommitting || st.phase == webSocketActive) && !st.resultSent
	if valid {
		st.resultSent = true
	}
	st.stateMu.Unlock()
	if !valid {
		return st.terminalCause()
	}
	if err = st.sendFrameLocked(ctx, wire.FrameAPIResult, payload); err != nil {
		st.terminate(err, true)
		return err
	}
	st.terminate(io.EOF, false)
	return nil
}

func (st *WebSocketTargetStream) openFrame(ctx context.Context, payload []byte) bool {
	st.stateMu.Lock()
	if st.phase != webSocketNew {
		st.stateMu.Unlock()
		return false
	}
	st.stateMu.Unlock()
	var open wire.Open
	if wire.DecodeMetadata(payload, &open, st.limits.MaxMetadataBytes) != nil || open.API == nil || open.API.Protocol != "websocket" || open.WebSocket == nil || open.WebSocket.ResponseWindow <= 0 || open.WebSocket.ResponseWindow > st.limits.InitialStreamWindow {
		return false
	}
	st.stateMu.Lock()
	st.phase = webSocketOpening
	st.sendCredit = newCreditWindow(open.WebSocket.ResponseWindow)
	api := cloneAPIAttemptMeta(*open.API)
	st.open = app.WebSocketOpen{
		TargetAgentID: open.TargetAgentID, RouteID: open.RouteID, RequestID: open.RequestID,
		Path: open.Path, Header: http.Header(open.Header).Clone(), Remaining: time.Duration(open.RemainingNanos),
		Hop: open.Hop, API: api,
	}
	st.stateMu.Unlock()
	accepted, err := wire.EncodeMetadata(wire.WebSocketAccepted{RequestWindow: st.limits.InitialStreamWindow}, st.limits.MaxMetadataBytes)
	if err != nil || st.sendFrame(ctx, wire.FrameReady, accepted) != nil {
		return false
	}
	return true
}

// OpenMetadata returns the validated, owned application metadata for the
// committed target handler. Callers receive fresh header storage.
func (st *WebSocketTargetStream) OpenMetadata() app.WebSocketOpen {
	if st == nil {
		return app.WebSocketOpen{}
	}
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	open := st.open
	open.Header = open.Header.Clone()
	open.API = cloneAPIAttemptMeta(open.API)
	return open
}

// MetadataLimit returns the negotiated bound for typed control metadata.
func (st *WebSocketTargetStream) MetadataLimit() int64 {
	if st == nil {
		return 0
	}
	return st.limits.MaxMetadataBytes
}

func (st *WebSocketTargetStream) SendEvent(ctx context.Context, event app.WebSocketEvent) error {
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
		for data := event.Data; len(data) > 0; {
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
		// behavior change: Close remains an event; SendResult is terminal.
		st.stateMu.Lock()
		st.localClosed = true
		st.stateMu.Unlock()
		st.sendCredit.Close(io.EOF)
		return st.sendFrame(ctx, wire.FrameWebSocketClose, payload)
	default:
		return st.failProtocol(ctx, "event")
	}
}

func (st *WebSocketTargetStream) ReceiveEvent(ctx context.Context) (app.WebSocketEvent, error) {
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

func (st *WebSocketTargetStream) validateFrame(frame wire.Frame) error {
	if frame.Version != wire.ProtocolVersion || frame.StreamID != st.id {
		return errors.New("invalid websocket frame")
	}
	limit, err := wire.FramePayloadLimit(frame.Type, st.limits)
	if err != nil || int64(len(frame.Payload)) > limit {
		return errors.New("invalid websocket payload")
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
func (st *WebSocketTargetStream) active() bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.phase == webSocketActive
}
func (st *WebSocketTargetStream) isTerminal() bool {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.phase == webSocketTerminal
}
func (st *WebSocketTargetStream) markAcceptedForTest() {
	st.stateMu.Lock()
	st.phase = webSocketActive
	_ = st.sendCredit.Set(st.limits.InitialStreamWindow)
	st.stateMu.Unlock()
}
func (st *WebSocketTargetStream) sendFrame(ctx context.Context, typ wire.Type, payload []byte) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if requiresActiveWebSocketOutbound(typ) && !st.active() {
		return st.terminalCause()
	}
	return st.sendFrameLocked(ctx, typ, payload)
}
func (st *WebSocketTargetStream) sendFrameLocked(ctx context.Context, typ wire.Type, payload []byte) error {
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
func (st *WebSocketTargetStream) failProtocol(ctx context.Context, _ string) error {
	err := errors.New("websocket tunnel protocol error")
	payload, _ := wire.EncodeWebSocketClose(wire.WebSocketCloseProtocolError, "tunnel protocol error")
	if sendErr := st.sendTerminal(nonNilContext(ctx), payload, err, true); sendErr != nil {
		return sendErr
	}
	return err
}
func (st *WebSocketTargetStream) terminalCause() error {
	st.stateMu.Lock()
	defer st.stateMu.Unlock()
	return st.terminalErr
}
func (st *WebSocketTargetStream) terminate(cause error, discard bool) {
	if !st.claimTerminal(cause) {
		return
	}
	st.finishTerminal(cause, discard)
}
func (st *WebSocketTargetStream) claimTerminal(cause error) bool {
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
func (st *WebSocketTargetStream) finishTerminal(cause error, discard bool) {
	if cause == nil {
		cause = io.EOF
	}
	st.sendCredit.Close(cause)
	st.receiveCredit.Close(cause)
	st.events.Close(cause, discard)
	st.finishOnce.Do(func() {
		if st.onDone != nil {
			st.onDone()
		}
	})
}

func (st *WebSocketTargetStream) sendTerminal(ctx context.Context, payload []byte, cause error, discard bool) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	if !st.claimTerminal(cause) {
		return st.terminalCause()
	}
	err := st.sendFrameLocked(ctx, wire.FrameWebSocketClose, payload)
	st.finishTerminal(cause, discard)
	return err
}
