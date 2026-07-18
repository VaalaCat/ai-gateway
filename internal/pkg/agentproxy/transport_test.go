package agentproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type directReaderTestBody struct {
	opens  int
	closed []int
}

type emptyDirectReaderTestBody struct {
	directReaderTestBody
}

func (*emptyDirectReaderTestBody) Size() int64 { return 0 }

func (b *directReaderTestBody) Size() int64 { return 4 }
func (b *directReaderTestBody) Open() (io.ReadCloser, error) {
	b.opens++
	index := b.opens - 1
	return &directReaderTestCloser{
		Reader: bytes.NewReader([]byte("body")),
		close:  func() { b.closed[index]++ },
	}, nil
}
func (*directReaderTestBody) Bytes(int64) ([]byte, error) { return []byte("body"), nil }
func (*directReaderTestBody) Close() error                { return nil }

type directReaderTestCloser struct {
	*bytes.Reader
	close func()
}

type blockingDirectResponseWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type untouchedDirectResponseWriter struct {
	header       http.Header
	headerWrites int
	bodyWrites   int
}

func (w *untouchedDirectResponseWriter) Header() http.Header { return w.header }
func (w *untouchedDirectResponseWriter) WriteHeader(int)     { w.headerWrites++ }
func (w *untouchedDirectResponseWriter) Write(p []byte) (int, error) {
	w.bodyWrites++
	return len(p), nil
}

func (w *blockingDirectResponseWriter) Header() http.Header { return w.header }
func (*blockingDirectResponseWriter) WriteHeader(int)       {}
func (w *blockingDirectResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func (r *directReaderTestCloser) Close() error {
	r.close()
	return nil
}

func TestDirectTransportPoolReusesExactKey(t *testing.T) {
	pool := newDirectTransportPool(directTransportPoolOptions{Limit: 3})
	key := directTransportKey{TargetAgentID: "a", AddressFingerprint: "fp", Scheme: "http"}
	first := pool.get(key, nil)
	second := pool.get(key, nil)
	require.Same(t, first, second)
	require.Equal(t, 1, pool.resourceCount())
}

func TestDirectTransportPoolBoundsEntriesWithLRU(t *testing.T) {
	pool := newDirectTransportPool(directTransportPoolOptions{Limit: 2})
	a := directTransportKey{TargetAgentID: "a", AddressFingerprint: "1", Scheme: "http"}
	b := directTransportKey{TargetAgentID: "b", AddressFingerprint: "1", Scheme: "http"}
	c := directTransportKey{TargetAgentID: "c", AddressFingerprint: "1", Scheme: "http"}
	aTransport := pool.get(a, nil)
	_ = pool.get(b, nil)
	require.Same(t, aTransport, pool.get(a, nil))
	_ = pool.get(c, nil)
	require.Equal(t, 2, pool.resourceCount())
	require.Same(t, aTransport, pool.get(a, nil))
}

func TestDirectTransportPoolInvalidatesFingerprintAndProxyChange(t *testing.T) {
	pool := newDirectTransportPool(directTransportPoolOptions{Limit: 4})
	proxyA, err := url.Parse("http://proxy-a")
	require.NoError(t, err)
	proxyB, err := url.Parse("http://proxy-b")
	require.NoError(t, err)
	base := directTransportKey{TargetAgentID: "a", AddressFingerprint: "fp-1", Scheme: "http", Proxy: canonicalProxyURL(proxyA)}
	first := pool.get(base, proxyA)
	changedProxy := base
	changedProxy.Proxy = canonicalProxyURL(proxyB)
	require.NotSame(t, first, pool.get(changedProxy, proxyB))
	require.Equal(t, 1, pool.resourceCount())
	changedFingerprint := changedProxy
	changedFingerprint.AddressFingerprint = "fp-2"
	_ = pool.get(changedFingerprint, proxyB)
	require.Equal(t, 1, pool.resourceCount())
}

func TestCanonicalProxyURLIsStableAndDoesNotRetainSecrets(t *testing.T) {
	proxyA, err := url.Parse("HTTP://user:secret@Proxy.Example:8080/path?token=private#fragment")
	require.NoError(t, err)
	proxyB, err := url.Parse("http://user:secret@proxy.example:8080/path?token=private")
	require.NoError(t, err)
	got := canonicalProxyURL(proxyA)
	require.Equal(t, got, canonicalProxyURL(proxyB))
	for _, secret := range []string{"user", "secret", "private", "token="} {
		require.NotContains(t, got, secret)
	}
}

func TestDirectTransportPoolEvictionClosesIdleConnection(t *testing.T) {
	closed := make(chan struct{})
	var closeOnce sync.Once
	first := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	first.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closeOnce.Do(func() { close(closed) })
		}
	}
	first.Start()
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer second.Close()

	pool := newDirectTransportPool(directTransportPoolOptions{Limit: 1})
	firstURL, err := url.Parse(first.URL)
	require.NoError(t, err)
	response, err := pool.get(directTransportKey{TargetAgentID: "a", AddressFingerprint: "a", Scheme: "http"}, nil).RoundTrip(&http.Request{
		Method: http.MethodGet, URL: firstURL, Header: make(http.Header),
	})
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, response.Body)
	require.NoError(t, response.Body.Close())
	_ = pool.get(directTransportKey{TargetAgentID: "b", AddressFingerprint: "b", Scheme: "http"}, nil)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("evicted transport did not close its idle connection")
	}
}

