package genericapi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	sharedhttp "github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

type countingRoundTripper struct {
	calls    atomic.Int64
	response *http.Response
	err      error
}

func (r *countingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return r.response, r.err
}

func TestHTTPTransportCallsRoundTripperExactlyOnceOnAllStatuses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			roundTripper := &countingRoundTripper{response: &http.Response{StatusCode: status, Header: make(http.Header)}}
			transport := newHTTPTransportWithRoundTripper(roundTripper)
			result := &apiattempt.APIExecutionResult{}
			request, err := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
			require.NoError(t, err)

			response, err := transport.Do(request, result)
			require.NoError(t, err)
			require.Equal(t, status, response.StatusCode)
			require.Equal(t, int64(1), roundTripper.calls.Load())
			require.True(t, result.ProviderDispatchKnown)
			require.True(t, result.ProviderDispatched)
		})
	}
}

func TestHTTPTransportDoesNotFollowRedirectOrReplayNetworkError(t *testing.T) {
	t.Run("redirect returned unchanged", func(t *testing.T) {
		roundTripper := &countingRoundTripper{response: &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://other.example"}},
		}}
		request, err := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
		require.NoError(t, err)

		response, err := newHTTPTransportWithRoundTripper(roundTripper).Do(request, &apiattempt.APIExecutionResult{})
		require.NoError(t, err)
		require.Equal(t, http.StatusFound, response.StatusCode)
		require.Equal(t, "https://other.example", response.Header.Get("Location"))
		require.Equal(t, int64(1), roundTripper.calls.Load())
	})

	t.Run("network error returned without replay", func(t *testing.T) {
		transportErr := errors.New("dial failed")
		roundTripper := &countingRoundTripper{err: transportErr}
		request, err := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
		require.NoError(t, err)
		result := &apiattempt.APIExecutionResult{}

		response, err := newHTTPTransportWithRoundTripper(roundTripper).Do(request, result)
		require.Nil(t, response)
		require.ErrorIs(t, err, transportErr)
		require.Equal(t, int64(1), roundTripper.calls.Load())
		require.True(t, result.ProviderDispatchKnown)
		require.True(t, result.ProviderDispatched)
	})
}

func TestHTTPTransportRejectsNilInputsWithoutDispatch(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
	require.NoError(t, err)

	t.Run("nil result", func(t *testing.T) {
		roundTripper := &countingRoundTripper{}
		_, err := newHTTPTransportWithRoundTripper(roundTripper).Do(request, nil)
		require.Error(t, err)
		require.Zero(t, roundTripper.calls.Load())
	})

	t.Run("nil request", func(t *testing.T) {
		roundTripper := &countingRoundTripper{}
		result := &apiattempt.APIExecutionResult{}
		_, err := newHTTPTransportWithRoundTripper(roundTripper).Do(nil, result)
		require.Error(t, err)
		require.Zero(t, roundTripper.calls.Load())
		require.True(t, result.ProviderDispatchKnown)
		require.False(t, result.ProviderDispatched)
	})

	t.Run("nil round tripper", func(t *testing.T) {
		result := &apiattempt.APIExecutionResult{}
		_, err := newHTTPTransportWithRoundTripper(nil).Do(request, result)
		require.Error(t, err)
		require.True(t, result.ProviderDispatchKnown)
		require.False(t, result.ProviderDispatched)
	})

	t.Run("nil transport", func(t *testing.T) {
		result := &apiattempt.APIExecutionResult{}
		var transport *HTTPTransport
		_, err := transport.Do(request, result)
		require.Error(t, err)
		require.True(t, result.ProviderDispatchKnown)
		require.False(t, result.ProviderDispatched)
	})
}

