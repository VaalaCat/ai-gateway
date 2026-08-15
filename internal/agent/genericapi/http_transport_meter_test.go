package genericapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

var (
	errInjectedZeroWrite    = errors.New("injected zero-byte write failure")
	errInjectedPartialWrite = errors.New("injected partial write failure")
)

type testWriteMeter interface {
	WrittenBytes() uint64
}

type controlledWriteMode int32

const (
	controlledWriteNormal controlledWriteMode = iota
	controlledWriteZeroFailure
	controlledWritePartialFailure
)

type controlledWriteConn struct {
	net.Conn
	written atomic.Uint64
	mode    atomic.Int32
}

func (c *controlledWriteConn) Write(value []byte) (int, error) {
	switch controlledWriteMode(c.mode.Swap(int32(controlledWriteNormal))) {
	case controlledWriteZeroFailure:
		_ = c.Conn.Close()
		return 0, errInjectedZeroWrite
	case controlledWritePartialFailure:
		limit := min(7, len(value))
		count, err := c.Conn.Write(value[:limit])
		c.written.Add(uint64(count))
		_ = c.Conn.Close()
		if err != nil {
			return count, err
		}
		return count, errInjectedPartialWrite
	default:
		count, err := c.Conn.Write(value)
		c.written.Add(uint64(count))
		return count, err
	}
}

func (c *controlledWriteConn) WrittenBytes() uint64 { return c.written.Load() }

func (c *controlledWriteConn) failNextWrite(mode controlledWriteMode) {
	c.mode.Store(int32(mode))
}

type controlledWriteDialer struct {
	dialer net.Dialer
	calls  atomic.Int64
	last   atomic.Pointer[controlledWriteConn]
}

func (d *controlledWriteDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	connection, err := d.dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	controlled := &controlledWriteConn{Conn: connection}
	d.calls.Add(1)
	d.last.Store(controlled)
	return controlled, nil
}

func TestHTTPTransportZeroByteWriteReconnects(t *testing.T) {
	var businessRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/business" {
			businessRequests.Add(1)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	transport := NewHTTPTransport("")
	underlying := underlyingHTTPTransport(t, transport)
	dialer := &controlledWriteDialer{}
	underlying.DialContext = dialer.DialContext
	warmPooledConnection(t, transport, server.URL)
	firstConnection := dialer.last.Load()
	require.NotNil(t, firstConnection)
	firstConnection.failNextWrite(controlledWriteZeroFailure)

	var getConnections atomic.Int64
	request := mustHTTPRequest(t, http.MethodGet, server.URL+"/business", nil)
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GetConn: func(string) { getConnections.Add(1) },
	}))
	response, err := transport.Do(request, &apiattempt.APIExecutionResult{})
	require.NoError(t, err, "a zero-byte socket write may safely acquire a fresh connection")
	require.NoError(t, response.Body.Close())
	require.Equal(t, int64(2), getConnections.Load(), "the same outer RoundTrip must acquire a second connection")
	require.Equal(t, int64(2), dialer.calls.Load())
	require.Equal(t, int64(1), businessRequests.Load())
}

func TestHTTPTransportPartialWriteDoesNotReplay(t *testing.T) {
	transportCause := errors.New("transport stopped after partial write")
	connection := &scriptedPartialWriteConn{}
	roundTripper := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		require.NotNil(t, trace)
		trace.GetConn("upstream.example:80")
		trace.GotConn(httptrace.GotConnInfo{Conn: connection})
		count, err := connection.Write([]byte("GET /business HTTP/1.1\r\n"))
		require.Equal(t, 7, count)
		require.ErrorIs(t, err, errInjectedPartialWrite)
		trace.GetConn("upstream.example:80")
		return nil, transportCause
	})

	response, err := newHTTPTransportWithRoundTripper(roundTripper).Do(
		mustHTTPRequest(t, http.MethodGet, "http://upstream.example/business", nil),
		&apiattempt.APIExecutionResult{},
	)
	require.Nil(t, response)
	require.Equal(t, uint64(7), connection.WrittenBytes(), "partial n must be visible before Write returns its error")
	require.ErrorIs(t, err, errProviderReplayPrevented)
	require.ErrorIs(t, err, transportCause)
}

