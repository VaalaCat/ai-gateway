package genericapi

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
)

type APIServiceRouteByIDFinder interface {
	FindServiceRouteByID(serviceID, routeID uint) (ServiceRoute, error)
}

type apiTargetStream interface {
	OpenMetadata() wire.Open
	ReceiveRequest(context.Context) (agenttunnel.APIRequestEvent, error)
	SendHeaders(context.Context, wire.Headers) error
	SendResponseData(context.Context, []byte) error
	EndResponse(context.Context, wire.Trailers) error
	SendResult(context.Context, apiattempt.APIExecutionResult) error
}

// APITargetHandler bridges one committed tunnel stream into the local HTTP
// executor. Source authorization, quota, and Agent selection are deliberately
// absent; the target consumes only the frozen service/route IDs.
type APITargetHandler struct {
	finder APIServiceRouteByIDFinder
	local  ProtocolHandler
}

func NewAPITargetHandler(finder APIServiceRouteByIDFinder, local ProtocolHandler) *APITargetHandler {
	return &APITargetHandler{finder: finder, local: local}
}

func (h *APITargetHandler) ServeHTTPAPI(ctx context.Context, stream *agenttunnel.APITargetStream) error {
	if stream == nil {
		return ErrExecutionUnavailable
	}
	return h.serveStream(ctx, stream)
}

func (h *APITargetHandler) serveStream(ctx context.Context, stream apiTargetStream) error {
	if ctx == nil || h == nil || h.finder == nil || h.local == nil || stream == nil {
		return ErrExecutionUnavailable
	}
	open := stream.OpenMetadata()
	result := apiattempt.APIExecutionResult{ProviderDispatchKnown: true}
	response := newTargetHTTPResponseWriter(ctx, stream)
	route, request, err := h.prepareRequest(ctx, stream, open)
	if err == nil {
		ginContext := &gin.Context{Request: request, Writer: response}
		rc := &RequestContext{
			Context: ginContext, Service: route.Service, Route: route.Route, Protocol: ProtocolHTTP,
			Subpath: open.API.Subpath, RequestID: open.RequestID,
			UserID: open.API.UserID, GroupID: open.API.GroupID, TokenID: open.API.TokenID,
			TracePolicy: open.API.TracePolicy,
		}
		err = h.local.Serve(ctx, rc)
		result = rc.Execution
	}
	if !result.ProviderDispatchKnown {
		result.ProviderDispatchKnown = true
	}
	if err != nil {
		if result.ErrorStage == "" {
			result.ErrorStage = "execution"
		}
		if result.ErrorCode == "" {
			result.ErrorCode = ErrorCode(err)
		}
	}
	if finishErr := response.finish(err); finishErr != nil {
		return finishErr
	}
	result = apiattempt.NormalizeRateLimitResult(result)
	if validationErr := result.Validate(); validationErr != nil {
		return validationErr
	}
	return stream.SendResult(ctx, result)
}

func (h *APITargetHandler) prepareRequest(
	ctx context.Context,
	stream apiTargetStream,
	open wire.Open,
) (ServiceRoute, *http.Request, error) {
	if open.API == nil || open.RequestID == "" || open.API.APIServiceID == 0 || open.API.APIRouteID == 0 ||
		open.API.Protocol != apiattempt.APIProtocolHTTP || open.Method != open.API.Method || !open.API.TracePolicy.Valid() {
		return ServiceRoute{}, nil, ErrExecutionUnavailable
	}
	route, err := h.finder.FindServiceRouteByID(open.API.APIServiceID, open.API.APIRouteID)
	if err != nil {
		return ServiceRoute{}, nil, err
	}
	requestURL := &url.URL{Scheme: "http", Host: "gateway.invalid", Path: open.Path, RawQuery: open.API.RawQuery}
	trailers := make(http.Header, len(open.API.RequestTrailerKeys))
	for _, name := range open.API.RequestTrailerKeys {
		trailers[http.CanonicalHeaderKey(name)] = nil
	}
	body := &targetHTTPRequestBody{ctx: ctx, stream: stream, trailers: trailers}
	request := &http.Request{
		Method: open.Method, URL: requestURL, Header: http.Header(open.Header).Clone(), Body: body,
		ContentLength: open.BodyLength, Host: requestURL.Host, Trailer: trailers,
	}
	return route, request.WithContext(ctx), nil
}

