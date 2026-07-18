package legacy

import (
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/service"
)

type requestContextCarrier interface {
	RequestContext() context.Context
}

type contextReadCloser struct {
	io.Reader
	ctx context.Context
}

func (r *contextReadCloser) RequestContext() context.Context { return r.ctx }

func (r *contextReadCloser) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type contextRoundTripper struct{ base http.RoundTripper }

func (t *contextRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if carrier, ok := req.Body.(requestContextCarrier); ok {
		req = req.WithContext(carrier.RequestContext())
	}
	return t.base.RoundTrip(req)
}

func (t *contextRoundTripper) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

var legacyClientTransportMu sync.Mutex

var sharedLegacyClients = struct {
	sync.Mutex
	refs map[*http.Client]int
}{refs: make(map[*http.Client]int)}

type TransportOwner struct {
	mu      sync.Mutex
	clients map[*http.Client]struct{}
	closed  bool
}

func NewTransportOwner() *TransportOwner {
	return &TransportOwner{clients: make(map[*http.Client]struct{})}
}

func bindLegacyClientTransport(client *http.Client) {
	if client == nil {
		return
	}
	legacyClientTransportMu.Lock()
	defer legacyClientTransportMu.Unlock()
	if _, ok := client.Transport.(*contextRoundTripper); ok {
		return
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &contextRoundTripper{base: base}
}

func (o *TransportOwner) Client(proxyURL string) (*http.Client, error) {
	client, err := service.GetHttpClientWithProxy(proxyURL)
	if err != nil {
		return nil, err
	}
	bindLegacyClientTransport(client)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, context.Canceled
	}
	if _, exists := o.clients[client]; !exists {
		o.clients[client] = struct{}{}
		sharedLegacyClients.Lock()
		sharedLegacyClients.refs[client]++
		sharedLegacyClients.Unlock()
	}
	return client, nil
}

func bindLegacyProxyTransport(owner *TransportOwner, proxyURL string) error {
	if owner != nil {
		_, err := owner.Client(proxyURL)
		return err
	}
	client, err := service.GetHttpClientWithProxy(proxyURL)
	if err != nil {
		return err
	}
	bindLegacyClientTransport(client)
	return nil
}

func (o *TransportOwner) CloseIdleConnections() {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	clients := make([]*http.Client, 0, len(o.clients))
	for client := range o.clients {
		clients = append(clients, client)
	}
	clear(o.clients)
	o.mu.Unlock()
	for _, client := range clients {
		sharedLegacyClients.Lock()
		sharedLegacyClients.refs[client]--
		last := sharedLegacyClients.refs[client] == 0
		if last {
			delete(sharedLegacyClients.refs, client)
		}
		sharedLegacyClients.Unlock()
		if last {
			client.CloseIdleConnections()
		}
	}
}

func (o *TransportOwner) ResourceCount() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.clients)
}
