package genericapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type completedRemoteTraceStream struct {
	mu      sync.Mutex
	request bytes.Buffer
	ended   chan struct{}
	once    sync.Once
	events  []app.APIResponseEvent
	index   int
}

func newCompletedRemoteTraceStream(events ...app.APIResponseEvent) *completedRemoteTraceStream {
	return &completedRemoteTraceStream{ended: make(chan struct{}), events: events}
}

func (*completedRemoteTraceStream) Open(context.Context, app.APIOpen) error { return nil }
func (stream *completedRemoteTraceStream) SendRequestData(_ context.Context, value []byte) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	_, _ = stream.request.Write(value)
	return nil
}
func (stream *completedRemoteTraceStream) EndRequest(context.Context, wire.Trailers) error {
	stream.once.Do(func() { close(stream.ended) })
	return nil
}
func (stream *completedRemoteTraceStream) Receive(ctx context.Context) (app.APIResponseEvent, error) {
	select {
	case <-stream.ended:
	case <-ctx.Done():
		return app.APIResponseEvent{}, context.Cause(ctx)
	}
	if stream.index >= len(stream.events) {
		<-ctx.Done()
		return app.APIResponseEvent{}, context.Cause(ctx)
	}
	event := stream.events[stream.index]
	stream.index++
	return event, nil
}
func (*completedRemoteTraceStream) Cancel(error) {}
func (*completedRemoteTraceStream) Close() error { return nil }
func (stream *completedRemoteTraceStream) Request() string {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.request.String()
}

type fixedRemoteTraceOpener struct {
	stream *completedRemoteTraceStream
	open   app.APIOpen
	calls  int
}

func (opener *fixedRemoteTraceOpener) OpenHTTPAPIStream(_ context.Context, open app.APIOpen) (app.HTTPAPIStream, error) {
	opener.calls++
	opener.open = open
	return opener.stream, nil
}

func TestRemoteExecutionTraceMergesSourceFactsIntoOneUsageEntry(t *testing.T) {
	result := apiattempt.APIExecutionResult{
		APIUpstreamID: 11, APIUpstreamName: "Primary", UpstreamStatus: http.StatusOK,
		ProviderDispatchKnown: true, ProviderDispatched: true, RequestBytes: 14, ResponseBytes: 15,
		Trace: &apiattempt.APIExecutionTrace{
			RequestHeaders:  map[string][]string{"Content-Type": {"text/plain"}},
			ResponseHeaders: map[string][]string{"Content-Type": {"text/plain"}},
			RequestBody: &apiattempt.APIBodyCapture{
				Captured: true, Status: "captured", Data: "-request", CapturedBytes: 8, TotalBytes: 14, Truncated: true,
			},
			ResponseBody: &apiattempt.APIBodyCapture{
				Captured: true, Status: "captured", Data: "response", CapturedBytes: 8, TotalBytes: 15, Truncated: true,
			},
		},
	}
	stream := newCompletedRemoteTraceStream(
		app.APIResponseEvent{Kind: app.APIResponseHeaders, Headers: &wire.Headers{StatusCode: http.StatusOK, Header: map[string][]string{"Content-Type": {"text/plain"}}}},
		app.APIResponseEvent{Kind: app.APIResponseData, Data: []byte("remote-response")},
		app.APIResponseEvent{Kind: app.APIResponseEnd, Trailers: &wire.Trailers{}},
		app.APIResponseEvent{Kind: app.APIResponseResult, Result: &result},
	)
	opener := &fixedRemoteTraceOpener{stream: stream}
	reporter := &traceUsageReporter{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{order: &[]string{}, route: ServiceRoute{
			Service: protocol.SyncedAPIService{ID: 7, Name: "Weather"},
			Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current"}, Protocol: ProtocolHTTP,
		}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		AgentPicker: fixedTraceAgentPicker{pick: AgentPick{
			ExecutionAgentID: "execution-a", AgentRouteID: 42,
			Target: models.Agent{AgentID: "execution-a", Status: 1, RelayInboundEnabled: true},
		}},
		// behavior change: Generic API execution capability is required before remote dispatch.
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Usage:                 NewUsageBuilder(nil), Reporter: reporter, SourceAgentID: "source-a", TraceSettings: traceSettingsStub{maxBody: 8},
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: NewRemoteHTTPHandler(RemoteHTTPHandlerOptions{Relay: opener})},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", bytes.NewBufferString("remote-request"))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Authorization", "Bearer source-secret")
	response := httptest.NewRecorder()

	sourceTraceRouter(handler, &app.UserInfo{TokenID: 3, UserID: 5, TraceEnabled: true, TraceMode: models.TokenTraceModeFull}).ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "remote-response", response.Body.String())
	require.Equal(t, "remote-request", stream.Request())
	require.Equal(t, 1, opener.calls)
	require.Equal(t, apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeFull, MaxBodyBytes: 8}, opener.open.API.TracePolicy)
	require.Len(t, reporter.entries, 1, "only the Source Handler builds and reports UsageEntry")
	entry := reporter.entries[0]
	require.Equal(t, "source-a", entry.SourceAgentID)
	require.Equal(t, "execution-a", entry.ExecutionAgentID)
	require.NotNil(t, entry.Trace)
	require.Equal(t, "***", http.Header(entry.Trace.SourceRequestHeaders).Get("Authorization"))
	require.Equal(t, "-request", entry.Trace.SourceRequestBody.Data)
	require.Equal(t, "-request", entry.Trace.RequestBody.Data)
	require.Equal(t, "response", entry.Trace.ResponseBody.Data)
}

