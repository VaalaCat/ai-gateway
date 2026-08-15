package genericapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/sourcegraph/conc/pool"
)

type HTTPUpstreamPicker interface {
	Pick(uint, apiattempt.APIProtocol, string) (*APIUpstreamLease, error)
}

// ErrUploadIdleTimeout means an HTTP request body made no forwarding progress
// for the configured continuous idle interval.
var ErrUploadIdleTimeout = errors.New("generic API upload idle timeout")

// HTTPHandler executes one local Generic API HTTP request against one frozen
// upstream lease. It never retries, replays, falls back, or re-picks.
type HTTPHandler struct {
	picker         HTTPUpstreamPicker
	transport      *HTTPTransport
	builder        RequestBuilder
	limiter        SourceLimiter
	settingsFinder SettingsFinder
}

// WithSettings injects the Agent's lock-free settings snapshot into the
// handler and its HTTP transport. It is configured during server assembly.
func (h *HTTPHandler) WithSettings(finder SettingsFinder) *HTTPHandler {
	if h == nil {
		return nil
	}
	h.settingsFinder = finder
	if h.transport != nil {
		h.transport.WithSettings(finder)
	}
	return h
}

func NewHTTPHandler(picker HTTPUpstreamPicker, transport *HTTPTransport, limiters ...SourceLimiter) *HTTPHandler {
	handler := &HTTPHandler{picker: picker, transport: transport}
	if len(limiters) > 0 {
		handler.limiter = limiters[0]
	}
	return handler
}

func (h *HTTPHandler) Serve(ctx context.Context, rc *RequestContext) error {
	if err := validLocalHTTPRequest(ctx, h, rc); err != nil {
		return err
	}
	startedAt := time.Now()
	result := &apiattempt.APIExecutionResult{ProviderDispatchKnown: true}
	traceCapture := newExecutionAPITraceCapture(rc.TracePolicy, rc.Protocol)
	var executionErr error
	defer func() { rc.Execution = *result }()
	defer func() {
		result.Trace = traceCapture.finish(infrastructureAPIFailure(ctx, *result, executionErr))
	}()
	lease, err := h.picker.Pick(rc.Route.BackendID, apiattempt.APIProtocolHTTP, rc.RequestID)
	if err != nil {
		result.ErrorStage, result.ErrorCode = "upstream_pick", CodeUnavailable
		executionErr = err
		return err
	}
	if lease == nil {
		return unavailableUpstream("picker returned nil upstream lease")
	}
	defer func() {
		lease.Finish(APIBreakerCompletion{Result: result, Err: executionErr, ClientAbort: clientAbortReason(ctx)})
	}()
	if !lease.valid() {
		executionErr = unavailableUpstream("picker returned invalid upstream lease")
		return executionErr
	}
	result.APIUpstreamID = lease.Upstream.ID
	result.APIUpstreamName = lease.Upstream.Name
	rc.UpstreamName = lease.Upstream.Name
	limiterPermit, err := h.acquireUpstreamLimiter(ctx, rc, lease.Upstream.ID)
	if err != nil {
		mergeRateLimitResult(result, rateLimitResult(err))
		result.ErrorStage, result.ErrorCode = "limiter", ErrorCode(err)
		return err
	}
	mergeRateLimitResult(result, rateLimitResult(limiterPermit))
	if limiterPermit != nil {
		defer limiterPermit.Release()
	}

	executionErr = h.execute(ctx, rc, lease, result, traceCapture, startedAt)
	if executionErr != nil && result.ErrorStage != "" && result.ErrorCode == "" {
		result.ErrorCode = ErrorCode(executionErr)
	}
	if executionErr != nil && rc.Context.Writer.Written() {
		return nil
	}
	return executionErr
}

func (h *HTTPHandler) acquireUpstreamLimiter(ctx context.Context, rc *RequestContext, upstreamID uint) (APIPermit, error) {
	if h.limiter == nil {
		return nil, nil
	}
	return h.limiter.Acquire(ctx, APIRequestFacts{
		UserID: rc.UserID, GroupID: rc.GroupID, TokenID: rc.TokenID,
		APIServiceID: rc.Service.ID, APIRouteID: rc.Route.ID, APIUpstreamID: upstreamID, RequestID: rc.RequestID,
	})
}

