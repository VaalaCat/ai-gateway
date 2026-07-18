package attemptproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

type ResponseExecutor struct{}

func NewResponseExecutor() *ResponseExecutor {
	return &ResponseExecutor{}
}

func (e *ResponseExecutor) Execute(
	rctx *state.RelayContext,
	attempt state.Attempt,
	provider attemptexec.ProviderAttemptExecutor,
) {
	if e == nil || invalidRelayContext(rctx) || !attemptexec.ProviderExecutorAvailable(provider) {
		writeResponseExecutorRejection(rctx)
		return
	}

	base := rctx.Context.Writer
	writer := newAttemptResponseWriter(base)
	rctx.Context.Writer = writer
	providerResult := provider.Execute(rctx, attempt)
	proxyResult := resultFromProvider(rctx, providerResult, writer)
	if writer.resultTrailerUnsupported && !writer.ResponseStarted() {
		rctx.Context.Writer = base
		writeControl(base, unsupportedTrailerControlResult(proxyResult))
		return
	}

	switch {
	case providerResult.Outcome.Written || writer.ResponseStarted():
		writer.FinishResponse(proxyResult)
	case providerResult.Outcome.Err == nil:
		writer.CommitBufferedSuccess(proxyResult)
	default:
		rctx.Context.Writer = base
		writeControl(base, proxyResult)
	}
}

func invalidRelayContext(rctx *state.RelayContext) bool {
	return rctx == nil || rctx.Context == nil || rctx.Context.Writer == nil || rctx.State == nil
}

func writeResponseExecutorRejection(rctx *state.RelayContext) {
	if rctx == nil || rctx.Context == nil || rctx.Context.Writer == nil || rctx.Context.Writer.Written() {
		return
	}
	writeProxyRejection(rctx.Context.Writer, http.StatusInternalServerError, "response_executor_unavailable", "attempt response executor unavailable")
}

func writeControl(writer gin.ResponseWriter, result attemptwire.AttemptProxyResult) {
	if writer == nil || writer.Written() {
		return
	}
	body, err := attemptwire.EncodeResultJSON(result)
	if err != nil {
		body, _ = json.Marshal(minimalControlResult(result, "control_encode_failed"))
	}
	header := writer.Header()
	header.Set("Content-Type", "application/json")
	header.Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	deleteHeaderCaseInsensitive(header, "Content-Length")
	header.Del("Trailer")
	header.Del(http.TrailerPrefix + attemptwire.TrailerResult)
	writer.WriteHeader(http.StatusOK)
	writer.WriteHeaderNow()
	_, _ = writer.Write(body)
}

type attemptResponseWriter struct {
	base                     gin.ResponseWriter
	header                   http.Header
	status                   int
	committed                bool
	responseStarted          bool
	uncertain                bool
	resultTrailerUnsupported bool
}

var _ gin.ResponseWriter = (*attemptResponseWriter)(nil)

func newAttemptResponseWriter(base gin.ResponseWriter) *attemptResponseWriter {
	started := base != nil && base.Written()
	return &attemptResponseWriter{
		base: base, header: make(http.Header), status: http.StatusOK,
		committed: started, responseStarted: started, uncertain: started,
	}
}

func (w *attemptResponseWriter) Header() http.Header {
	if w.committed && w.base != nil {
		return w.base.Header()
	}
	return w.header
}

func (w *attemptResponseWriter) WriteHeader(code int) {
	if w == nil || w.committed || code <= 0 {
		return
	}
	w.status = code
	w.resultTrailerUnsupported = statusDisallowsResultTrailer(code)
}

func (w *attemptResponseWriter) WriteHeaderNow() {
	_ = w.commit()
}

func (w *attemptResponseWriter) Write(body []byte) (n int, err error) {
	if err := w.commit(); err != nil {
		return 0, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			w.markUncertain()
			n = 0
			err = fmt.Errorf("attempt response write interrupted: %v", recovered)
		}
	}()
	n, err = w.base.Write(body)
	if err != nil || n != len(body) {
		w.markUncertain()
	}
	return n, err
}

