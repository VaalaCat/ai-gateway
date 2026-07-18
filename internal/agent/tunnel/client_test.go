package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	pkgauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type deadlineFailClientConn struct {
	sessionConn
	failReadSet, failWriteSet, failReadClear, failWriteClear bool
}

type closeCountingClientConn struct {
	sessionConn
	closeCalls atomic.Int32
}

func (c *closeCountingClientConn) Close() error {
	c.closeCalls.Add(1)
	return c.sessionConn.Close()
}

type confirmedAfterCloseClientConn struct {
	sessionConn
	reads          atomic.Int32
	closeCalls     atomic.Int32
	confirmedReady chan struct{}
	closed         chan struct{}
	readyOnce      sync.Once
	closedOnce     sync.Once
}

func newConfirmedAfterCloseClientConn(conn sessionConn) *confirmedAfterCloseClientConn {
	return &confirmedAfterCloseClientConn{
		sessionConn: conn, confirmedReady: make(chan struct{}), closed: make(chan struct{}),
	}
}

func (c *confirmedAfterCloseClientConn) ReadMessage() (int, []byte, error) {
	messageType, payload, err := c.sessionConn.ReadMessage()
	if c.reads.Add(1) == 2 && err == nil {
		c.readyOnce.Do(func() { close(c.confirmedReady) })
		<-c.closed
	}
	return messageType, payload, err
}

func (c *confirmedAfterCloseClientConn) Close() error {
	c.closeCalls.Add(1)
	c.closedOnce.Do(func() { close(c.closed) })
	return c.sessionConn.Close()
}

type clientDialResult struct {
	session *Session
	err     error
}

func requireClientClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("%s did not close", label)
	}
}

func newConfirmedRaceClientDialer(t *testing.T) (*ClientDialer, string, <-chan *confirmedAfterCloseClientConn) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer conn.Close()
		_, payload, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		var hello wire.Hello
		require.NoError(t, json.Unmarshal(payload, &hello))
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
			AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
			SessionGeneration: 46, DesiredGeneration: hello.DesiredGeneration,
		})
		token.Header["kid"] = "key-a"
		proof, signErr := token.SignedString(private)
		require.NoError(t, signErr)
		require.NoError(t, conn.WriteJSON(wire.Welcome{
			NonceProof: proof, MasterInstanceID: "master-a", SessionGeneration: 46, Limits: testLimits(4),
		}))
		_, _, readErr = conn.ReadMessage()
		require.NoError(t, readErr)
		require.NoError(t, conn.WriteJSON(wire.Confirmed{DesiredGeneration: 7, SessionGeneration: 46}))
	}))
	t.Cleanup(server.Close)
	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a",
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	wrappedConnections := make(chan *confirmedAfterCloseClientConn, 1)
	baseDial := dialer.dial
	dialer.dial = func(ctx context.Context, target string, header http.Header) (sessionConn, *http.Response, error) {
		conn, response, dialErr := baseDial(ctx, target, header)
		if dialErr != nil {
			return nil, response, dialErr
		}
		wrapped := newConfirmedAfterCloseClientConn(conn)
		wrappedConnections <- wrapped
		return wrapped, response, nil
	}
	return dialer, "ws" + strings.TrimPrefix(server.URL, "http") + "/relay", wrappedConnections
}

func (c *deadlineFailClientConn) SetReadDeadline(deadline time.Time) error {
	if (deadline.IsZero() && c.failReadClear) || (!deadline.IsZero() && c.failReadSet) {
		return errors.New("injected read deadline failure")
	}
	return c.sessionConn.SetReadDeadline(deadline)
}

func (c *deadlineFailClientConn) SetWriteDeadline(deadline time.Time) error {
	if (deadline.IsZero() && c.failWriteClear) || (!deadline.IsZero() && c.failWriteSet) {
		return errors.New("injected write deadline failure")
	}
	return c.sessionConn.SetWriteDeadline(deadline)
}

func TestClientDialDisablesCompressionAndEnablesTCPKeepAlive(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConn := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotContains(t, r.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate")
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		serverConn <- conn
	}))
	t.Cleanup(server.Close)

	conn, _, err := Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	peer := <-serverConn
	t.Cleanup(func() { _ = peer.Close() })

	tcp, ok := conn.UnderlyingConn().(*net.TCPConn)
	require.True(t, ok)
	require.NoError(t, tcp.SetKeepAlive(true))
	require.False(t, newDialer().EnableCompression)
}