func validLocalHTTPRequest(ctx context.Context, handler *HTTPHandler, rc *RequestContext) error {
	if ctx == nil || handler == nil || handler.picker == nil || handler.transport == nil || rc == nil ||
		rc.Context == nil || rc.Context.Request == nil || rc.Context.Writer == nil || rc.Service.ID == 0 ||
		rc.RequestID == "" || rc.Protocol != ProtocolHTTP {
		return fmt.Errorf("%w: invalid local HTTP handler input", ErrExecutionUnavailable)
	}
	return nil
}

func (h *HTTPHandler) execute(
	ctx context.Context,
	rc *RequestContext,
	lease *APIUpstreamLease,
	result *apiattempt.APIExecutionResult,
	traceCapture *executionAPITraceCapture,
	startedAt time.Time,
) error {
	if err := enableLocalHTTPFullDuplex(rc.Context.Writer); err != nil {
		result.ErrorStage = "response_setup"
		return err
	}
	request, err := h.builder.Build(ctx, RequestBuilderInput{
		Request: rc.Context.Request, Route: rc.Route, Upstream: lease.Upstream,
		Subpath: rc.Subpath, RawQuery: rc.Context.Request.URL.RawQuery,
	})
	if err != nil {
		result.ErrorStage = "request_build"
		return err
	}
	body := newStreamingRequestBody(request.Body, rc.Context.Request.Trailer, request.Trailer, h.uploadIdleTimeout())
	request.Body = body
	traceCapture.wrapRequest(request, lease.Upstream.Credential.HeaderName)
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer func() {
		stopClose()
		_ = body.Close()
		if body.reachedEOF.Load() {
			result.RequestBytes = body.bytesRead.Load()
		}
	}()
	return h.stream(ctx, rc.Context.Writer, request, result, traceCapture, startedAt)
}

func (h *HTTPHandler) uploadIdleTimeout() time.Duration {
	if h == nil || h.settingsFinder == nil {
		return 0
	}
	return time.Duration(h.settingsFinder.Settings().APIUploadIdleTimeoutMs) * time.Millisecond
}