func (w *attemptResponseWriter) WriteString(body string) (n int, err error) {
	if err := w.commit(); err != nil {
		return 0, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			w.markUncertain()
			n = 0
			err = fmt.Errorf("attempt response string write interrupted: %v", recovered)
		}
	}()
	n, err = w.base.WriteString(body)
	if err != nil || n != len(body) {
		w.markUncertain()
	}
	return n, err
}

func (w *attemptResponseWriter) Status() int {
	if w == nil {
		return 0
	}
	if w.committed && w.base != nil {
		return w.base.Status()
	}
	return w.status
}

func (w *attemptResponseWriter) Size() int {
	if w == nil || !w.committed || w.base == nil {
		return -1
	}
	return w.base.Size()
}

func (w *attemptResponseWriter) Written() bool {
	return w != nil && (w.committed || (w.base != nil && w.base.Written()))
}

func (w *attemptResponseWriter) Flush() {
	if err := w.commit(); err != nil {
		return
	}
	defer func() {
		if recover() != nil {
			w.markUncertain()
		}
	}()
	w.base.Flush()
}

func (w *attemptResponseWriter) Hijack() (conn net.Conn, rw *bufio.ReadWriter, err error) {
	if w == nil || w.base == nil {
		return nil, nil, errors.New("attempt response writer unavailable")
	}
	w.markUncertain()
	defer func() {
		if recovered := recover(); recovered != nil {
			conn, rw = nil, nil
			err = fmt.Errorf("attempt response hijack interrupted: %v", recovered)
		}
	}()
	return w.base.Hijack()
}

func (w *attemptResponseWriter) CloseNotify() (notified <-chan bool) {
	if w != nil && w.base != nil {
		defer func() {
			if recover() != nil {
				notified = closedNotification()
			}
		}()
		return w.base.CloseNotify()
	}
	return closedNotification()
}

func closedNotification() <-chan bool {
	closed := make(chan bool)
	close(closed)
	return closed
}

func (w *attemptResponseWriter) Pusher() http.Pusher {
	if w == nil || w.base == nil {
		return nil
	}
	return w.base.Pusher()
}

func (w *attemptResponseWriter) ResponseStarted() bool {
	return w != nil && (w.responseStarted || (w.base != nil && w.base.Written()))
}

func (w *attemptResponseWriter) FinishResponse(result attemptwire.AttemptProxyResult) {
	if w == nil {
		return
	}
	if err := w.commit(); err != nil {
		w.markUncertain()
	}
	result.ResponseStarted = result.ResponseStarted || w.ResponseStarted()
	if result.ResponseStarted {
		result.PlanAdvanceAllowed = false
	}
	w.writeResultTrailer(result)
}

func (w *attemptResponseWriter) CommitBufferedSuccess(result attemptwire.AttemptProxyResult) {
	w.FinishResponse(result)
}

func (w *attemptResponseWriter) commit() (err error) {
	if w == nil || w.base == nil {
		return errors.New("attempt response writer unavailable")
	}
	if w.committed {
		if w.base.Written() {
			w.responseStarted = true
		}
		return nil
	}
	if statusDisallowsResultTrailer(w.status) {
		w.resultTrailerUnsupported = true
		return errResultTrailerUnsupported
	}

	deleteHeaderCaseInsensitive(w.header, "Content-Length")
	deleteHeaderCaseInsensitive(w.base.Header(), "Content-Length")
	w.committed = true
	w.responseStarted = true
	copyAttemptHeaders(w.base.Header(), w.header)
	w.base.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	declareResultTrailer(w.base.Header())
	defer func() {
		if recovered := recover(); recovered != nil {
			w.markUncertain()
			err = fmt.Errorf("attempt response commit interrupted: %v", recovered)
		}
	}()
	w.base.WriteHeader(w.status)
	w.base.WriteHeaderNow()
	return nil
}