func TestClientDialerAuthenticatesHelloWelcomeAndPreservesTargetURI(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/custom/relay", r.URL.Path)
		require.Equal(t, "secret", r.URL.Query().Get("token"))
		require.Equal(t, "Bearer ticket-a", r.Header.Get("Authorization"))
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer conn.Close()
		_, payload, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		var hello wire.Hello
		require.NoError(t, json.Unmarshal(payload, &hello))
		require.Equal(t, uint64(17), hello.DesiredGeneration)
		proof, signErr := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
			AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
			SessionGeneration: 44, DesiredGeneration: hello.DesiredGeneration,
		}).SignedString(private)
		require.NoError(t, signErr)
		parsed, parseErr := jwt.Parse(proof, func(token *jwt.Token) (any, error) { return public, nil })
		require.NoError(t, parseErr)
		require.True(t, parsed.Valid)
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
			AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
			SessionGeneration: 44, DesiredGeneration: hello.DesiredGeneration,
		})
		token.Header["kid"] = "key-a"
		proof, signErr = token.SignedString(private)
		require.NoError(t, signErr)
		require.NoError(t, conn.WriteJSON(wire.Welcome{
			NonceProof: proof, MasterInstanceID: "master-a", SessionGeneration: 44,
			Limits: testLimits(4),
		}))
		_, ackPayload, ackErr := conn.ReadMessage()
		require.NoError(t, ackErr)
		var ack wire.Authenticated
		require.NoError(t, json.Unmarshal(ackPayload, &ack))
		require.Equal(t, uint64(17), ack.DesiredGeneration)
		require.Equal(t, uint64(44), ack.SessionGeneration)
		require.NoError(t, conn.WriteJSON(wire.Confirmed{DesiredGeneration: 17, SessionGeneration: 44}))
	}))
	t.Cleanup(server.Close)

	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a",
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	countedConnections := make(chan *closeCountingClientConn, 1)
	baseDial := dialer.dial
	dialer.dial = func(ctx context.Context, target string, header http.Header) (sessionConn, *http.Response, error) {
		conn, response, dialErr := baseDial(ctx, target, header)
		if dialErr != nil {
			return nil, response, dialErr
		}
		counted := &closeCountingClientConn{sessionConn: conn}
		countedConnections <- counted
		return counted, response, nil
	}
	rawURI := "ws" + strings.TrimPrefix(server.URL, "http") + "/custom/relay?token=secret"
	session, err := dialer.Dial(t.Context(), rawURI, pkgauth.RelayTicket("ticket-a"), 17)
	require.NoError(t, err)
	require.Equal(t, uint64(44), session.Generation())
	session.Cancel(context.Canceled)
	requireClientClosed(t, session.Done(), "successful client session")
	require.EqualValues(t, 1, (<-countedConnections).closeCalls.Load())
}

func TestClientDialerRejectsAuthenticatedACKWithoutConfirmed(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ackRead := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer conn.Close()
		_, payload, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		var hello wire.Hello
		require.NoError(t, json.Unmarshal(payload, &hello))
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
			AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
			SessionGeneration: 45, DesiredGeneration: hello.DesiredGeneration,
		})
		token.Header["kid"] = "key-a"
		proof, signErr := token.SignedString(private)
		require.NoError(t, signErr)
		require.NoError(t, conn.WriteJSON(wire.Welcome{
			NonceProof: proof, MasterInstanceID: "master-a", SessionGeneration: 45, Limits: testLimits(4),
		}))
		_, ackPayload, ackErr := conn.ReadMessage()
		require.NoError(t, ackErr)
		var ack wire.Authenticated
		require.NoError(t, json.Unmarshal(ackPayload, &ack))
		require.Equal(t, uint64(17), ack.DesiredGeneration)
		require.Equal(t, uint64(45), ack.SessionGeneration)
		close(ackRead)
	}))
	t.Cleanup(server.Close)

	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a",
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	_, err = dialer.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/relay", pkgauth.RelayTicket("ticket-a"), 17)
	<-ackRead
	require.Error(t, err)
}