func TestAPITraceAutoFullOnlyForInfrastructureFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		cancel     bool
		roundTrip  roundTripFunc
		wantTrace  bool
		wantStatus int
	}{
		{
			name: "connect infrastructure failure",
			roundTrip: func(request *http.Request) (*http.Response, error) {
				_, _ = io.ReadAll(request.Body)
				return nil, errors.New("dial tcp: connection refused")
			},
			wantTrace: true, wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "dns infrastructure failure",
			roundTrip: func(request *http.Request) (*http.Response, error) {
				_, _ = io.ReadAll(request.Body)
				return nil, &net.DNSError{Err: "no such host", Name: "upstream.invalid"}
			},
			wantTrace: true, wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "tls infrastructure failure",
			roundTrip: func(request *http.Request) (*http.Response, error) {
				_, _ = io.ReadAll(request.Body)
				return nil, tls.RecordHeaderError{Msg: "invalid TLS record"}
			},
			wantTrace: true, wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "http 500 is an application response",
			roundTrip: func(request *http.Request) (*http.Response, error) {
				_, _ = io.ReadAll(request.Body)
				return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(bytes.NewBufferString("server-failure")), Request: request}, nil
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "client cancellation is not infrastructure",
			cancel: true,
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reporter := &traceUsageReporter{}
			handler := sourceLocalTraceHandler(t, newHTTPTransportWithRoundTripper(test.roundTrip), reporter, protocol.SyncedAPIUpstream{
				ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none",
			})
			router := sourceTraceRouter(handler, &app.UserInfo{TokenID: 3, UserID: 5})
			request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", bytes.NewBufferString("request-body"))
			request.Header.Set("Content-Type", "text/plain")
			if test.cancel {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			require.Equal(t, test.wantStatus, response.Code)
			require.Len(t, reporter.entries, 1)
			entry := reporter.entries[0]
			if test.wantTrace {
				require.NotNil(t, entry.Trace)
				require.True(t, entry.Trace.SourceRequestBody.Captured)
				require.True(t, entry.Trace.RequestBody.Captured)
				return
			}
			require.Nil(t, entry.Trace)
		})
	}
}

type fixedTraceAgentPicker struct{ pick AgentPick }