func TestNewHTTPTransportPreservesEnvironmentProxyAndSupportsExplicitProxy(t *testing.T) {
	withoutExplicit := NewHTTPTransport("")
	defaultTransport := underlyingHTTPTransport(t, withoutExplicit)
	require.NotNil(t, defaultTransport.Proxy, "empty explicit proxy must retain DefaultTransport environment proxy behavior")

	withExplicit := NewHTTPTransport("http://proxy.example:3128")
	explicitTransport := underlyingHTTPTransport(t, withExplicit)
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "upstream.example"}}
	proxyURL, err := explicitTransport.Proxy(request)
	require.NoError(t, err)
	require.Equal(t, "http://proxy.example:3128", proxyURL.String())
}

func TestHTTPTransportDoesNotRetryStaleReusedConnectionOnWire(t *testing.T) {
	baselineURL, baselineCount := startStaleConnectionProvider(t)
	baseline := sharedhttp.NewTransport("")
	baseline.Proxy = nil
	runWarmThenBusinessRequest(t, &HTTPTransport{roundTripper: baseline}, baselineURL)
	require.Equal(t, int64(2), baselineCount.Load(), "fixture must trigger net/http's hidden stale-connection retry")

	strictURL, strictCount := startStaleConnectionProvider(t)
	runWarmThenBusinessRequest(t, NewHTTPTransport(""), strictURL)
	require.Equal(t, int64(1), strictCount.Load(), "one Generic API dispatch must produce one provider request on the wire")
}

func TestHTTPTransportPreservesCompressedRepresentation(t *testing.T) {
	var receivedAcceptEncoding atomic.Value
	compressed := gzipBytes(t, []byte("provider representation"))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedAcceptEncoding.Store(request.Header.Get("Accept-Encoding"))
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", httpContentLength(compressed))
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(compressed)
	}))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := NewHTTPTransport("").Do(request, &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	require.Equal(t, "", receivedAcceptEncoding.Load())
	require.Equal(t, "gzip", response.Header.Get("Content-Encoding"))
	require.Equal(t, int64(len(compressed)), response.ContentLength)
	require.Equal(t, compressed, body)
}

