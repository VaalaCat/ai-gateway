package rpc

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type directProbeMetric struct{ results []pkgmetrics.ProbeResult }

func (m *directProbeMetric) IncDirectProbe(result pkgmetrics.ProbeResult) {
	m.results = append(m.results, result)
}

func TestDirectProbeMetricRecordsOneBoundedResultPerProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"contract":"direct_ingress_v1","role":"agent","agent_id":"agent-b"}`))
	}))
	t.Cleanup(server.Close)
	metric := &directProbeMetric{}
	prober := NewDirectProber(DirectProberOptions{Metrics: metric})

	prober.Probe(t.Context(), directProbeTarget(server.URL, "agent-b"))
	prober.Probe(t.Context(), protocol.DirectProbeTarget{})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	prober.Probe(cancelled, directProbeTarget(server.URL, "agent-b"))

	require.Equal(t, []pkgmetrics.ProbeResult{
		pkgmetrics.ProbeVerified, pkgmetrics.ProbeInvalid, pkgmetrics.ProbeCancelled,
	}, metric.results)
}

type directProbeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f directProbeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type cancellingDirectProbeBody struct {
	ctx     context.Context
	started chan struct{}
}

func (b *cancellingDirectProbeBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.ctx.Done()
	return 0, context.Cause(b.ctx)
}

func (*cancellingDirectProbeBody) Close() error { return nil }

func TestDirectProbeVerifiesAgentIdentityWithoutBusinessHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, protocol.DirectIngressIdentityPath, r.URL.Path)
		require.Equal(t, "agent-b", r.URL.Query().Get("target_agent_id"))
		require.Len(t, r.URL.Query(), 1)
		for _, header := range []string{"Authorization", "X-API-Key", "X-Vaala-Forward-Ticket"} {
			require.Empty(t, r.Header.Get(header), header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contract":"direct_ingress_v1","role":"agent","agent_id":"agent-b"}`))
	}))
	t.Cleanup(server.Close)

	result := NewDirectProber(DirectProberOptions{}).Probe(t.Context(), protocol.DirectProbeTarget{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b",
		Addresses: []protocol.Address{{URL: server.URL + "/ignored/base?stale=value#fragment"}},
	})
	require.Equal(t, protocol.DirectProbeResult{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: result.CheckedAt, LatencyMS: result.LatencyMS,
	}, result)
	require.Positive(t, result.CheckedAt)
	require.GreaterOrEqual(t, result.LatencyMS, int64(0))
}

func TestDirectProbeAnyHTTPResponseIsNetworkReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	result := directProbeForTest(t, server.URL, "agent-b")
	require.Equal(t, "reachable", result.Network)
	require.Equal(t, "unverified", result.Identity)
	require.False(t, result.Eligible)
	require.Equal(t, "http_status", result.ReasonCode)
}

func TestDirectProbeRejectsWrongOrInvalidIdentity(t *testing.T) {
	tests := []struct {
		name, body, identity, reason string
	}{
		{name: "wrong contract", body: `{"contract":"other","role":"agent","agent_id":"agent-b"}`, identity: "mismatch", reason: "identity_contract_mismatch"},
		{name: "wrong role", body: `{"contract":"direct_ingress_v1","role":"master","agent_id":"agent-b"}`, identity: "mismatch", reason: "identity_role_mismatch"},
		{name: "wrong id", body: `{"contract":"direct_ingress_v1","role":"agent","agent_id":"agent-c"}`, identity: "mismatch", reason: "identity_agent_mismatch"},
		{name: "malformed", body: `{`, identity: "invalid", reason: "identity_malformed"},
		{name: "oversized", body: strings.Repeat("x", (64<<10)+1), identity: "invalid", reason: "identity_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			result := directProbeForTest(t, server.URL, "agent-b")
			require.Equal(t, "reachable", result.Network)
			require.Equal(t, test.identity, result.Identity)
			require.False(t, result.Eligible)
			require.Equal(t, test.reason, result.ReasonCode)
		})
	}
}

func TestDirectProbeRejectsInterruptedBody(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		_, _ = fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\nContent-Type: application/json\r\n\r\n{")
	}()

	result := directProbeForTest(t, "http://"+listener.Addr().String(), "agent-b")
	<-done
	require.Equal(t, "reachable", result.Network)
	require.Equal(t, "invalid", result.Identity)
	require.Equal(t, "identity_interrupted", result.ReasonCode)
}