func (picker fixedTraceAgentPicker) Pick(uint, uint, uint, string) (AgentPick, error) {
	return picker.pick, nil
}

func TestAPITunnelOpenFailureAutoCapturesSourceWithoutTargetTokenLookup(t *testing.T) {
	reporter := &traceUsageReporter{}
	relay := &recordingRelayAPIOpener{err: errors.New("tunnel open failed")}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{order: &[]string{}, route: ServiceRoute{
			Service: protocol.SyncedAPIService{ID: 7, Name: "Weather"},
			Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current"}, Protocol: ProtocolHTTP,
		}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		AgentPicker: fixedTraceAgentPicker{pick: AgentPick{
			ExecutionAgentID: "execution-a",
			Target:           models.Agent{AgentID: "execution-a", Status: 1, RelayInboundEnabled: true},
		}},
		// behavior change: Generic API execution capability is required before remote dispatch.
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Usage:                 NewUsageBuilder(nil), Reporter: reporter, SourceAgentID: "source-a", TraceSettings: traceSettingsStub{maxBody: 8},
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: NewRemoteHTTPHandler(RemoteHTTPHandlerOptions{Relay: relay})},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", bytes.NewBufferString("unread-body"))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()

	sourceTraceRouter(handler, &app.UserInfo{TokenID: 3, UserID: 5}).ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, 1, relay.calls)
	require.Equal(t, apiattempt.APITraceModeOff, relay.open.API.TracePolicy.Mode)
	require.Equal(t, 8, relay.open.API.TracePolicy.MaxBodyBytes)
	require.Len(t, reporter.entries, 1)
	require.NotNil(t, reporter.entries[0].Trace)
	require.Equal(t, "empty", reporter.entries[0].Trace.SourceRequestBody.Status)
}

func TestAPITraceFullStreamingBodyDecisionsTrailersAndDynamicAuthRedaction(t *testing.T) {
	for _, test := range []struct {
		name, contentType, contentEncoding string
		responseBody                       io.ReadCloser
		wantData, wantReason               string
	}{
		{name: "utf8 tail", contentType: "text/plain", responseBody: io.NopCloser(bytes.NewBufferString("prefix-你好z")), wantData: "你好z"},
		{name: "invalid utf8", contentType: "application/json", responseBody: io.NopCloser(bytes.NewReader([]byte{0xff})), wantReason: "binary_detected"},
		{name: "gzip", contentType: "application/json", contentEncoding: "gzip", responseBody: io.NopCloser(bytes.NewBufferString("compressed")), wantReason: "content_encoded"},
		{name: "multipart", contentType: "multipart/form-data; boundary=x", responseBody: io.NopCloser(bytes.NewBufferString("part")), wantReason: "multipart"},
		{name: "binary content type", contentType: "application/octet-stream", responseBody: io.NopCloser(bytes.NewBufferString("plain")), wantReason: "binary_content_type"},
		{name: "read failure", contentType: "text/plain", responseBody: &failingTraceBody{value: []byte("partial")}, wantReason: "capture_read_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			picker := &localHTTPPicker{lease: &APIUpstreamLease{
				Upstream: protocol.SyncedAPIUpstream{
					ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "header",
					Credential: protocol.APIUpstreamCredential{HeaderName: "X-Custom-Auth", HeaderValue: "dynamic-secret"},
				},
				permit: newLocalHTTPPermit(),
			}}
			transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				_, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {test.contentType}, "Content-Encoding": {test.contentEncoding}, "Set-Cookie": {"header-secret"}},
					Trailer:    http.Header{"X-Api-Key": {"trailer-secret"}}, Body: test.responseBody, Request: request,
				}, nil
			}))
			request := httptest.NewRequest(http.MethodPost, "http://gateway.invalid/v1/api/weather/current", bytes.NewBufferString("request-body"))
			request.Header.Set("Content-Type", "text/plain")
			request.Trailer = http.Header{"Cookie": {"trailer-cookie"}}
			rc, response := newLocalHTTPRequestContext(request)
			rc.TracePolicy = apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeFull, MaxBodyBytes: 7}

			require.NoError(t, NewHTTPHandler(picker, transport).Serve(t.Context(), rc))
			require.NotNil(t, rc.Execution.Trace)
			trace := rc.Execution.Trace
			require.Equal(t, "***", http.Header(trace.RequestHeaders).Get("X-Custom-Auth"))
			require.Equal(t, "***", http.Header(trace.RequestTrailers).Get("Cookie"))
			require.Equal(t, "***", http.Header(trace.ResponseHeaders).Get("Set-Cookie"))
			require.Equal(t, "***", http.Header(trace.ResponseTrailers).Get("X-Api-Key"))
			if test.wantReason != "" {
				require.False(t, trace.ResponseBody.Captured)
				require.Equal(t, test.wantReason, trace.ResponseBody.SkipReason)
				require.Empty(t, trace.ResponseBody.Data)
				if test.wantReason == "capture_read_failed" {
					require.Equal(t, "partial", response.Body.String(), "capture must preserve bytes returned with the read error")
					require.Equal(t, "response_body", rc.Execution.ErrorStage)
				}
				return
			}
			require.True(t, trace.ResponseBody.Captured)
			require.Equal(t, test.wantData, trace.ResponseBody.Data)
			require.True(t, trace.ResponseBody.Truncated)
		})
	}
}

