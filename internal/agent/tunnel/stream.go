package tunnel

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"golang.org/x/net/http/httpguts"
)

var (
	errOpenCommitTimeout  = errors.New("agent tunnel: OPEN did not reach COMMIT before timeout")
	errStreamClosed       = errors.New("agent tunnel: stream closed")
	errStreamComplete     = errors.New("agent tunnel: stream complete")
	errStreamProtocol     = errors.New("agent tunnel: invalid stream frame")
	errUploadStarted      = errors.New("agent tunnel: upload already started")
	errCopyStarted        = errors.New("agent tunnel: response copy already started")
	errControlSendTimeout = errors.New("agent tunnel: control frame send timeout")
	errNilReader          = errors.New("agent tunnel: nil reader")
	errNilResponseWriter  = errors.New("agent tunnel: nil response writer")
	errInvalidReadCount   = errors.New("agent tunnel: invalid reader count")
)

const maxConsecutiveEmptyReads = 100

type headersResult struct {
	headers wire.Headers
	err     error
}

type StreamResetError struct {
	reset wire.Reset
}

func (e *StreamResetError) Error() string { return errStreamClosed.Error() }
func (e *StreamResetError) Unwrap() error { return errStreamClosed }
func (e *StreamResetError) ResetCode() string {
	if e == nil {
		return ""
	}
	return e.reset.Code
}

type receivePhase uint8

const (
	receiveWaitingReady receivePhase = iota
	receiveReady
	receiveCommitted
	receiveResponding
	receiveResult
	receiveTerminal
)

type streamKind uint8

const (
	streamKindAttempt streamKind = iota
	streamKindProbe
)

type Stream struct {
	session      *Session
	id           wire.StreamID
	requestID    string
	generation   uint64
	ctx          context.Context
	cancel       context.CancelCauseFunc
	stopSession  func() bool
	stopDeadline context.CancelFunc

	inbound      chan wire.Frame
	done         chan struct{}
	ready        chan error
	committed    chan error
	headers      chan headersResult
	responseData *responseBuffer

	requestWindow  *creditWindow
	responseWindow *creditWindow
	commitState    atomic.Uint32
	closed         atomic.Bool
	uploadStarted  atomic.Bool

	cancelOnce    sync.Once
	readyOnce     sync.Once
	committedOnce sync.Once
	headersOnce   sync.Once
	commitMu      sync.Mutex
	commitStarted bool
	commitDone    chan struct{}
	commitErr     error
	sequenceMu    sync.Mutex
	sequence      uint32
	operations    operationTracker
	responseOwner responseConsumer
	receivePhase  receivePhase
	receiveSeq    uint32
	kind          streamKind

	terminalMu              sync.Mutex
	terminalSet             bool
	terminalErr             error
	responseHeaders         wire.Headers
	finalTrailers           http.Header
	finalDynamicTrailerKeys []string
	finalTrailerSet         bool
	attemptResult           attemptwire.AttemptProxyResult
	resultSet               bool
}

func newStream(session *Session, sessionCtx context.Context, id wire.StreamID, remaining time.Duration, requestID string) *Stream {
	parent := sessionCtx
	stopDeadline := func() {}
	if remaining > 0 {
		parent, stopDeadline = withClockTimeoutCause(sessionCtx, session.opts.clock, remaining, context.DeadlineExceeded)
	}
	ctx, cancel := context.WithCancelCause(parent)
	stream := &Stream{
		session: session, id: id, requestID: requestID, generation: session.generation, ctx: ctx, cancel: cancel, stopDeadline: stopDeadline,
		inbound: make(chan wire.Frame, 16), done: make(chan struct{}), ready: make(chan error, 1),
		committed: make(chan error, 1), headers: make(chan headersResult, 1), responseData: newResponseBuffer(session.limits.InitialStreamWindow),
		requestWindow:  newCreditWindowWithClock(session.limits.InitialStreamWindow, session.opts.clock),
		responseWindow: newCreditWindowWithClock(session.limits.InitialStreamWindow, session.opts.clock),
		commitDone:     make(chan struct{}), sequence: 1, operations: newOperationTracker(), responseOwner: newResponseConsumer(),
	}
	stream.commitState.Store(uint32(wire.PreCommit))
	stream.stopSession = context.AfterFunc(sessionCtx, func() {
		stream.Cancel(context.Cause(sessionCtx))
	})
	return stream
}