func TestClientDialerHandshakeCancellationClosesConnectionImmediately(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverClosed := make(chan struct{})
	helloRead := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer close(serverClosed)
		defer conn.Close()
		_, _, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		close(helloRead)
		_, _, readErr = conn.ReadMessage()
		require.Error(t, readErr)
	}))
	t.Cleanup(server.Close)

	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a",
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, dialErr := dialer.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/relay", pkgauth.RelayTicket("ticket-a"), 7)
		result <- dialErr
	}()
	<-helloRead
	cancel()
	select {
	case dialErr := <-result:
		require.ErrorIs(t, dialErr, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("handshake cancellation did not interrupt websocket read")
	}
	select {
	case <-serverClosed:
	case <-time.After(time.Second):
		t.Fatal("handshake cancellation did not close server connection")
	}
}

func TestClientDialerCanceledOwnerRejectsConfirmedAfterClose(t *testing.T) {
	dialer, rawURI, wrappedConnections := newConfirmedRaceClientDialer(t)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan clientDialResult, 1)
	go func() {
		session, err := dialer.Dial(ctx, rawURI, pkgauth.RelayTicket("ticket-a"), 7)
		result <- clientDialResult{session: session, err: err}
	}()
	conn := <-wrappedConnections
	requireClientClosed(t, conn.confirmedReady, "CONFIRMED buffered before context cancellation")
	cancel()
	requireClientClosed(t, conn.closed, "client handshake watcher after context cancellation")
	got := <-result
	if got.session != nil {
		got.session.Cancel(context.Canceled)
	}
	require.Nil(t, got.session)
	require.ErrorIs(t, got.err, context.Canceled)
	require.EqualValues(t, 1, conn.closeCalls.Load())
}

func TestClientDialerTimeoutOwnerRejectsConfirmedAfterClose(t *testing.T) {
	dialer, rawURI, wrappedConnections := newConfirmedRaceClientDialer(t)
	dialer.handshakeTimeout = 100 * time.Millisecond
	result := make(chan clientDialResult, 1)
	go func() {
		session, err := dialer.Dial(t.Context(), rawURI, pkgauth.RelayTicket("ticket-a"), 7)
		result <- clientDialResult{session: session, err: err}
	}()
	conn := <-wrappedConnections
	requireClientClosed(t, conn.confirmedReady, "CONFIRMED buffered before handshake timeout")
	requireClientClosed(t, conn.closed, "client handshake watcher after timeout")
	got := <-result
	if got.session != nil {
		got.session.Cancel(context.DeadlineExceeded)
	}
	require.Nil(t, got.session)
	require.Error(t, got.err)
	require.Contains(t, got.err.Error(), "timed out")
	require.EqualValues(t, 1, conn.closeCalls.Load())
}

func TestClientDialerRejectsWelcomeForAnotherDesiredGeneration(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer conn.Close()
		_, payload, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		var hello wire.Hello
		require.NoError(t, json.Unmarshal(payload, &hello))
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
			AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
			SessionGeneration: 3, DesiredGeneration: hello.DesiredGeneration + 1,
		})
		token.Header["kid"] = "key-a"
		proof, signErr := token.SignedString(private)
		require.NoError(t, signErr)
		require.NoError(t, conn.WriteJSON(wire.Welcome{NonceProof: proof, MasterInstanceID: "master-a", SessionGeneration: 3, Limits: testLimits(4)}))
	}))
	t.Cleanup(server.Close)

	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a",
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	rawURI := "ws" + strings.TrimPrefix(server.URL, "http") + "/relay?token=secret"
	_, err = dialer.Dial(t.Context(), rawURI, pkgauth.RelayTicket("ticket-a"), 7)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}

func TestClientDialerRejectsOversizedWelcomeBeforeJSONParsing(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer conn.Close()
		_, _, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		payload := []byte(`{"nonce_proof":"invalid","master_instance_id":"master-a","session_generation":3,"limits":{"max_metadata_bytes":65536,"max_data_bytes":65536,"initial_stream_window":1048576,"max_queued_session_bytes":1048576,"max_concurrent_streams":4},"padding":"` + strings.Repeat("x", 128<<10) + `"}`)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, payload))
	}))
	t.Cleanup(server.Close)
	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a",
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	_, err = dialer.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/relay", pkgauth.RelayTicket("ticket-a"), 7)
	require.Error(t, err)
	require.Contains(t, err.Error(), "receive WELCOME")
}