type failingTraceBody struct {
	value []byte
	read  bool
}

func (body *failingTraceBody) Read(target []byte) (int, error) {
	if body.read {
		return 0, errors.New("upstream body interrupted")
	}
	body.read = true
	return copy(target, body.value), errors.New("upstream body interrupted")
}

func (*failingTraceBody) Close() error { return nil }

type partialErrorTraceResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	maxWrite int
	err      error
}

func (writer *partialErrorTraceResponseWriter) Header() http.Header { return writer.header }

func (writer *partialErrorTraceResponseWriter) WriteHeader(status int) { writer.status = status }

func (writer *partialErrorTraceResponseWriter) Write(value []byte) (int, error) {
	count := min(len(value), writer.maxWrite)
	_, _ = writer.body.Write(value[:count])
	return count, writer.err
}

func TestAPITraceDistinguishesClientResponseWriteFromUpstreamReadFailure(t *testing.T) {
	t.Run("client response write failure never auto captures body", func(t *testing.T) {
		for _, mode := range []apiattempt.APITraceMode{apiattempt.APITraceModeOff, apiattempt.APITraceModeHeaders} {
			t.Run(string(mode), func(t *testing.T) {
				writeFailure := errors.New("client response write failed")
				var providerCalls atomic.Int32
				transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					providerCalls.Add(1)
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}},
						Body: io.NopCloser(bytes.NewBufferString("provider-response")), Request: request,
					}, nil
				}))
				permit := newLocalHTTPPermit()
				picker := &localHTTPPicker{lease: &APIUpstreamLease{
					Upstream: protocol.SyncedAPIUpstream{
						ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none",
					},
					permit: permit,
				}}
				request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/client-write", http.NoBody)
				response := &partialErrorTraceResponseWriter{
					header: make(http.Header), maxWrite: 4, err: writeFailure,
				}
				ginContext, _ := gin.CreateTestContext(response)
				ginContext.Request = request
				rc := &RequestContext{
					Context: ginContext, Service: protocol.SyncedAPIService{ID: 7},
					Route: protocol.SyncedAPIRoute{ID: 9}, Protocol: ProtocolHTTP, RequestID: "client-write",
					TracePolicy: apiattempt.APITracePolicy{Mode: mode, MaxBodyBytes: 8},
				}

				require.NoError(t, NewHTTPHandler(picker, transport).Serve(request.Context(), rc), "committed response keeps outer writer quiet")
				completion := permit.completion(t)
				require.ErrorIs(t, completion.Err, writeFailure, "typed classification must preserve the original writer error")
				require.Equal(t, "client_response", completion.Result.ErrorStage)
				require.Equal(t, http.StatusOK, response.status)
				require.Equal(t, "prov", response.body.String())
				require.Equal(t, int64(4), completion.Result.ResponseBytes)
				require.Equal(t, int32(1), providerCalls.Load(), "writer failure must not cause another provider dispatch")
				if mode == apiattempt.APITraceModeOff {
					require.Nil(t, completion.Result.Trace)
					return
				}
				require.NotNil(t, completion.Result.Trace)
				require.Equal(t, "trace_headers_only", completion.Result.Trace.ResponseBody.SkipReason)
				require.False(t, completion.Result.Trace.ResponseBody.Captured)
			})
		}
	})

	t.Run("upstream read failure stays infrastructure and auto captures", func(t *testing.T) {
		var providerCalls atomic.Int32
		transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			providerCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}},
				Body: &failingTraceBody{value: []byte("partial")}, Request: request,
			}, nil
		}))
		permit := newLocalHTTPPermit()
		picker := &localHTTPPicker{lease: &APIUpstreamLease{
			Upstream: protocol.SyncedAPIUpstream{
				ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none",
			},
			permit: permit,
		}}
		request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/upstream-read", http.NoBody)
		rc, response := newLocalHTTPRequestContext(request)
		rc.TracePolicy = apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeOff, MaxBodyBytes: 8}

		require.NoError(t, NewHTTPHandler(picker, transport).Serve(request.Context(), rc), "committed response keeps outer writer quiet")
		completion := permit.completion(t)
		require.Error(t, completion.Err)
		require.Equal(t, "upstream body interrupted", completion.Err.Error())
		require.Equal(t, "response_body", completion.Result.ErrorStage)
		require.Equal(t, "partial", response.Body.String())
		require.Equal(t, int64(len("partial")), completion.Result.ResponseBytes)
		require.NotNil(t, completion.Result.Trace)
		require.Equal(t, "capture_read_failed", completion.Result.Trace.ResponseBody.SkipReason)
		require.Equal(t, int32(1), providerCalls.Load())
	})
}

