package genericapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/tracecapture"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
)

type APITraceSettingsReader interface {
	Settings() settings.AgentSettings
}

func apiTracePolicy(c *gin.Context, reader APITraceSettingsReader) apiattempt.APITracePolicy {
	maxBodyBytes := 0
	if reader != nil {
		maxBodyBytes = reader.Settings().TraceMaxBodySize
	}
	if c == nil {
		policy, _ := tracecapture.PolicyFromToken(false, "", maxBodyBytes)
		return policy
	}
	value, ok := c.Get(consts.CtxKeyUserInfo)
	user, ok := value.(*app.UserInfo)
	if !ok || user == nil {
		policy, _ := tracecapture.PolicyFromToken(false, "", maxBodyBytes)
		return policy
	}
	policy, _ := tracecapture.PolicyFromToken(user.TraceEnabled, string(user.TraceMode), maxBodyBytes)
	return policy
}

type apiBodyCapture struct {
	window          *tracecapture.BodyWindow
	contentType     string
	contentEncoding string
}

func newAPIBodyCapture(policy apiattempt.APITracePolicy, header http.Header) *apiBodyCapture {
	return &apiBodyCapture{
		window:          tracecapture.NewBodyWindow(policy.MaxBodyBytes),
		contentType:     header.Get("Content-Type"),
		contentEncoding: header.Get("Content-Encoding"),
	}
}

func (capture *apiBodyCapture) finish(protocol string, mode apiattempt.APITraceMode) *apiattempt.APIBodyCapture {
	if capture == nil || capture.window == nil {
		return nil
	}
	decision := tracecapture.BodyCaptureDecision{Capture: true}
	if mode == apiattempt.APITraceModeHeaders {
		decision = tracecapture.BodyCaptureDecision{Reason: tracecapture.ReasonTraceHeadersOnly}
	} else if mode == apiattempt.APITraceModeFull {
		decision = tracecapture.DecideBody(protocol, capture.contentType, capture.contentEncoding, capture.window.Text())
	}
	body := capture.window.Capture(decision)
	return &body
}

type capturedReadCloser struct {
	body    io.ReadCloser
	window  *tracecapture.BodyWindow
	readEOF bool
}

func wrapCapturedBody(body io.ReadCloser, capture *apiBodyCapture) io.ReadCloser {
	if body == nil {
		return http.NoBody
	}
	if body == http.NoBody || capture == nil || capture.window == nil {
		return body
	}
	return &capturedReadCloser{body: body, window: capture.window}
}

func (reader *capturedReadCloser) Read(value []byte) (int, error) {
	count, err := reader.body.Read(value)
	if count > 0 {
		_, _ = reader.window.Write(value[:count])
	}
	if errors.Is(err, io.EOF) {
		reader.readEOF = true
	} else if err != nil {
		reader.window.MarkReadFailed()
	}
	return count, err
}

func (reader *capturedReadCloser) Close() error { return reader.body.Close() }

type sourceAPITraceCapture struct {
	policy           apiattempt.APITracePolicy
	protocol         string
	headers          http.Header
	headersTruncated bool
	body             *apiBodyCapture
	request          *http.Request
}

func startSourceAPITrace(rc *RequestContext) {
	if rc == nil || rc.Context == nil || rc.Context.Request == nil {
		return
	}
	request := rc.Context.Request
	headers, truncated := tracecapture.RedactHeaders(request.Header)
	capture := &sourceAPITraceCapture{
		policy: rc.TracePolicy, protocol: rc.Protocol, headers: headers,
		headersTruncated: truncated, body: newAPIBodyCapture(rc.TracePolicy, request.Header), request: request,
	}
	request.Body = wrapCapturedBody(request.Body, capture.body)
	rc.sourceTrace = capture
}

func finishSourceAPITrace(rc *RequestContext, executionErr error) {
	if rc == nil || rc.sourceTrace == nil {
		return
	}
	autoFull := infrastructureAPIFailure(requestContext(rc), rc.Execution, executionErr)
	mode := effectiveAPITraceMode(rc.TracePolicy.Mode, autoFull)
	enforceSourceAPITracePolicy(&rc.Execution, mode)
	source := rc.sourceTrace.finish(autoFull)
	if source == nil {
		return
	}
	if rc.Execution.Trace == nil {
		rc.Execution.Trace = &apiattempt.APIExecutionTrace{}
	}
	rc.Execution.Trace.SourceRequestHeaders = source.SourceRequestHeaders
	rc.Execution.Trace.SourceRequestTrailers = source.SourceRequestTrailers
	rc.Execution.Trace.SourceRequestHeadersTruncated = source.SourceRequestHeadersTruncated
	rc.Execution.Trace.SourceRequestTrailersTruncated = source.SourceRequestTrailersTruncated
	rc.Execution.Trace.SourceRequestBody = source.SourceRequestBody
}

func enforceSourceAPITracePolicy(result *apiattempt.APIExecutionResult, mode apiattempt.APITraceMode) {
	if result == nil {
		return
	}
	if mode == apiattempt.APITraceModeOff {
		result.Trace = nil
		return
	}
	if result.Trace == nil {
		result.Trace = &apiattempt.APIExecutionTrace{}
	}
	trace := result.Trace
	trace.SourceRequestHeaders = nil
	trace.SourceRequestTrailers = nil
	trace.SourceRequestHeadersTruncated = false
	trace.SourceRequestTrailersTruncated = false
	trace.SourceRequestBody = nil
	if mode != apiattempt.APITraceModeHeaders {
		return
	}
	trace.RequestBody = headersOnlyAPIBody(trace.RequestBody)
	trace.ResponseBody = headersOnlyAPIBody(trace.ResponseBody)
}