func TestHTTPTransportUsesHTTP1Only(t *testing.T) {
	protocol := make(chan int, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		protocol <- request.ProtoMajor
		writer.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	transport := NewHTTPTransport("")
	underlying := underlyingHTTPTransport(t, transport)
	serverTransport := server.Client().Transport.(*http.Transport)
	underlying.TLSClientConfig = serverTransport.TLSClientConfig.Clone()

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := transport.Do(request, &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, 1, <-protocol, "HTTP/2 has its own internal replay loop and must not be negotiated")
}

func TestHTTPTransportPoolsConnectionsWithoutReplaying(t *testing.T) {
	t.Run("normal requests reuse one TCP connection", func(t *testing.T) {
		var connections atomic.Int64
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, "ok")
		}))
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				connections.Add(1)
			}
		}
		server.Start()
		t.Cleanup(server.Close)

		transport := NewHTTPTransport("")
		for range 2 {
			response := roundTripRequest(t, transport, http.MethodGet, server.URL, nil)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "ok", string(body))
			require.NoError(t, response.Body.Close())
		}

		require.Equal(t, int64(1), connections.Load(), "normal requests must reuse the same pooled TCP connection")
	})

	t.Run("normal HTTPS requests reuse one TLS connection", func(t *testing.T) {
		var connections atomic.Int64
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, "secure-ok")
		}))
		server.EnableHTTP2 = true
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				connections.Add(1)
			}
		}
		server.StartTLS()
		t.Cleanup(server.Close)

		transport := NewHTTPTransport("")
		underlying := underlyingHTTPTransport(t, transport)
		underlying.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		for range 2 {
			response := roundTripRequest(t, transport, http.MethodGet, server.URL, nil)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "secure-ok", string(body))
			require.NoError(t, response.Body.Close())
		}

		require.Equal(t, int64(1), connections.Load(), "normal HTTPS requests must reuse the same pooled TLS connection")
	})

	t.Run("provider close after reading empty GET is not replayed", func(t *testing.T) {
		transport := NewHTTPTransport("")
		serverURL, requests := startProviderCloseAfterReadServer(t)
		warmPooledConnection(t, transport, serverURL)

		request, err := http.NewRequest(http.MethodGet, serverURL+"/empty-get", nil)
		require.NoError(t, err)
		request.Header.Set("X-Original", "preserved")
		response, err := transport.Do(request, &apiattempt.APIExecutionResult{})
		if response != nil {
			_ = response.Body.Close()
		}
		require.ErrorIs(t, err, errProviderReplayPrevented)
		require.Equal(t, int64(1), requests.Load(), "one business GET must reach the provider at most once")
	})

	t.Run("fixed and streaming POST preserve wire semantics without replay", func(t *testing.T) {
		tests := []struct {
			name             string
			path             string
			body             func(http.Header) io.ReadCloser
			contentLength    int64
			wantBody         string
			wantTransfer     []string
			wantTrailerValue string
		}{
			{
				name: "fixed body", path: "/fixed-post", contentLength: 5, wantBody: "fixed",
				body: func(http.Header) io.ReadCloser { return io.NopCloser(strings.NewReader("fixed")) },
			},
			{
				name: "streaming body with trailer", path: "/stream-post", contentLength: -1,
				wantBody: "stream-body", wantTransfer: []string{"chunked"}, wantTrailerValue: "sha256:value",
				body: func(trailer http.Header) io.ReadCloser {
					return &trailerStreamingBody{
						reader: strings.NewReader("stream-body"), trailer: trailer,
						key: "X-Upload-Checksum", value: "sha256:value",
					}
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				transport := NewHTTPTransport("")
				serverURL, requests, captured := startCapturingProviderCloseAfterReadServer(t)
				warmPooledConnection(t, transport, serverURL)

				trailer := make(http.Header)
				if test.wantTrailerValue != "" {
					trailer["X-Upload-Checksum"] = nil
				}
				request, err := http.NewRequest(http.MethodPost, serverURL+test.path, test.body(trailer))
				require.NoError(t, err)
				request.ContentLength = test.contentLength
				request.GetBody = nil
				request.Trailer = trailer
				request.Header.Set("X-Original", "preserved")

				response, err := transport.Do(request, &apiattempt.APIExecutionResult{})
				if response != nil {
					_ = response.Body.Close()
				}
				require.Error(t, err)
				require.Equal(t, int64(1), requests.Load())
				wire := <-captured
				require.Equal(t, http.MethodPost, wire.method)
				require.Equal(t, "preserved", wire.header.Get("X-Original"))
				require.Equal(t, test.wantBody, wire.body)
				require.Equal(t, test.contentLength, wire.contentLength)
				require.Equal(t, test.wantTransfer, wire.transferEncoding)
				require.Equal(t, test.wantTrailerValue, wire.trailer.Get("X-Upload-Checksum"))
			})
		}
	})

	t.Run("one guarded retry does not close another active pooled request", func(t *testing.T) {
		type requestResult struct {
			body string
			err  error
		}
		started := make(chan struct{})
		release := make(chan struct{})
		var failedRequests atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/hold":
				close(started)
				<-release
				_, _ = io.WriteString(writer, "held-ok")
			case "/fail":
				failedRequests.Add(1)
				closeHijackedConnection(writer)
			default:
				writer.WriteHeader(http.StatusNoContent)
			}
		}))
		t.Cleanup(server.Close)

		transport := NewHTTPTransport("")
		holdResult := make(chan requestResult, 1)
		holdRequest := mustHTTPRequest(t, http.MethodGet, server.URL+"/hold", nil)
		var workers conc.WaitGroup
		workers.Go(func() {
			response, err := transport.Do(holdRequest, &apiattempt.APIExecutionResult{})
			if err != nil {
				holdResult <- requestResult{err: err}
				return
			}
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil {
				holdResult <- requestResult{err: readErr}
				return
			}
			holdResult <- requestResult{body: string(body), err: closeErr}
		})
		<-started

		warmPooledConnection(t, transport, server.URL)
		failedResponse, err := transport.Do(mustHTTPRequest(t, http.MethodGet, server.URL+"/fail", nil), &apiattempt.APIExecutionResult{})
		if failedResponse != nil {
			_ = failedResponse.Body.Close()
		}
		close(release)
		require.Error(t, err)
		require.Equal(t, int64(1), failedRequests.Load())

		result := <-holdResult
		require.NoError(t, result.err)
		require.Equal(t, "held-ok", result.body)
		workers.Wait()
	})
}