func TestDirectTransportPoolInvalidationAndShutdownCloseIdleConnections(t *testing.T) {
	t.Run("fingerprint change", func(t *testing.T) {
		server, closed := newIdleSocketServer(t)
		pool := newDirectTransportPool(directTransportPoolOptions{Limit: 4})
		url, err := url.Parse(server.URL)
		require.NoError(t, err)
		key := directTransportKey{TargetAgentID: "a", AddressFingerprint: "old", Scheme: "http"}
		openIdleSocket(t, pool.get(key, nil), url)
		key.AddressFingerprint = "new"
		_ = pool.get(key, nil)
		waitForIdleSocketClose(t, closed)
	})

	t.Run("proxy change", func(t *testing.T) {
		proxyA, closed := newIdleSocketServer(t)
		proxyB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer proxyB.Close()
		proxyAURL, err := url.Parse(proxyA.URL)
		require.NoError(t, err)
		proxyBURL, err := url.Parse(proxyB.URL)
		require.NoError(t, err)
		targetURL, err := url.Parse("http://target.invalid/v1/test")
		require.NoError(t, err)
		pool := newDirectTransportPool(directTransportPoolOptions{Limit: 4})
		key := directTransportKey{TargetAgentID: "a", AddressFingerprint: "fp", Scheme: "http", Proxy: canonicalProxyURL(proxyAURL)}
		openIdleSocket(t, pool.get(key, proxyAURL), targetURL)
		key.Proxy = canonicalProxyURL(proxyBURL)
		_ = pool.get(key, proxyBURL)
		waitForIdleSocketClose(t, closed)
	})

	t.Run("shutdown", func(t *testing.T) {
		server, closed := newIdleSocketServer(t)
		pool := newDirectTransportPool(directTransportPoolOptions{Limit: 4})
		url, err := url.Parse(server.URL)
		require.NoError(t, err)
		key := directTransportKey{TargetAgentID: "a", AddressFingerprint: "fp", Scheme: "http"}
		openIdleSocket(t, pool.get(key, nil), url)
		pool.closeIdleConnections()
		waitForIdleSocketClose(t, closed)
	})
}

func newIdleSocketServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{})
	var closeOnce sync.Once
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closeOnce.Do(func() { close(closed) })
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server, closed
}

func openIdleSocket(t *testing.T, transport *http.Transport, target *url.URL) {
	t.Helper()
	response, err := transport.RoundTrip(&http.Request{Method: http.MethodGet, URL: target, Header: make(http.Header)})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
}

func waitForIdleSocketClose(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle connection remained open")
	}
}

