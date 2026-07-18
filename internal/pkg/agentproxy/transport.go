package agentproxy

import (
	"container/list"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultDirectTransportLimit = 256

type directTransportKey struct {
	TargetAgentID      string
	AddressFingerprint string
	Scheme             string
	Proxy              string
}

type directTransportPoolOptions struct {
	Limit                 int
	DialContext           func(context.Context, string, string) (net.Conn, error)
	TLSClientConfig       *tls.Config
	ResponseHeaderTimeout time.Duration
	TLSHandshakeTimeout   time.Duration
}

type directTransportEntry struct {
	transport *http.Transport
	lru       *list.Element
}

type directTransportPool struct {
	mu      sync.Mutex
	options directTransportPoolOptions
	entries map[directTransportKey]*directTransportEntry
	lru     list.List
	closed  bool
}

func newDirectTransportPool(opts directTransportPoolOptions) *directTransportPool {
	if opts.Limit <= 0 {
		opts.Limit = defaultDirectTransportLimit
	}
	if opts.TLSHandshakeTimeout <= 0 {
		opts.TLSHandshakeTimeout = 10 * time.Second
	}
	return &directTransportPool{
		options: opts,
		entries: make(map[directTransportKey]*directTransportEntry),
	}
}

func (p *directTransportPool) get(key directTransportKey, proxyURL *url.URL) *http.Transport {
	var retired []*http.Transport
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	if entry := p.entries[key]; entry != nil {
		p.lru.MoveToFront(entry.lru)
		transport := entry.transport
		p.mu.Unlock()
		return transport
	}
	retired = append(retired, p.removeChangedTargetEntriesLocked(key)...)
	transport := p.buildTransport(proxyURL)
	entry := &directTransportEntry{transport: transport}
	entry.lru = p.lru.PushFront(key)
	p.entries[key] = entry
	for len(p.entries) > p.options.Limit {
		oldest := p.lru.Back()
		if oldest == nil {
			break
		}
		retired = append(retired, p.removeLocked(oldest.Value.(directTransportKey)))
	}
	p.mu.Unlock()
	closeIdleTransports(retired)
	return transport
}

func (p *directTransportPool) removeChangedTargetEntriesLocked(next directTransportKey) []*http.Transport {
	var retired []*http.Transport
	for key := range p.entries {
		fingerprintChanged := key.TargetAgentID == next.TargetAgentID && key.AddressFingerprint != next.AddressFingerprint
		proxyChanged := key.TargetAgentID == next.TargetAgentID &&
			key.AddressFingerprint == next.AddressFingerprint && key.Scheme == next.Scheme && key.Proxy != next.Proxy
		if fingerprintChanged || proxyChanged {
			retired = append(retired, p.removeLocked(key))
		}
	}
	return retired
}

func (p *directTransportPool) removeLocked(key directTransportKey) *http.Transport {
	entry := p.entries[key]
	if entry == nil {
		return nil
	}
	delete(p.entries, key)
	p.lru.Remove(entry.lru)
	return entry.transport
}

func (p *directTransportPool) buildTransport(proxyURL *url.URL) *http.Transport {
	dial := p.options.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	dialBeforeWrite := func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err == nil {
			return conn, nil
		}
		if context.Cause(ctx) != nil {
			return nil, err
		}
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return nil, &BeforeWriteError{Stage: "dns", Code: CodeDirectDNS, Err: err}
		}
		return nil, &BeforeWriteError{Stage: "connect", Code: CodeDirectConnect, Err: err}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialBeforeWrite,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   p.options.TLSHandshakeTimeout,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: p.options.ResponseHeaderTimeout,
		TLSClientConfig:       cloneTLSConfig(p.options.TLSClientConfig),
	}
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialBeforeWrite(ctx, network, address)
		if err != nil {
			return nil, err
		}
		config := cloneTLSConfig(transport.TLSClientConfig)
		if config.ServerName == "" {
			host, _, splitErr := net.SplitHostPort(address)
			if splitErr == nil {
				config.ServerName = host
			}
		}
		tlsConn := tls.Client(conn, config)
		handshakeCtx, cancelHandshake := context.WithTimeout(ctx, p.options.TLSHandshakeTimeout)
		handshakeErr := tlsConn.HandshakeContext(handshakeCtx)
		cancelHandshake()
		if handshakeErr != nil {
			_ = conn.Close()
			if context.Cause(ctx) != nil {
				return nil, handshakeErr
			}
			return nil, &BeforeWriteError{Stage: "tls", Code: CodeDirectTLS, Err: handshakeErr}
		}
		return tlsConn, nil
	}
	return transport
}

func (p *directTransportPool) closeIdleConnections() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	retired := make([]*http.Transport, 0, len(p.entries))
	for key := range p.entries {
		retired = append(retired, p.removeLocked(key))
	}
	p.mu.Unlock()
	closeIdleTransports(retired)
}

func (p *directTransportPool) resourceCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

func canonicalProxyURL(proxyURL *url.URL) string {
	if proxyURL == nil {
		return ""
	}
	canonical := *proxyURL
	canonical.Scheme = strings.ToLower(canonical.Scheme)
	canonical.Host = strings.ToLower(canonical.Host)
	canonical.Fragment = ""
	digest := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(digest[:])
}

func cloneTLSConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return config.Clone()
}

func closeIdleTransports(transports []*http.Transport) {
	for _, transport := range transports {
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
}