func enableLocalHTTPFullDuplex(client http.ResponseWriter) error {
	err := http.NewResponseController(client).EnableFullDuplex()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

type upstreamHTTPResponse struct {
	response *http.Response
	err      error
}

func (h *HTTPHandler) stream(
	ctx context.Context,
	client http.ResponseWriter,
	request *http.Request,
	result *apiattempt.APIExecutionResult,
	traceCapture *executionAPITraceCapture,
	startedAt time.Time,
) error {
	responses := make(chan upstreamHTTPResponse, 1)
	workers := pool.New().WithContext(ctx).WithCancelOnError().WithFirstError()
	workers.Go(func(workerContext context.Context) error {
		response, err := h.transport.Do(request.WithContext(workerContext), result)
		responses <- upstreamHTTPResponse{response: response, err: err}
		return nil
	})
	workers.Go(func(workerContext context.Context) error {
		outcome := <-responses
		if outcome.err != nil {
			if outcome.response != nil && outcome.response.Body != nil {
				_ = outcome.response.Body.Close()
			}
			if errors.Is(outcome.err, ErrUploadIdleTimeout) {
				result.ErrorStage = "upload"
			} else {
				result.ErrorStage = "transport"
			}
			return outcome.err
		}
		return copyLocalHTTPResponse(workerContext, client, outcome.response, result, traceCapture, startedAt)
	})
	return workers.Wait()
}

func copyLocalHTTPResponse(
	ctx context.Context,
	client http.ResponseWriter,
	response *http.Response,
	result *apiattempt.APIExecutionResult,
	traceCapture *executionAPITraceCapture,
	startedAt time.Time,
) error {
	if response == nil {
		result.ErrorStage = "response_header"
		return ErrInvalidUpstreamResponse
	}
	traceCapture.wrapResponse(response)
	body := newCloseOnceBody(response.Body)
	response.Body = body
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer func() {
		stopClose()
		_ = body.Close()
	}()

	streamResult, err := newHTTPStream(startedAt).Copy(client, response)
	mergeHTTPStreamResult(result, streamResult)
	if err != nil {
		result.ErrorStage = responseErrorStage(streamResult, err)
	}
	return err
}

func responseErrorStage(stream apiattempt.APIExecutionResult, err error) string {
	var clientWriteFailure *clientResponseWriteError
	if errors.As(err, &clientWriteFailure) {
		return "client_response"
	}
	if stream.UpstreamStatus == 0 {
		return "response_header"
	}
	return "response_body"
}

func mergeHTTPStreamResult(target *apiattempt.APIExecutionResult, stream apiattempt.APIExecutionResult) {
	target.UpstreamStatus = stream.UpstreamStatus
	target.ResponseBytes = stream.ResponseBytes
	target.FirstByteMs = stream.FirstByteMs
}

func clientAbortReason(ctx context.Context) APIClientAbortReason {
	if ctx == nil {
		return ""
	}
	switch ctx.Err() {
	case context.Canceled:
		return APIClientAbortCanceled
	case context.DeadlineExceeded:
		return APIClientAbortDeadlineExceeded
	default:
		return ""
	}
}

type streamingRequestBody struct {
	body        io.ReadCloser
	source      http.Header
	target      http.Header
	bytesRead   atomic.Int64
	reachedEOF  atomic.Bool
	closeOnce   sync.Once
	closeErr    error
	idleTimeout time.Duration
	idleExpired atomic.Bool
	idleMu      sync.Mutex
	idleTimer   *time.Timer
}

func newStreamingRequestBody(
	body io.ReadCloser,
	source, target http.Header,
	idleTimeout time.Duration,
) *streamingRequestBody {
	if body == nil {
		body = http.NoBody
	}
	return &streamingRequestBody{body: body, source: source, target: target, idleTimeout: idleTimeout}
}

func (b *streamingRequestBody) Read(value []byte) (int, error) {
	b.startIdleTimer()
	count, err := b.body.Read(value)
	if b.idleExpired.Load() {
		return count, ErrUploadIdleTimeout
	}
	b.bytesRead.Add(int64(count))
	if count > 0 {
		b.resetIdleTimer()
	}
	if errors.Is(err, io.EOF) {
		b.stopIdleTimer()
		if trailerErr := copyFinalRequestTrailers(b.target, b.source); trailerErr != nil {
			return count, trailerErr
		}
		b.reachedEOF.Store(true)
	} else if err != nil {
		b.stopIdleTimer()
	}
	return count, err
}

func (b *streamingRequestBody) Close() error {
	b.stopIdleTimer()
	b.closeOnce.Do(func() { b.closeErr = b.body.Close() })
	return b.closeErr
}

func (b *streamingRequestBody) startIdleTimer() {
	if b.idleTimeout <= 0 || b.idleExpired.Load() {
		return
	}
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	if b.idleTimer == nil {
		b.idleTimer = time.AfterFunc(b.idleTimeout, b.expireIdleUpload)
	}
}

func (b *streamingRequestBody) resetIdleTimer() {
	if b.idleTimeout <= 0 || b.idleExpired.Load() {
		return
	}
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	if b.idleTimer != nil {
		b.idleTimer.Reset(b.idleTimeout)
	}
}

func (b *streamingRequestBody) stopIdleTimer() {
	b.idleMu.Lock()
	defer b.idleMu.Unlock()
	if b.idleTimer != nil {
		b.idleTimer.Stop()
		b.idleTimer = nil
	}
}

func (b *streamingRequestBody) expireIdleUpload() {
	b.idleMu.Lock()
	b.idleTimer = nil
	b.idleMu.Unlock()
	b.idleExpired.Store(true)
	_ = b.Close()
}

func copyFinalRequestTrailers(target, source http.Header) error {
	for name := range target {
		values := source.Values(name)
		if !validHeaderValues(values) {
			return ErrUnsafeUpstreamTrailer
		}
		target[name] = append([]string(nil), values...)
	}
	return nil
}

type closeOnceBody struct {
	body      io.ReadCloser
	closeOnce sync.Once
	closeErr  error
}

func newCloseOnceBody(body io.ReadCloser) *closeOnceBody {
	if body == nil {
		body = http.NoBody
	}
	return &closeOnceBody{body: body}
}

func (b *closeOnceBody) Read(value []byte) (int, error) {
	return b.body.Read(value)
}

func (b *closeOnceBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.body.Close() })
	return b.closeErr
}