func TestDirectTransportPoolCloseIsIdempotentAndRejectsReuse(t *testing.T) {
	pool := newDirectTransportPool(directTransportPoolOptions{Limit: 2})
	key := directTransportKey{TargetAgentID: "a", AddressFingerprint: "fp", Scheme: "http"}
	_ = pool.get(key, nil)
	pool.closeIdleConnections()
	pool.closeIdleConnections()
	require.Zero(t, pool.resourceCount())
	require.Nil(t, pool.get(key, nil))
}

func TestDirectForwarderCloseJoinsActiveRequests(t *testing.T) {
	f := NewDirectForwarder(DirectForwarderOptions{})
	f.Cancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, f.Close(ctx))
	select {
	case <-f.Done():
	default:
		t.Fatal("forwarder Done remained open")
	}
	require.Zero(t, f.ResourceCount())
}

func TestDirectForwarderCancelAndCloseWhileForwardIsActive(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "blocked-response")
	}))
	defer target.Close()
	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)
	forwarder := NewDirectForwarder(DirectForwarderOptions{})
	body := &directReaderTestBody{closed: make([]int, 1)}
	dst := &blockingDirectResponseWriter{
		header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{}),
	}
	outcomes := make(chan DirectOutcome, 1)
	request := validDirectRequestForTest(DirectRequest{
		TargetAgentID: "a", AddressFingerprint: "fp", TargetURL: targetURL,
		Request: httptest.NewRequest(http.MethodPost, "/v1/test", nil), Body: body,
	})
	var forwards conc.WaitGroup
	forwards.Go(func() {
		outcomes <- forwarder.Forward(t.Context(), request, dst)
	})
	select {
	case <-dst.entered:
	case <-time.After(time.Second):
		t.Fatal("active Forward did not reach the response writer")
	}

	cancelReturned := make(chan struct{})
	var cancels conc.WaitGroup
	cancels.Go(func() {
		forwarder.Cancel()
		forwarder.Cancel()
		close(cancelReturned)
	})
	select {
	case <-cancelReturned:
	case <-time.After(time.Second):
		t.Fatal("Cancel waited for the active Forward")
	}
	cancels.Wait()

	closeCtx, cancelClose := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelClose()
	require.ErrorIs(t, forwarder.Close(closeCtx), context.DeadlineExceeded)
	close(dst.release)
	<-outcomes
	forwards.Wait()
	select {
	case <-forwarder.Done():
	case <-time.After(time.Second):
		t.Fatal("Done remained open after the active Forward completed")
	}
	require.Zero(t, forwarder.ResourceCount())
}

func TestDirectForwarderTimesOutStalledTLSHandshakeAndClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	connectionClosed := make(chan struct{})
	var server conc.WaitGroup
	server.Go(func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			close(connectionClosed)
			return
		}
		defer close(connectionClosed)
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	})
	target, err := url.Parse("https://" + listener.Addr().String())
	require.NoError(t, err)
	forwarder := NewDirectForwarder(DirectForwarderOptions{
		CircuitFailureThreshold: 1,
		TLSHandshakeTimeout:     20 * time.Millisecond,
		TLSClientConfig:         &tls.Config{InsecureSkipVerify: true}, //nolint:gosec -- stalled local TLS endpoint
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })
	body := &directReaderTestBody{closed: make([]int, 2)}
	request := validDirectRequestForTest(DirectRequest{
		TargetAgentID: "a", AddressFingerprint: "fp", TargetURL: target,
		Request: httptest.NewRequest(http.MethodPost, "/v1/test", nil), Body: body,
	})
	outcome := forwarder.Forward(t.Context(), request, httptest.NewRecorder())
	require.Error(t, outcome.Err)
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.Equal(t, CodeDirectTLS, outcome.Code)
	select {
	case <-connectionClosed:
	case <-time.After(time.Second):
		t.Fatal("stalled TLS connection remained open after handshake timeout")
	}
	server.Wait()
	require.NoError(t, listener.Close())
	blocked := forwarder.Forward(t.Context(), request, httptest.NewRecorder())
	require.Equal(t, CodeDirectCircuitOpen, blocked.Code)
}