func TestHTTPTransportPartialSocketWriteStopsWithoutRedial(t *testing.T) {
	var businessRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/business" {
			businessRequests.Add(1)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	transport := NewHTTPTransport("")
	underlying := underlyingHTTPTransport(t, transport)
	dialer := &controlledWriteDialer{}
	underlying.DialContext = dialer.DialContext
	warmPooledConnection(t, transport, server.URL)
	firstConnection := dialer.last.Load()
	require.NotNil(t, firstConnection)
	baseline := firstConnection.WrittenBytes()
	firstConnection.failNextWrite(controlledWritePartialFailure)

	response, err := transport.Do(
		mustHTTPRequest(t, http.MethodGet, server.URL+"/business", nil),
		&apiattempt.APIExecutionResult{},
	)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err)
	require.Greater(t, firstConnection.WrittenBytes(), baseline, "partial n must be counted on the pooled connection")
	require.Equal(t, int64(1), dialer.calls.Load(), "a partial socket write must not be redialed or replayed")
	require.Zero(t, businessRequests.Load(), "an incomplete request must not become a second provider request")
}

func TestHTTPTransportUsesSocketWriteMeter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	var meterVisible atomic.Bool
	request := mustHTTPRequest(t, http.MethodGet, server.URL, nil)
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			meterVisible.Store(testFindWriteMeter(info.Conn) != nil)
		},
	}))
	response, err := NewHTTPTransport("").Do(request, &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.True(t, meterVisible.Load(), "production DialContext must return a connection-scoped write meter")
}

func TestWriteMeteredConnCountsPartialWritesAndDetectsCounterWrap(t *testing.T) {
	underlying := &scriptedPartialWriteConn{}
	meter := &writeMeteredConn{Conn: underlying}
	count, err := meter.Write([]byte("partial provider request"))
	require.Equal(t, 7, count)
	require.ErrorIs(t, err, errInjectedPartialWrite)
	require.Equal(t, uint64(7), meter.WrittenBytes(), "n must be counted even when Write also returns an error")

	delta, monotonic := socketWriteDelta(5, 12)
	require.True(t, monotonic)
	require.Equal(t, uint64(7), delta)
	_, monotonic = socketWriteDelta(^uint64(0), 0)
	require.False(t, monotonic, "counter wrap must fail closed instead of looking like a zero-byte write")
}

func TestHTTPTransportCustomDialTLSContextPreservesTLSState(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(provider.Close)

	transport := newCustomDialTLSTransport(t, provider)
	meterHTTPTransportConnections(transport)
	t.Cleanup(transport.CloseIdleConnections)

	var gotTLSConnection atomic.Bool
	var gotMeter atomic.Bool
	var handshakeStarts atomic.Int64
	var handshakeDone atomic.Int64
	var handshakeFailed atomic.Bool
	var handshakeState atomic.Pointer[tls.ConnectionState]
	request := mustHTTPRequest(t, http.MethodGet, provider.URL, nil)
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			_, isTLSConnection := info.Conn.(*tls.Conn)
			gotTLSConnection.Store(isTLSConnection)
			gotMeter.Store(testFindWriteMeter(info.Conn) != nil)
		},
		TLSHandshakeStart: func() { handshakeStarts.Add(1) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			if err != nil {
				handshakeFailed.Store(true)
				return
			}
			handshakeDone.Add(1)
			handshakeState.Store(&state)
		},
	}))

	response, err := newHTTPTransportWithRoundTripper(transport).Do(request, &apiattempt.APIExecutionResult{})
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.True(t, gotTLSConnection.Load(), "custom DialTLSContext must keep the exact *tls.Conn dynamic type")
	require.False(t, gotMeter.Load(), "custom TLS plaintext writes must not be treated as socket ciphertext writes")
	require.Equal(t, int64(1), handshakeStarts.Load())
	require.Equal(t, int64(1), handshakeDone.Load())
	require.False(t, handshakeFailed.Load())
	require.NotNil(t, response.TLS, "net/http must retain the custom TLS connection state")
	require.True(t, response.TLS.HandshakeComplete)
	require.NotEmpty(t, response.TLS.PeerCertificates)
	traceState := handshakeState.Load()
	require.NotNil(t, traceState)
	require.Equal(t, response.TLS.Version, traceState.Version)
	require.Equal(t, 1, response.ProtoMajor)
}

