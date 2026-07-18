package route

import (
	"container/list"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	defaultEdgeLimit     = 1024
	defaultQueueSize     = 1024
	defaultEdgeTTL       = time.Hour
	defaultSuccessWindow = 30 * time.Second
)

type ObserverOptions struct {
	Generation    uint64
	MaxEdges      int
	QueueSize     int
	EdgeTTL       time.Duration
	SuccessWindow time.Duration
	Now           func() time.Time
	Metrics       TelemetryDropCounter
}

type TelemetryDropCounter interface {
	IncRouteTelemetryDropped()
}

type edgeKey struct {
	target       string
	routeID      uint
	selectorKind string
}

type observedEdge struct {
	snapshot protocol.RouteEdgeSnapshot
	lastEmit time.Time
	lru      *list.Element
}

type Observer struct {
	generation    uint64
	maxEdges      int
	edgeTTL       time.Duration
	successWindow time.Duration
	now           func() time.Time
	events        chan protocol.RouteEvent
	dropped       atomic.Uint64

	recordMu sync.Mutex
	mu       sync.Mutex
	edges    map[edgeKey]*observedEdge
	lru      list.List
	sequence uint64

	beforeEventEnqueue func(protocol.RouteEvent)
	metrics            TelemetryDropCounter
}

func NewObserver(opts ObserverOptions) (*Observer, error) {
	generation := opts.Generation
	if generation == 0 {
		var err error
		generation, err = randomGeneration()
		if err != nil {
			return nil, err
		}
	}
	if opts.MaxEdges <= 0 || opts.MaxEdges > defaultEdgeLimit {
		opts.MaxEdges = defaultEdgeLimit
	}
	if opts.QueueSize <= 0 || opts.QueueSize > defaultQueueSize {
		opts.QueueSize = defaultQueueSize
	}
	if opts.EdgeTTL <= 0 {
		opts.EdgeTTL = defaultEdgeTTL
	}
	if opts.SuccessWindow <= 0 {
		opts.SuccessWindow = defaultSuccessWindow
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Observer{
		generation: generation, maxEdges: opts.MaxEdges, edgeTTL: opts.EdgeTTL,
		successWindow: opts.SuccessWindow, now: opts.Now,
		events: make(chan protocol.RouteEvent, opts.QueueSize), edges: make(map[edgeKey]*observedEdge),
		metrics: opts.Metrics,
	}, nil
}

func randomGeneration() (uint64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	generation := binary.LittleEndian.Uint64(raw[:])
	if generation == 0 {
		return 0, errors.New("route observer: random generation is zero")
	}
	return generation, nil
}

func (o *Observer) Record(event protocol.RouteEvent) {
	if o == nil || event.TargetAgentID == "" {
		return
	}
	o.recordMu.Lock()
	now := o.now()
	if event.ObservedAt == 0 {
		event.ObservedAt = now.Unix()
	}
	if event.DurationMS < 0 {
		event.DurationMS = 0
	}
	event, emit := o.update(event, now)
	if !emit {
		o.recordMu.Unlock()
		return
	}
	if o.beforeEventEnqueue != nil {
		o.beforeEventEnqueue(event)
	}
	dropped := false
	select {
	case o.events <- event:
	default:
		dropped = true
	}
	o.recordMu.Unlock()
	if dropped {
		o.dropped.Add(1)
		if o.metrics != nil {
			o.metrics.IncRouteTelemetryDropped()
		}
	}
}

func (o *Observer) update(event protocol.RouteEvent, now time.Time) (protocol.RouteEvent, bool) {
	key := edgeKey{target: event.TargetAgentID, routeID: event.RouteID, selectorKind: event.SelectorKind}
	directResult := eventDirectResult(event)
	o.mu.Lock()
	defer o.mu.Unlock()
	event.Sequence = 0
	sequenceAvailable := o.sequence != ^uint64(0)
	if sequenceAvailable {
		o.sequence++
		event.Sequence = o.sequence
	}

	edge := o.edges[key]
	isNew := edge == nil
	if isNew {
		edge = &observedEdge{snapshot: protocol.RouteEdgeSnapshot{
			TargetAgentID: event.TargetAgentID, RouteID: event.RouteID, SelectorKind: event.SelectorKind,
		}}
		edge.lru = o.lru.PushFront(key)
		o.edges[key] = edge
		o.evictOldestLocked()
	} else {
		o.lru.MoveToFront(edge.lru)
	}
	stateChanged := isNew || edge.snapshot.LastDirectResult != directResult || edge.snapshot.AddressFingerprint != event.AddressFingerprint
	edge.snapshot.LastUsedAt = event.ObservedAt
	edge.snapshot.LastDirectResult = directResult
	edge.snapshot.AddressFingerprint = event.AddressFingerprint
	if event.Result == "success" {
		edge.snapshot.SuccessCount++
		edge.snapshot.LatencyTotalMS += uint64(event.DurationMS)
	}
	immediate := event.Result != "success" || stateChanged
	if immediate || edge.lastEmit.IsZero() || now.Sub(edge.lastEmit) >= o.successWindow {
		edge.lastEmit = now
		return event, sequenceAvailable
	}
	return event, false
}

func eventDirectResult(event protocol.RouteEvent) string {
	if event.PathKind == "direct" && event.Result == "success" {
		return "success"
	}
	if event.ReasonCode != "" {
		return event.ReasonCode
	}
	return event.Result
}

func (o *Observer) evictOldestLocked() {
	if len(o.edges) <= o.maxEdges {
		return
	}
	oldest := o.lru.Back()
	if oldest == nil {
		return
	}
	delete(o.edges, oldest.Value.(edgeKey))
	o.lru.Remove(oldest)
}

func (o *Observer) Digest() protocol.RouteEdgeDigest {
	if o == nil {
		return protocol.RouteEdgeDigest{}
	}
	cutoff := o.now().Add(-o.edgeTTL).Unix()
	o.mu.Lock()
	o.expireLocked(cutoff)
	edges := make([]protocol.RouteEdgeSnapshot, 0, len(o.edges))
	for _, edge := range o.edges {
		edges = append(edges, edge.snapshot)
	}
	coveredThrough := o.sequence
	o.mu.Unlock()
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].LastUsedAt != edges[j].LastUsedAt {
			return edges[i].LastUsedAt > edges[j].LastUsedAt
		}
		if edges[i].TargetAgentID != edges[j].TargetAgentID {
			return edges[i].TargetAgentID < edges[j].TargetAgentID
		}
		if edges[i].RouteID != edges[j].RouteID {
			return edges[i].RouteID < edges[j].RouteID
		}
		return edges[i].SelectorKind < edges[j].SelectorKind
	})
	return protocol.RouteEdgeDigest{Generation: o.generation, Edges: edges, CoveredThrough: coveredThrough}
}

func (o *Observer) expireLocked(cutoff int64) {
	for key, edge := range o.edges {
		if edge.snapshot.LastUsedAt > cutoff {
			continue
		}
		o.lru.Remove(edge.lru)
		delete(o.edges, key)
	}
}

func (o *Observer) Events() <-chan protocol.RouteEvent { return o.events }
func (o *Observer) Dropped() uint64                    { return o.dropped.Load() }
func (o *Observer) Generation() uint64                 { return o.generation }