func TestDirectForwarderNormalizesCircuitCapacityToStableOpenCode(t *testing.T) {
	forwarder := NewDirectForwarder(DirectForwarderOptions{CircuitStateLimit: 1})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })
	active, reason := forwarder.circuit.admit(directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"})
	require.Equal(t, directCircuitAllowed, reason)
	target, err := url.Parse("http://unused.invalid")
	require.NoError(t, err)
	outcome := forwarder.Forward(t.Context(), validDirectRequestForTest(DirectRequest{
		TargetAgentID: "b", AddressFingerprint: "fp", TargetURL: target,
		Request: httptest.NewRequest(http.MethodPost, "/v1/test", nil),
		Body:    &emptyDirectReaderTestBody{directReaderTestBody: directReaderTestBody{closed: make([]int, 1)}},
	}), httptest.NewRecorder())
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.Equal(t, CodeDirectCircuitOpen, outcome.Code)
	require.ErrorIs(t, outcome.Err, errDirectCircuitCapacity)
	forwarder.circuit.cancelled(active)
}

func TestDirectForwarderNormalizesClosedLifecycleToDisabled(t *testing.T) {
	forwarder := NewDirectForwarder(DirectForwarderOptions{})
	require.NoError(t, forwarder.Close(context.Background()))
	target, err := url.Parse("http://unused.invalid")
	require.NoError(t, err)
	outcome := forwarder.Forward(t.Context(), validDirectRequestForTest(DirectRequest{
		TargetAgentID: "target", AddressFingerprint: "fp", TargetURL: target,
		Request: httptest.NewRequest(http.MethodPost, "/v1/test", nil),
		Body:    &emptyDirectReaderTestBody{directReaderTestBody: directReaderTestBody{closed: make([]int, 1)}},
	}), httptest.NewRecorder())
	require.Equal(t, CodeDirectDisabled, outcome.Code)
	require.ErrorIs(t, outcome.Err, errDirectClosed)
}

func TestDirectForwardRejectsInvalidAttemptsLocallyWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DirectRequest)
	}{
		{name: "zero channel id", mutate: func(req *DirectRequest) { req.Attempt.Attempt.Channel.ID = 0 }},
		{name: "unknown mode", mutate: func(req *DirectRequest) { req.Attempt.Attempt.Mode = "unknown" }},
		{name: "empty model", mutate: func(req *DirectRequest) { req.Attempt.Attempt.RealModel = "" }},
		{name: "empty path", mutate: func(req *DirectRequest) { req.Attempt.RequestPath = "" }},
		{name: "disallowed provider path", mutate: func(req *DirectRequest) { req.Attempt.RequestPath = attemptwire.EndpointPath }},
		{name: "non post method", mutate: func(req *DirectRequest) { req.Request.Method = http.MethodGet }},
		{name: "missing forward ticket", mutate: func(req *DirectRequest) { req.ForwardTicket = "" }},
		{name: "missing attempt", mutate: func(req *DirectRequest) { req.Attempt = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := url.Parse("http://unused.invalid")
			require.NoError(t, err)
			body := &directReaderTestBody{closed: make([]int, 1)}
			networkCalls := 0
			forwarder := NewDirectForwarder(DirectForwarderOptions{
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					networkCalls++
					return nil, errors.New("network must not run")
				},
			})
			t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })
			meta := validDirectAttemptMetaForTest()
			request := DirectRequest{
				TargetAgentID: "target-a", AddressFingerprint: "fp-a", TargetURL: target,
				Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil), Body: body,
				ForwardTicket: agentauth.ForwardTicket("managed-ticket"), Attempt: &meta,
			}
			tt.mutate(&request)
			writer := &untouchedDirectResponseWriter{header: make(http.Header)}

			outcome := forwarder.Forward(t.Context(), request, writer)

			require.Equal(t, tunnel.PreCommit, outcome.Commit)
			require.Equal(t, "validate", outcome.Stage)
			require.Equal(t, CodeDirectInvalidInput, outcome.Code)
			require.EqualError(t, outcome.Err, "direct forward: invalid attempt proxy request")
			require.Zero(t, body.opens)
			require.Zero(t, networkCalls)
			require.Zero(t, forwarder.ResourceCount())
			require.Empty(t, forwarder.circuit.states)
			require.Zero(t, writer.headerWrites)
			require.Zero(t, writer.bodyWrites)
			require.Empty(t, writer.header)
		})
	}
}