type targetHTTPRequestBody struct {
	ctx      context.Context
	stream   apiTargetStream
	trailers http.Header
	pending  []byte
	done     bool
}

func (b *targetHTTPRequestBody) Read(target []byte) (int, error) {
	for len(b.pending) == 0 && !b.done {
		event, err := b.stream.ReceiveRequest(b.ctx)
		if err != nil {
			return 0, err
		}
		switch event.Kind {
		case agenttunnel.APIRequestData:
			b.pending = event.Data
		case agenttunnel.APIRequestEnd:
			copyHTTPHeader(b.trailers, http.Header(event.Trailers.Header))
			b.done = true
		default:
			return 0, ErrExecutionUnavailable
		}
	}
	if len(b.pending) == 0 && b.done {
		return 0, io.EOF
	}
	count := copy(target, b.pending)
	b.pending = b.pending[count:]
	return count, nil
}

func (b *targetHTTPRequestBody) Close() error {
	if b != nil {
		b.done = true
		b.pending = nil
	}
	return nil
}

type targetHTTPResponseWriter struct {
	ctx      context.Context
	stream   apiTargetStream
	header   http.Header
	status   int
	size     int
	written  bool
	trailers []string
	err      error
}

func newTargetHTTPResponseWriter(ctx context.Context, stream apiTargetStream) *targetHTTPResponseWriter {
	return &targetHTTPResponseWriter{ctx: ctx, stream: stream, header: make(http.Header), status: http.StatusOK, size: -1}
}

func (w *targetHTTPResponseWriter) Header() http.Header { return w.header }

func (w *targetHTTPResponseWriter) WriteHeader(status int) {
	if w.written || w.err != nil {
		return
	}
	w.status = status
	w.trailers = declaredTargetResponseTrailers(w.header)
	ordinary := w.header.Clone()
	ordinary.Del("Trailer")
	declared := make(http.Header, len(w.trailers))
	for _, name := range w.trailers {
		declared[name] = nil
	}
	w.err = w.stream.SendHeaders(w.ctx, wire.Headers{StatusCode: status, Header: ordinary, Trailer: declared})
	w.written = w.err == nil
	if w.written {
		w.size = 0
	}
}

func (w *targetHTTPResponseWriter) Write(value []byte) (int, error) {
	if !w.written {
		w.WriteHeader(w.status)
	}
	if w.err != nil {
		return 0, w.err
	}
	if len(value) == 0 {
		return 0, nil
	}
	w.err = w.stream.SendResponseData(w.ctx, value)
	if w.err != nil {
		return 0, w.err
	}
	w.size += len(value)
	return len(value), nil
}

func (w *targetHTTPResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *targetHTTPResponseWriter) WriteHeaderNow()     { w.WriteHeader(w.status) }
func (w *targetHTTPResponseWriter) Status() int         { return w.status }
func (w *targetHTTPResponseWriter) Size() int           { return w.size }
func (w *targetHTTPResponseWriter) Written() bool       { return w.written }
func (w *targetHTTPResponseWriter) Flush()              { w.WriteHeaderNow() }
func (w *targetHTTPResponseWriter) Pusher() http.Pusher { return nil }
func (w *targetHTTPResponseWriter) CloseNotify() <-chan bool {
	done := make(chan bool, 1)
	go func() {
		<-w.ctx.Done()
		done <- true
	}()
	return done
}
func (w *targetHTTPResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}

func (w *targetHTTPResponseWriter) finish(executionErr error) error {
	if w.err != nil {
		return w.err
	}
	if !w.written {
		status := http.StatusOK
		if executionErr != nil {
			status = gatewayErrorFor(executionErr).status
		}
		w.WriteHeader(status)
	}
	if w.err != nil {
		return w.err
	}
	final := make(http.Header, len(w.trailers))
	for _, name := range w.trailers {
		final[name] = append([]string(nil), w.header.Values(name)...)
	}
	return w.stream.EndResponse(w.ctx, wire.Trailers{Header: final})
}

func declaredTargetResponseTrailers(header http.Header) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, value := range header.Values("Trailer") {
		for _, name := range strings.Split(value, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}
	return result
}

var _ agenttunnel.APITargetHandler = (*APITargetHandler)(nil)
var _ gin.ResponseWriter = (*targetHTTPResponseWriter)(nil)