func (st *Stream) run() {
	timer := st.session.opts.clock.NewTimer(st.session.opts.OpenCommitTimeout)
	defer timer.Stop()
	defer st.finalize()
	for {
		select {
		case <-st.ctx.Done():
			st.sendCancellation()
			return
		case <-timer.Chan():
			if st.CommitState() != wire.Committed {
				st.Cancel(errOpenCommitTimeout)
			}
		case frame := <-st.inbound:
			if st.handleFrame(frame) {
				return
			}
		}
	}
}

func (st *Stream) handleFrame(frame wire.Frame) bool {
	expected := uint32(1)
	if st.receiveSeq != 0 {
		var err error
		expected, err = wire.NextSequence(st.receiveSeq)
		if err != nil {
			st.observeRejectedDirectResult(frame)
			return st.frameProtocolViolation(frame, "sequence")
		}
	}
	if frame.Sequence != expected {
		st.observeRejectedDirectResult(frame)
		return st.frameProtocolViolation(frame, "sequence")
	}
	if st.resultProtocolPoisoning(frame) && st.receivePhase == receiveResult &&
		frame.Type != wire.FrameEnd && frame.Type != wire.FrameReset && frame.Type != wire.FrameCancel {
		st.observeRejectedDirectResult(frame)
		return st.resultProtocolViolation("phase")
	}
	switch frame.Type {
	case wire.FrameReady:
		if st.receivePhase != receiveWaitingReady {
			return st.protocolViolation("phase")
		}
		var ready wire.Ready
		if wire.DecodeMetadata(frame.Payload, &ready, st.session.limits.MaxMetadataBytes) != nil {
			return st.protocolViolation("metadata")
		}
		if st.requestWindow.Set(ready.RequestWindow) != nil {
			return st.protocolViolation("window")
		}
		st.receivePhase = receiveReady
		st.signalReady(nil)
	case wire.FrameCommitted:
		if st.receivePhase != receiveReady || st.CommitState() != wire.CommitUncertain {
			return st.protocolViolation("phase")
		}
		st.receivePhase = receiveCommitted
		st.commitState.Store(uint32(wire.Committed))
		st.signalCommitted(nil)
	case wire.FrameWindowUpdate:
		if st.receivePhase != receiveCommitted && st.receivePhase != receiveResponding {
			return st.protocolViolation("phase")
		}
		var update wire.WindowUpdate
		if wire.DecodeMetadata(frame.Payload, &update, st.session.limits.MaxMetadataBytes) != nil {
			return st.protocolViolation("metadata")
		}
		if st.requestWindow.Add(update.Bytes) != nil {
			return st.protocolViolation("window")
		}
	case wire.FrameHeaders:
		if st.receivePhase != receiveCommitted {
			return st.protocolViolation("phase")
		}
		var headers wire.Headers
		if wire.DecodeMetadata(frame.Payload, &headers, st.session.limits.MaxMetadataBytes) != nil || headers.StatusCode < 200 || headers.StatusCode > 599 {
			return st.protocolViolation("metadata")
		}
		normalizedHeader, err := normalizeResponseHeaders(http.Header(headers.Header))
		if err != nil {
			return st.protocolViolation("metadata")
		}
		normalized, _, err := normalizeTrailers(http.Header(headers.Trailer))
		if err != nil {
			return st.protocolViolation("metadata")
		}
		headers.Header = map[string][]string(normalizedHeader)
		headers.Trailer = map[string][]string(normalized)
		st.receivePhase = receiveResponding
		st.responseHeaders = headers
		st.signalHeaders(headersResult{headers: headers})
	case wire.FrameResponseData:
		if st.receivePhase != receiveResponding {
			return st.protocolViolation("phase")
		}
		accepted, err := st.responseWindow.TryTake(int64(len(frame.Payload)))
		if err != nil || !accepted {
			return st.protocolViolation("window")
		}
		bytes := int64(len(frame.Payload))
		if err := st.session.reserveIncoming(bytes); err != nil {
			return st.protocolViolation("window")
		}
		if err := st.responseData.Push(frame.Payload); err != nil {
			_ = st.session.releaseIncoming(bytes)
			return st.protocolViolation("window")
		}
	case wire.FrameAttemptResult:
		if st.kind != streamKindAttempt || st.receivePhase != receiveResponding || st.resultSet {
			st.observeDirectResultFailure(pkgmetrics.ResultInvalid, errStreamProtocol)
			return st.resultProtocolViolation("phase")
		}
		resultLimit, limitErr := wire.FramePayloadLimit(wire.FrameAttemptResult, st.session.limits)
		if limitErr != nil {
			st.observeDirectResultFailure(pkgmetrics.ResultInvalid, limitErr)
			return st.resultProtocolViolation("result_payload")
		}
		result, err := attemptwire.DecodeResultJSONWithin(frame.Payload, int(resultLimit))
		if err != nil {
			kind := pkgmetrics.ResultInvalid
			if errors.Is(err, attemptwire.ErrResultTooLarge) {
				kind = pkgmetrics.ResultTooLarge
			}
			st.observeDirectResultFailure(kind, err)
			return st.resultProtocolViolation("result_payload")
		}
		st.terminalMu.Lock()
		st.attemptResult = result
		st.resultSet = true
		st.terminalMu.Unlock()
		st.receivePhase = receiveResult
		st.observeDirectResultSuccess(result, len(frame.Payload))
	case wire.FrameEnd:
		expectedPhase := receiveResult
		if st.kind == streamKindProbe {
			expectedPhase = receiveResponding
		}
		if st.receivePhase != expectedPhase {
			return st.frameProtocolViolation(frame, "phase")
		}
		if len(frame.Payload) > 0 {
			var final wire.Trailers
			if wire.DecodeMetadata(frame.Payload, &final, st.session.limits.MaxMetadataBytes) != nil {
				return st.protocolViolation("metadata")
			}
			normalized, dynamic, err := normalizeFinalTrailers(final, http.Header(st.responseHeaders.Trailer))
			if err != nil {
				return st.protocolViolation("metadata")
			}
			st.setFinalTrailers(normalized, dynamic)
		}
		st.receivePhase = receiveTerminal
		st.receiveSeq = frame.Sequence
		st.setTerminal(nil)
		return true
	case wire.FrameReset:
		if st.receivePhase == receiveTerminal {
			return st.protocolViolation("phase")
		}
		var reset wire.Reset
		if wire.DecodeMetadata(frame.Payload, &reset, st.session.limits.MaxMetadataBytes) != nil {
			return st.protocolViolation("metadata")
		}
		st.receivePhase = receiveTerminal
		st.receiveSeq = frame.Sequence
		st.setTerminal(&StreamResetError{reset: reset})
		return true
	case wire.FrameCancel:
		if st.receivePhase == receiveTerminal {
			return st.protocolViolation("phase")
		}
		st.receivePhase = receiveTerminal
		st.receiveSeq = frame.Sequence
		st.setTerminal(errStreamClosed)
		return true
	default:
		return st.protocolViolation("phase")
	}
	st.receiveSeq = frame.Sequence
	return false
}