func TestDirectRequestGetBodyOpensIndependentReadersAndClosesEachOnce(t *testing.T) {
	body := &directReaderTestBody{closed: make([]int, 3)}
	primary, err := openOwnedReader(body)
	require.NoError(t, err)
	request := buildDirectRequest(t.Context(), DirectRequest{
		TargetURL: &url.URL{Scheme: "http", Host: "target.invalid"},
		Request:   httptest.NewRequest(http.MethodPost, "/v1/test", nil),
		Body:      body,
	}, primary, preparedDirectAttempt{})

	firstReplay, err := request.GetBody()
	require.NoError(t, err)
	secondReplay, err := request.GetBody()
	require.NoError(t, err)
	require.NotSame(t, primary, firstReplay)
	require.NotSame(t, firstReplay, secondReplay)

	require.NoError(t, primary.Close())
	require.NoError(t, primary.Close())
	require.NoError(t, firstReplay.Close())
	require.NoError(t, firstReplay.Close())
	require.NoError(t, secondReplay.Close())
	require.NoError(t, secondReplay.Close())
	require.Equal(t, 3, body.opens)
	require.Equal(t, []int{1, 1, 1}, body.closed)
}

func TestDirectRequestClearsOriginalBodyFraming(t *testing.T) {
	body := &directReaderTestBody{closed: make([]int, 1)}
	primary, err := openOwnedReader(body)
	require.NoError(t, err)
	defer primary.Close()
	inbound := httptest.NewRequest(http.MethodPost, "/v1/test", nil)
	inbound.TransferEncoding = []string{"chunked"}
	inbound.Trailer = http.Header{"X-Original-Trailer": {"forged"}}
	inbound.Header.Set("Transfer-Encoding", "chunked")
	inbound.Header.Set("Trailer", "X-Original-Trailer")

	outbound := buildDirectRequest(t.Context(), DirectRequest{
		TargetURL: &url.URL{Scheme: "http", Host: "target.invalid"},
		Request:   inbound,
		Body:      body,
	}, primary, preparedDirectAttempt{})
	require.Nil(t, outbound.TransferEncoding)
	require.Nil(t, outbound.Trailer)
	require.Empty(t, outbound.Header.Get("Transfer-Encoding"))
	require.Empty(t, outbound.Header.Get("Trailer"))
}

func TestDirectRequestUsesNoBodyForEmptyReplayBody(t *testing.T) {
	body := &emptyDirectReaderTestBody{directReaderTestBody: directReaderTestBody{closed: make([]int, 2)}}
	primary, err := openOwnedReader(body)
	require.NoError(t, err)
	defer primary.Close()
	outbound := buildDirectRequest(t.Context(), DirectRequest{
		TargetURL: &url.URL{Scheme: "http", Host: "target.invalid"},
		Request:   httptest.NewRequest(http.MethodPost, "/v1/test", nil),
		Body:      body,
	}, primary, preparedDirectAttempt{})
	require.Equal(t, http.NoBody, outbound.Body)
	replay, err := outbound.GetBody()
	require.NoError(t, err)
	require.Equal(t, http.NoBody, replay)
	require.Zero(t, outbound.ContentLength)
}

func TestDirectRequestRebuildsManagedForwardTicket(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ticket     agentauth.ForwardTicket
		routeID    uint
		wantTicket string
		wantRoute  string
	}{
		{name: "managed metadata replaces forged values", ticket: agentauth.ForwardTicket("managed"), routeID: 42, wantTicket: "managed", wantRoute: "42"},
		{name: "empty managed ticket strips forged value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &directReaderTestBody{closed: make([]int, 1)}
			primary, err := openOwnedReader(body)
			require.NoError(t, err)
			defer primary.Close()
			inbound := httptest.NewRequest(http.MethodPost, "/v1/test", nil)
			inbound.Header.Set(consts.HeaderXAgentForwardTicket, "forged")
			inbound.Header.Set(consts.HeaderXAgentRouteID, "999")
			outbound := buildDirectRequest(t.Context(), DirectRequest{
				TargetURL:     &url.URL{Scheme: "http", Host: "target.invalid"},
				Request:       inbound,
				Body:          body,
				ForwardTicket: tc.ticket,
				RouteID:       tc.routeID,
			}, primary, preparedDirectAttempt{})
			require.Equal(t, tc.wantTicket, outbound.Header.Get(consts.HeaderXAgentForwardTicket))
			require.Equal(t, tc.wantRoute, outbound.Header.Get(consts.HeaderXAgentRouteID))
		})
	}
}