func TestDirectProbeClassifiesDNSConnectAndTLSFailures(t *testing.T) {
	dnsTransport := http.DefaultTransport.(*http.Transport).Clone()
	dnsTransport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "direct-probe.invalid", IsNotFound: true}
	}
	dns := NewDirectProber(DirectProberOptions{Transport: dnsTransport}).Probe(
		t.Context(), directProbeTarget("http://direct-probe.invalid", "agent-b"),
	)
	require.Equal(t, "unreachable", dns.Network)
	require.Equal(t, "direct_dns", dns.ReasonCode)

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddress := closed.Addr().String()
	require.NoError(t, closed.Close())

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(tlsServer.Close)

	tests := []struct {
		name, address, reason string
	}{
		{name: "connect", address: "http://" + closedAddress, reason: "direct_connect"},
		{name: "tls", address: tlsServer.URL, reason: "direct_tls"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := directProbeForTest(t, test.address, "agent-b")
			require.Equal(t, "unreachable", result.Network)
			require.Equal(t, "unknown", result.Identity)
			require.False(t, result.Eligible)
			require.Equal(t, test.reason, result.ReasonCode)
		})
	}
}

func TestDirectProbeUsesTLSAndEffectiveProxy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"contract":"direct_ingress_v1","role":"agent","agent_id":"agent-b"}`))
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec -- test TLS endpoint
	prober := NewDirectProber(DirectProberOptions{Transport: transport})
	result := prober.Probe(t.Context(), protocol.DirectProbeTarget{
		TargetAgentID: "agent-b", Addresses: []protocol.Address{{URL: server.URL}},
	})
	require.True(t, result.Eligible)
}

func TestDirectProbeHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan protocol.DirectProbeResult, 1)
	go func() {
		done <- NewDirectProber(DirectProberOptions{}).Probe(ctx, directProbeTarget(server.URL, "agent-b"))
	}()
	<-started
	cancel()
	select {
	case result := <-done:
		require.Equal(t, "unreachable", result.Network)
		require.Equal(t, "cancelled", result.ReasonCode)
	case <-time.After(time.Second):
		t.Fatal("direct probe ignored context cancellation")
	}
}

func TestDirectProbeCancellationWhileReadingBodyRemainsUnknown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	readStarted := make(chan struct{})
	client := &http.Client{Transport: directProbeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
			Body: &cancellingDirectProbeBody{ctx: request.Context(), started: readStarted},
		}, nil
	})}
	prober := NewDirectProber(DirectProberOptions{})
	target := directProbeTarget("http://agent-b", "agent-b")
	done := make(chan protocol.DirectProbeResult, 1)
	go func() {
		done <- prober.probeAddress(ctx, client, target, target.Addresses[0], time.Now())
	}()
	<-readStarted
	cancel()
	select {
	case result := <-done:
		require.Equal(t, "reachable", result.Network)
		require.Equal(t, "unknown", result.Identity)
		require.Equal(t, "cancelled", result.ReasonCode)
	case <-time.After(time.Second):
		t.Fatal("direct probe body read ignored context cancellation")
	}
}

func TestDirectProbeOverallTimeoutInterruptsBodyAndReleasesServerHandler(t *testing.T) {
	var active atomic.Int64
	var finished atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		active.Add(1)
		defer finished.Add(1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	prober := NewDirectProber(DirectProberOptions{Timeout: 40 * time.Millisecond})
	for range 3 {
		startedAt := time.Now()
		result := prober.Probe(t.Context(), directProbeTarget(server.URL, "agent-b"))
		require.Less(t, time.Since(startedAt), time.Second)
		require.Equal(t, "reachable", result.Network)
		require.Equal(t, "invalid", result.Identity)
		require.Equal(t, "identity_interrupted", result.ReasonCode)
	}
	require.Eventually(t, func() bool {
		return active.Load() == 3 && finished.Load() == active.Load()
	}, time.Second, 10*time.Millisecond)
}

func TestDirectProbeUsesDefaultTimeoutForNonPositiveOption(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		prober := NewDirectProber(DirectProberOptions{Timeout: timeout})
		require.Equal(t, 10*time.Second, prober.timeout)
	}
}

func TestHandleDirectProbeUsesTypedTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.DirectIngressIdentity{
			Contract: protocol.DirectIngressContractV1, Role: "agent", AgentID: "agent-b",
		})
	}))
	t.Cleanup(server.Close)
	raw, err := json.Marshal(directProbeTarget(server.URL, "agent-b"))
	require.NoError(t, err)
	got, err := HandleDirectProbe(t.Context(), raw, NewDirectProber(DirectProberOptions{}), nil)
	require.NoError(t, err)
	result, ok := got.(protocol.DirectProbeResult)
	require.True(t, ok)
	require.True(t, result.Eligible)
}

func directProbeForTest(t *testing.T, address, agentID string) protocol.DirectProbeResult {
	t.Helper()
	return NewDirectProber(DirectProberOptions{}).Probe(t.Context(), directProbeTarget(address, agentID))
}

func directProbeTarget(address, agentID string) protocol.DirectProbeTarget {
	return protocol.DirectProbeTarget{TargetAgentID: agentID, AddressFingerprint: "fp", Addresses: []protocol.Address{{URL: address}}}
}