func sourceLocalTraceHandler(t *testing.T, transport *HTTPTransport, reporter *traceUsageReporter, upstream protocol.SyncedAPIUpstream) *Handler {
	t.Helper()
	picker := &localHTTPPicker{lease: &APIUpstreamLease{Upstream: upstream, permit: newLocalHTTPPermit()}}
	return NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{order: &[]string{}, route: ServiceRoute{
			Service: protocol.SyncedAPIService{ID: 7, Name: "Weather"},
			Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current"}, Protocol: ProtocolHTTP,
		}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, AgentPicker: fixedLocalAgentPicker{},
		Usage: NewUsageBuilder(nil), Reporter: reporter, SourceAgentID: "source-a", TraceSettings: traceSettingsStub{maxBody: 8},
		Handlers: map[string]ProtocolHandler{ProtocolHTTP: NewHTTPHandler(picker, transport)},
	})
}

func sourceTraceRouter(handler *Handler, user *app.UserInfo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set(consts.CtxKeyUserInfo, user) })
	RegisterRoutes(v1, handler)
	return router
}

type traceSettingsStub struct{ maxBody int }

func (s traceSettingsStub) Settings() settings.AgentSettings {
	return settings.AgentSettings{TraceMaxBodySize: s.maxBody}
}

type fixedLocalAgentPicker struct{}

func (fixedLocalAgentPicker) Pick(uint, uint, uint, string) (AgentPick, error) {
	return AgentPick{ExecutionAgentID: "source-a"}, nil
}

type traceUsageReporter struct{ entries []protocol.APIUsageEntry }