func TestHTTPTransportCustomDialTLSContextMissingMeterFailsClosed(t *testing.T) {
	var businessRequests atomic.Int64
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/business" {
			businessRequests.Add(1)
			closeHijackedConnection(writer)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	provider.StartTLS()
	t.Cleanup(provider.Close)

	transport := newCustomDialTLSTransport(t, provider)
	meterHTTPTransportConnections(transport)
	t.Cleanup(transport.CloseIdleConnections)
	httpTransport := newHTTPTransportWithRoundTripper(transport)

	warm := roundTripRequest(t, httpTransport, http.MethodGet, provider.URL+"/warm", nil)
	require.NoError(t, warm.Body.Close(), "the first custom TLS request must not require a socket meter")

	var getConnections atomic.Int64
	request := mustHTTPRequest(t, http.MethodGet, provider.URL+"/business", nil)
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GetConn: func(string) { getConnections.Add(1) },
	}))
	response, err := httpTransport.Do(request, &apiattempt.APIExecutionResult{})
	if response != nil {
		_ = response.Body.Close()
	}
	require.ErrorIs(t, err, errProviderReplayPrevented)
	require.Equal(t, int64(2), getConnections.Load(), "meter-missing custom TLS retry must reach the fail-closed guard")
	require.Equal(t, int64(1), businessRequests.Load(), "provider request must not be replayed")
}

func TestHTTPTransportReplayErrorDoesNotDuplicateSentinel(t *testing.T) {
	roundTripper := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		trace.GetConn("upstream.example:443")
		trace.GetConn("upstream.example:443")
		return nil, errProviderReplayPrevented
	})

	_, err := newHTTPTransportWithRoundTripper(roundTripper).Do(
		mustHTTPRequest(t, http.MethodGet, "https://upstream.example", nil),
		&apiattempt.APIExecutionResult{},
	)
	require.ErrorIs(t, err, errProviderReplayPrevented)
	require.Equal(t, 1, strings.Count(err.Error(), errProviderReplayPrevented.Error()))
}

func TestHTTPTransportMetersHTTPProxy(t *testing.T) {
	var proxyConnections atomic.Int64
	var failedRequests atomic.Int64
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/fail" {
			failedRequests.Add(1)
			closeHijackedConnection(writer)
			return
		}
		_, _ = io.WriteString(writer, "proxied")
	}))
	proxy.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			proxyConnections.Add(1)
		}
	}
	proxy.Start()
	t.Cleanup(proxy.Close)

	transport := NewHTTPTransport(proxy.URL)
	underlying := underlyingHTTPTransport(t, transport)
	t.Cleanup(underlying.CloseIdleConnections)
	var meterVisible atomic.Bool
	for index := range 2 {
		request := mustHTTPRequest(t, http.MethodGet, fmt.Sprintf("http://provider.invalid/ok?request=%d", index), nil)
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { meterVisible.Store(testFindWriteMeter(info.Conn) != nil) },
		}))
		response, err := transport.Do(request, &apiattempt.APIExecutionResult{})
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "proxied", string(body))
		require.NoError(t, response.Body.Close())
	}
	require.True(t, meterVisible.Load())
	require.Equal(t, int64(1), proxyConnections.Load(), "HTTP proxy connection must be pooled")

	response, err := transport.Do(
		mustHTTPRequest(t, http.MethodGet, "http://provider.invalid/fail", nil),
		&apiattempt.APIExecutionResult{},
	)
	if response != nil {
		_ = response.Body.Close()
	}
	require.ErrorIs(t, err, errProviderReplayPrevented)
	require.Equal(t, int64(1), failedRequests.Load())
}

