package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const DirectTunnelPath = wire.DirectTunnelPath

const directHandshakeReadLimit = 64 * 1024

type DirectSessionDialRequest struct {
	SourceAgentID string
	TargetAgentID string
	TargetURL     string
	ProxyURL      *url.URL
	Credential    agentauthcache.ForwardCredential
	Limits        wire.Limits
}

type DirectSessionDialer interface {
	DialDirectSession(context.Context, DirectSessionDialRequest) (*Session, error)
}

type DirectDialerOptions struct {
	TLSClientConfig  *tls.Config
	HandshakeTimeout time.Duration
	DialTimeout      time.Duration
	KeepAlive        time.Duration
	Now              func() time.Time
	Logger           *zap.Logger
}

type DirectDialer struct {
	opts DirectDialerOptions
}

func NewDirectDialer(opts DirectDialerOptions) *DirectDialer {
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = handshakeTimeout
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = dialTimeout
	}
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = tcpKeepAlive
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	return &DirectDialer{opts: opts}
}

func (d *DirectDialer) DialDirectSession(ctx context.Context, req DirectSessionDialRequest) (*Session, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if d == nil {
		d = NewDirectDialer(DirectDialerOptions{})
	}
	endpoint, err := DirectWebSocketURL(req.TargetURL, req.TargetAgentID)
	if err != nil {
		return nil, err
	}
	sanitizedEndpoint := sanitizedDirectEndpointURL(endpoint)
	if !validDirectAgentID(req.SourceAgentID) || req.Credential.Ticket == "" ||
		req.Credential.ExpiresAt.IsZero() || !d.opts.Now().Before(req.Credential.ExpiresAt) {
		return nil, directEndpointError("credentials", "invalid", sanitizedEndpoint)
	}
	offer, err := wire.SelectDirectLimits(req.Limits, req.Limits)
	if err != nil {
		return nil, directEndpointError("config", "invalid_limits", sanitizedEndpoint)
	}

	websocketDialer, err := d.websocketDialer(req.ProxyURL)
	if err != nil {
		return nil, directEndpointError("proxy", "invalid", sanitizedEndpoint)
	}
	// behavior change: caller cancellation closes sockets still owned by dial setup.
	dialBinding := newDirectDialConnectionBinding(ctx)
	websocketDialer.NetDialContext = dialBinding.dialContext(websocketDialer.NetDialContext)
	header := http.Header{"Authorization": {"Bearer " + string(req.Credential.Ticket)}}
	conn, response, err := websocketDialer.DialContext(ctx, endpoint, header)
	detached := dialBinding.detach()
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if !detached {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, directContextError("dial", sanitizedEndpoint, ctx.Err())
	}
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, directContextError("dial", sanitizedEndpoint, ctx.Err())
		}
		return nil, directEndpointError("dial", "failed", sanitizedEndpoint)
	}
	owner := wire.NewConnectionCloseOwner(conn.Close)
	owned := &closeOwnedClientConn{sessionConn: conn, closeOwner: owner}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = owner.Close()
		}
	}()

	handshakeCtx, cancel := context.WithTimeout(ctx, d.opts.HandshakeTimeout)
	stopCancellation := context.AfterFunc(handshakeCtx, func() { _ = owner.Close() })
	defer func() {
		stopCancellation()
		cancel()
	}()
	owned.SetReadLimit(directHandshakeReadLimit)
	if err := writeDirectText(handshakeCtx, owned, d.handshakeDeadline(handshakeCtx), wire.DirectHello{
		ProtocolVersion: wire.ProtocolVersion, Limits: offer,
	}); err != nil {
		return nil, directHandshakeFailure(handshakeCtx, "hello", sanitizedEndpoint, err)
	}

	var ready wire.DirectReady
	if err := readDirectText(handshakeCtx, owned, d.handshakeDeadline(handshakeCtx), &ready); err != nil {
		return nil, directHandshakeFailure(handshakeCtx, "ready", sanitizedEndpoint, err)
	}
	if !validDirectReady(ready, req.TargetAgentID, offer) {
		return nil, directHandshakeFailure(handshakeCtx, "ready", sanitizedEndpoint, nil)
	}
	if err := writeDirectText(handshakeCtx, owned, d.handshakeDeadline(handshakeCtx), wire.DirectAccepted{
		SessionGeneration: ready.SessionGeneration,
	}); err != nil {
		return nil, directHandshakeFailure(handshakeCtx, "accepted", sanitizedEndpoint, err)
	}
	var confirmed wire.DirectConfirmed
	if err := readDirectText(handshakeCtx, owned, d.handshakeDeadline(handshakeCtx), &confirmed); err != nil {
		return nil, directHandshakeFailure(handshakeCtx, "confirmed", sanitizedEndpoint, err)
	}
	if confirmed.SessionGeneration != ready.SessionGeneration {
		return nil, directHandshakeFailure(handshakeCtx, "confirmed", sanitizedEndpoint, nil)
	}
	if cause := context.Cause(handshakeCtx); cause != nil {
		return nil, directContextError("confirmed", sanitizedEndpoint, handshakeCtx.Err())
	}
	if err := owned.SetReadDeadline(time.Time{}); err != nil {
		return nil, directEndpointError("confirmed", "clear_read_deadline", sanitizedEndpoint)
	}
	if err := owned.SetWriteDeadline(time.Time{}); err != nil {
		return nil, directEndpointError("confirmed", "clear_write_deadline", sanitizedEndpoint)
	}
	owned.SetReadLimit(sessionMessageReadLimit(ready.Limits))
	if !stopCancellation() {
		if cause := context.Cause(handshakeCtx); cause != nil {
			return nil, directContextError("confirmed", sanitizedEndpoint, handshakeCtx.Err())
		}
		return nil, directEndpointError("confirmed", "canceled", sanitizedEndpoint)
	}
	succeeded = true
	return newSession(owned, ready.SessionGeneration, ready.Limits, SessionOptions{
		Direction: SessionDirectionDirectOutgoing, IngressKind: agentproxy.IngressKindDirectTunnel,
		Logger: d.opts.Logger,
	}), nil
}