func (r *traceUsageReporter) EnqueueAPI(entry protocol.APIUsageEntry) error {
	r.entries = append(r.entries, entry)
	return nil
}

func TestSourceHandlerLocalHTTPTracePolicyControlsSuccessfulBodies(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		mode    models.TokenTraceMode
		want    apiattempt.APITraceMode
		body    bool
	}{
		{name: "off", want: apiattempt.APITraceModeOff},
		{name: "headers", enabled: true, mode: models.TokenTraceModeHeaders, want: apiattempt.APITraceModeHeaders},
		{name: "full", enabled: true, mode: models.TokenTraceModeFull, want: apiattempt.APITraceModeFull, body: true},
		{name: "unknown falls back to full", enabled: true, mode: "future", want: apiattempt.APITraceModeFull, body: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			picker := &localHTTPPicker{lease: &APIUpstreamLease{
				Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none"},
				permit:   newLocalHTTPPermit(),
			}}
			transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				payload, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				require.Equal(t, "source-request-body", string(payload))
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/plain"}, "Set-Cookie": {"secret"}},
					Body:       io.NopCloser(http.NoBody), Request: request,
				}, nil
			}))
			reporter := &traceUsageReporter{}
			handler := NewHandler(HandlerOptions{
				Finder: orderedServiceRouteFinder{order: &[]string{}, route: ServiceRoute{
					Service: protocol.SyncedAPIService{ID: 7, Name: "Weather"},
					Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current"}, Protocol: ProtocolHTTP,
				}},
				Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, AgentPicker: fixedLocalAgentPicker{},
				Usage: NewUsageBuilder(nil), Reporter: reporter, SourceAgentID: "source-a",
				TraceSettings: traceSettingsStub{maxBody: 8},
				Handlers:      map[string]ProtocolHandler{ProtocolHTTP: NewHTTPHandler(picker, transport)},
			})

			gin.SetMode(gin.TestMode)
			router := gin.New()
			v1 := router.Group("/v1")
			v1.Use(func(c *gin.Context) {
				c.Set(consts.CtxKeyUserInfo, &app.UserInfo{TokenID: 3, UserID: 5, TraceEnabled: test.enabled, TraceMode: test.mode})
			})
			RegisterRoutes(v1, handler)
			request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", io.NopCloser(io.LimitReader(&repeatingReader{value: []byte("source-request-body")}, int64(len("source-request-body")))))
			request.ContentLength = int64(len("source-request-body"))
			request.Header.Set("Content-Type", "text/plain")
			request.Header.Set("Api-Key", "legacy-api-secret")
			request.Header.Set("X-Goog-Api-Key", "google-api-secret")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			require.Len(t, reporter.entries, 1)
			entry := reporter.entries[0]
			if test.want == apiattempt.APITraceModeOff {
				require.Nil(t, entry.Trace)
				return
			}
			require.NotNil(t, entry.Trace)
			require.Equal(t, "***", http.Header(entry.Trace.ResponseHeaders).Get("Set-Cookie"))
			for _, name := range []string{"Api-Key", "X-Goog-Api-Key"} {
				require.Equal(t, "***", http.Header(entry.Trace.SourceRequestHeaders).Get(name), "Source trace must redact "+name)
				require.Equal(t, "***", http.Header(entry.Trace.RequestHeaders).Get(name), "Execution trace must redact "+name)
			}
			if test.body {
				require.True(t, entry.Trace.SourceRequestBody.Captured)
				require.Equal(t, "est-body", entry.Trace.SourceRequestBody.Data)
				require.True(t, entry.Trace.RequestBody.Captured)
			} else {
				require.Equal(t, "trace_headers_only", entry.Trace.SourceRequestBody.SkipReason)
				require.Equal(t, "trace_headers_only", entry.Trace.RequestBody.SkipReason)
				require.Equal(t, "trace_headers_only", entry.Trace.ResponseBody.SkipReason)
			}
		})
	}
}

