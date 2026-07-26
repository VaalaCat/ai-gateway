package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

func TestDirectDialerDetachesProxyURLFromCaller(t *testing.T) {
	proxyURL := &url.URL{
		Scheme: "http", Host: "proxy.example:8080", Path: "/proxy",
		User: url.UserPassword("proxy-user", "proxy-password"),
	}
	dialer, err := NewDirectDialer(DirectDialerOptions{}).websocketDialer(proxyURL)
	require.NoError(t, err)
	require.NotNil(t, dialer.Proxy)

	proxyURL.Scheme = "https"
	proxyURL.Host = "mutated.example:8443"
	proxyURL.Path = "/mutated"
	proxyURL.User = url.UserPassword("mutated-user", "mutated-password")
	request, err := http.NewRequest(http.MethodGet, "http://target.example", nil)
	require.NoError(t, err)
	got, err := dialer.Proxy(request)
	require.NoError(t, err)
	require.Equal(t, "http://proxy-user:proxy-password@proxy.example:8080/proxy", got.String())
}

func TestDirectWebSocketURLReplacesUntrustedBaseComponents(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "http", base: "http://user:password@target.example/old/path?token=secret#fragment", want: "ws://target.example" + DirectTunnelPath + "?target_agent_id=target-a"},
		{name: "https", base: "https://target.example:8443/ignored?q=secret", want: "wss://target.example:8443" + DirectTunnelPath + "?target_agent_id=target-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DirectWebSocketURL(test.base, "target-a")
			require.NoError(t, err)
			require.Equal(t, test.want, got)
			require.NotContains(t, got, "secret")
			require.NotContains(t, got, "password")
		})
	}
}

func TestDirectHandshakeDialerCompletesFourTextSteps(t *testing.T) {
	offer := testLimits(8)
	selected := testLimits(4)
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, DirectTunnelPath, r.URL.Path)
		require.Equal(t, []string{"target-a"}, r.URL.Query()["target_agent_id"])
		require.Len(t, r.URL.Query(), 1)
		require.Equal(t, "Bearer direct-ticket", r.Header.Get("Authorization"))
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		requestSeen <- struct{}{}

		messageType, payload, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.TextMessage, messageType)
		var hello wire.DirectHello
		require.NoError(t, json.Unmarshal(payload, &hello))
		require.Equal(t, wire.ProtocolVersion, hello.ProtocolVersion)
		require.Equal(t, offer, hello.Limits)

		require.NoError(t, conn.WriteJSON(wire.DirectReady{
			ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-a", SessionGeneration: 77, Limits: selected,
		}))
		messageType, payload, err = conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.TextMessage, messageType)
		var accepted wire.DirectAccepted
		require.NoError(t, json.Unmarshal(payload, &accepted))
		require.Equal(t, uint64(77), accepted.SessionGeneration)
		require.NoError(t, conn.WriteJSON(wire.DirectConfirmed{SessionGeneration: 77}))
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)

	dialer := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second})
	session, err := dialer.DialDirectSession(t.Context(), DirectSessionDialRequest{
		SourceAgentID: "source-a", TargetAgentID: "target-a", TargetURL: server.URL + "/untrusted?secret=query",
		Credential: agentauthcache.ForwardCredential{Ticket: agentauth.ForwardTicket("direct-ticket"), ExpiresAt: time.Now().Add(time.Hour)},
		Limits:     offer,
	})
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Equal(t, uint64(77), session.Generation())
	require.Equal(t, SessionDirectionDirectOutgoing, session.opts.Direction)
	require.Equal(t, selected, session.limits)
	receiveWithDirectTimeout(t, requestSeen)
	require.NoError(t, session.Close(t.Context()))
}

func TestDirectHandshakeDialerCompletesOverTrustedWSS(t *testing.T) {
	limits := testLimits(2)
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		requestSeen <- struct{}{}
		completeDirectServerHandshake(t, conn, limits, 81)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()

	session, err := NewDirectDialer(DirectDialerOptions{
		TLSClientConfig: tlsConfig, HandshakeTimeout: time.Second,
	}).DialDirectSession(t.Context(), validDirectDialRequest(server.URL, limits))
	require.NoError(t, err)
	require.Equal(t, uint64(81), session.Generation())
	receiveWithDirectTimeout(t, requestSeen)
	require.NoError(t, session.Close(t.Context()))
}

func TestDirectHandshakeDialerRejectsUntrustedWSS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)

	session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), validDirectDialRequest(server.URL, testLimits(1)),
	)
	require.Error(t, err)
	require.Nil(t, session)
	require.Contains(t, err.Error(), "stage=dial")
	require.Contains(t, err.Error(), "code=failed")
	require.NotContains(t, err.Error(), "direct-ticket")
}

