package genericapi

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	sharedhttp "github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

var (
	errMissingAPIExecutionResult = errors.New("missing API execution result")
	errMissingUpstreamRequest    = errors.New("missing upstream request")
	errMissingRoundTripper       = errors.New("missing upstream round tripper")
	errProviderReplayPrevented   = errors.New("provider request replay prevented")
)

type HTTPTransport struct {
	roundTripper    http.RoundTripper
	proxyURL        string
	settingsFinder  SettingsFinder
	dynamic         bool
	timeouts        httpTransportTimeouts
	timeoutsApplied bool
	mu              sync.Mutex
}

func NewHTTPTransport(proxyURL string) *HTTPTransport {
	return &HTTPTransport{roundTripper: newHTTPRoundTripper(proxyURL, httpTransportTimeouts{}, false), proxyURL: proxyURL}
}

type httpTransportTimeouts struct {
	dial           time.Duration
	tlsHandshake   time.Duration
	responseHeader time.Duration
}

func newHTTPRoundTripper(proxyURL string, timeouts httpTransportTimeouts, applyTimeouts bool) http.RoundTripper {
	transport := sharedhttp.NewTransport(proxyURL)
	if applyTimeouts {
		transport.DialContext = newHTTPTimeoutDialer(timeouts.dial).DialContext
		transport.TLSHandshakeTimeout = timeouts.tlsHandshake
		transport.ResponseHeaderTimeout = timeouts.responseHeader
	}
	meterHTTPTransportConnections(transport)
	transport.DisableCompression = true
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport.Protocols = protocols
	return &noReplayRoundTripper{base: transport}
}

func newHTTPTimeoutDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
}

func newHTTPTransportWithRoundTripper(roundTripper http.RoundTripper) *HTTPTransport {
	if roundTripper == nil {
		return &HTTPTransport{}
	}
	return &HTTPTransport{roundTripper: &noReplayRoundTripper{base: roundTripper}}
}

// WithSettings makes every future request obtain its timeout tuple from the
// latest Agent settings snapshot. A changed tuple receives a fresh immutable
// http.Transport; the retired transport only has idle connections closed.
func (t *HTTPTransport) WithSettings(finder SettingsFinder) *HTTPTransport {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.settingsFinder = finder
	t.dynamic = finder != nil
	return t
}

func (t *HTTPTransport) Do(req *http.Request, result *apiattempt.APIExecutionResult) (*http.Response, error) {
	if result == nil {
		return nil, errMissingAPIExecutionResult
	}
	result.ProviderDispatchKnown = true
	result.ProviderDispatched = false
	if req == nil {
		return nil, errMissingUpstreamRequest
	}
	if t == nil {
		return nil, errMissingRoundTripper
	}
	roundTripper := t.currentRoundTripper()
	if roundTripper == nil {
		return nil, errMissingRoundTripper
	}

	result.ProviderDispatchKnown = true
	result.ProviderDispatched = true
	return roundTripper.RoundTrip(req)
}

func (t *HTTPTransport) currentRoundTripper() http.RoundTripper {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.dynamic || t.settingsFinder == nil {
		return t.roundTripper
	}
	nextTimeouts := httpTransportTimeoutsFromSettings(t.settingsFinder.Settings())
	if t.timeoutsApplied && nextTimeouts == t.timeouts {
		return t.roundTripper
	}
	previous := t.roundTripper
	t.roundTripper = newHTTPRoundTripper(t.proxyURL, nextTimeouts, true)
	t.timeouts = nextTimeouts
	t.timeoutsApplied = true
	closeIdleConnections(previous)
	return t.roundTripper
}

func httpTransportTimeoutsFromSettings(value settings.AgentSettings) httpTransportTimeouts {
	return httpTransportTimeouts{
		dial:           time.Duration(value.APIUpstreamDialTimeoutMs) * time.Millisecond,
		tlsHandshake:   time.Duration(value.APIUpstreamTLSHandshakeTimeoutMs) * time.Millisecond,
		responseHeader: time.Duration(value.APIUpstreamResponseHeaderTimeoutMs) * time.Millisecond,
	}
}

