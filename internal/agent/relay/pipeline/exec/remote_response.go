package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

const (
	reasonAttemptResultInterrupted = "attempt_result_interrupted"
)

var reservedAttemptResponseMetadata = [...]string{
	attemptwire.HeaderMode,
}

type attemptResponseReceiver struct {
	client          http.ResponseWriter
	header          http.Header
	status          int
	mode            string
	wroteHeader     bool
	responseStarted bool
	declared        map[string]struct{}
	protocolErr     error
}

type remoteTraceResponseWriter struct {
	http.ResponseWriter
	written       int
	writeObserved bool
}

func newRemoteTraceResponseWriter(writer http.ResponseWriter) *remoteTraceResponseWriter {
	if writer == nil {
		return nil
	}
	return &remoteTraceResponseWriter{ResponseWriter: writer}
}

func (w *remoteTraceResponseWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	w.writeObserved = true
	if n > 0 {
		w.written += n
	}
	return n, err
}

func (w *remoteTraceResponseWriter) CorrectClientResponseBody(record *trace.TraceRecord) {
	if w == nil || !w.writeObserved || record == nil {
		return
	}
	if w.written >= len(record.ClientResponseBody) {
		return
	}
	record.ClientResponseBody = record.ClientResponseBody[:w.written]
}

func (w *remoteTraceResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func newAttemptResponseReceiver(client http.ResponseWriter) *attemptResponseReceiver {
	return &attemptResponseReceiver{client: client, header: make(http.Header), status: http.StatusOK}
}

func (r *attemptResponseReceiver) Header() http.Header {
	if r == nil {
		return nil
	}
	return r.header
}

func (r *attemptResponseReceiver) WriteHeader(status int) {
	if r == nil || r.wroteHeader || status < 100 {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.mode = strings.TrimSpace(r.header.Get(attemptwire.HeaderMode))
	switch r.mode {
	case attemptwire.ModeResponse:
	case attemptwire.ModeControl:
		if status != http.StatusOK {
			r.protocolErr = attemptwire.ErrInvalidContract
		}
		return
	default:
		r.protocolErr = attemptwire.ErrInvalidContract
		return
	}
	if r.client == nil {
		r.protocolErr = errors.New("attempt response client unavailable")
		return
	}
	r.declared = declaredTrailerNames(r.header)
	copyResponseHeader(r.client.Header(), r.header)
	r.responseStarted = true
	r.client.WriteHeader(status)
}

func (r *attemptResponseReceiver) Write(body []byte) (int, error) {
	if r == nil {
		return 0, errors.New("attempt response receiver unavailable")
	}
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.mode == attemptwire.ModeResponse {
		if r.client == nil {
			return 0, errors.New("attempt response client unavailable")
		}
		r.responseStarted = true
		return r.client.Write(body)
	}
	r.protocolErr = attemptwire.ErrInvalidContract
	return 0, r.protocolErr
}

func (r *attemptResponseReceiver) Flush() {
	if r == nil {
		return
	}
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.mode != attemptwire.ModeResponse || r.client == nil {
		return
	}
	if flusher, ok := r.client.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *attemptResponseReceiver) ResponseStarted() bool {
	return r != nil && r.responseStarted
}

func (r *attemptResponseReceiver) Unwrap() http.ResponseWriter {
	if r == nil {
		return nil
	}
	return r.client
}

func (r *attemptResponseReceiver) FinishWithResult(
	executionAgentID string,
	path app.RoutePath,
	commit tunnel.CommitState,
	result attemptwire.AttemptProxyResult,
	transportCode string,
	transportErr error,
) AttemptOutcome {
	if r == nil {
		return interruptedRemoteTransportOutcome(executionAgentID, path, commit, false, transportCode, transportErr)
	}
	if result.Validate() != nil {
		return interruptedRemoteTransportOutcome(
			executionAgentID, path, commit, r.responseStarted, transportCode,
			errors.Join(transportErr, r.protocolErr, attemptwire.ErrInvalidContract),
		)
	}
	if r.mode == attemptwire.ModeResponse {
		r.copyProviderTrailers()
	}
	contractErr := r.resultContractError(result)
	terminalErr := errors.Join(transportErr, r.protocolErr, contractErr)
	if terminalErr != nil {
		return interruptedAttemptResultOutcome(executionAgentID, path, commit, result, transportCode, terminalErr)
	}
	return outcomeFromAttemptResult(executionAgentID, path, commit, result)
}

func (r *attemptResponseReceiver) resultContractError(result attemptwire.AttemptProxyResult) error {
	switch r.mode {
	case attemptwire.ModeResponse:
		if !responseResultConsistent(result, r.responseStarted) {
			return attemptwire.ErrInvalidContract
		}
	case attemptwire.ModeControl:
		if r.status != http.StatusOK || !controlResultConsistent(result) {
			return attemptwire.ErrInvalidContract
		}
	default:
		return attemptwire.ErrInvalidContract
	}
	return nil
}

func (r *attemptResponseReceiver) copyProviderTrailers() {
	if r == nil || r.client == nil {
		return
	}
	for name := range r.declared {
		if isReservedAttemptResponseMetadata(name) {
			continue
		}
		values := headerValuesCaseInsensitive(r.header, name)
		if len(values) > 0 {
			r.client.Header()[name] = append([]string(nil), values...)
		}
	}
	for key, values := range r.header {
		if !strings.HasPrefix(key, http.TrailerPrefix) {
			continue
		}
		name := strings.TrimPrefix(key, http.TrailerPrefix)
		if name == "" || isReservedAttemptResponseMetadata(name) {
			continue
		}
		r.client.Header()[key] = append([]string(nil), values...)
	}
}

func controlResultConsistent(result attemptwire.AttemptProxyResult) bool {
	if result.ResponseStarted || result.Written {
		return false
	}
	switch result.Kind {
	case attemptwire.ResultProviderFailed, attemptwire.ResultExecutionRejected,
		attemptwire.ResultProxyRejected, attemptwire.ResultCommitUncertain, attemptwire.ResultCanceled:
		return true
	default:
		return false
	}
}

func responseResultConsistent(result attemptwire.AttemptProxyResult, responseStarted bool) bool {
	if !result.ResponseStarted || !responseStarted {
		return false
	}
	switch result.Kind {
	case attemptwire.ResultSucceeded, attemptwire.ResultProviderFailed,
		attemptwire.ResultCommitUncertain, attemptwire.ResultCanceled:
		return true
	default:
		return false
	}
}

func outcomeFromAttemptResult(
	executionAgentID string,
	path app.RoutePath,
	commit tunnel.CommitState,
	result attemptwire.AttemptProxyResult,
) AttemptOutcome {
	outcome := AttemptOutcome{
		Kind: result.Kind, ExecutionAgentID: executionAgentID, Path: path, Commit: commit,
		ProviderResultKnown: result.ProviderResultKnown, ProviderDispatched: result.ProviderDispatched || result.Dispatches > 0,
		PlanAdvanceAllowed: result.PlanAdvanceAllowed, ResponseStarted: result.ResponseStarted,
		ReasonCode: result.ReasonCode, Dispatches: result.Dispatches,
		Trace: traceRecordFromWire(result.Trace),
		Result: state.AttemptResult{
			PromptTokens: result.PromptTokens, CompletionTokens: result.CompletionTokens,
			CacheReadTokens: result.CacheReadTokens, CacheWriteTokens: result.CacheWriteTokens,
			FirstResponseMs: result.FirstResponseMs, UpstreamModel: result.UpstreamModel,
			TokenSource: result.TokenSource, Written: result.Written,
		},
	}
	if result.Kind != attemptwire.ResultSucceeded {
		outcome.Result.Err = attemptResultError(result)
	}
	if result.Kind == attemptwire.ResultCommitUncertain {
		outcome.Commit = tunnel.CommitUncertain
	}
	return outcome
}

func interruptedAttemptResultOutcome(
	executionAgentID string,
	path app.RoutePath,
	commit tunnel.CommitState,
	result attemptwire.AttemptProxyResult,
	transportCode string,
	transportErr error,
) AttemptOutcome {
	outcome := outcomeFromAttemptResult(executionAgentID, path, commit, result)
	outcome.Trace = traceRecordFromWireFailure(result.Trace)
	outcome.remoteFailureFallback = result.Trace != nil && result.Trace.FailureFallback != nil
	resultErr := outcome.Result.Err
	outcome.Kind = AttemptCommitUncertain
	outcome.Commit = tunnel.CommitUncertain
	outcome.PlanAdvanceAllowed = false
	if transportCode == "" {
		transportCode = reasonAttemptResultInterrupted
	}
	outcome.ReasonCode = transportCode
	outcome.Result.Err = errors.Join(transportErr, resultErr)
	return outcome
}

func traceRecordFromWireFailure(wire *attemptwire.AttemptTraceWire) *trace.TraceRecord {
	record := traceRecordFromWire(wire)
	if record == nil || wire.FailureFallback == nil {
		return record
	}
	record.InboundBody = wire.FailureFallback.InboundBody
	record.OutboundBody = wire.FailureFallback.OutboundBody
	record.UpstreamBody = wire.FailureFallback.ResponseBody
	record.ClientResponseBody = wire.FailureFallback.ClientResponseBody
	return record
}

func traceRecordFromWire(wire *attemptwire.AttemptTraceWire) *trace.TraceRecord {
	if wire == nil {
		return nil
	}
	record := &trace.TraceRecord{
		InboundPath:        wire.InboundPath,
		InboundHeaders:     traceHeaderFromWire(wire.InboundHeaders),
		InboundBody:        wire.InboundBody,
		OutboundPath:       wire.OutboundPath,
		OutboundHeaders:    traceHeaderFromWire(wire.OutboundHeaders),
		OutboundBody:       wire.OutboundBody,
		ResponseHeaders:    traceHeaderFromWire(wire.ResponseHeaders),
		UpstreamBody:       wire.ResponseBody,
		ClientResponseBody: wire.ClientResponseBody,
		UpstreamStatus:     wire.UpstreamStatus,
		FailStage:          trace.Stage(wire.ErrorStage),
		Timings:            map[trace.Stage]time.Duration{},
		Verbose:            true,
	}
	if wire.FailureFallback != nil {
		record.InboundBody = ""
		record.OutboundBody = ""
		record.UpstreamBody = ""
		record.ClientResponseBody = ""
	}
	return record
}

func traceHeaderFromWire(raw string) http.Header {
	if raw == "" {
		return nil
	}
	var header http.Header
	if err := json.Unmarshal([]byte(raw), &header); err != nil {
		return nil
	}
	return header
}

type remoteProviderErrorEnvelope struct {
	Error remoteProviderError `json:"error"`
}

type remoteProviderError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
}

func encodeRemoteProviderErrorBody(message, errorType string) ([]byte, error) {
	return json.Marshal(remoteProviderErrorEnvelope{
		Error: remoteProviderError{Message: message, Type: errorType},
	})
}

func attemptResultError(result attemptwire.AttemptProxyResult) error {
	switch {
	case result.Kind == attemptwire.ResultCanceled && result.ReasonCode == "request_deadline":
		return context.DeadlineExceeded
	case result.Kind == attemptwire.ResultCanceled:
		return context.Canceled
	case result.Kind == attemptwire.ResultProviderFailed:
		body, err := encodeRemoteProviderErrorBody(result.ErrorMessage, result.ErrorType)
		if err != nil {
			return fmt.Errorf("encode remote provider error: %w", err)
		}
		return &common.UpstreamError{
			Status: result.HTTPStatus, ProviderErrorType: result.ErrorType, Body: body,
		}
	case result.ErrorMessage != "":
		return errors.New(result.ErrorMessage)
	case result.ReasonCode != "":
		return errors.New(result.ReasonCode)
	default:
		return fmt.Errorf("attempt ended with %s", result.Kind)
	}
}

func canceledAttemptOutcome(executionAgentID string, path app.RoutePath, commit tunnel.CommitState, started bool, err error) AttemptOutcome {
	return AttemptOutcome{
		Kind: AttemptCanceled, ExecutionAgentID: executionAgentID, Path: path, Commit: commit,
		ResponseStarted: started, ReasonCode: agentRequestCancellationCode(err), Result: state.AttemptResult{Err: err},
	}
}

func interruptedRemoteTransportOutcome(
	executionAgentID string,
	path app.RoutePath,
	commit tunnel.CommitState,
	started bool,
	code string,
	err error,
) AttemptOutcome {
	if code == "" {
		code = reasonAttemptResultInterrupted
	}
	if err == nil {
		err = errors.New(code)
	}
	return AttemptOutcome{
		Kind: AttemptCommitUncertain, ExecutionAgentID: executionAgentID, Path: path,
		Commit: tunnel.CommitUncertain, ResponseStarted: started, ReasonCode: code,
		Result: state.AttemptResult{Err: err},
	}
}

func agentRequestCancellationCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_deadline"
	}
	return "request_canceled"
}