func TestDirectHandshakeDialerUsesConfiguredHTTPProxy(t *testing.T) {
	limits := testLimits(2)
	target := directHandshakeServer(t, func(conn *websocket.Conn) {
		completeDirectServerHandshake(t, conn, limits, 82)
	})
	t.Cleanup(target.Close)
	proxyRequest := make(chan string, 1)
	proxyDone := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(proxyDone)
		proxyRequest <- r.Method + " " + r.Host
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "proxy dial failed", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		copyFinished := make(chan struct{}, 2)
		var copies conc.WaitGroup
		copies.Go(func() { _, _ = io.Copy(upstream, buffered); copyFinished <- struct{}{} })
		copies.Go(func() { _, _ = io.Copy(client, upstream); copyFinished <- struct{}{} })
		<-copyFinished
		_ = client.Close()
		_ = upstream.Close()
		copies.Wait()
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	request := validDirectDialRequest(target.URL, limits)
	request.ProxyURL = proxyURL

	session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(t.Context(), request)
	require.NoError(t, err)
	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)
	select {
	case got := <-proxyRequest:
		require.Equal(t, http.MethodConnect+" "+targetURL.Host, got)
	case <-time.After(time.Second):
		t.Fatal("HTTP proxy did not receive the direct tunnel CONNECT")
	}
	require.NoError(t, session.Close(t.Context()))
	receiveWithDirectTimeout(t, proxyDone)
}

func TestDirectDialerCallerCancelClosesStalledHTTPConnect(t *testing.T) {
	peer := newStalledDirectPeer(t, func(conn net.Conn) error {
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return err
		}
		defer request.Body.Close()
		if request.Method != http.MethodConnect || request.Host != "target.example:8443" {
			return fmt.Errorf("unexpected proxy request: %s %s", request.Method, request.Host)
		}
		return nil
	})
	request := validDirectDialRequest("http://target-user:target-password@target.example:8443/old?target-query-secret=value", testLimits(1))
	request.ProxyURL = &url.URL{
		Scheme: "http", Host: peer.address,
		User: url.UserPassword("proxy-user-secret", "proxy-password-secret"), RawQuery: "proxy-query-secret=value",
	}
	requireStalledDirectDialCanceled(t, peer, request)
}

func TestDirectDialerCallerCancelClosesStalledSOCKSGreeting(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			peer := newStalledDirectPeer(t, func(conn net.Conn) error {
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return err
				}
				if header[0] != 5 || header[1] == 0 {
					return errors.New("invalid SOCKS greeting")
				}
				methods := make([]byte, int(header[1]))
				_, err := io.ReadFull(conn, methods)
				return err
			})
			request := validDirectDialRequest("http://target-user:target-password@target.example:8443/old?target-query-secret=value", testLimits(1))
			request.ProxyURL = &url.URL{
				Scheme: scheme, Host: peer.address,
				User: url.UserPassword("proxy-user-secret", "proxy-password-secret"), RawQuery: "proxy-query-secret=value",
			}
			requireStalledDirectDialCanceled(t, peer, request)
		})
	}
}

func TestDirectDialerCallerCancelClosesStalledWebSocketUpgrade(t *testing.T) {
	peer := newStalledDirectPeer(t, func(conn net.Conn) error {
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return err
		}
		defer request.Body.Close()
		if request.Method != http.MethodGet || !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			return errors.New("invalid WebSocket upgrade request")
		}
		return nil
	})
	targetURL := "http://target-user:target-password@" + peer.address + "/old?target-query-secret=value"
	requireStalledDirectDialCanceled(t, peer, validDirectDialRequest(targetURL, testLimits(1)))
}

func TestDirectDialerDetachesCallerContextAfterSuccessfulHandshake(t *testing.T) {
	limits := testLimits(1)
	roundTripSeen := make(chan struct{})
	connectionClosed := make(chan struct{})
	server := directHandshakeServer(t, func(conn *websocket.Conn) {
		exchangeDirectServerHandshake(t, conn, limits, 84)
		messageType, payload, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.BinaryMessage, messageType)
		require.Equal(t, []byte("session-still-open"), payload)
		require.NoError(t, conn.WriteMessage(messageType, payload))
		close(roundTripSeen)
		_, _, _ = conn.ReadMessage()
		close(connectionClosed)
	})
	defer server.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		ctx, validDirectDialRequest(server.URL, limits),
	)
	require.NoError(t, err)
	cancel(errors.New("detached-caller-secret"))
	<-ctx.Done()
	deadline := time.Now().Add(time.Second)
	require.NoError(t, session.conn.SetWriteDeadline(deadline))
	require.NoError(t, session.conn.SetReadDeadline(deadline))
	require.NoError(t, session.conn.WriteMessage(websocket.BinaryMessage, []byte("session-still-open")))
	messageType, payload, err := session.conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	require.Equal(t, []byte("session-still-open"), payload)
	receiveWithDirectTimeout(t, roundTripSeen)
	require.NoError(t, session.conn.SetWriteDeadline(time.Time{}))
	require.NoError(t, session.conn.SetReadDeadline(time.Time{}))
	require.NoError(t, session.Close(t.Context()))
	receiveWithDirectTimeout(t, connectionClosed)
}

