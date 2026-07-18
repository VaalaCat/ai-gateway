package agentproxy_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type directReplayBody struct {
	data       []byte
	openErr    error
	opens      atomic.Int64
	closes     atomic.Int64
	bodyCloses atomic.Int64
}

type nilReaderReplayBody struct{ directReplayBody }

func (*nilReaderReplayBody) Open() (io.ReadCloser, error) { return nil, nil }

func (b *directReplayBody) Size() int64 { return int64(len(b.data)) }
func (b *directReplayBody) Open() (io.ReadCloser, error) {
	b.opens.Add(1)
	if b.openErr != nil {
		return nil, b.openErr
	}
	return &countedReadCloser{Reader: bytes.NewReader(b.data), closed: &b.closes}, nil
}
func (b *directReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, errors.New("limit")
	}
	return append([]byte(nil), b.data...), nil
}
func (b *directReplayBody) Close() error { b.bodyCloses.Add(1); return nil }

type countedReadCloser struct {
	*bytes.Reader
	closed *atomic.Int64
}

func (r *countedReadCloser) Close() error { r.closed.Add(1); return nil }

type trackedInboundBody struct {
	*strings.Reader
	closes atomic.Int64
}

func (b *trackedInboundBody) Close() error {
	b.closes.Add(1)
	return nil
}

type flushingResponseWriter struct {
	header  http.Header
	status  int
	body    bytes.Buffer
	flushes atomic.Int64
}

func (w *flushingResponseWriter) Header() http.Header         { return w.header }
func (w *flushingResponseWriter) WriteHeader(status int)      { w.status = status }
func (w *flushingResponseWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func (w *flushingResponseWriter) Flush()                      { w.flushes.Add(1) }

func directRequest(t *testing.T, target string, body *directReplayBody) agentproxy.DirectRequest {
	t.Helper()
	targetURL, err := url.Parse(target)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "http://source/v1/messages?q=1", strings.NewReader("stale"))
	request.Header.Set(consts.HeaderXAgentID, "forged")
	request.Header.Set(consts.HeaderXAgentTag, "forged-tag")
	request.Header.Set(consts.HeaderXAgentAddressTag, "forged-address")
	request.Header.Set("Connection", "keep-alive, X-Hop")
	request.Header.Set("X-Hop", "secret")
	request.Header.Set("Content-Length", "999")
	request.ContentLength = 999
	meta := directAttemptMeta("/v1/messages")
	return agentproxy.DirectRequest{
		TargetAgentID: "target-a", RouteID: 7, Hop: 1, AddressFingerprint: "fp-a",
		TargetURL: targetURL, Request: request, Body: body,
		ForwardTicket: agentauth.ForwardTicket("forward-ticket"), Attempt: &meta,
	}
}

func directAttemptMeta(path string) attemptwire.AttemptProxyMeta {
	return attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModePassthrough,
		},
		RequestPath: path,
	}
}

func ownedDirectForTest(t *testing.T) *agentproxy.DirectForwarder {
	t.Helper()
	direct := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	t.Cleanup(func() { require.NoError(t, direct.Close(context.Background())) })
	return direct
}

func TestDirectForwardResponseCopyAndRequestSanitization(t *testing.T) {
	var gotBody, gotHop, gotLength, gotSelector, gotConnection string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody, gotHop = string(body), r.Header.Get(consts.HeaderXAgentHop)
		gotLength, gotSelector = r.Header.Get("Content-Length"), r.Header.Get(consts.HeaderXAgentID)
		gotConnection = r.Header.Get("X-Hop")
		w.Header().Set("X-Upstream", "yes")
		w.Header().Set("Connection", "X-Private")
		w.Header().Set("X-Private", "secret")
		w.Header().Set(consts.HeaderXAgentForwardTicket, "must-not-escape")
		w.Header().Set(consts.HeaderXAgentRouteID, "must-not-escape")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ordinary-response"))
	}))
	defer target.Close()
	body := &directReplayBody{data: []byte("fresh")}
	f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	t.Cleanup(func() { _ = f.Close(context.Background()) })
	w := httptest.NewRecorder()
	out := f.Forward(context.Background(), directRequest(t, target.URL, body), w)
	require.NoError(t, out.Err)
	require.True(t, out.ResponseStarted)
	require.Equal(t, tunnel.Committed, out.Commit)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "ordinary-response", w.Body.String())
	require.Equal(t, "yes", w.Header().Get("X-Upstream"))
	require.Empty(t, w.Header().Get("X-Private"))
	require.Empty(t, w.Header().Get(consts.HeaderXAgentForwardTicket))
	require.Empty(t, w.Header().Get(consts.HeaderXAgentRouteID))
	require.Equal(t, "fresh", gotBody)
	require.Equal(t, "1", gotHop)
	require.Equal(t, "5", gotLength)
	require.Empty(t, gotSelector)
	require.Empty(t, gotConnection)
	require.Equal(t, int64(1), body.opens.Load())
	require.Equal(t, int64(1), body.closes.Load())
	require.Zero(t, body.bodyCloses.Load(), "Forward must not close shared ReplayBody")
}