func (w *attemptResponseWriter) writeResultTrailer(result attemptwire.AttemptProxyResult) {
	if w == nil || w.base == nil {
		return
	}
	if w.uncertain {
		result = minimalCommitUncertainResult(result, "response_commit_uncertain")
	}
	encoded, err := attemptwire.EncodeResult(result)
	if err != nil {
		result = minimalCommitUncertainResult(result, "result_encode_failed")
		encoded, err = attemptwire.EncodeResult(result)
	}
	if err != nil {
		return
	}
	w.base.Header().Set(http.TrailerPrefix+attemptwire.TrailerResult, encoded)
}

func (w *attemptResponseWriter) markUncertain() {
	if w == nil {
		return
	}
	w.responseStarted = true
	w.uncertain = true
}

func copyAttemptHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, attemptwire.HeaderMode) || strings.EqualFold(key, attemptwire.TrailerResult) {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
}

func deleteHeaderCaseInsensitive(header http.Header, name string) {
	for key := range header {
		if strings.EqualFold(key, name) {
			delete(header, key)
		}
	}
}

var errResultTrailerUnsupported = errors.New("HTTP status does not support attempt result trailer")

func statusDisallowsResultTrailer(status int) bool {
	return status < http.StatusOK || status == http.StatusNoContent || status == http.StatusNotModified
}

func declareResultTrailer(header http.Header) {
	for _, line := range header.Values("Trailer") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), attemptwire.TrailerResult) {
				return
			}
		}
	}
	header.Add("Trailer", attemptwire.TrailerResult)
}

func minimalCommitUncertainResult(result attemptwire.AttemptProxyResult, reason string) attemptwire.AttemptProxyResult {
	return attemptwire.AttemptProxyResult{
		Kind:                attemptwire.ResultCommitUncertain,
		Dispatches:          result.Dispatches,
		ProviderDispatched:  result.ProviderDispatched,
		ProviderResultKnown: result.ProviderResultKnown,
		Written:             result.Written,
		ResponseStarted:     true,
		PlanAdvanceAllowed:  false,
		ReasonCode:          reason,
		ErrorMessage:        "response commit state uncertain",
	}
}

func minimalControlResult(result attemptwire.AttemptProxyResult, reason string) attemptwire.AttemptProxyResult {
	return attemptwire.AttemptProxyResult{
		Kind:                attemptwire.ResultCommitUncertain,
		Dispatches:          result.Dispatches,
		ProviderDispatched:  result.ProviderDispatched || result.Dispatches > 0,
		ProviderResultKnown: result.ProviderResultKnown,
		ReasonCode:          reason,
		ErrorMessage:        "control result encoding failed",
	}
}

func unsupportedTrailerControlResult(result attemptwire.AttemptProxyResult) attemptwire.AttemptProxyResult {
	return attemptwire.AttemptProxyResult{
		Kind:                attemptwire.ResultCommitUncertain,
		PromptTokens:        result.PromptTokens,
		CompletionTokens:    result.CompletionTokens,
		CacheReadTokens:     result.CacheReadTokens,
		CacheWriteTokens:    result.CacheWriteTokens,
		FirstResponseMs:     result.FirstResponseMs,
		UpstreamModel:       result.UpstreamModel,
		TokenSource:         result.TokenSource,
		Dispatches:          result.Dispatches,
		ProviderDispatched:  result.ProviderDispatched,
		ProviderResultKnown: true,
		Written:             false,
		PlanAdvanceAllowed:  false,
		ResponseStarted:     false,
		ReasonCode:          "result_trailer_unsupported",
		ErrorMessage:        "provider response status cannot carry attempt result trailer",
	}
}