type repeatingReader struct {
	value []byte
	pos   int
}

func (r *repeatingReader) Read(target []byte) (int, error) {
	if r.pos >= len(r.value) {
		return 0, io.EOF
	}
	count := copy(target, r.value[r.pos:])
	r.pos += count
	return count, nil
}

type tracePolicyProtocolHandler struct{ got apiattempt.APITracePolicy }

func (h *tracePolicyProtocolHandler) Serve(_ context.Context, rc *RequestContext) error {
	h.got = rc.TracePolicy
	rc.Context.Header("Content-Type", "text/plain")
	_, _ = rc.Context.Writer.Write([]byte("target-response"))
	rc.Execution = apiattempt.APIExecutionResult{ProviderDispatchKnown: true}
	return nil
}

func TestBuildRemoteAPIOpenPropagatesTracePolicyThroughTargetAndResultCodec(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://gateway.invalid/v1/api/weather/current", http.NoBody)
	rc := &RequestContext{
		Context: testGinContext(t, request), Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9},
		Protocol: ProtocolHTTP, RequestID: "trace-policy", UserID: 5, TokenID: 3,
		Agent:       AgentPick{ExecutionAgentID: "execution-a"},
		TracePolicy: apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeFull, MaxBodyBytes: 4096},
	}
	open := buildRemoteAPIOpen(t.Context(), rc)
	require.Equal(t, rc.TracePolicy, open.API.TracePolicy)

	local := &tracePolicyProtocolHandler{}
	finder := &frozenServiceRouteFinder{value: ServiceRoute{Service: rc.Service, Route: rc.Route, Protocol: ProtocolHTTP}}
	target := NewAPITargetHandler(finder, local)
	stream := newTargetHTTPStream("")
	stream.open.API.TracePolicy = open.API.TracePolicy
	stream.open.BodyLength = 0
	stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}
	require.NoError(t, target.serveStream(t.Context(), stream))
	require.Equal(t, rc.TracePolicy, local.got)
	require.Len(t, stream.results, 1)

	payload, err := apiattempt.EncodeResultJSONWithin(stream.results[0], 64*1024)
	require.NoError(t, err)
	decoded, err := apiattempt.DecodeResultJSONWithin(payload, 64*1024)
	require.NoError(t, err)
	require.Equal(t, stream.results[0], decoded)
}

func TestAPITargetHeadersPolicyReturnsValidHeadersOnlyTrace(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7},
		Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7}, Protocol: ProtocolHTTP,
	}}
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}},
			Body: io.NopCloser(bytes.NewBufferString("target-response")), Request: request,
		}, nil
	}))
	stream := newTargetHTTPStream("target-request")
	stream.open.API.TracePolicy = apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeHeaders, MaxBodyBytes: 8}

	require.NoError(t, NewAPITargetHandler(finder, NewHTTPHandler(picker, transport)).serveStream(t.Context(), stream))
	require.Len(t, stream.results, 1)
	result := stream.results[0]
	require.NoError(t, result.Validate())
	require.NotNil(t, result.Trace)
	require.Equal(t, "trace_headers_only", result.Trace.RequestBody.SkipReason)
	require.Equal(t, int64(len("target-request")), result.Trace.RequestBody.TotalBytes)
	require.Empty(t, result.Trace.RequestBody.Data)
	require.Equal(t, "trace_headers_only", result.Trace.ResponseBody.SkipReason)
	require.Equal(t, int64(len("target-response")), result.Trace.ResponseBody.TotalBytes)
	require.Empty(t, result.Trace.ResponseBody.Data)

	payload, err := apiattempt.EncodeResultJSONWithin(result, 64*1024)
	require.NoError(t, err)
	decoded, err := apiattempt.DecodeResultJSONWithin(payload, 64*1024)
	require.NoError(t, err)
	require.Equal(t, result, decoded)
}