func TestDirectForwardEmptyPOSTHasNoChunkedBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Empty(t, data)
		require.Empty(t, r.TransferEncoding)
		require.Zero(t, r.ContentLength)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })
	request := directRequest(t, target.URL, &directReplayBody{})
	outcome := forwarder.Forward(context.Background(), request, httptest.NewRecorder())
	require.NoError(t, outcome.Err)
}

func TestDirectForwardEveryHTTPStatusIsCommittedWithoutFallbackSignal(t *testing.T) {
	for _, status := range []int{401, 429, 500, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer target.Close()
			f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
			defer f.Close(context.Background())
			w := httptest.NewRecorder()
			out := f.Forward(context.Background(), directRequest(t, target.URL, &directReplayBody{}), w)
			require.NoError(t, out.Err)
			require.Equal(t, tunnel.Committed, out.Commit)
			require.True(t, out.ResponseStarted)
			require.Equal(t, status, w.Code)
		})
	}
}

func TestDirectForwardNoReplayClassifiesRealBeforeWriteFailures(t *testing.T) {
	t.Run("dns", func(t *testing.T) {
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, &net.DNSError{Err: "no such host", Name: "missing.invalid", IsNotFound: true}
			},
		})
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, "http://missing.invalid", &directReplayBody{}), httptest.NewRecorder())
		require.Error(t, out.Err)
		require.Equal(t, tunnel.PreCommit, out.Commit)
		require.Equal(t, "direct_dns", out.Code)
		require.False(t, out.ResponseStarted)
	})

	t.Run("tcp refusal", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		address := ln.Addr().String()
		require.NoError(t, ln.Close())
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, "http://"+address, &directReplayBody{}), httptest.NewRecorder())
		require.Error(t, out.Err)
		require.Equal(t, tunnel.PreCommit, out.Commit)
		require.Equal(t, "direct_connect", out.Code)
	})

	t.Run("dial timeout before connection", func(t *testing.T) {
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, context.DeadlineExceeded
			},
		})
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, "http://timeout.invalid", &directReplayBody{}), httptest.NewRecorder())
		require.Error(t, out.Err)
		require.Equal(t, tunnel.PreCommit, out.Commit)
		require.Equal(t, "direct_connect", out.Code)
	})

	t.Run("dial-local cancellation before connection", func(t *testing.T) {
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, context.Canceled
			},
		})
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, "http://cancelled.invalid", &directReplayBody{}), httptest.NewRecorder())
		require.Error(t, out.Err)
		require.Equal(t, tunnel.PreCommit, out.Commit)
		require.Equal(t, "direct_connect", out.Code)
	})

	t.Run("tls handshake", func(t *testing.T) {
		plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer plain.Close()
		target := "https" + strings.TrimPrefix(plain.URL, "http")
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}) //nolint:gosec -- deliberate broken TLS endpoint
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, target, &directReplayBody{}), httptest.NewRecorder())
		require.Error(t, out.Err)
		require.Equal(t, tunnel.PreCommit, out.Commit)
		require.Equal(t, "direct_tls", out.Code)
	})
}

