package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	dialTimeout                   = 30 * time.Second
	handshakeTimeout              = 45 * time.Second
	tcpKeepAlive                  = 30 * time.Second
	maxClientHandshakeMessageSize = 64 * 1024
)

func Dial(ctx context.Context, url string, header http.Header) (*websocket.Conn, *http.Response, error) {
	return newDialer().DialContext(ctx, url, header)
}

func newDialer() *websocket.Dialer {
	return &websocket.Dialer{
		HandshakeTimeout:  handshakeTimeout,
		EnableCompression: false,
		NetDialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: tcpKeepAlive,
		}).DialContext,
	}
}

type ClientDialerOptions struct {
	AgentID       string
	Bootstrap     func() agentauthcache.BootstrapSnapshot
	Limits        func() wire.Limits
	DrainTimeout  func() time.Duration
	TargetHandler *TargetHandler
	Logger        *zap.Logger
}

type clientDialFunc func(context.Context, string, http.Header) (sessionConn, *http.Response, error)

type closeOwnedClientConn struct {
	sessionConn
	closeOwner *wire.ConnectionCloseOwner
}

func (c *closeOwnedClientConn) Close() error {
	return c.closeOwner.Close()
}

type ClientDialer struct {
	opts             ClientDialerOptions
	dial             clientDialFunc
	handshakeTimeout time.Duration
}

func NewClientDialer(opts ClientDialerOptions) *ClientDialer {
	return &ClientDialer{
		opts: opts, handshakeTimeout: handshakeTimeout,
		dial: func(ctx context.Context, target string, header http.Header) (sessionConn, *http.Response, error) {
			return Dial(ctx, target, header)
		},
	}
}

func (d *ClientDialer) managerRuntimeSettings() (wire.Limits, time.Duration) {
	if d == nil {
		return wire.Limits{}, 0
	}
	var limits wire.Limits
	if d.opts.Limits != nil {
		limits = d.opts.Limits()
	}
	var drainTimeout time.Duration
	if d.opts.DrainTimeout != nil {
		drainTimeout = d.opts.DrainTimeout()
	}
	return limits, drainTimeout
}