func headersOnlyAPIBody(observed *apiattempt.APIBodyCapture) *apiattempt.APIBodyCapture {
	total := int64(0)
	if observed != nil {
		total = observed.TotalBytes
	}
	return &apiattempt.APIBodyCapture{
		Status: "skipped", SkipReason: tracecapture.ReasonTraceHeadersOnly, TotalBytes: total,
	}
}

func requestContext(rc *RequestContext) context.Context {
	if rc == nil || rc.Context == nil || rc.Context.Request == nil {
		return nil
	}
	return rc.Context.Request.Context()
}

func (capture *sourceAPITraceCapture) finish(autoFull bool) *apiattempt.APIExecutionTrace {
	if capture == nil || capture.request == nil {
		return nil
	}
	mode := effectiveAPITraceMode(capture.policy.Mode, autoFull)
	if mode == apiattempt.APITraceModeOff {
		return nil
	}
	trailers, trailersTruncated := tracecapture.RedactHeaders(capture.request.Trailer)
	return &apiattempt.APIExecutionTrace{
		SourceRequestHeaders: capture.headers, SourceRequestTrailers: trailers,
		SourceRequestHeadersTruncated: capture.headersTruncated, SourceRequestTrailersTruncated: trailersTruncated,
		SourceRequestBody: capture.body.finish(capture.protocol, mode),
	}
}

type executionAPITraceCapture struct {
	policy                    apiattempt.APITracePolicy
	protocol                  string
	request, response         *http.Request
	upstreamResponse          *http.Response
	requestHeaders            http.Header
	responseHeaders           http.Header
	requestHeadersTruncated   bool
	responseHeadersTruncated  bool
	requestBody, responseBody *apiBodyCapture
	dynamicAuthHeader         string
}

func newExecutionAPITraceCapture(policy apiattempt.APITracePolicy, protocol string) *executionAPITraceCapture {
	return &executionAPITraceCapture{policy: policy, protocol: protocol}
}

func (capture *executionAPITraceCapture) wrapRequest(request *http.Request, dynamicAuthHeader string) {
	if capture == nil || request == nil {
		return
	}
	capture.request = request
	capture.dynamicAuthHeader = dynamicAuthHeader
	capture.requestHeaders, capture.requestHeadersTruncated = tracecapture.RedactHeaders(request.Header, dynamicAuthHeader)
	capture.requestBody = newAPIBodyCapture(capture.policy, request.Header)
	request.Body = wrapCapturedBody(request.Body, capture.requestBody)
}

func (capture *executionAPITraceCapture) wrapResponse(response *http.Response) {
	if capture == nil || response == nil {
		return
	}
	capture.upstreamResponse = response
	capture.responseHeaders, capture.responseHeadersTruncated = tracecapture.RedactHeaders(response.Header)
	capture.responseBody = newAPIBodyCapture(capture.policy, response.Header)
	response.Body = wrapCapturedBody(response.Body, capture.responseBody)
}

func (capture *executionAPITraceCapture) finish(autoFull bool) *apiattempt.APIExecutionTrace {
	if capture == nil {
		return nil
	}
	mode := effectiveAPITraceMode(capture.policy.Mode, autoFull)
	if mode == apiattempt.APITraceModeOff {
		return nil
	}
	requestTrailers := http.Header(nil)
	if capture.request != nil {
		requestTrailers = capture.request.Trailer
	}
	responseTrailers := http.Header(nil)
	if capture.upstreamResponse != nil {
		responseTrailers = capture.upstreamResponse.Trailer
	}
	requestTrailers, requestTrailersTruncated := tracecapture.RedactHeaders(requestTrailers, capture.dynamicAuthHeader)
	responseTrailers, responseTrailersTruncated := tracecapture.RedactHeaders(responseTrailers)
	return &apiattempt.APIExecutionTrace{
		RequestHeaders: capture.requestHeaders, RequestTrailers: requestTrailers,
		ResponseHeaders: capture.responseHeaders, ResponseTrailers: responseTrailers,
		RequestHeadersTruncated: capture.requestHeadersTruncated, RequestTrailersTruncated: requestTrailersTruncated,
		ResponseHeadersTruncated: capture.responseHeadersTruncated, ResponseTrailersTruncated: responseTrailersTruncated,
		RequestBody: capture.requestBody.finish(capture.protocol, mode), ResponseBody: capture.responseBody.finish(capture.protocol, mode),
	}
}

func effectiveAPITraceMode(configured apiattempt.APITraceMode, autoFull bool) apiattempt.APITraceMode {
	if autoFull {
		return apiattempt.APITraceModeFull
	}
	if configured == "" {
		return apiattempt.APITraceModeOff
	}
	return configured
}

type apiInfrastructureError struct{ cause error }

func (failure *apiInfrastructureError) Error() string { return failure.cause.Error() }
func (failure *apiInfrastructureError) Unwrap() error { return failure.cause }

func markAPIInfrastructureFailure(err error) error {
	if err == nil {
		return nil
	}
	return &apiInfrastructureError{cause: err}
}

func infrastructureAPIFailure(ctx context.Context, result apiattempt.APIExecutionResult, err error) bool {
	if ctx != nil && ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var marked *apiInfrastructureError
	if errors.As(err, &marked) {
		return true
	}
	switch result.ErrorStage {
	case "transport", "response_header", "response_body", "tunnel", "protocol", "dns", "tls", "connect":
		return true
	default:
		return false
	}
}