func TestHTTPTransportMetersHTTPSConnect(t *testing.T) {
	var providerConnections atomic.Int64
	var failedRequests atomic.Int64
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/fail" {
			failedRequests.Add(1)
			closeHijackedConnection(writer)
			return
		}
		_, _ = io.WriteString(writer, "secure-proxied")
	}))
	provider.EnableHTTP2 = true
	provider.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			providerConnections.Add(1)
		}
	}
	provider.StartTLS()
	t.Cleanup(provider.Close)

	var connectRequests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			http.Error(writer, "CONNECT required", http.StatusBadRequest)
			return
		}
		connectRequests.Add(1)
		tunnelHTTPSConnect(writer, request.Host)
	}))
	t.Cleanup(proxy.Close)

	transport := NewHTTPTransport(proxy.URL)
	underlying := underlyingHTTPTransport(t, transport)
	underlying.TLSClientConfig = provider.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(underlying.CloseIdleConnections)
	var meterVisible atomic.Bool
	for range 2 {
		request := mustHTTPRequest(t, http.MethodGet, provider.URL+"/ok", nil)
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { meterVisible.Store(testFindWriteMeter(info.Conn) != nil) },
		}))
		response, err := transport.Do(request, &apiattempt.APIExecutionResult{})
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "secure-proxied", string(body))
		require.NoError(t, response.Body.Close())
	}
	require.True(t, meterVisible.Load(), "TLS NetConn chain must expose the underlying write meter")
	require.Equal(t, int64(1), connectRequests.Load(), "HTTPS requests must reuse one CONNECT tunnel")
	require.Equal(t, int64(1), providerConnections.Load(), "HTTPS requests must reuse one provider TLS connection")

	response, err := transport.Do(
		mustHTTPRequest(t, http.MethodGet, provider.URL+"/fail", nil),
		&apiattempt.APIExecutionResult{},
	)
	if response != nil {
		_ = response.Body.Close()
	}
	require.ErrorIs(t, err, errProviderReplayPrevented)
	require.Equal(t, int64(1), failedRequests.Load())
}

func newCustomDialTLSTransport(t *testing.T, provider *httptest.Server) *http.Transport {
	t.Helper()
	providerTransport, ok := provider.Client().Transport.(*http.Transport)
	require.True(t, ok)
	tlsConfig := providerTransport.TLSClientConfig.Clone()
	tlsConfig.NextProtos = []string{"http/1.1"}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	dialer := &net.Dialer{}
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			rawConnection, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				_ = rawConnection.Close()
				return nil, err
			}
			connectionTLSConfig := tlsConfig.Clone()
			connectionTLSConfig.ServerName = host
			tlsConnection := tls.Client(rawConnection, connectionTLSConfig)
			if err := tlsConnection.HandshakeContext(ctx); err != nil {
				_ = rawConnection.Close()
				return nil, err
			}
			return tlsConnection, nil
		},
		ForceAttemptHTTP2: false,
		TLSNextProto:      make(map[string]func(string, *tls.Conn) http.RoundTripper),
		Protocols:         protocols,
	}
}

type scriptedPartialWriteConn struct {
	written atomic.Uint64
}

func (c *scriptedPartialWriteConn) Read([]byte) (int, error)          { return 0, io.EOF }
func (c *scriptedPartialWriteConn) Close() error                      { return nil }
func (c *scriptedPartialWriteConn) LocalAddr() net.Addr               { return staticNetAddr("local") }
func (c *scriptedPartialWriteConn) RemoteAddr() net.Addr              { return staticNetAddr("remote") }
func (c *scriptedPartialWriteConn) SetDeadline(_ time.Time) error     { return nil }
func (c *scriptedPartialWriteConn) SetReadDeadline(_ time.Time) error { return nil }
func (c *scriptedPartialWriteConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

func (c *scriptedPartialWriteConn) Write(value []byte) (int, error) {
	count := min(7, len(value))
	c.written.Add(uint64(count))
	return count, errInjectedPartialWrite
}

func (c *scriptedPartialWriteConn) WrittenBytes() uint64 { return c.written.Load() }

type staticNetAddr string

func (a staticNetAddr) Network() string { return string(a) }
func (a staticNetAddr) String() string  { return string(a) }

func testFindWriteMeter(connection net.Conn) testWriteMeter {
	for depth := 0; connection != nil && depth < 8; depth++ {
		if meter, ok := connection.(testWriteMeter); ok {
			return meter
		}
		unwrapper, ok := connection.(interface{ NetConn() net.Conn })
		if !ok {
			return nil
		}
		next := unwrapper.NetConn()
		if next == connection {
			return nil
		}
		connection = next
	}
	return nil
}

func tunnelHTTPSConnect(writer http.ResponseWriter, target string) {
	providerConnection, err := net.Dial("tcp", target)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	clientConnection, buffered, err := writer.(http.Hijacker).Hijack()
	if err != nil {
		_ = providerConnection.Close()
		return
	}
	_, _ = io.WriteString(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		_ = clientConnection.Close()
		_ = providerConnection.Close()
		return
	}

	var copies conc.WaitGroup
	copies.Go(func() {
		_, _ = io.Copy(providerConnection, clientConnection)
		_ = providerConnection.Close()
	})
	copies.Go(func() {
		_, _ = io.Copy(clientConnection, providerConnection)
		_ = clientConnection.Close()
	})
	copies.Wait()
}