func (d *DirectDialer) websocketDialer(proxyURL *url.URL) (*websocket.Dialer, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: d.opts.HandshakeTimeout, EnableCompression: false,
		NetDialContext: (&net.Dialer{Timeout: d.opts.DialTimeout, KeepAlive: d.opts.KeepAlive}).DialContext,
	}
	if d.opts.TLSClientConfig != nil {
		dialer.TLSClientConfig = d.opts.TLSClientConfig.Clone()
	}
	if proxyURL != nil {
		proxyCopy := *proxyURL
		if !validDirectProxyEndpoint(&proxyCopy) {
			return nil, errors.New("invalid direct proxy")
		}
		// behavior change: validate and normalize a detached proxy URL before dialing.
		switch proxyCopy.Scheme {
		case "http", "socks5":
		case "socks5h":
			proxyCopy.Scheme = "socks5"
		default:
			return nil, errors.New("invalid direct proxy")
		}
		dialer.Proxy = http.ProxyURL(&proxyCopy)
	}
	return dialer, nil
}

func validDirectProxyEndpoint(proxyURL *url.URL) bool {
	if proxyURL == nil || !proxyURL.IsAbs() || proxyURL.Opaque != "" ||
		proxyURL.Host == "" || proxyURL.Hostname() == "" {
		return false
	}
	_, port, hasPort, validHost := directProxyHostPort(proxyURL.Host)
	if !validHost || hasPort && !validDirectProxyPort(port) {
		return false
	}
	switch proxyURL.Scheme {
	case "http":
		return true
	case "socks5", "socks5h":
		return hasPort
	default:
		return false
	}
}

func directProxyHostPort(hostPort string) (host, port string, hasPort, valid bool) {
	if strings.Contains(hostPort, "@") {
		return "", "", false, false
	}
	if strings.HasPrefix(hostPort, "[") {
		closingBracket := strings.IndexByte(hostPort, ']')
		if closingBracket < 0 {
			return "", "", false, false
		}
		host = hostPort[1:closingBracket]
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() {
			return "", "", false, false
		}
		remainder := hostPort[closingBracket+1:]
		if remainder == "" {
			return host, "", false, true
		}
		if !strings.HasPrefix(remainder, ":") || strings.Contains(remainder[1:], ":") {
			return "", "", false, false
		}
		return host, remainder[1:], true, true
	}
	if strings.ContainsAny(hostPort, "[]") || strings.Count(hostPort, ":") > 1 {
		return "", "", false, false
	}
	if !strings.Contains(hostPort, ":") {
		return hostPort, "", false, hostPort != ""
	}
	host, port, err := net.SplitHostPort(hostPort)
	return host, port, true, err == nil && host != ""
}

func validDirectProxyPort(port string) bool {
	if port == "" {
		return false
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return false
		}
	}
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value > 0
}

type directDialConnectionBinding struct {
	ctx       context.Context
	mu        sync.Mutex
	conn      net.Conn
	canceled  bool
	stop      func() bool
	watchDone chan struct{}
}