func TestHTTPTransportPreservesExistingTrace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	var gotConnections atomic.Int64
	var wroteRequests atomic.Int64
	trace := &httptrace.ClientTrace{
		GotConn:      func(httptrace.GotConnInfo) { gotConnections.Add(1) },
		WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequests.Add(1) },
	}
	request := mustHTTPRequest(t, http.MethodGet, server.URL, nil)
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := NewHTTPTransport("").Do(request, &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, int64(1), gotConnections.Load(), "the no-replay trace must compose with the caller trace")
	require.Equal(t, int64(1), wroteRequests.Load(), "the no-replay write hook must compose with the caller trace")
}

func TestHTTPTransportReleasesDispatchContextWithResponseBody(t *testing.T) {
	readFailure := errors.New("response read failed")
	tests := []struct {
		name      string
		body      io.ReadCloser
		wantCause error
		act       func(*testing.T, io.ReadCloser, context.Context)
	}{
		{
			name: "EOF", body: io.NopCloser(strings.NewReader("response")), wantCause: io.EOF,
			act: func(t *testing.T, body io.ReadCloser, _ context.Context) {
				value, err := io.ReadAll(body)
				require.NoError(t, err)
				require.Equal(t, "response", string(value))
			},
		},
		{
			name: "Close", body: io.NopCloser(strings.NewReader("response")), wantCause: context.Canceled,
			act: func(t *testing.T, body io.ReadCloser, ctx context.Context) {
				value := make([]byte, 1)
				count, err := body.Read(value)
				require.NoError(t, err)
				require.Equal(t, 1, count)
				requireContextActive(t, ctx)
				require.NoError(t, body.Close())
			},
		},
		{
			name: "read error", body: &failingReadCloser{err: readFailure}, wantCause: readFailure,
			act: func(t *testing.T, body io.ReadCloser, _ context.Context) {
				_, err := body.Read(make([]byte, 1))
				require.ErrorIs(t, err, readFailure)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contexts := make(chan context.Context, 1)
			roundTripper := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				contexts <- request.Context()
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: test.body}, nil
			})
			response, err := newHTTPTransportWithRoundTripper(roundTripper).Do(
				mustHTTPRequest(t, http.MethodGet, "https://upstream.example", nil),
				&apiattempt.APIExecutionResult{},
			)
			require.NoError(t, err)
			dispatchContext := <-contexts
			requireContextActive(t, dispatchContext)

			test.act(t, response.Body, dispatchContext)
			requireContextDone(t, dispatchContext)
			require.ErrorIs(t, context.Cause(dispatchContext), test.wantCause)
		})
	}
}

func TestHTTPTransportReplayPreventionErrorPreservesTransportCause(t *testing.T) {
	transportCause := errors.New("second connection closed")
	roundTripper := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		require.NotNil(t, trace)
		trace.GetConn("upstream.example:443")
		trace.GetConn("upstream.example:443")
		return nil, transportCause
	})

	response, err := newHTTPTransportWithRoundTripper(roundTripper).Do(
		mustHTTPRequest(t, http.MethodGet, "https://upstream.example", nil),
		&apiattempt.APIExecutionResult{},
	)
	require.Nil(t, response)
	require.ErrorIs(t, err, errProviderReplayPrevented)
	require.ErrorIs(t, err, transportCause)
}

type capturedHTTPRequest struct {
	method           string
	header           http.Header
	body             string
	contentLength    int64
	transferEncoding []string
	trailer          http.Header
}

type trailerStreamingBody struct {
	reader  *strings.Reader
	trailer http.Header
	key     string
	value   string
	ended   bool
}