func TestDirectForwardClassifiesPostConnectFailuresAsUncertain(t *testing.T) {
	t.Run("connection closes after request may be written", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, acceptErr := ln.Accept()
			if acceptErr == nil {
				buf := make([]byte, 4096)
				_, _ = conn.Read(buf)
				_ = conn.Close()
			}
		}()
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, "http://"+ln.Addr().String(), &directReplayBody{data: []byte("body")}), httptest.NewRecorder())
		_ = ln.Close()
		<-done
		require.Error(t, out.Err)
		require.Equal(t, tunnel.CommitUncertain, out.Commit)
		require.False(t, out.ResponseStarted)
	})

	t.Run("response header timeout", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(100 * time.Millisecond) }))
		defer target.Close()
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{ResponseHeaderTimeout: 10 * time.Millisecond})
		defer f.Close(context.Background())
		out := f.Forward(context.Background(), directRequest(t, target.URL, &directReplayBody{}), httptest.NewRecorder())
		require.Error(t, out.Err)
		require.Equal(t, tunnel.CommitUncertain, out.Commit)
	})

	t.Run("caller cancellation", func(t *testing.T) {
		started := make(chan struct{})
		var startedOnce sync.Once
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			startedOnce.Do(func() { close(started) })
			<-time.After(time.Second)
		}))
		defer target.Close()
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{CircuitFailureThreshold: 1})
		defer f.Close(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-started; cancel() }()
		out := f.Forward(ctx, directRequest(t, target.URL, &directReplayBody{}), httptest.NewRecorder())
		require.ErrorIs(t, out.Err, context.Canceled)
		require.Equal(t, tunnel.CommitUncertain, out.Commit)
		second := f.Forward(context.Background(), directRequest(t, target.URL, &directReplayBody{}), httptest.NewRecorder())
		require.NotEqual(t, "direct_circuit_open", second.Code, "caller cancellation must not count as circuit failure")
	})
}

func TestDirectForwardProxyCONNECTFailureIsCommitUncertain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		line, readErr := reader.ReadString('\n')
		if readErr != nil || !strings.HasPrefix(line, "CONNECT ") {
			return
		}
		for {
			line, readErr = reader.ReadString('\n')
			if readErr != nil || line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	}()
	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	require.NoError(t, err)
	request := directRequest(t, "https://target.invalid", &directReplayBody{})
	request.ProxyURL = proxyURL
	f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	defer f.Close(context.Background())
	out := f.Forward(context.Background(), request, httptest.NewRecorder())
	_ = listener.Close()
	<-done
	require.Error(t, out.Err)
	require.Equal(t, tunnel.CommitUncertain, out.Commit)
	require.False(t, out.ResponseStarted)
}

func TestDirectForwardInterruptedSSEDisconnectRemainsCommitted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 50\r\n\r\ndata: one\n\n")
	}()
	f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	defer f.Close(context.Background())
	w := httptest.NewRecorder()
	out := f.Forward(context.Background(), directRequest(t, "http://"+ln.Addr().String(), &directReplayBody{}), w)
	_ = ln.Close()
	<-done
	require.Error(t, out.Err)
	require.Equal(t, tunnel.Committed, out.Commit)
	require.True(t, out.ResponseStarted)
	require.Contains(t, w.Body.String(), "data: one")
}

func TestDirectResponseCopyFlushesStreamingBodyAndCopiesTrailers(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Trailer", "X-Usage")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		w.Header().Set("X-Usage", "9")
	}))
	defer target.Close()
	f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	defer f.Close(context.Background())
	w := &flushingResponseWriter{header: make(http.Header)}
	out := f.Forward(context.Background(), directRequest(t, target.URL, &directReplayBody{}), w)
	require.NoError(t, out.Err)
	require.Equal(t, tunnel.Committed, out.Commit)
	require.Equal(t, http.StatusOK, w.status)
	require.Contains(t, w.body.String(), "data: one")
	require.Positive(t, w.flushes.Load())
	require.Equal(t, "9", w.header.Get("X-Usage"))
}