func resultFromProvider(
	rctx *state.RelayContext,
	provider attemptexec.ProviderResult,
	writer *attemptResponseWriter,
) attemptwire.AttemptProxyResult {
	outcome := provider.Outcome
	result := attemptwire.AttemptProxyResult{
		PromptTokens:        outcome.PromptTokens,
		CompletionTokens:    outcome.CompletionTokens,
		CacheReadTokens:     outcome.CacheReadTokens,
		CacheWriteTokens:    outcome.CacheWriteTokens,
		FirstResponseMs:     outcome.FirstResponseMs,
		UpstreamModel:       outcome.UpstreamModel,
		TokenSource:         outcome.TokenSource,
		Dispatches:          provider.Dispatches,
		ProviderDispatched:  provider.ProviderDispatched || provider.Dispatches > 0,
		ProviderResultKnown: true,
		Written:             outcome.Written,
		ResponseStarted:     outcome.Written || writer.ResponseStarted(),
	}
	classifyProviderResult(&result, outcome)
	result.PlanAdvanceAllowed = outcome.Err != nil && result.Kind != attemptwire.ResultCanceled &&
		!result.ResponseStarted && !resilience.Classify(outcome).AbortAll
	if writer != nil && writer.uncertain {
		result = minimalCommitUncertainResult(result, "response_commit_uncertain")
	}
	result.Trace = finalizeAttemptTrace(rctx)
	return result
}

func classifyProviderResult(result *attemptwire.AttemptProxyResult, outcome state.AttemptResult) {
	if result == nil {
		return
	}
	if outcome.Err == nil {
		result.Kind = attemptwire.ResultSucceeded
		return
	}
	if errors.Is(outcome.Err, context.Canceled) || errors.Is(outcome.Err, context.DeadlineExceeded) {
		result.Kind = attemptwire.ResultCanceled
		if errors.Is(outcome.Err, context.DeadlineExceeded) {
			result.ReasonCode = "request_deadline"
			result.ErrorMessage = "request deadline exceeded"
		} else {
			result.ReasonCode = "request_canceled"
			result.ErrorMessage = "request canceled"
		}
		return
	}

	var upstream *common.UpstreamError
	if errors.As(outcome.Err, &upstream) && upstream != nil {
		result.Kind = attemptwire.ResultProviderFailed
		result.HTTPStatus = upstream.Status
		result.ErrorType = upstream.ProviderErrorType
		if upstream.Status == 0 {
			result.ReasonCode = "provider_transport_error"
			result.ErrorMessage = "provider transport failed"
		} else {
			result.ReasonCode = "provider_http_error"
			result.ErrorMessage = fmt.Sprintf("provider returned HTTP %d", upstream.Status)
		}
		return
	}
	if result.ProviderDispatched {
		result.Kind = attemptwire.ResultProviderFailed
		result.ReasonCode = "provider_execution_failed"
		result.ErrorMessage = "provider execution failed"
		return
	}
	result.Kind = attemptwire.ResultExecutionRejected
	if errors.Is(outcome.Err, state.ErrRateLimited) {
		result.ReasonCode = "attempt_rate_limited"
		result.ErrorMessage = "attempt rate limited"
		return
	}
	result.ReasonCode = "attempt_execution_rejected"
	result.ErrorMessage = "attempt execution rejected"
}

func finalizeAttemptTrace(rctx *state.RelayContext) *attemptwire.AttemptTraceWire {
	if rctx == nil || rctx.State == nil || rctx.State.Recorder == nil {
		return nil
	}
	record := rctx.State.Recorder.Finalize()
	if record == nil || !record.Verbose {
		return nil
	}
	return traceToWire(record)
}

func traceToWire(record *trace.TraceRecord) *attemptwire.AttemptTraceWire {
	if record == nil {
		return nil
	}
	return &attemptwire.AttemptTraceWire{
		InboundPath:        record.InboundPath,
		OutboundPath:       record.OutboundPath,
		InboundHeaders:     marshalTraceHeaders(record.InboundHeaders),
		OutboundHeaders:    marshalTraceHeaders(record.OutboundHeaders),
		InboundBody:        record.InboundBody,
		OutboundBody:       record.OutboundBody,
		ResponseHeaders:    marshalTraceHeaders(record.ResponseHeaders),
		ResponseBody:       record.UpstreamBody,
		ClientResponseBody: record.ClientResponseBody,
		UpstreamStatus:     record.UpstreamStatus,
		ErrorStage:         string(record.FailStage),
	}
}

func marshalTraceHeaders(header http.Header) string {
	raw, err := json.Marshal(map[string][]string(header))
	if err != nil {
		return ""
	}
	return string(raw)
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