type stalledDirectPeer struct {
	address   string
	listener  net.Listener
	accepted  chan net.Conn
	setup     chan error
	closed    chan error
	done      chan struct{}
	closeOnce sync.Once
}

func newStalledDirectPeer(t *testing.T, readSetup func(net.Conn) error) *stalledDirectPeer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	peer := &stalledDirectPeer{
		address: listener.Addr().String(), listener: listener, accepted: make(chan net.Conn, 1),
		setup: make(chan error, 1), closed: make(chan error, 1), done: make(chan struct{}),
	}
	go func() {
		defer close(peer.done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		peer.accepted <- conn
		_ = listener.Close()
		defer conn.Close()
		if err := readSetup(conn); err != nil {
			peer.setup <- err
			return
		}
		peer.setup <- nil
		buffer := make([]byte, 1)
		_, err = conn.Read(buffer)
		peer.closed <- err
	}()
	t.Cleanup(func() {
		peer.Close()
		select {
		case <-peer.done:
		case <-time.After(time.Second):
			t.Error("timed out waiting for stalled direct peer shutdown")
		}
	})
	return peer
}

func (p *stalledDirectPeer) Close() {
	p.closeOnce.Do(func() {
		_ = p.listener.Close()
		select {
		case conn := <-p.accepted:
			_ = conn.Close()
		case <-p.done:
		}
	})
}

func requireStalledDirectDialCanceled(t *testing.T, peer *stalledDirectPeer, request DirectSessionDialRequest) {
	t.Helper()
	request.Credential.Ticket = agentauth.ForwardTicket("direct-ticket-secret")
	ctx, cancel := context.WithCancelCause(t.Context())
	result := make(chan struct {
		session *Session
		err     error
	}, 1)
	go func() {
		session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Hour}).DialDirectSession(ctx, request)
		result <- struct {
			session *Session
			err     error
		}{session: session, err: err}
	}()
	require.NoError(t, receiveDirectError(t, peer.setup, "dial setup"))
	cancel(errors.New("custom-cancel-cause-secret proxy-user-secret proxy-password-secret proxy-query-secret"))
	returnedBeforeRelease := true
	var got struct {
		session *Session
		err     error
	}
	select {
	case got = <-result:
	case <-time.After(time.Second):
		returnedBeforeRelease = false
		peer.Close()
		got = <-result
	}
	require.Nil(t, got.session)
	require.ErrorIs(t, got.err, context.Canceled)
	require.Contains(t, got.err.Error(), "stage=dial")
	require.Contains(t, got.err.Error(), "code=canceled")
	for _, secret := range []string{
		"custom-cancel-cause-secret", "direct-ticket-secret", "target-query-secret", "target-user", "target-password",
		"proxy-user-secret", "proxy-password-secret", "proxy-query-secret",
	} {
		require.NotContains(t, got.err.Error(), secret)
	}
	require.Error(t, receiveDirectError(t, peer.closed, "peer socket close"))
	require.True(t, returnedBeforeRelease, "DialDirectSession did not return promptly after caller cancellation")
}

func receiveDirectError(t *testing.T, results <-chan error, event string) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", event)
		return nil
	}
}