func newDirectDialConnectionBinding(ctx context.Context) *directDialConnectionBinding {
	binding := &directDialConnectionBinding{ctx: ctx, watchDone: make(chan struct{})}
	binding.stop = context.AfterFunc(ctx, binding.closePendingConnection)
	return binding
}

func (b *directDialConnectionBinding) dialContext(
	dial func(context.Context, string, string) (net.Conn, error),
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if b.bind(conn) {
			return conn, nil
		}
		return nil, b.ctx.Err()
	}
}

func (b *directDialConnectionBinding) bind(conn net.Conn) bool {
	b.mu.Lock()
	canceled := b.canceled
	if !canceled {
		b.conn = conn
	}
	b.mu.Unlock()
	if canceled {
		_ = conn.Close()
	}
	return !canceled
}

func (b *directDialConnectionBinding) closePendingConnection() {
	b.mu.Lock()
	b.canceled = true
	conn := b.conn
	b.conn = nil
	b.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	close(b.watchDone)
}

func (b *directDialConnectionBinding) detach() bool {
	if !b.stop() {
		<-b.watchDone
		return false
	}
	b.mu.Lock()
	canceled := context.Cause(b.ctx) != nil
	conn := b.conn
	b.conn = nil
	b.canceled = canceled
	b.mu.Unlock()
	if canceled && conn != nil {
		_ = conn.Close()
	}
	return !canceled
}

func (d *DirectDialer) handshakeDeadline(ctx context.Context) time.Time {
	deadline := d.opts.Now().Add(d.opts.HandshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}

func validDirectReady(ready wire.DirectReady, targetAgentID string, offer wire.Limits) bool {
	if ready.ProtocolVersion != wire.ProtocolVersion || ready.TargetAgentID != targetAgentID || ready.SessionGeneration == 0 {
		return false
	}
	normalized, err := wire.NormalizeV2Limits(ready.Limits)
	return err == nil && normalized == ready.Limits && sessionLimitsAllowed(ready.Limits, offer)
}

func writeDirectText(ctx context.Context, conn sessionConn, deadline time.Time, value any) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func readDirectText(ctx context.Context, conn sessionConn, deadline time.Time, target any) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if messageType != websocket.TextMessage {
		return errUnexpectedMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing direct handshake JSON")
	}
	return nil
}

func directHandshakeFailure(ctx context.Context, stage, endpoint string, operationErr error) error {
	if cause := context.Cause(ctx); cause != nil {
		return directContextError(stage, endpoint, ctx.Err())
	}
	var timeout net.Error
	if errors.As(operationErr, &timeout) && timeout.Timeout() {
		return directContextError(stage, endpoint, context.DeadlineExceeded)
	}
	return directEndpointError(stage, "invalid", endpoint)
}

func directContextError(stage, endpoint string, cause error) error {
	code := "canceled"
	canonicalCause := context.Canceled
	if errors.Is(cause, context.DeadlineExceeded) {
		code = "timeout"
		canonicalCause = context.DeadlineExceeded
	}
	// behavior change: never expose caller-provided context causes.
	return &directOpenError{stage: stage, code: code, endpoint: endpoint, cause: canonicalCause}
}

func DirectWebSocketURL(base, targetAgentID string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", directEndpointError("url", "invalid_base", sanitizedDirectEndpoint(parsed))
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http":
		scheme = "ws"
	case "https":
		scheme = "wss"
	default:
		return "", directEndpointError("url", "invalid_scheme", sanitizedDirectEndpoint(parsed))
	}
	if !validDirectAgentID(targetAgentID) {
		return "", directEndpointError("url", "invalid_target", sanitizedDirectEndpoint(parsed))
	}
	target := &url.URL{Scheme: scheme, Host: parsed.Host, Path: DirectTunnelPath}
	query := url.Values{}
	query.Set("target_agent_id", targetAgentID)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func validDirectAgentID(value string) bool {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			index > 0 && (char == '-' || char == '_' || char == '.' || char == ':') {
			continue
		}
		return false
	}
	return true
}

func sanitizedDirectEndpoint(parsed *url.URL) string {
	if parsed == nil {
		return "invalid://invalid" + DirectTunnelPath
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		scheme = "invalid"
	}
	host := parsed.Host
	if host == "" {
		host = "invalid"
	}
	return scheme + "://" + host + DirectTunnelPath
}

func sanitizedDirectEndpointURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "invalid://invalid" + DirectTunnelPath
	}
	return sanitizedDirectEndpoint(parsed)
}

func directEndpointError(stage, code, endpoint string) error {
	return &directOpenError{stage: stage, code: code, endpoint: endpoint}
}