func (st *Stream) observeRejectedDirectResult(frame wire.Frame) {
	if frame.Type == wire.FrameAttemptResult {
		st.observeDirectResultFailure(pkgmetrics.ResultInvalid, errStreamProtocol)
	}
}

func (st *Stream) observeDirectResultFailure(kind pkgmetrics.ResultKind, cause error) {
	if st == nil || st.session == nil || st.session.opts.Direction != SessionDirectionDirectOutgoing {
		return
	}
	st.session.opts.Metrics.ObserveDirectResultFrame(kind, pkgmetrics.DirectReasonProtocol)
	st.session.opts.directLogs.ResultProtocolFailed(st.directResultLogEvent(string(kind)), "", cause)
}

func (st *Stream) observeDirectResultSuccess(result attemptwire.AttemptProxyResult, payloadBytes int) {
	if st == nil || st.session == nil || st.session.opts.Direction != SessionDirectionDirectOutgoing {
		return
	}
	st.session.opts.Metrics.ObserveDirectResultFrame(pkgmetrics.ResultKind(result.Kind), pkgmetrics.DirectReasonNone, payloadBytes)
	st.session.opts.directLogs.ResultProtocolRecovered(st.directResultLogEvent(string(result.Kind)))
}

func (st *Stream) directResultLogEvent(kind string) directLogEvent {
	return directLogEvent{
		SourceAgentID: st.session.opts.directSourceAgentID, TargetAgentID: st.session.opts.directTargetAgentID,
		Stage: "result", ReasonCode: kind, SessionGeneration: st.generation,
		StreamID: hex.EncodeToString(st.id[:]), RequestID: st.requestID, SourceOutbound: true, ResultKind: kind,
	}
}