func TestClientDialerRejectsLimitsBeforeAuthenticatedACK(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ackResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, upgradeErr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, upgradeErr)
		defer conn.Close()
		_, payload, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		var hello wire.Hello
		require.NoError(t, json.Unmarshal(payload, &hello))
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
			AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
			SessionGeneration: 5, DesiredGeneration: hello.DesiredGeneration,
		})
		token.Header["kid"] = "key-a"
		proof, signErr := token.SignedString(private)
		require.NoError(t, signErr)
		granted := testLimits(2)
		require.NoError(t, conn.WriteJSON(wire.Welcome{NonceProof: proof, MasterInstanceID: "master-a", SessionGeneration: 5, Limits: granted}))
		_, _, ackErr := conn.ReadMessage()
		ackResult <- ackErr
	}))
	t.Cleanup(server.Close)
	allowed := testLimits(1)
	dialer := NewClientDialer(ClientDialerOptions{
		AgentID: "agent-a", Limits: func() wire.Limits { return allowed },
		Bootstrap: func() agentauthcache.BootstrapSnapshot {
			return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
		},
	})
	_, err = dialer.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/relay", pkgauth.RelayTicket("ticket-a"), 7)
	require.Error(t, err)
	require.Contains(t, err.Error(), "limits")
	require.Error(t, <-ackResult, "client must close without authenticated ACK")
}

func TestClientDialHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := Dial(ctx, "ws://127.0.0.1:1", nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestClientDialerHasBoundedHandshake(t *testing.T) {
	require.Equal(t, 45*time.Second, newDialer().HandshakeTimeout)
}

func TestClientDialerRejectsEveryDeadlineFailure(t *testing.T) {
	for _, tc := range []struct {
		name                                                     string
		failReadSet, failWriteSet, failReadClear, failWriteClear bool
	}{
		{name: "set write", failWriteSet: true},
		{name: "set read", failReadSet: true},
		{name: "clear read", failReadClear: true},
		{name: "clear write", failWriteClear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			var ackSeen atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, upgradeErr := upgrader.Upgrade(w, r, nil)
				if upgradeErr != nil {
					return
				}
				defer conn.Close()
				_, payload, readErr := conn.ReadMessage()
				if readErr != nil {
					return
				}
				var hello wire.Hello
				if json.Unmarshal(payload, &hello) != nil {
					return
				}
				token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, pkgauth.WelcomeProofClaims{
					AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
					SessionGeneration: 46, DesiredGeneration: hello.DesiredGeneration,
				})
				token.Header["kid"] = "key-a"
				proof, signErr := token.SignedString(private)
				if signErr != nil || conn.WriteJSON(wire.Welcome{NonceProof: proof, MasterInstanceID: "master-a", SessionGeneration: 46, Limits: testLimits(4)}) != nil {
					return
				}
				if _, _, readErr = conn.ReadMessage(); readErr != nil {
					return
				}
				ackSeen.Store(true)
				_ = conn.WriteJSON(wire.Confirmed{DesiredGeneration: 7, SessionGeneration: 46})
			}))
			t.Cleanup(server.Close)
			dialer := NewClientDialer(ClientDialerOptions{
				AgentID: "agent-a",
				Bootstrap: func() agentauthcache.BootstrapSnapshot {
					return agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-a", SigningKeys: []pkgauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: public}}}
				},
			})
			baseDial := dialer.dial
			dialer.dial = func(ctx context.Context, target string, header http.Header) (sessionConn, *http.Response, error) {
				conn, response, dialErr := baseDial(ctx, target, header)
				if dialErr != nil {
					return nil, response, dialErr
				}
				return &deadlineFailClientConn{sessionConn: conn,
					failReadSet: tc.failReadSet, failWriteSet: tc.failWriteSet,
					failReadClear: tc.failReadClear, failWriteClear: tc.failWriteClear,
				}, response, nil
			}
			_, err = dialer.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/relay?token=secret", pkgauth.RelayTicket("ticket-a"), 7)
			require.Error(t, err)
			require.Contains(t, err.Error(), "deadline")
			require.NotContains(t, err.Error(), "secret")
			require.False(t, ackSeen.Load(), "fallible deadline preparation must finish before authenticated ACK")
		})
	}
}