func closeIdleConnections(roundTripper http.RoundTripper) {
	if closer, ok := roundTripper.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
		return
	}
	if guarded, ok := roundTripper.(*noReplayRoundTripper); ok {
		if closer, ok := guarded.base.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

// noReplayRoundTripper preserves net/http's HTTP/1 connection pool while
// preventing its internal RoundTrip retry loop from sending a second provider
// request. The trace callbacks run before the second persistConn can write.
type noReplayRoundTripper struct {
	base http.RoundTripper
}

func (t *noReplayRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	dispatchContext, cancel := context.WithCancelCause(request.Context())
	var connectionAttempts atomic.Int32
	var replayBlocked atomic.Bool
	var attemptMeter socketWriteAttemptMeter
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			attempt := connectionAttempts.Add(1)
			if attempt > 1 && !attemptMeter.zeroBytesWritten() {
				replayBlocked.Store(true)
				cancel(errProviderReplayPrevented)
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			if replayBlocked.Load() {
				// HTTP/2 is disabled by NewHTTPTransport. An HTTP/1 connection
				// returned by GotConn is exclusively assigned to this attempt.
				_ = info.Conn.Close()
				return
			}
			attemptMeter.start(info.Conn)
		},
	}
	dispatchRequest := request.WithContext(httptrace.WithClientTrace(dispatchContext, trace))
	response, err := t.base.RoundTrip(dispatchRequest)
	if err != nil {
		if replayBlocked.Load() && !errors.Is(err, errProviderReplayPrevented) {
			err = errors.Join(errProviderReplayPrevented, err)
		}
		cancel(err)
		return response, err
	}
	if response.Request == dispatchRequest {
		response.Request = request
	}
	if response.Body == nil || response.Body == http.NoBody {
		cancel(nil)
		return response, nil
	}
	response.Body = &dispatchResponseBody{body: response.Body, cancel: cancel}
	return response, nil
}

type socketWriteMeter interface {
	WrittenBytes() uint64
}

type writeMeteredConn struct {
	net.Conn
	written atomic.Uint64
}

func (c *writeMeteredConn) Write(value []byte) (int, error) {
	count, err := c.Conn.Write(value)
	c.written.Add(uint64(count))
	return count, err
}

func (c *writeMeteredConn) WrittenBytes() uint64 {
	return c.written.Load()
}

func meterHTTPTransportConnections(transport *http.Transport) {
	if transport == nil {
		return
	}
	if dialContext := transport.DialContext; dialContext != nil {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			connection, err := dialContext(ctx, network, address)
			return meterDialedConnection(connection), err
		}
	}
}

func meterDialedConnection(connection net.Conn) net.Conn {
	if connection == nil {
		return nil
	}
	if findSocketWriteMeter(connection) != nil {
		return connection
	}
	return &writeMeteredConn{Conn: connection}
}

func findSocketWriteMeter(connection net.Conn) socketWriteMeter {
	for depth := 0; connection != nil && depth < 8; depth++ {
		if meter, ok := connection.(socketWriteMeter); ok {
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

type socketWriteAttemptMeter struct {
	mu       sync.Mutex
	meter    socketWriteMeter
	baseline uint64
}

func (m *socketWriteAttemptMeter) start(connection net.Conn) {
	meter := findSocketWriteMeter(connection)
	m.mu.Lock()
	m.meter = meter
	if meter == nil {
		m.baseline = 0
	} else {
		m.baseline = meter.WrittenBytes()
	}
	m.mu.Unlock()
}

func (m *socketWriteAttemptMeter) zeroBytesWritten() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.meter == nil {
		return false
	}
	delta, monotonic := socketWriteDelta(m.baseline, m.meter.WrittenBytes())
	return monotonic && delta == 0
}

func socketWriteDelta(baseline, current uint64) (uint64, bool) {
	if current < baseline {
		return 0, false
	}
	return current - baseline, true
}

type dispatchResponseBody struct {
	body   io.ReadCloser
	cancel context.CancelCauseFunc
	once   sync.Once
}

func (b *dispatchResponseBody) Read(value []byte) (int, error) {
	count, err := b.body.Read(value)
	if err != nil {
		b.finish(err)
	}
	return count, err
}

func (b *dispatchResponseBody) Close() error {
	err := b.body.Close()
	b.finish(err)
	return err
}

func (b *dispatchResponseBody) finish(cause error) {
	b.once.Do(func() { b.cancel(cause) })
}