func TestDirectRequestAttemptRebuildsManagedHeadersAndUsesDedicatedEndpoint(t *testing.T) {
	body := &directReaderTestBody{closed: make([]int, 2)}
	primary, err := openOwnedReader(body)
	require.NoError(t, err)
	defer primary.Close()
	inbound := httptest.NewRequest(http.MethodPost, "/v1/responses?api-version=2026-07-17", nil)
	inbound.Header.Set("Authorization", "Bearer business-token")
	inbound.Header.Set(consts.HeaderXAgentForwardTicket, "forged-ticket")
	inbound.Header.Set(attemptwire.HeaderMeta, `{"attempt":"forged"}`)
	inbound.Header.Set(consts.HeaderXAgentRouteID, "999")
	inbound.Header.Set(consts.HeaderXAgentHop, "99")
	inbound.Header.Set(consts.HeaderXAgentID, "forged-agent")
	inbound.Header.Set(consts.HeaderXAgentSecret, "forged-secret")
	inbound.Header.Set(consts.HeaderXAgentTag, "forged-tag")
	inbound.Header.Set(consts.HeaderXAgentAddressTag, "forged-address-tag")
	meta := validDirectAttemptMetaForTest()

	directRequest := DirectRequest{
		TargetURL:     &url.URL{Scheme: "https", Host: "target.invalid", Path: "/ignored", RawQuery: "managed=true"},
		Request:       inbound,
		Body:          body,
		ForwardTicket: agentauth.ForwardTicket("managed-ticket"),
		RouteID:       0,
		Hop:           77,
		Attempt:       &meta,
	}
	prepared, err := prepareDirectAttempt(directRequest)
	require.NoError(t, err)
	outbound := buildDirectRequest(t.Context(), directRequest, primary, prepared)

	require.Equal(t, attemptwire.EndpointPath, outbound.URL.Path)
	require.Equal(t, http.MethodPost, outbound.Method)
	require.Equal(t, "managed=true&api-version=2026-07-17", outbound.URL.RawQuery)
	require.Equal(t, int64(4), outbound.ContentLength)
	requestBody, err := io.ReadAll(outbound.Body)
	require.NoError(t, err)
	require.Equal(t, "body", string(requestBody))
	replay, err := outbound.GetBody()
	require.NoError(t, err)
	replayBody, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, "body", string(replayBody))
	require.NoError(t, replay.Close())
	require.Equal(t, "Bearer business-token", outbound.Header.Get("Authorization"))
	require.Equal(t, "managed-ticket", outbound.Header.Get(consts.HeaderXAgentForwardTicket))
	require.Equal(t, "0", outbound.Header.Get(consts.HeaderXAgentRouteID))
	require.Equal(t, "1", outbound.Header.Get(consts.HeaderXAgentHop))
	encodedMeta := outbound.Header.Get(attemptwire.HeaderMeta)
	require.NotEmpty(t, encodedMeta)
	decodedMeta, err := attemptwire.DecodeMeta(encodedMeta)
	require.NoError(t, err)
	require.Equal(t, meta, decodedMeta)
	for _, name := range []string{
		consts.HeaderXAgentID, consts.HeaderXAgentSecret, consts.HeaderXAgentTag, consts.HeaderXAgentAddressTag,
	} {
		require.Empty(t, outbound.Header.Values(name), name)
	}
}

func validDirectAttemptMetaForTest() attemptwire.AttemptProxyMeta {
	return attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
}

func validDirectRequestForTest(request DirectRequest) DirectRequest {
	meta := validDirectAttemptMetaForTest()
	request.ForwardTicket = agentauth.ForwardTicket("managed-ticket")
	request.Attempt = &meta
	return request
}