func (b *trailerStreamingBody) Read(value []byte) (int, error) {
	count, err := b.reader.Read(value)
	if errors.Is(err, io.EOF) && !b.ended {
		b.ended = true
		b.trailer.Set(b.key, b.value)
	}
	return count, err
}

func (*trailerStreamingBody) Close() error { return nil }

type failingReadCloser struct {
	err error
}

func (b *failingReadCloser) Read([]byte) (int, error) { return 0, b.err }
func (*failingReadCloser) Close() error               { return nil }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func roundTripRequest(t *testing.T, transport *HTTPTransport, method, target string, body io.Reader) *http.Response {
	t.Helper()
	response, err := transport.Do(mustHTTPRequest(t, method, target, body), &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	return response
}

func underlyingHTTPTransport(t *testing.T, transport *HTTPTransport) *http.Transport {
	t.Helper()
	require.NotNil(t, transport)
	guard, ok := transport.roundTripper.(*noReplayRoundTripper)
	require.True(t, ok)
	underlying, ok := guard.base.(*http.Transport)
	require.True(t, ok)
	return underlying
}

func mustHTTPRequest(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, body)
	require.NoError(t, err)
	return request
}

func warmPooledConnection(t *testing.T, transport *HTTPTransport, serverURL string) {
	t.Helper()
	response := roundTripRequest(t, transport, http.MethodGet, serverURL+"/warm", nil)
	_, err := io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
}

func startProviderCloseAfterReadServer(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	serverURL, requests, _ := startCapturingProviderCloseAfterReadServer(t)
	return serverURL, requests
}

func startCapturingProviderCloseAfterReadServer(t *testing.T) (string, *atomic.Int64, <-chan capturedHTTPRequest) {
	t.Helper()
	requests := &atomic.Int64{}
	captured := make(chan capturedHTTPRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/warm" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return
		}
		requests.Add(1)
		captured <- capturedHTTPRequest{
			method: request.Method, header: request.Header.Clone(), body: string(body),
			contentLength: request.ContentLength, transferEncoding: append([]string(nil), request.TransferEncoding...),
			trailer: request.Trailer.Clone(),
		}
		closeHijackedConnection(writer)
	}))
	t.Cleanup(server.Close)
	return server.URL, requests, captured
}

func closeHijackedConnection(writer http.ResponseWriter) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return
	}
	connection, _, err := hijacker.Hijack()
	if err == nil {
		_ = connection.Close()
	}
}

func requireContextActive(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("dispatch context canceled before response body completion: %v", context.Cause(ctx))
	default:
	}
}

func requireContextDone(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("dispatch context was not released after response body completion")
	}
}

func startStaleConnectionProvider(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	businessRequests := &atomic.Int64{}
	var workers conc.WaitGroup
	workers.Go(func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			handleStaleConnection(connection, businessRequests)
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		workers.Wait()
	})
	return "http://" + listener.Addr().String(), businessRequests
}

func handleStaleConnection(connection net.Conn, businessRequests *atomic.Int64) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	for {
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
		if request.URL.Path == "/business" {
			businessRequests.Add(1)
			return // close after reading the request but before any response byte
		}
		_, _ = io.WriteString(connection, "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")
		if request.Close {
			return
		}
	}
}

func runWarmThenBusinessRequest(t *testing.T, transport *HTTPTransport, baseURL string) {
	t.Helper()
	warm, err := http.NewRequest(http.MethodGet, baseURL+"/warm", nil)
	require.NoError(t, err)
	warmResponse, err := transport.Do(warm, &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	require.NoError(t, warmResponse.Body.Close())

	business, err := http.NewRequest(http.MethodGet, baseURL+"/business", nil)
	require.NoError(t, err)
	response, err := transport.Do(business, &apiattempt.APIExecutionResult{})
	require.Error(t, err)
	if response != nil {
		_ = response.Body.Close()
	}
}

func gzipBytes(t *testing.T, value []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, err := writer.Write(value)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

func httpContentLength(value []byte) string {
	return strconv.Itoa(len(value))
}