func (d *ClientDialer) Dial(
	ctx context.Context,
	rawURI string,
	ticket agentauth.RelayTicket,
	desiredGeneration uint64,
) (*Session, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if d == nil || d.opts.AgentID == "" || d.opts.Bootstrap == nil || ticket == "" {
		return nil, errors.New("agent tunnel: incomplete dial credentials")
	}
	effectiveHandshakeTimeout := d.handshakeTimeout
	if effectiveHandshakeTimeout <= 0 {
		effectiveHandshakeTimeout = handshakeTimeout
	}
	target, err := netaddr.AgentRelayTarget(rawURI)
	if err != nil {
		return nil, errors.New("agent tunnel: invalid relay target")
	}
	bootstrap := d.opts.Bootstrap()
	if bootstrap.MasterInstanceID == "" || len(bootstrap.SigningKeys) == 0 {
		return nil, errors.New("agent tunnel: auth bootstrap unavailable")
	}

	header := http.Header{"Authorization": {"Bearer " + string(ticket)}}
	dial := d.dial
	if dial == nil {
		dial = func(ctx context.Context, target string, header http.Header) (sessionConn, *http.Response, error) {
			return Dial(ctx, target, header)
		}
	}
	conn, response, err := dial(ctx, target, header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, errors.New(sanitizeManagerError(fmt.Errorf("agent tunnel: websocket dial failed: %w", err), target))
	}
	rawConn := conn
	conn = &closeOwnedClientConn{
		sessionConn: rawConn,
		closeOwner:  wire.NewConnectionCloseOwner(rawConn.Close),
	}
	conn.SetReadLimit(maxClientHandshakeMessageSize)
	watchOwner := wire.NewHandshakeOutcomeOwner()
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	watchTimer := time.NewTimer(effectiveHandshakeTimeout)
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			if watchOwner.TryOwn(wire.HandshakeCanceled) {
				_ = conn.Close()
			}
		case <-watchTimer.C:
			if watchOwner.TryOwn(wire.HandshakeTimedOut) {
				_ = conn.Close()
			}
		case <-watchStop:
		}
	}()
	finishWatch := func(outcome wire.HandshakeOutcome) wire.HandshakeOutcome {
		watchOwner.TryOwn(outcome)
		watchTimer.Stop()
		close(watchStop)
		<-watchDone
		return watchOwner.Outcome()
	}
	watchFinished := false
	defer func() {
		if !watchFinished {
			finishWatch(wire.HandshakeStopped)
		}
	}()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()

	nonceBytes := make([]byte, 24)
	if _, err = rand.Read(nonceBytes); err != nil {
		return nil, errors.New("agent tunnel: create handshake nonce")
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	if err = conn.SetWriteDeadline(time.Now().Add(effectiveHandshakeTimeout)); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: set handshake write deadline failed")
	}
	hello, _ := json.Marshal(wire.Hello{Nonce: nonce, DesiredGeneration: desiredGeneration})
	if err = conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		return nil, errors.New("agent tunnel: send HELLO failed")
	}
	if err = conn.SetReadDeadline(time.Now().Add(effectiveHandshakeTimeout)); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: set handshake read deadline failed")
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: receive WELCOME failed")
	}
	var welcome wire.Welcome
	if json.Unmarshal(payload, &welcome) != nil || welcome.MasterInstanceID != bootstrap.MasterInstanceID || welcome.SessionGeneration == 0 {
		return nil, errors.New("agent tunnel: invalid WELCOME")
	}
	if _, err := wire.NormalizeV1Limits(welcome.Limits); err != nil {
		return nil, errors.New("agent tunnel: invalid WELCOME limits")
	}
	if d.opts.Limits != nil && !sessionLimitsAllowed(welcome.Limits, d.opts.Limits()) {
		return nil, errors.New("agent tunnel: WELCOME limits exceed configured limits")
	}
	verifier := agentauth.NewVerifier(bootstrapKeySource(bootstrap.SigningKeys))
	if err := verifier.VerifyWelcome(welcome.NonceProof, agentauth.WelcomeProofClaims{
		AgentID: d.opts.AgentID, Nonce: nonce, MasterInstanceID: bootstrap.MasterInstanceID,
		SessionGeneration: welcome.SessionGeneration, DesiredGeneration: desiredGeneration,
	}); err != nil {
		return nil, errors.New("agent tunnel: WELCOME proof verification failed")
	}
	conn.SetReadLimit(sessionMessageReadLimit(welcome.Limits))
	if err = conn.SetReadDeadline(time.Time{}); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: clear handshake read deadline failed")
	}
	if err = conn.SetWriteDeadline(time.Time{}); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: clear handshake write deadline failed")
	}
	ack, _ := json.Marshal(wire.Authenticated{
		DesiredGeneration: desiredGeneration, SessionGeneration: welcome.SessionGeneration,
	})
	if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: send authenticated ACK failed")
	}
	messageType, payload, err = conn.ReadMessage()
	var confirmed wire.Confirmed
	if err != nil || messageType != websocket.TextMessage || json.Unmarshal(payload, &confirmed) != nil ||
		confirmed.DesiredGeneration != desiredGeneration || confirmed.SessionGeneration != welcome.SessionGeneration {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: receive CONFIRMED failed")
	}
	watchOutcome := finishWatch(wire.HandshakeSucceeded)
	watchFinished = true
	switch watchOutcome {
	case wire.HandshakeSucceeded:
	case wire.HandshakeCanceled:
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, errors.New("agent tunnel: handshake canceled")
	case wire.HandshakeTimedOut:
		return nil, errors.New("agent tunnel: handshake timed out")
	default:
		return nil, errors.New("agent tunnel: handshake stopped")
	}
	succeeded = true
	return newSession(conn, welcome.SessionGeneration, welcome.Limits, SessionOptions{TargetHandler: d.opts.TargetHandler, Logger: d.opts.Logger}), nil
}

type bootstrapKeySource []agentauth.PublicKey

func (s bootstrapKeySource) LookupKey(keyID string) (ed25519.PublicKey, bool) {
	for _, key := range s {
		if key.KeyID == keyID && key.Algorithm == "EdDSA" && len(key.Key) == ed25519.PublicKeySize {
			return append(ed25519.PublicKey(nil), key.Key...), true
		}
	}
	return nil, false
}

var _ Dialer = (*ClientDialer)(nil)