func TestDirectResponseCopyForwardsDeclaredAndDynamicTrailersOverHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Declared, "+consts.HeaderXAgentForwardTicket)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "response")
		w.Header().Set("X-Declared", "declared-value")
		w.Header().Set(consts.HeaderXAgentForwardTicket, "declared-secret")
		w.Header().Set(http.TrailerPrefix+"X-Dynamic", "dynamic-value")
		w.Header().Set(http.TrailerPrefix+consts.HeaderXAgentRouteID, "dynamic-secret")
	}))
	defer upstream.Close()
	targetURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })
	outcomes := make(chan agentproxy.DirectOutcome, 1)
	meta := directAttemptMeta("/v1/chat/completions")
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outcomes <- forwarder.Forward(r.Context(), agentproxy.DirectRequest{
			TargetAgentID:      "target-a",
			AddressFingerprint: "fp-a",
			TargetURL:          targetURL,
			Request:            r,
			Body:               &directReplayBody{},
			ForwardTicket:      agentauth.ForwardTicket("forward-ticket"),
			Attempt:            &meta,
		}, w)
	}))
	defer downstream.Close()

	response, err := http.Post(downstream.URL, consts.ContentTypeJSON, http.NoBody) //nolint:noctx -- httptest request is bounded by local server cleanup
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	outcome := <-outcomes
	require.NoError(t, outcome.Err)
	require.Equal(t, "declared-value", response.Trailer.Get("X-Declared"))
	require.Equal(t, "dynamic-value", response.Trailer.Get("X-Dynamic"))
	t.Run("declared reserved trailer", func(t *testing.T) {
		require.Empty(t, response.Trailer.Get(consts.HeaderXAgentForwardTicket))
	})
	t.Run("dynamic reserved trailer", func(t *testing.T) {
		require.Empty(t, response.Trailer.Get(consts.HeaderXAgentRouteID))
	})
}

func TestDirectForwardThreeRealFailuresOpenCircuitAndAdminReset(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := ln.Addr().String()
	require.NoError(t, ln.Close())
	f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{CircuitFailureThreshold: 3, CircuitOpenDuration: time.Minute})
	defer f.Close(context.Background())
	request := directRequest(t, "http://"+address, &directReplayBody{})
	for range 3 {
		out := f.Forward(context.Background(), request, httptest.NewRecorder())
		require.Error(t, out.Err)
	}
	blocked := f.Forward(context.Background(), request, httptest.NewRecorder())
	require.Equal(t, tunnel.PreCommit, blocked.Commit)
	require.Equal(t, "direct_circuit_open", blocked.Code)
	f.ResetCircuit("target-a", "fp-a")
	afterReset := f.Forward(context.Background(), request, httptest.NewRecorder())
	require.NotEqual(t, "direct_circuit_open", afterReset.Code)
}

func TestDirectForwardRejectsNilOpenedReaderBeforeRoundTrip(t *testing.T) {
	f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{})
	defer f.Close(context.Background())
	request := directRequest(t, "http://unused.invalid", &directReplayBody{})
	request.Body = &nilReaderReplayBody{}
	out := f.Forward(context.Background(), request, httptest.NewRecorder())
	require.Error(t, out.Err)
	require.Equal(t, tunnel.PreCommit, out.Commit)
	require.Equal(t, "direct_body", out.Code)
}

type routeDirectStub func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.DirectOutcome

func (f routeDirectStub) Forward(ctx context.Context, req agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.DirectOutcome {
	return f(ctx, req, dst)
}

func TestPrepareDirectTargetUsesOneAddressSnapshot(t *testing.T) {
	prepared, err := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
		AgentID:       "target-a",
		HTTPAddresses: `[{"url":"http://private.example:8139","tag":"private"},{"url":"https://public.example:8140","tag":"public"}]`,
		AgentProxyURL: "http://target-proxy.example:3128", GlobalProxyURL: "http://global-proxy.example:3128",
		AddressTag: "public", PreferredTag: "private",
	})
	require.NoError(t, err)
	require.Equal(t, "https://public.example:8140", prepared.TargetURL.String())
	require.Equal(t, "http://target-proxy.example:3128", prepared.ProxyURL.String())
	require.NotEmpty(t, prepared.AddressFingerprint)

	_, err = agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{AgentID: "target-a", HTTPAddresses: `[{"url":"://bad","tag":"public"}]`, AddressTag: "public"})
	require.Error(t, err)
}

