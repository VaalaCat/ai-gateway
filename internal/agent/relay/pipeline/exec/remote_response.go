package exec

import (
	"bytes"
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

const reasonAttemptResultMissing = "attempt_result_missing"

var reservedAttemptResponseMetadata = [...]string{
	attemptwire.HeaderMode,
	attemptwire.TrailerResult,
}

type attemptResponseReceiver struct {
	client          http.ResponseWriter
	header          http.Header
	status          int
	mode            string
	wroteHeader     bool
	responseStarted bool
	resultDeclared  bool
	declared        map[string]struct{}
	control         bytes.Buffer
	controlTooLarge bool
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
	r.resultDeclared = headerDeclares(r.header, attemptwire.TrailerResult)
	if r.mode != attemptwire.ModeResponse || r.client == nil {
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
	if r.control.Len()+len(body) > attemptwire.MaxResultWireBytes {
		r.controlTooLarge = true
		remaining := attemptwire.MaxResultWireBytes + 1 - r.control.Len()
		if remaining > 0 {
			_, _ = r.control.Write(body[:min(remaining, len(body))])
		}
		return len(body), nil
	}
	_, _ = r.control.Write(body)
	return len(body), nil
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

func (r *attemptResponseReceiver) Finish(
	executionAgentID string,
	path app.RoutePath,
	commit tunnel.CommitState,
	transportErr error,
) AttemptOutcome {
	if r == nil {
		return missingAttemptResult(executionAgentID, path, transportErr)
	}
	if errors.Is(transportErr, context.Canceled) || errors.Is(transportErr, context.DeadlineExceeded) {
		return canceledAttemptOutcome(executionAgentID, path, commit, r.responseStarted, transportErr)
	}
	if r.mode == attemptwire.ModeResponse {
		r.copyProviderTrailers()
		if transportErr != nil {
			return missingAttemptResultWithStarted(executionAgentID, path, r.responseStarted, transportErr)
		}
		return r.finishResponse(executionAgentID, path, commit)
	}
	if transportErr != nil {
		return missingAttemptResult(executionAgentID, path, transportErr)
	}
	if r.mode == attemptwire.ModeControl {
		return r.finishControl(executionAgentID, path, commit)
	}
	if r.mode == "" && r.status >= http.StatusBadRequest {
		if outcome, ok := r.finishProxyRejection(executionAgentID, path, commit); ok {
			return outcome
		}
	}
	return missingAttemptResultWithStarted(executionAgentID, path, r.responseStarted, nil)
}

func (r *attemptResponseReceiver) finishControl(executionAgentID string, path app.RoutePath, commit tunnel.CommitState) AttemptOutcome {
	if r.status != http.StatusOK || r.controlTooLarge {
		return missingAttemptResultWithStarted(executionAgentID, path, false, nil)
	}
	result, err := decodeControlResult(r.control.Bytes())
	if err != nil || !controlResultConsistent(result) {
		return missingAttemptResultWithStarted(executionAgentID, path, false, err)
	}
	return outcomeFromAttemptResult(executionAgentID, path, commit, result)
}

func (r *attemptResponseReceiver) finishResponse(executionAgentID string, path app.RoutePath, commit tunnel.CommitState) AttemptOutcome {
	if !r.resultDeclared {
		return missingAttemptResultWithStarted(executionAgentID, path, r.responseStarted, nil)
	}
	values := headerValuesCaseInsensitive(r.header, attemptwire.TrailerResult)
	if len(values) != 1 {
		return missingAttemptResultWithStarted(executionAgentID, path, r.responseStarted, nil)
	}
	result, err := attemptwire.DecodeResult(values[0])
	if err != nil || !responseResultConsistent(result, r.responseStarted) {
		return missingAttemptResultWithStarted(executionAgentID, path, r.responseStarted, err)
	}
	return outcomeFromAttemptResult(executionAgentID, path, commit, result)
}

func (r *attemptResponseReceiver) finishProxyRejection(executionAgentID string, path app.RoutePath, commit tunnel.CommitState) (AttemptOutcome, bool) {
	if r.controlTooLarge {
		return AttemptOutcome{}, false
	}
	result, err := decodeControlResult(r.control.Bytes())
	if err != nil || result.Kind != attemptwire.ResultProxyRejected {
		return AttemptOutcome{}, false
	}
	if result.HTTPStatus == 0 {
		result.HTTPStatus = r.status
	}
	return outcomeFromAttemptResult(executionAgentID, path, commit, result), true
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
}

func decodeControlResult(raw []byte) (attemptwire.AttemptProxyResult, error) {
	var result attemptwire.AttemptProxyResult
	if len(raw) == 0 || len(raw) > attemptwire.MaxResultWireBytes || json.Unmarshal(raw, &result) != nil || result.Validate() != nil {
		return attemptwire.AttemptProxyResult{}, attemptwire.ErrInvalidContract
	}
	return result, nil
}

func controlResultConsistent(result attemptwire.AttemptProxyResult) bool {
	if result.ResponseStarted || result.Written {
		return false
	}
	switch result.Kind {
	case attemptwire.ResultProviderFailed, attemptwire.ResultExecutionRejected,
		attemptwire.ResultCommitUncertain, attemptwire.ResultCanceled:
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

func traceRecordFromWire(wire *attemptwire.AttemptTraceWire) *trace.TraceRecord {
	if wire == nil {
		return nil
	}
	return &trace.TraceRecord{
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

func missingAttemptResult(executionAgentID string, path app.RoutePath, err error) AttemptOutcome {
	return missingAttemptResultWithStarted(executionAgentID, path, false, err)
}

func missingAttemptResultWithStarted(executionAgentID string, path app.RoutePath, started bool, err error) AttemptOutcome {
	if err == nil {
		err = errors.New(reasonAttemptResultMissing)
	}
	return AttemptOutcome{
		Kind: AttemptCommitUncertain, ExecutionAgentID: executionAgentID, Path: path,
		Commit: tunnel.CommitUncertain, ResponseStarted: started, ReasonCode: reasonAttemptResultMissing,
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