func (st *Stream) protocolViolation(stage string) bool {
	return st.finishProtocolViolation(stage, false)
}

func (st *Stream) frameProtocolViolation(frame wire.Frame, stage string) bool {
	if st.resultProtocolPoisoning(frame) {
		return st.resultProtocolViolation(stage)
	}
	return st.protocolViolation(stage)
}

func (st *Stream) resultProtocolViolation(stage string) bool {
	return st.finishProtocolViolation(stage, st.session.opts.Direction == SessionDirectionDirectOutgoing)
}

func (st *Stream) resultProtocolPoisoning(frame wire.Frame) bool {
	if st.session.opts.Direction != SessionDirectionDirectOutgoing || st.kind != streamKindAttempt {
		return false
	}
	if frame.Type == wire.FrameAttemptResult {
		return true
	}
	if frame.Type == wire.FrameEnd {
		return st.receivePhase != receiveResult
	}
	return st.receivePhase == receiveResult && frame.Type != wire.FrameReset && frame.Type != wire.FrameCancel
}

func (st *Stream) finishProtocolViolation(stage string, poisonSession bool) bool {
	st.receivePhase = receiveTerminal
	st.setTerminal(errStreamProtocol)
	if poisonSession {
		// behavior change: an invalid Direct Result poisons its authenticated peer session;
		// ordinary Relay stream violations retain the existing per-stream isolation.
		st.session.setAccepting(false)
	}
	payload, _ := wire.EncodeMetadata(wire.Reset{
		Code: wire.ErrorCodeRelayProtocol, Stage: stage, Committed: st.CommitState() != wire.PreCommit,
	}, st.session.limits.MaxMetadataBytes)
	ctx, cleanup := withClockTimeoutCause(st.session.ctx, st.session.opts.clock, st.session.opts.WriteTimeout, errControlSendTimeout)
	if err := st.enqueueFrame(ctx, wire.FrameReset, payload, nil, true); err == nil && poisonSession && st.session.isRunning() {
		// behavior change: a session close cannot discard the protocol Reset that explains the eviction.
		_ = st.session.writer.WaitStreamFlushed(ctx, st.id)
	}
	cleanup()
	if poisonSession {
		st.session.Cancel(errStreamProtocol)
	}
	return true
}

func (st *Stream) Commit(ctx context.Context) error {
	if isNilInterface(ctx) {
		return errNilContext
	}
	if !st.operations.Begin() {
		return st.operationError()
	}
	defer st.operations.End()
	opCtx, stop := st.operationContext(ctx)
	defer stop()
	if st.CommitState() == wire.Committed {
		return nil
	}
	st.commitMu.Lock()
	if st.commitStarted {
		st.commitMu.Unlock()
		return st.waitCommit(opCtx)
	}
	st.commitStarted = true
	st.commitMu.Unlock()

	err := waitResult(opCtx, st.ready)
	if err == nil {
		err = st.enqueueFrame(opCtx, wire.FrameCommit, nil, func() {
			st.commitState.CompareAndSwap(uint32(wire.PreCommit), uint32(wire.CommitUncertain))
		}, false)
	}
	if err == nil {
		err = waitResult(opCtx, st.committed)
	}
	st.commitMu.Lock()
	st.commitErr = err
	close(st.commitDone)
	st.commitMu.Unlock()
	return err
}