func copyResponseHeader(dst, src http.Header) {
	clean := src.Clone()
	for _, name := range reservedAttemptResponseMetadata {
		deleteHeaderCaseInsensitive(clean, name)
		deleteHeaderCaseInsensitive(clean, http.TrailerPrefix+name)
	}
	cleanTrailerDeclaration(clean)
	for name, values := range clean {
		dst[name] = append([]string(nil), values...)
	}
}

func cleanTrailerDeclaration(header http.Header) {
	var kept []string
	for _, line := range header.Values("Trailer") {
		for token := range strings.SplitSeq(line, ",") {
			name := strings.TrimSpace(token)
			if name != "" && !isReservedAttemptResponseMetadata(name) {
				kept = append(kept, http.CanonicalHeaderKey(name))
			}
		}
	}
	header.Del("Trailer")
	if len(kept) > 0 {
		header.Set("Trailer", strings.Join(kept, ", "))
	}
}

func isReservedAttemptResponseMetadata(name string) bool {
	for _, reserved := range reservedAttemptResponseMetadata {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

func declaredTrailerNames(header http.Header) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, line := range header.Values("Trailer") {
		for token := range strings.SplitSeq(line, ",") {
			if name := http.CanonicalHeaderKey(strings.TrimSpace(token)); name != "" {
				declared[name] = struct{}{}
			}
		}
	}
	return declared
}

func headerDeclares(header http.Header, name string) bool {
	_, ok := declaredTrailerNames(header)[http.CanonicalHeaderKey(name)]
	return ok
}

func deleteHeaderCaseInsensitive(header http.Header, name string) {
	for key := range header {
		if strings.EqualFold(key, name) {
			delete(header, key)
		}
	}
}

func headerValueCaseInsensitive(header http.Header, name string) string {
	values := headerValuesCaseInsensitive(header, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func headerValuesCaseInsensitive(header http.Header, name string) []string {
	for key, values := range header {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}