func TestExecuteDirectTransportBuildsRequestForOneFrozenTarget(t *testing.T) {
	var got agentproxy.DirectRequest
	direct := routeDirectStub(func(_ context.Context, request agentproxy.DirectRequest, _ http.ResponseWriter) agentproxy.DirectOutcome {
		got = request
		return agentproxy.DirectOutcome{Commit: tunnel.Committed}
	})
	body := &directReplayBody{data: []byte("body")}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	prepared := agentproxy.PreparedDirectTarget{
		AddressFingerprint: "fingerprint", TargetURL: parseURLForForwardTest(t, "https://target.example:8140"),
		ProxyURL: parseURLForForwardTest(t, "http://proxy.example:3128"),
	}

	outcome := agentproxy.ExecuteDirectTransport(t.Context(), direct, agentproxy.DirectTransportRequest{
		TargetAgentID: "target-a", RouteID: 0, Hop: 8, PreparedTarget: prepared,
		Request: request, Body: body, ForwardTicket: agentauth.ForwardTicket("ticket"), Attempt: &meta,
	}, httptest.NewRecorder())

	require.NoError(t, outcome.Err)
	require.Equal(t, "target-a", got.TargetAgentID)
	require.Zero(t, got.RouteID)
	require.Equal(t, uint8(8), got.Hop)
	require.Equal(t, prepared.AddressFingerprint, got.AddressFingerprint)
	require.Equal(t, prepared.TargetURL, got.TargetURL)
	require.Equal(t, prepared.ProxyURL, got.ProxyURL)
	require.Same(t, request, got.Request)
	require.Same(t, body, got.Body)
	require.Equal(t, agentauth.ForwardTicket("ticket"), got.ForwardTicket)
	require.Equal(t, meta, *got.Attempt)
}

func parseURLForForwardTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return parsed
}

type helperRelayLink struct {
	request agentproxy.RelayRequest
	stream  *helperRelayStream
	order   *[]string
	err     error
}

func (l *helperRelayLink) OpenStream(_ context.Context, request agentproxy.RelayRequest) (agentproxy.RelayStream, error) {
	*l.order = append(*l.order, "open")
	l.request = request
	if l.err != nil {
		return nil, l.err
	}
	return l.stream, nil
}

type helperRelayStream struct {
	order     *[]string
	commit    tunnel.CommitState
	commitErr error
	uploadErr error
	copyErr   error
	uploaded  string
	cancelErr error
}

func TestExecuteRelayTransportRejectsMissingAttemptBeforeSideEffects(t *testing.T) {
	order := make([]string, 0, 5)
	stream := &helperRelayStream{order: &order, commit: tunnel.Committed}
	link := &helperRelayLink{stream: stream, order: &order}
	body := &directReplayBody{data: []byte("request-body")}

	outcome := agentproxy.ExecuteRelayTransport(t.Context(), link, agentproxy.RelayTransportRequest{
		TargetAgentID: "target-a", RequestID: "request-a",
		Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil), Body: body,
	}, httptest.NewRecorder())

	assert.Error(t, outcome.Err)
	assert.Equal(t, tunnel.PreCommit, outcome.Commit)
	assert.Equal(t, "validate", outcome.Stage)
	assert.Equal(t, agentproxy.CodeRelayNotReady, outcome.Code)
	assert.Empty(t, order, "OpenStream and provider lifecycle must not start")
	assert.Zero(t, body.opens.Load(), "request body must not be opened")
	assert.Empty(t, stream.uploaded, "provider body must not be dispatched")
}

func (s *helperRelayStream) Commit(context.Context) error {
	*s.order = append(*s.order, "commit")
	return s.commitErr
}

func (s *helperRelayStream) Upload(_ context.Context, source io.Reader) error {
	*s.order = append(*s.order, "upload")
	body, _ := io.ReadAll(source)
	s.uploaded = string(body)
	return s.uploadErr
}