func (st *Stream) waitCommit(ctx context.Context) error {
	select {
	case <-st.commitDone:
		return st.commitError()
	default:
	}
	select {
	case <-st.commitDone:
		return st.commitError()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (st *Stream) commitError() error {
	st.commitMu.Lock()
	defer st.commitMu.Unlock()
	return st.commitErr
}

func (st *Stream) Upload(ctx context.Context, src io.Reader) error {
	if isNilInterface(ctx) {
		return errNilContext
	}
	if isNilInterface(src) {
		return errNilReader
	}
	if !st.operations.Begin() {
		return st.operationError()
	}
	defer st.operations.End()
	if !st.uploadStarted.CompareAndSwap(false, true) {
		return errUploadStarted
	}
	opCtx, stop := st.operationContext(ctx)
	defer stop()
	stopReader := closeReaderOnCancel(opCtx, src)
	defer stopReader()
	if st.CommitState() != wire.Committed {
		return errStreamProtocol
	}
	buffer := make([]byte, int(st.session.limits.MaxDataBytes))
	emptyReads := 0
	for {
		select {
		case <-opCtx.Done():
			return context.Cause(opCtx)
		default:
		}
		n, readErr := src.Read(buffer)
		if n < 0 || n > len(buffer) {
			return errInvalidReadCount
		}
		if n > 0 {
			emptyReads = 0
			if err := st.uploadBytes(opCtx, buffer[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return st.enqueue(opCtx, wire.FrameRequestEnd, nil)
		}
		if readErr != nil {
			return readErr
		}
		if n == 0 {
			emptyReads++
			if emptyReads >= maxConsecutiveEmptyReads {
				return io.ErrNoProgress
			}
			select {
			case <-opCtx.Done():
				return context.Cause(opCtx)
			default:
			}
		}
	}
}

func closeReaderOnCancel(ctx context.Context, src io.Reader) func() bool {
	closer, ok := src.(io.Closer)
	if !ok {
		return func() bool { return true }
	}
	return context.AfterFunc(ctx, func() { _ = closer.Close() })
}

func (st *Stream) uploadBytes(ctx context.Context, payload []byte) error {
	for len(payload) > 0 {
		credit, err := st.requestWindow.TakeUpTo(ctx, int64(len(payload)), st.session.opts.WindowStallTimeout)
		if err != nil {
			return err
		}
		chunk := append([]byte(nil), payload[:credit]...)
		if err := st.enqueue(ctx, wire.FrameRequestData, chunk); err != nil {
			return err
		}
		payload = payload[credit:]
	}
	return nil
}

func (st *Stream) CopyAttemptResponse(ctx context.Context, dst http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	if st.kind != streamKindAttempt {
		return attemptwire.AttemptProxyResult{}, errStreamProtocol
	}
	return st.copyResponseFrames(ctx, dst)
}

func (st *Stream) CopyResponse(ctx context.Context, dst http.ResponseWriter) error {
	if st.kind != streamKindProbe {
		return errStreamProtocol
	}
	_, err := st.copyResponseFrames(ctx, dst)
	return err
}

func (st *Stream) copyResponseFrames(ctx context.Context, dst http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	if isNilInterface(ctx) {
		return attemptwire.AttemptProxyResult{}, errNilContext
	}
	if isNilInterface(dst) {
		return attemptwire.AttemptProxyResult{}, errNilResponseWriter
	}
	switch st.responseOwner.Claim() {
	case responseClaimAlreadyClaimed:
		return attemptwire.AttemptProxyResult{}, errCopyStarted
	case responseClaimAbandoned:
		return st.attemptResultWithError(errCopyStarted)
	}
	defer st.responseOwner.Finish()
	if cause := context.Cause(ctx); cause != nil {
		st.Cancel(cause)
		return st.attemptResultWithError(cause)
	}
	opCtx, stop := st.operationContext(ctx)
	defer stop()
	result, err := waitHeaders(opCtx, st.headers)
	if err != nil {
		st.Cancel(err)
		return st.attemptResultWithError(err)
	}
	normalizedHeaders, err := normalizeResponseHeaders(http.Header(result.Header))
	if err != nil {
		st.Cancel(err)
		return st.attemptResultWithError(err)
	}
	normalizedTrailers, trailerKeys, err := normalizeTrailers(http.Header(result.Trailer))
	if err != nil {
		st.Cancel(err)
		return st.attemptResultWithError(err)
	}
	responseHeader := dst.Header()
	copyHeaders(responseHeader, normalizedHeaders)
	for _, key := range trailerKeys {
		responseHeader.Add("Trailer", key)
	}
	dst.WriteHeader(result.StatusCode)
	for {
		chunk, err := st.responseData.ReadChunk(opCtx, int(st.session.limits.MaxDataBytes))
		if errors.Is(err, errResponseBufferClosed) {
			break
		}
		if err != nil {
			if st.kind == streamKindAttempt && errors.Is(err, context.Canceled) && errors.Is(context.Cause(ctx), context.Canceled) && st.ctx.Err() == nil {
				return st.finishInterruptedAttemptResponse(context.Canceled, true, 0, nil)
			}
			st.Cancel(err)
			return st.attemptResultWithError(err)
		}
		n, writeErr := dst.Write(chunk)
		n, writeErr = normalizeResponseWriteCount(n, len(chunk), writeErr)
		release := &responseChunkRelease{size: int64(len(chunk))}
		_ = st.releaseResponseChunk(opCtx, release)
		if result, err, done := st.finishResponseChunk(ctx, len(chunk), n, writeErr, release); done {
			return result, err
		}
		if flusher, ok := dst.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	dynamicTrailerKeys := []string(nil)
	if final, dynamic, ok := st.getFinalTrailers(); ok {
		normalizedTrailers = final
		dynamicTrailerKeys = dynamic
	}
	writeResponseTrailers(responseHeader, normalizedTrailers, trailerKeys, dynamicTrailerKeys)
	attemptResult, resultSet, _, terminalErr := st.getAttemptResultAndTerminal()
	if terminalErr != nil {
		if resultSet {
			return attemptResult, terminalErr
		}
		return attemptwire.AttemptProxyResult{}, terminalErr
	}
	return attemptResult, nil
}

func normalizeTrailers(source http.Header) (http.Header, []string, error) {
	rawKeys := make([]string, 0, len(source))
	for key := range source {
		rawKeys = append(rawKeys, key)
	}
	sort.Strings(rawKeys)
	normalized := make(http.Header, len(source))
	for _, key := range rawKeys {
		if !httpguts.ValidHeaderFieldName(key) {
			return nil, nil, errStreamProtocol
		}
		canonical := http.CanonicalHeaderKey(key)
		if reservedForwardHeader(canonical) {
			continue
		}
		if forbiddenTrailerKey(canonical) {
			return nil, nil, errStreamProtocol
		}
		if _, ok := normalized[canonical]; !ok {
			normalized[canonical] = nil
		}
		for _, value := range source[key] {
			if !httpguts.ValidHeaderFieldValue(value) {
				return nil, nil, errStreamProtocol
			}
			normalized[canonical] = append(normalized[canonical], value)
		}
	}
	ordered := make([]string, 0, len(normalized))
	for key := range normalized {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	return normalized, ordered, nil
}

func forbiddenTrailerKey(key string) bool {
	return strings.EqualFold(key, "Trailer") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Content-Length")
}

func writeResponseTrailers(dst, trailers http.Header, declared, dynamic []string) {
	for _, key := range declared {
		dst.Del(key)
		for _, value := range trailers[key] {
			dst.Add(key, value)
		}
	}
	for _, key := range dynamic {
		dst[http.TrailerPrefix+key] = append([]string(nil), trailers[key]...)
	}
}

func normalizeFinalTrailers(final wire.Trailers, declared http.Header) (http.Header, []string, error) {
	normalized, _, err := normalizeTrailers(http.Header(final.Header))
	if err != nil {
		return nil, nil, err
	}
	dynamic, err := normalizeDynamicTrailerKeys(final.Dynamic)
	if err != nil {
		return nil, nil, err
	}
	dynamicSet := make(map[string]struct{}, len(dynamic))
	for _, key := range dynamic {
		if _, ok := declared[key]; ok {
			return nil, nil, errStreamProtocol
		}
		if _, ok := normalized[key]; !ok {
			return nil, nil, errStreamProtocol
		}
		dynamicSet[key] = struct{}{}
	}
	for key := range normalized {
		if _, ok := declared[key]; ok {
			continue
		}
		if _, ok := dynamicSet[key]; !ok {
			return nil, nil, errStreamProtocol
		}
	}
	return normalized, dynamic, nil
}

func normalizeDynamicTrailerKeys(source []string) ([]string, error) {
	normalized := make([]string, 0, len(source))
	seen := make(map[string]struct{}, len(source))
	for _, key := range source {
		if !httpguts.ValidHeaderFieldName(key) {
			return nil, errStreamProtocol
		}
		canonical := http.CanonicalHeaderKey(key)
		if reservedForwardHeader(canonical) {
			continue
		}
		if forbiddenTrailerKey(canonical) {
			return nil, errStreamProtocol
		}
		if _, ok := seen[canonical]; ok {
			return nil, errStreamProtocol
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func (st *Stream) setFinalTrailers(trailer http.Header, dynamic []string) {
	st.terminalMu.Lock()
	st.finalTrailers = trailer.Clone()
	st.finalDynamicTrailerKeys = append([]string(nil), dynamic...)
	st.finalTrailerSet = true
	st.terminalMu.Unlock()
}

func (st *Stream) getFinalTrailers() (http.Header, []string, bool) {
	st.terminalMu.Lock()
	defer st.terminalMu.Unlock()
	return st.finalTrailers.Clone(), append([]string(nil), st.finalDynamicTrailerKeys...), st.finalTrailerSet
}

func (st *Stream) isTerminalSuccess() bool {
	set, err := st.getTerminal()
	return set && err == nil
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func (st *Stream) enqueue(ctx context.Context, frameType wire.Type, payload []byte) error {
	return st.enqueueFrame(ctx, frameType, payload, nil, false)
}

func (st *Stream) enqueueFrame(ctx context.Context, frameType wire.Type, payload []byte, onAccept func(), replace bool) error {
	st.sequenceMu.Lock()
	defer st.sequenceMu.Unlock()
	next, err := wire.NextSequence(st.sequence)
	if err != nil {
		return err
	}
	frame := wire.Frame{Version: wire.ProtocolVersion, Type: frameType, StreamID: st.id, Sequence: next, Payload: payload}
	accept := func(sequence uint32) {
		st.sequence = sequence
		if onAccept != nil {
			onAccept()
		}
	}
	if replace {
		return st.session.writer.Replace(ctx, frame, accept)
	}
	return st.session.writer.Enqueue(ctx, frame, func() { accept(next) })
}

func (st *Stream) Cancel(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	st.setTerminal(cause)
	st.responseOwner.Abandon()
	st.cancelOnce.Do(func() { st.cancel(cause) })
}

func (st *Stream) Close() error {
	st.Cancel(errStreamClosed)
	<-st.done
	return nil
}

func (st *Stream) Done() <-chan struct{} { return st.done }

func (st *Stream) CommitState() wire.CommitState {
	return wire.CommitState(st.commitState.Load())
}

func (st *Stream) operationContext(ctx context.Context) (context.Context, func()) {
	opCtx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(st.ctx, func() { cancel(context.Cause(st.ctx)) })
	return opCtx, func() {
		stop()
		cancel(context.Canceled)
	}
}

func (st *Stream) sendCancellation() {
	frameType := wire.FrameCancel
	if st.CommitState() == wire.PreCommit {
		frameType = wire.FrameReset
	}
	payload, _ := wire.EncodeMetadata(wire.Reset{
		Code: wire.ErrorCodeRequestCancelled, Stage: "source", Committed: st.CommitState() != wire.PreCommit,
	}, st.session.limits.MaxMetadataBytes)
	controlCtx, cancel := withClockTimeoutCause(st.session.ctx, st.session.opts.clock, st.session.opts.WriteTimeout, errControlSendTimeout)
	err := st.enqueueFrame(controlCtx, frameType, payload, nil, true)
	cancel()
	if err != nil {
		st.session.writer.discard(st.id)
	}
}

func (st *Stream) finalize() {
	waitOperations := st.operations.Stop()
	terminalSet, terminalErr := st.getTerminal()
	if !terminalSet {
		cause := context.Cause(st.ctx)
		if cause == nil {
			cause = io.EOF
		}
		st.setTerminal(cause)
		_, terminalErr = st.getTerminal()
	}
	cause := terminalErr
	if terminalErr == nil {
		cause = errStreamComplete
	}
	st.closed.Store(true)
	st.signalReady(cause)
	st.signalCommitted(cause)
	st.signalHeaders(headersResult{err: cause})
	st.requestWindow.Close(cause)
	st.responseWindow.Close(cause)
	st.responseData.Close()
	st.cancel(cause)
	if terminalErr != nil {
		st.responseOwner.Abandon()
	}
	st.stopSession()
	st.stopDeadline()
	<-waitOperations
	<-st.responseOwner.Done()
	if discarded := st.responseData.Discard(); discarded > 0 {
		_ = st.session.releaseIncoming(discarded)
	}
	st.session.writer.Forget(st.id)
	st.session.removeStream(st)
	close(st.done)
}

func (st *Stream) abortBeforeRun(cause error) {
	waitOperations := st.operations.Stop()
	st.setTerminal(cause)
	st.signalReady(cause)
	st.signalCommitted(cause)
	st.signalHeaders(headersResult{err: cause})
	st.requestWindow.Close(cause)
	st.responseWindow.Close(cause)
	st.cancel(cause)
	st.stopSession()
	st.stopDeadline()
	st.responseData.Close()
	if discarded := st.responseData.Discard(); discarded > 0 {
		_ = st.session.releaseIncoming(discarded)
	}
	<-waitOperations
	close(st.done)
}

func (st *Stream) operationError() error {
	if set, err := st.getTerminal(); set {
		if err != nil {
			return err
		}
		return errStreamClosed
	}
	if err := context.Cause(st.ctx); err != nil {
		return err
	}
	return errStreamClosed
}

func (st *Stream) signalReady(err error) {
	st.readyOnce.Do(func() { st.ready <- err })
}

func (st *Stream) signalCommitted(err error) {
	st.committedOnce.Do(func() { st.committed <- err })
}

func (st *Stream) signalHeaders(result headersResult) {
	st.headersOnce.Do(func() { st.headers <- result })
}

func (st *Stream) setTerminal(err error) {
	st.terminalMu.Lock()
	if !st.terminalSet {
		st.terminalSet = true
		st.terminalErr = err
	}
	st.terminalMu.Unlock()
}

func (st *Stream) getTerminal() (bool, error) {
	st.terminalMu.Lock()
	defer st.terminalMu.Unlock()
	return st.terminalSet, st.terminalErr
}

func (st *Stream) getAttemptResultAndTerminal() (attemptwire.AttemptProxyResult, bool, bool, error) {
	st.terminalMu.Lock()
	defer st.terminalMu.Unlock()
	return st.attemptResult, st.resultSet, st.terminalSet, st.terminalErr
}

func (st *Stream) attemptResultWithError(err error) (attemptwire.AttemptProxyResult, error) {
	result, resultSet, terminalSet, terminalErr := st.getAttemptResultAndTerminal()
	if terminalSet && terminalErr != nil {
		err = terminalErr
	}
	if resultSet {
		return result, err
	}
	return attemptwire.AttemptProxyResult{}, err
}

func waitResult(ctx context.Context, result <-chan error) error {
	select {
	case err := <-result:
		return err
	default:
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func waitHeaders(ctx context.Context, result <-chan headersResult) (wire.Headers, error) {
	select {
	case value := <-result:
		return value.headers, value.err
	default:
	}
	select {
	case value := <-result:
		return value.headers, value.err
	case <-ctx.Done():
		return wire.Headers{}, context.Cause(ctx)
	}
}

type operationTracker struct {
	mu        sync.Mutex
	accepting bool
	active    int
	zero      chan struct{}
}

type responseConsumer struct {
	mu      sync.Mutex
	claimed bool
	closed  bool
	done    chan struct{}
}

type responseClaimStatus uint8

const (
	responseClaimAcquired responseClaimStatus = iota
	responseClaimAlreadyClaimed
	responseClaimAbandoned
)

func newResponseConsumer() responseConsumer {
	return responseConsumer{done: make(chan struct{})}
}

func (c *responseConsumer) Claim() responseClaimStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claimed {
		return responseClaimAlreadyClaimed
	}
	c.claimed = true
	if c.closed {
		return responseClaimAbandoned
	}
	return responseClaimAcquired
}

func (c *responseConsumer) Finish() { c.close(true) }

func (c *responseConsumer) Abandon() { c.close(false) }

func (c *responseConsumer) close(claimed bool) {
	c.mu.Lock()
	if c.closed || (!claimed && c.claimed) {
		c.mu.Unlock()
		return
	}
	c.closed = true
	done := c.done
	c.mu.Unlock()
	close(done)
}

func (c *responseConsumer) Done() <-chan struct{} { return c.done }

func newOperationTracker() operationTracker {
	zero := make(chan struct{})
	close(zero)
	return operationTracker{accepting: true, zero: zero}
}

func (t *operationTracker) Begin() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.accepting {
		return false
	}
	if t.active == 0 {
		t.zero = make(chan struct{})
	}
	t.active++
	return true
}

func (t *operationTracker) End() {
	t.mu.Lock()
	t.active--
	if t.active == 0 {
		close(t.zero)
	}
	t.mu.Unlock()
}

func (t *operationTracker) Stop() <-chan struct{} {
	t.mu.Lock()
	t.accepting = false
	zero := t.zero
	t.mu.Unlock()
	return zero
}

var _ interface {
	Commit(context.Context) error
	Upload(context.Context, io.Reader) error
	CopyResponse(context.Context, http.ResponseWriter) error
} = (*Stream)(nil)
