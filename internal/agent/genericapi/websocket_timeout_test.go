package genericapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestDefaultWebSocketDialersDoNotRetainGorillaHandshakeCeiling(t *testing.T) {
	originalTimeout := websocket.DefaultDialer.HandshakeTimeout

	tests := []struct {
		name   string
		dialer WebSocketDialer
	}{
		{name: "local", dialer: NewWebSocketHandler(WebSocketHandlerOptions{}).dialer},
		{name: "target", dialer: NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{}).dialer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialer, ok := test.dialer.(*websocket.Dialer)
			require.True(t, ok)
			require.Zero(t, dialer.HandshakeTimeout)
			require.False(t, dialer.EnableCompression)
		})
	}

	require.Equal(t, originalTimeout, websocket.DefaultDialer.HandshakeTimeout)
}

func TestDefaultWebSocketDialersUseOnlyHotHandshakeContext(t *testing.T) {
	requestStarted := make(chan struct{}, 16)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestStarted <- struct{}{}
		if request.URL.Query().Has("reject") {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	target := "ws" + strings.TrimPrefix(server.URL, "http")

	tests := []struct {
		name   string
		dialer WebSocketDialer
	}{
		{name: "local", dialer: NewWebSocketHandler(WebSocketHandlerOptions{}).dialer},
		{name: "target", dialer: NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{}).dialer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finder := &mutableAgentSettingsFinder{}

			t.Run("positive timeout", func(t *testing.T) {
				finder.Update(settings.AgentSettings{APIWebSocketHandshakeTimeoutMs: 25})
				dialContext, cancel := webSocketHandshakeContext(t.Context(), finder)
				defer cancel()

				_, _, err := test.dialer.DialContext(dialContext, target, nil)

				require.Error(t, err)
				requireWebSocketHandshakeDeadlineExceeded(t, dialContext)
				requireWebSocketHandshakeStarted(t, requestStarted)
			})

			t.Run("zero uses caller deadline", func(t *testing.T) {
				finder.Update(settings.AgentSettings{})
				parent, cancelParent := context.WithTimeout(t.Context(), 35*time.Millisecond)
				defer cancelParent()
				dialContext, cancelDial := webSocketHandshakeContext(parent, finder)
				defer cancelDial()

				_, _, err := test.dialer.DialContext(dialContext, target, nil)

				require.Error(t, err)
				requireWebSocketHandshakeDeadlineExceeded(t, dialContext)
				requireWebSocketHandshakeStarted(t, requestStarted)
			})

			t.Run("timeout above forty five seconds remains context owned", func(t *testing.T) {
				finder.Update(settings.AgentSettings{APIWebSocketHandshakeTimeoutMs: 46_000})
				dialContext, cancelDial := webSocketHandshakeContext(t.Context(), finder)
				defer cancelDial()
				deadline, ok := dialContext.Deadline()
				require.True(t, ok)
				require.Greater(t, time.Until(deadline), 45*time.Second)

				_, response, err := test.dialer.DialContext(dialContext, target+"?reject=1", nil)

				require.Error(t, err)
				require.NotNil(t, response)
				require.Equal(t, http.StatusBadRequest, response.StatusCode)
				require.NoError(t, context.Cause(dialContext))
				requireWebSocketHandshakeStarted(t, requestStarted)
			})
		})
	}
}

func requireWebSocketHandshakeStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("WebSocket handshake did not reach the provider")
	}
}

func requireWebSocketHandshakeDeadlineExceeded(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
		require.ErrorIs(t, context.Cause(ctx), context.DeadlineExceeded)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("WebSocket handshake context did not reach its deadline")
	}
}