func TestDirectHandshakeDialerUsesConfiguredSOCKSProxy(t *testing.T) {
	tests := []struct {
		name        string
		proxyScheme string
		secure      bool
	}{
		{name: "socks5 ws", proxyScheme: "socks5"},
		{name: "socks5 wss", proxyScheme: "socks5", secure: true},
		{name: "socks5h ws", proxyScheme: "socks5h"},
		{name: "socks5h wss", proxyScheme: "socks5h", secure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(2)
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
				conn, err := upgrader.Upgrade(w, r, nil)
				require.NoError(t, err)
				defer conn.Close()
				completeDirectServerHandshake(t, conn, limits, 83)
			}))
			target.Config.ErrorLog = log.New(io.Discard, "", 0)
			var tlsConfig *tls.Config
			if test.secure {
				target.StartTLS()
				tlsConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			} else {
				target.Start()
			}
			t.Cleanup(target.Close)

			targetURL, err := url.Parse(target.URL)
			require.NoError(t, err)
			_, port, err := net.SplitHostPort(targetURL.Host)
			require.NoError(t, err)
			targetURL.Host = net.JoinHostPort("example.com", port)
			proxyAddress, observation, proxyDone := directSOCKSProxy(t, target.Listener.Addr().String())
			proxyURL := &url.URL{Scheme: test.proxyScheme, Host: proxyAddress}
			request := validDirectDialRequest(targetURL.String(), limits)
			request.ProxyURL = proxyURL

			session, err := NewDirectDialer(DirectDialerOptions{
				TLSClientConfig: tlsConfig, HandshakeTimeout: time.Second,
			}).DialDirectSession(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, test.proxyScheme, proxyURL.Scheme)
			got := receiveDirectSOCKSObservation(t, observation)
			require.NoError(t, got.Err)
			require.Equal(t, byte(3), got.AddressType)
			require.Equal(t, targetURL.Host, got.Target)
			require.NoError(t, session.Close(t.Context()))
			receiveWithDirectTimeout(t, proxyDone)
		})
	}
}

func TestDirectHandshakeDialerRejectsInvalidProxyBeforeNetwork(t *testing.T) {
	tests := []struct {
		name     string
		proxyURL func(string) *url.URL
	}{
		{
			name: "https",
			proxyURL: func(host string) *url.URL {
				return &url.URL{Scheme: "https", Host: host, User: url.UserPassword("proxy-user-secret", "proxy-password-secret")}
			},
		},
		{
			name: "unknown scheme",
			proxyURL: func(host string) *url.URL {
				return &url.URL{Scheme: "unknown", Host: host, User: url.UserPassword("proxy-user-secret", "proxy-password-secret")}
			},
		},
		{name: "relative", proxyURL: func(string) *url.URL { return &url.URL{Path: "relative-proxy-secret"} }},
		{name: "empty host", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http"} }},
		{
			name: "opaque",
			proxyURL: func(host string) *url.URL {
				return &url.URL{Scheme: "http", Host: host, Opaque: "proxy-opaque-secret"}
			},
		},
		{
			name: "http empty hostname",
			proxyURL: func(host string) *url.URL {
				_, port, _ := net.SplitHostPort(host)
				return &url.URL{Scheme: "http", Host: ":" + port}
			},
		},
		{
			name: "socks5 empty hostname",
			proxyURL: func(host string) *url.URL {
				_, port, _ := net.SplitHostPort(host)
				return &url.URL{Scheme: "socks5", Host: ":" + port}
			},
		},
		{
			name: "socks5h empty hostname",
			proxyURL: func(host string) *url.URL {
				_, port, _ := net.SplitHostPort(host)
				return &url.URL{Scheme: "socks5h", Host: ":" + port}
			},
		},
		{name: "http empty explicit port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http", Host: "127.0.0.1:"} }},
		{name: "http non-numeric port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http", Host: "127.0.0.1:proxy-port-secret"} }},
		{name: "http zero port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http", Host: "127.0.0.1:0"} }},
		{name: "http port above range", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http", Host: "127.0.0.1:65536"} }},
		{name: "socks5 missing port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5", Host: "127.0.0.1"} }},
		{name: "socks5h missing port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5h", Host: "127.0.0.1"} }},
		{name: "socks5 empty explicit port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5", Host: "127.0.0.1:"} }},
		{name: "socks5 non-numeric port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5", Host: "127.0.0.1:proxy-port-secret"} }},
		{name: "socks5 zero port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5", Host: "127.0.0.1:0"} }},
		{name: "socks5h port above range", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5h", Host: "127.0.0.1:65536"} }},
		{name: "malformed IPv6 bracket", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http", Host: "[::1"} }},
		{name: "malformed IPv6 suffix", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "http", Host: "[::1]proxy-port-secret"} }},
		{name: "unbracketed IPv6 port", proxyURL: func(string) *url.URL { return &url.URL{Scheme: "socks5", Host: "::1:1080"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			acceptResult := make(chan error, 1)
			go func() {
				conn, acceptErr := listener.Accept()
				if conn != nil {
					_ = conn.Close()
				}
				acceptResult <- acceptErr
			}()
			request := validDirectDialRequest("http://target-user:target-password@target.invalid/old?target-query-secret=value", testLimits(1))
			request.Credential.Ticket = agentauth.ForwardTicket("direct-ticket-secret")
			request.ProxyURL = test.proxyURL(listener.Addr().String())

			session, dialErr := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(t.Context(), request)
			require.NoError(t, listener.Close())
			acceptErr := <-acceptResult
			require.Error(t, acceptErr, "invalid proxy must be rejected before dialing")
			require.Nil(t, session)
			require.Error(t, dialErr)
			require.Contains(t, dialErr.Error(), "stage=proxy")
			require.Contains(t, dialErr.Error(), "code=invalid")
			for _, secret := range []string{
				"proxy-user-secret", "proxy-password-secret", "relative-proxy-secret", "proxy-opaque-secret", "proxy-port-secret",
				"direct-ticket-secret", "target-user", "target-password", "target-query-secret",
			} {
				require.NotContains(t, dialErr.Error(), secret)
			}
		})
	}
}

func TestDirectDialerAcceptsValidProxyEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		proxyURL   *url.URL
		wantScheme string
	}{
		{name: "http default port", proxyURL: &url.URL{Scheme: "http", Host: "proxy.example"}, wantScheme: "http"},
		{name: "http IPv4 minimum port", proxyURL: &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", "1")}, wantScheme: "http"},
		{name: "http bracketed IPv6 default port", proxyURL: &url.URL{Scheme: "http", Host: "[::1]"}, wantScheme: "http"},
		{name: "http bracketed IPv6 maximum port", proxyURL: &url.URL{Scheme: "http", Host: net.JoinHostPort("::1", "65535")}, wantScheme: "http"},
		{name: "socks5 IPv4", proxyURL: &url.URL{Scheme: "socks5", Host: net.JoinHostPort("127.0.0.1", "1080")}, wantScheme: "socks5"},
		{name: "socks5 bracketed IPv6", proxyURL: &url.URL{Scheme: "socks5", Host: net.JoinHostPort("::1", "1080")}, wantScheme: "socks5"},
		{name: "socks5h maximum port", proxyURL: &url.URL{Scheme: "socks5h", Host: net.JoinHostPort("::1", "65535")}, wantScheme: "socks5"},
	}
	request, err := http.NewRequest(http.MethodGet, "http://target.example", nil)
	require.NoError(t, err)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalScheme := test.proxyURL.Scheme
			dialer, err := NewDirectDialer(DirectDialerOptions{}).websocketDialer(test.proxyURL)
			require.NoError(t, err)
			got, err := dialer.Proxy(request)
			require.NoError(t, err)
			require.Equal(t, test.wantScheme, got.Scheme)
			require.Equal(t, test.proxyURL.Host, got.Host)
			require.Equal(t, originalScheme, test.proxyURL.Scheme)
		})
	}
}

type directSOCKSObservation struct {
	AddressType byte
	Target      string
	Err         error
}

func directSOCKSProxy(t *testing.T, upstreamAddress string) (string, <-chan directSOCKSObservation, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	accepted := make(chan net.Conn, 1)
	observation := make(chan directSOCKSObservation, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- client
		_ = listener.Close()
		defer client.Close()
		serveDirectSOCKSConnect(client, upstreamAddress, observation)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case client := <-accepted:
			_ = client.Close()
		case <-done:
			return
		case <-time.After(time.Second):
			t.Error("timed out waiting for SOCKS proxy accept")
			return
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("timed out waiting for SOCKS proxy shutdown")
		}
	})
	return listener.Addr().String(), observation, done
}

func serveDirectSOCKSConnect(client net.Conn, upstreamAddress string, observations chan<- directSOCKSObservation) {
	observation := readDirectSOCKSConnect(client)
	if observation.Err != nil {
		observations <- observation
		return
	}
	upstream, err := net.Dial("tcp", upstreamAddress)
	if err != nil {
		observation.Err = err
		observations <- observation
		return
	}
	defer upstream.Close()
	if _, err := client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		observation.Err = err
		observations <- observation
		return
	}
	observations <- observation
	copyFinished := make(chan struct{}, 2)
	var copies conc.WaitGroup
	copies.Go(func() { _, _ = io.Copy(upstream, client); copyFinished <- struct{}{} })
	copies.Go(func() { _, _ = io.Copy(client, upstream); copyFinished <- struct{}{} })
	<-copyFinished
	_ = client.Close()
	_ = upstream.Close()
	copies.Wait()
}

