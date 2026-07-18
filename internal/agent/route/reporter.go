package route

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const defaultDigestInterval = 5 * time.Minute

type Notifier interface {
	Notify(method string, params any) error
}

type ReporterOptions struct {
	DigestInterval time.Duration
}

type reporterClient struct {
	notifier Notifier
	epoch    uint64
}

type Reporter struct {
	observer       *Observer
	digestInterval time.Duration
	reconnect      chan struct{}
	clientMu       sync.RWMutex
	client         reporterClient
	sendFailures   atomic.Uint64

	beforeEventSend func()
}

func NewReporter(observer *Observer, opts ReporterOptions) *Reporter {
	if opts.DigestInterval <= 0 {
		opts.DigestInterval = defaultDigestInterval
	}
	return &Reporter{observer: observer, digestInterval: opts.DigestInterval, reconnect: make(chan struct{}, 1)}
}

func (r *Reporter) SetClient(client Notifier) {
	if r == nil {
		return
	}
	r.clientMu.Lock()
	r.client.epoch++
	if r.client.epoch == 0 {
		r.client.epoch++
	}
	r.client.notifier = client
	r.clientMu.Unlock()
	select {
	case r.reconnect <- struct{}{}:
	default:
	}
}

func (r *Reporter) Run(ctx context.Context) error {
	if r == nil || r.observer == nil {
		return nil
	}
	ticker := time.NewTicker(r.digestInterval)
	defer ticker.Stop()
	var readyEpoch uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.reconnect:
			r.sendDigest(&readyEpoch)
		case <-ticker.C:
			r.sendDigest(&readyEpoch)
		case event := <-r.observer.Events():
			if r.beforeEventSend != nil {
				r.beforeEventSend()
			}
			r.sendEvent(event, &readyEpoch)
		}
	}
}

func (r *Reporter) sendDigest(readyEpoch *uint64) bool {
	client := r.currentClient()
	if client.notifier == nil {
		return false
	}
	if !r.notify(client, consts.RPCAgentRouteDigest, r.observer.Digest()) {
		return false
	}
	*readyEpoch = client.epoch
	return true
}

func (r *Reporter) sendEvent(event protocol.RouteEvent, readyEpoch *uint64) {
	client := r.currentClient()
	if client.notifier == nil {
		return
	}
	if *readyEpoch != client.epoch {
		if !r.sendDigest(readyEpoch) {
			return
		}
		client = r.currentClient()
		if *readyEpoch != client.epoch {
			return
		}
	}
	r.notify(client, consts.RPCAgentRouteTelemetry, protocol.RouteTelemetryBatch{
		Generation: r.observer.Generation(), Events: []protocol.RouteEvent{event},
	})
}

func (r *Reporter) notify(client reporterClient, method string, params any) bool {
	if !r.isCurrent(client.epoch) {
		return false
	}
	err := client.notifier.Notify(method, params)
	current := r.isCurrent(client.epoch)
	if err != nil {
		r.incrementSendFailures()
		return false
	}
	return current
}

func (r *Reporter) currentClient() reporterClient {
	r.clientMu.RLock()
	defer r.clientMu.RUnlock()
	return r.client
}

func (r *Reporter) isCurrent(epoch uint64) bool {
	r.clientMu.RLock()
	defer r.clientMu.RUnlock()
	return epoch != 0 && r.client.epoch == epoch
}

func (r *Reporter) incrementSendFailures() {
	for {
		current := r.sendFailures.Load()
		if current == ^uint64(0) || r.sendFailures.CompareAndSwap(current, current+1) {
			return
		}
	}
}

func (r *Reporter) SendFailures() uint64 {
	if r == nil {
		return 0
	}
	return r.sendFailures.Load()
}