func (s *helperRelayStream) CopyResponse(_ context.Context, dst http.ResponseWriter) error {
	*s.order = append(*s.order, "copy")
	dst.Header().Set("X-Provider", "ok")
	dst.WriteHeader(http.StatusAccepted)
	_, _ = dst.Write([]byte("response"))
	return s.copyErr
}

func (s *helperRelayStream) CommitState() tunnel.CommitState { return s.commit }
func (s *helperRelayStream) Cancel(err error)                { s.cancelErr = err }
func (s *helperRelayStream) Close() error {
	*s.order = append(*s.order, "close")
	return nil
}

func TestExecuteRelayTransportPreservesAttemptRequestAndLifecycle(t *testing.T) {
	order := make([]string, 0, 5)
	stream := &helperRelayStream{order: &order, commit: tunnel.Committed}
	link := &helperRelayLink{stream: stream, order: &order}
	body := &directReplayBody{data: []byte("request-body")}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses?stream=true", nil)
	request.Header.Set("Authorization", "Bearer original")
	request.Header.Set(consts.HeaderXAgentForwardTicket, "forged")
	request.Header.Set(attemptwire.HeaderMeta, "forged-meta")
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	recorder := httptest.NewRecorder()

	outcome := agentproxy.ExecuteRelayTransport(t.Context(), link, agentproxy.RelayTransportRequest{
		TargetAgentID: "target-a", RouteID: 0, RequestID: "request-a",
		Request: request, Body: body, Attempt: &meta,
	}, recorder)

	require.NoError(t, outcome.Err)
	require.Equal(t, tunnel.Committed, outcome.Commit)
	require.True(t, outcome.ResponseStarted)
	require.Equal(t, []string{"open", "commit", "upload", "copy", "close"}, order)
	require.Equal(t, http.MethodPost, link.request.Method)
	require.Equal(t, attemptwire.EndpointPath, link.request.Path)
	require.Equal(t, uint8(1), link.request.Hop)
	require.Zero(t, link.request.RouteID)
	require.Equal(t, meta, *link.request.Attempt)
	require.Equal(t, "Bearer original", link.request.Header.Get("Authorization"))
	require.Empty(t, link.request.Header.Get(consts.HeaderXAgentForwardTicket))
	require.Empty(t, link.request.Header.Get(attemptwire.HeaderMeta))
	require.Equal(t, "request-body", stream.uploaded)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "response", recorder.Body.String())
}

func TestExecuteRelayTransportClassifiesPreCommitUncertainAndCancellation(t *testing.T) {
	tests := []struct {
		name       string
		linkError  error
		stream     *helperRelayStream
		cancel     bool
		wantCommit tunnel.CommitState
		wantCode   string
	}{
		{name: "open unavailable", linkError: errors.New("not ready"), wantCommit: tunnel.PreCommit, wantCode: agentproxy.CodeRelayNotReady},
		{name: "commit unavailable", stream: &helperRelayStream{commit: tunnel.PreCommit, commitErr: errors.New("not ready")}, wantCommit: tunnel.PreCommit, wantCode: agentproxy.CodeRelayNotReady},
		{name: "commit uncertain", stream: &helperRelayStream{commit: tunnel.CommitUncertain, commitErr: errors.New("lost ack")}, wantCommit: tunnel.CommitUncertain, wantCode: agentproxy.CodeRelayCommitUncertain},
		{name: "already canceled", cancel: true, wantCommit: tunnel.PreCommit, wantCode: agentproxy.CodeRequestCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			order := make([]string, 0, 5)
			stream := tt.stream
			if stream == nil {
				stream = &helperRelayStream{commit: tunnel.Committed}
			}
			stream.order = &order
			link := &helperRelayLink{order: &order, stream: stream, err: tt.linkError}
			meta := directAttemptMeta("/v1/responses")

			outcome := agentproxy.ExecuteRelayTransport(ctx, link, agentproxy.RelayTransportRequest{
				TargetAgentID: "target-a", RequestID: "request-a",
				Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil), Body: &directReplayBody{}, Attempt: &meta,
			}, httptest.NewRecorder())

			require.Error(t, outcome.Err)
			require.Equal(t, tt.wantCommit, outcome.Commit)
			require.Equal(t, tt.wantCode, outcome.Code)
		})
	}
}