func readDirectSOCKSConnect(client net.Conn) directSOCKSObservation {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(client, greeting); err != nil {
		return directSOCKSObservation{Err: err}
	}
	if greeting[0] != 5 || greeting[1] == 0 {
		return directSOCKSObservation{Err: errors.New("invalid SOCKS greeting")}
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return directSOCKSObservation{Err: err}
	}
	if !slices.Contains(methods, byte(0)) {
		return directSOCKSObservation{Err: errors.New("SOCKS client did not offer no-auth method")}
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return directSOCKSObservation{Err: err}
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(client, header); err != nil {
		return directSOCKSObservation{Err: err}
	}
	if header[0] != 5 || header[1] != 1 || header[2] != 0 || header[3] != 3 {
		return directSOCKSObservation{AddressType: header[3], Err: errors.New("invalid SOCKS CONNECT request")}
	}
	length := make([]byte, 1)
	if _, err := io.ReadFull(client, length); err != nil {
		return directSOCKSObservation{AddressType: header[3], Err: err}
	}
	host := make([]byte, int(length[0]))
	port := make([]byte, 2)
	if _, err := io.ReadFull(client, host); err != nil {
		return directSOCKSObservation{AddressType: header[3], Err: err}
	}
	if _, err := io.ReadFull(client, port); err != nil {
		return directSOCKSObservation{AddressType: header[3], Err: err}
	}
	return directSOCKSObservation{
		AddressType: header[3],
		Target:      net.JoinHostPort(string(host), fmt.Sprintf("%d", uint16(port[0])<<8|uint16(port[1]))),
	}
}

func receiveDirectSOCKSObservation(t *testing.T, observations <-chan directSOCKSObservation) directSOCKSObservation {
	t.Helper()
	select {
	case observation := <-observations:
		return observation
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SOCKS CONNECT request")
		return directSOCKSObservation{}
	}
}

func completeDirectServerHandshake(t *testing.T, conn *websocket.Conn, limits wire.Limits, generation uint64) {
	t.Helper()
	exchangeDirectServerHandshake(t, conn, limits, generation)
	_, _, _ = conn.ReadMessage()
}

func exchangeDirectServerHandshake(t *testing.T, conn *websocket.Conn, limits wire.Limits, generation uint64) {
	t.Helper()
	messageType, _, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.NoError(t, conn.WriteJSON(wire.DirectReady{
		ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-a",
		SessionGeneration: generation, Limits: limits,
	}))
	messageType, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	var accepted wire.DirectAccepted
	require.NoError(t, json.Unmarshal(payload, &accepted))
	require.Equal(t, generation, accepted.SessionGeneration)
	require.NoError(t, conn.WriteJSON(wire.DirectConfirmed{SessionGeneration: generation}))
}

func TestDirectHandshakeDialerRejectsInvalidReady(t *testing.T) {
	offer := testLimits(4)
	tests := []struct {
		name  string
		ready wire.DirectReady
	}{
		{name: "wrong protocol", ready: wire.DirectReady{ProtocolVersion: wire.ProtocolVersion + 1, TargetAgentID: "target-a", SessionGeneration: 1, Limits: offer}},
		{name: "wrong target", ready: wire.DirectReady{ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-b", SessionGeneration: 1, Limits: offer}},
		{name: "zero generation", ready: wire.DirectReady{ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-a", Limits: offer}},
		{name: "limits exceed offer", ready: wire.DirectReady{ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-a", SessionGeneration: 1, Limits: func() wire.Limits { limits := offer; limits.MaxConcurrentStreams++; return limits }()}},
		{name: "limits exceed hard cap", ready: wire.DirectReady{ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-a", SessionGeneration: 1, Limits: func() wire.Limits {
			limits := offer
			limits.InitialStreamWindow = wire.MaxV2StreamWindowBytes + 1
			return limits
		}()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := directHandshakeServer(t, func(conn *websocket.Conn) {
				_, _, err := conn.ReadMessage()
				require.NoError(t, err)
				require.NoError(t, conn.WriteJSON(test.ready))
			})
			defer server.Close()
			dialer := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second})
			session, err := dialer.DialDirectSession(t.Context(), validDirectDialRequest(server.URL, offer))
			require.Error(t, err)
			require.Nil(t, session)
			require.Contains(t, err.Error(), "stage=ready")
			require.NotContains(t, err.Error(), "direct-ticket")
			require.NotContains(t, err.Error(), "?")
		})
	}
}

func TestDirectHandshakeDialerRejectsNonTextMalformedAndWrongConfirmed(t *testing.T) {
	offer := testLimits(4)
	tests := []struct {
		name string
		run  func(*websocket.Conn)
	}{
		{name: "binary ready", run: func(conn *websocket.Conn) {
			_, _, _ = conn.ReadMessage()
			require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte(`{}`)))
		}},
		{name: "malformed ready", run: func(conn *websocket.Conn) {
			_, _, _ = conn.ReadMessage()
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"protocol_version":`)))
		}},
		{name: "wrong confirmed", run: func(conn *websocket.Conn) {
			_, _, _ = conn.ReadMessage()
			require.NoError(t, conn.WriteJSON(wire.DirectReady{ProtocolVersion: wire.ProtocolVersion, TargetAgentID: "target-a", SessionGeneration: 9, Limits: offer}))
			_, _, _ = conn.ReadMessage()
			require.NoError(t, conn.WriteJSON(wire.DirectConfirmed{SessionGeneration: 10}))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := directHandshakeServer(t, test.run)
			defer server.Close()
			session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
				t.Context(), validDirectDialRequest(server.URL, offer),
			)
			require.Error(t, err)
			require.Nil(t, session)
			require.NotContains(t, err.Error(), "direct-ticket")
		})
	}
}

func TestDirectHandshakeDialerCallerCancelClosesPendingSocket(t *testing.T) {
	helloRead := make(chan struct{})
	connectionClosed := make(chan struct{})
	server := directHandshakeServer(t, func(conn *websocket.Conn) {
		_, _, err := conn.ReadMessage()
		require.NoError(t, err)
		close(helloRead)
		_, _, _ = conn.ReadMessage()
		close(connectionClosed)
	})
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Hour}).DialDirectSession(
			ctx, validDirectDialRequest(server.URL, testLimits(1)),
		)
		result <- err
	}()
	receiveWithDirectTimeout(t, helloRead)
	cancel()
	dialErr := <-result
	require.ErrorIs(t, dialErr, context.Canceled)
	require.Contains(t, dialErr.Error(), "stage=ready")
	require.Contains(t, dialErr.Error(), "code=canceled")
	require.Contains(t, dialErr.Error(), "endpoint=ws://")
	require.NotContains(t, dialErr.Error(), "?")
	receiveWithDirectTimeout(t, connectionClosed)
}

func TestDirectHandshakeDialerTimeoutIsStableAndSanitized(t *testing.T) {
	helloRead := make(chan struct{})
	server := directHandshakeServer(t, func(conn *websocket.Conn) {
		_, _, err := conn.ReadMessage()
		require.NoError(t, err)
		close(helloRead)
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	_, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: 20 * time.Millisecond}).DialDirectSession(
		t.Context(), validDirectDialRequest(server.URL+"/old?secret=query", testLimits(1)),
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "stage=ready")
	require.Contains(t, err.Error(), "code=timeout")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "?")
	receiveWithDirectTimeout(t, helloRead)
}

func TestDirectHandshakeDialerSanitizesCustomContextCauses(t *testing.T) {
	tests := []struct {
		name     string
		newCtx   func(context.Context) (context.Context, func())
		wantCode string
		wantIs   error
		secret   string
	}{
		{
			name: "cancel",
			newCtx: func(parent context.Context) (context.Context, func()) {
				ctx, cancel := context.WithCancelCause(parent)
				return ctx, func() {
					cancel(errors.New("custom-cancel-cause-secret proxy-user-secret proxy-password-secret proxy-query-secret"))
				}
			},
			wantCode: "canceled", wantIs: context.Canceled, secret: "custom-cancel-cause-secret",
		},
		{
			name: "deadline",
			newCtx: func(parent context.Context) (context.Context, func()) {
				ctx, cancel := context.WithDeadlineCause(
					parent,
					time.Now().Add(time.Second),
					errors.New("custom-deadline-cause-secret proxy-user-secret proxy-password-secret proxy-query-secret"),
				)
				return ctx, cancel
			},
			wantCode: "timeout", wantIs: context.DeadlineExceeded, secret: "custom-deadline-cause-secret",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			helloRead := make(chan struct{})
			connectionClosed := make(chan struct{})
			server := directHandshakeServer(t, func(conn *websocket.Conn) {
				_, _, err := conn.ReadMessage()
				require.NoError(t, err)
				close(helloRead)
				_, _, _ = conn.ReadMessage()
				close(connectionClosed)
			})
			defer server.Close()

			ctx, trigger := test.newCtx(t.Context())
			defer trigger()
			request := validDirectDialRequest(strings.Replace(server.URL, "://", "://target-user:target-password@", 1)+"/old?target-query-secret=value", testLimits(1))
			request.Credential.Ticket = agentauth.ForwardTicket("direct-ticket-secret")
			result := make(chan error, 1)
			go func() {
				_, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Hour}).DialDirectSession(ctx, request)
				result <- err
			}()
			receiveWithDirectTimeout(t, helloRead)
			if test.wantIs == context.Canceled {
				trigger()
			}
			var dialErr error
			select {
			case dialErr = <-result:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for direct context failure")
			}
			require.Error(t, dialErr)
			require.NotContains(t, dialErr.Error(), test.secret)
			require.ErrorIs(t, dialErr, test.wantIs)
			require.Contains(t, dialErr.Error(), "stage=ready")
			require.Contains(t, dialErr.Error(), "code="+test.wantCode)
			for _, secret := range []string{
				"direct-ticket-secret", "target-query-secret", "target-user", "target-password",
				"proxy-user-secret", "proxy-password-secret", "proxy-query-secret",
			} {
				require.NotContains(t, dialErr.Error(), secret)
			}
			receiveWithDirectTimeout(t, connectionClosed)
		})
	}
}

func validDirectDialRequest(targetURL string, limits wire.Limits) DirectSessionDialRequest {
	return DirectSessionDialRequest{
		SourceAgentID: "source-a", TargetAgentID: "target-a", TargetURL: targetURL,
		Credential: agentauthcache.ForwardCredential{Ticket: agentauth.ForwardTicket("direct-ticket"), ExpiresAt: time.Now().Add(time.Hour)},
		Limits:     limits,
	}
}

func directHandshakeServer(t *testing.T, run func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		run(conn)
	}))
}

func receiveWithDirectTimeout(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for direct tunnel test signal")
	}
}

func TestDirectWebSocketURLRejectsInvalidInputsWithoutLeaks(t *testing.T) {
	const secret = "direct-url-secret"
	tests := []struct {
		name   string
		base   string
		target string
	}{
		{name: "empty", target: "target-a"},
		{name: "relative", base: "/path?token=" + secret, target: "target-a"},
		{name: "websocket scheme", base: "ws://target.example/?token=" + secret, target: "target-a"},
		{name: "ftp", base: "ftp://target.example/?token=" + secret, target: "target-a"},
		{name: "missing host", base: "https:///path?token=" + secret, target: "target-a"},
		{name: "empty target", base: "https://target.example/?token=" + secret},
		{name: "invalid target", base: "https://target.example/?token=" + secret, target: "bad target/../"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DirectWebSocketURL(test.base, test.target)
			require.Error(t, err)
			require.Contains(t, err.Error(), "stage=url")
			require.Contains(t, err.Error(), "code=")
			require.NotContains(t, err.Error(), secret)
			require.False(t, strings.Contains(err.Error(), "?"), err.Error())
		})
	}
}

func TestDirectDialFailuresExposeCircuitClassification(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stage  string
		code   string
		counts bool
	}{
		{name: "credentials", err: directEndpointError("credentials", "invalid", "wss://target.example"+DirectTunnelPath), stage: "credentials", code: "invalid"},
		{name: "proxy", err: directEndpointError("proxy", "invalid", "wss://target.example"+DirectTunnelPath), stage: "proxy", code: "invalid"},
		{name: "policy", err: directEndpointError("policy", "disabled", "wss://target.example"+DirectTunnelPath), stage: "policy", code: "disabled"},
		{name: "config", err: directEndpointError("config", "invalid", "wss://target.example"+DirectTunnelPath), stage: "config", code: "invalid"},
		{name: "invalid URL", err: directEndpointError("url", "invalid_target", "invalid://invalid"+DirectTunnelPath), stage: "url", code: "invalid_target"},
		{name: "invalid target", err: directEndpointError("target", "invalid_target", "invalid://invalid"+DirectTunnelPath), stage: "target", code: "invalid_target"},
		{name: "pool", err: directEndpointError("pool", "direct_closed", "wss://target.example"+DirectTunnelPath), stage: "pool", code: "direct_closed"},
		{name: "unknown local stage", err: directEndpointError("local_validation", "invalid", "wss://target.example"+DirectTunnelPath), stage: "local_validation", code: "invalid"},
		{name: "connect", err: directEndpointError("dial", "failed", "wss://target.example"+DirectTunnelPath), stage: "dial", code: "failed", counts: true},
		{name: "TLS", err: directEndpointError("tls", "failed", "wss://target.example"+DirectTunnelPath), stage: "tls", code: "failed", counts: true},
		{name: "upgrade", err: directEndpointError("upgrade", "failed", "wss://target.example"+DirectTunnelPath), stage: "upgrade", code: "failed", counts: true},
		{name: "hello", err: directEndpointError("hello", "invalid", "wss://target.example"+DirectTunnelPath), stage: "hello", code: "invalid", counts: true},
		{name: "ready", err: directEndpointError("ready", "invalid", "wss://target.example"+DirectTunnelPath), stage: "ready", code: "invalid", counts: true},
		{name: "accepted", err: directEndpointError("accepted", "invalid", "wss://target.example"+DirectTunnelPath), stage: "accepted", code: "invalid", counts: true},
		{name: "handshake timeout", err: directContextError("confirmed", "wss://target.example"+DirectTunnelPath, context.DeadlineExceeded), stage: "confirmed", code: "timeout", counts: true},
		{name: "session", err: directEndpointError("session", "failed", "wss://target.example"+DirectTunnelPath), stage: "session", code: "failed", counts: true},
		{name: "local cancellation", err: directContextError("dial", "wss://target.example"+DirectTunnelPath, context.Canceled), stage: "dial", code: "canceled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireDirectOpenFailure(t, test.err, test.stage, test.code, test.counts)
		})
	}
}
