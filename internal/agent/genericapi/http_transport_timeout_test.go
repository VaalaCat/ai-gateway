package genericapi

import (
	"context"
	"errors"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestHTTPTransportAppliesFirstZeroTimeoutTuple(t *testing.T) {
	finder := &mutableAgentSettingsFinder{}
	transport := NewHTTPTransport("").WithSettings(finder)
	initial := transport.roundTripper

	current := transport.currentRoundTripper()

	require.NotSame(t, initial, current)
	configured := underlyingHTTPTransport(t, transport)
	require.Zero(t, configured.TLSHandshakeTimeout)
	require.Zero(t, configured.ResponseHeaderTimeout)
	require.Same(t, current, transport.currentRoundTripper())
}

func TestHTTPTransportCanHotDisableTimeouts(t *testing.T) {
	finder := &mutableAgentSettingsFinder{settings: settings.AgentSettings{
		APIUpstreamDialTimeoutMs:           17,
		APIUpstreamTLSHandshakeTimeoutMs:   19,
		APIUpstreamResponseHeaderTimeoutMs: 23,
	}}
	transport := NewHTTPTransport("").WithSettings(finder)

	configured := underlyingHTTPTransportAfterRefresh(t, transport)
	require.Equal(t, 19*time.Millisecond, configured.TLSHandshakeTimeout)
	require.Equal(t, 23*time.Millisecond, configured.ResponseHeaderTimeout)

	finder.Update(settings.AgentSettings{})
	configured = underlyingHTTPTransportAfterRefresh(t, transport)
	require.Zero(t, configured.TLSHandshakeTimeout)
	require.Zero(t, configured.ResponseHeaderTimeout)
}

func TestHTTPTimeoutDialer(t *testing.T) {
	t.Run("positive timeout stops blocked dial", func(t *testing.T) {
		dialer := newHTTPTimeoutDialer(20 * time.Millisecond)
		dialer.ControlContext = blockDialUntilContextDone

		startedAt := time.Now()
		_, err := dialer.DialContext(t.Context(), "tcp", "127.0.0.1:1")

		require.Error(t, err)
		require.GreaterOrEqual(t, time.Since(startedAt), 10*time.Millisecond)
		require.Less(t, time.Since(startedAt), 2*time.Second)
	})

	t.Run("zero timeout leaves blocked dial to caller context", func(t *testing.T) {
		dialer := newHTTPTimeoutDialer(0)
		require.Zero(t, dialer.Timeout)
		require.Equal(t, 30*time.Second, dialer.KeepAlive)
		dialer.ControlContext = blockDialUntilContextDone
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()

		_, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:1")

		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("zero timeout preserves caller cancellation", func(t *testing.T) {
		dialer := newHTTPTimeoutDialer(0)
		dialer.ControlContext = blockDialUntilContextDone
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:1")

		require.Error(t, err)
		require.True(t, errors.Is(err, context.Canceled))
	})
}

func underlyingHTTPTransportAfterRefresh(t *testing.T, transport *HTTPTransport) *http.Transport {
	t.Helper()
	transport.currentRoundTripper()
	return underlyingHTTPTransport(t, transport)
}

func blockDialUntilContextDone(ctx context.Context, _, _ string, _ syscall.RawConn) error {
	<-ctx.Done()
	return context.Cause(ctx)
}
