package connectivity

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	maxRouteEdges          = 1024
	maxRouteFailures       = 20
	routeEdgeTTL           = time.Hour
	routeEdgeSweepInterval = time.Minute
)

var (
	errInvalidRouteObservation = errors.New("connectivity: invalid route observation")
	errRouteDigestTooLarge     = errors.New("connectivity: route digest exceeds edge limit")
)

type masterEdgeKey struct {
	target       string
	routeID      uint
	selectorKind string
}

type storedRouteEdge struct {
	snapshot               protocol.RouteEdgeSnapshot
	lastEventAt            int64
	legacySuccessCoveredAt int64
	lastLegacyEvent        protocol.RouteEvent
	hasLegacyEvent         bool
}

type sourceRouteEdges struct {
	generation          uint64
	controlGeneration   uint64
	sessionBound        bool
	lastActivityAt      int64
	lastAppliedSequence uint64
	edges               map[masterEdgeKey]storedRouteEdge
	failures            []RouteFailureDiagnostic
}

func (s *Service) ApplyEvents(sourceID string, batch protocol.RouteTelemetryBatch) error {
	if sourceID == "" || batch.Generation == 0 {
		return errInvalidRouteObservation
	}
	now := s.options.Now()
	s.edgesMu.Lock()
	defer s.edgesMu.Unlock()
	state := s.ensureSourceEdgesLocked(sourceID, batch.Generation)
	if state == nil || state.generation != batch.Generation {
		return nil
	}
	applyEventBatchLocked(state, sourceID, batch.Events, now)
	return nil
}

func (s *Service) AdvanceRouteObservationSession(sourceID string, controlGeneration uint64) {
	if s == nil || sourceID == "" || controlGeneration == 0 {
		return
	}
	s.edgesMu.Lock()
	defer s.edgesMu.Unlock()
	state := s.edges[sourceID]
	if state == nil {
		state = newSourceRouteEdges(0)
		s.edges[sourceID] = state
	}
	if state.sessionBound && controlGeneration <= state.controlGeneration {
		return
	}
	state.controlGeneration = controlGeneration
	state.generation = 0
	state.lastAppliedSequence = 0
	state.failures = nil
	state.sessionBound = true
	state.lastActivityAt = s.options.Now().Unix()
}

func (s *Service) ApplySessionEvents(sourceID string, controlGeneration uint64, batch protocol.RouteTelemetryBatch) error {
	if sourceID == "" || controlGeneration == 0 || batch.Generation == 0 {
		return errInvalidRouteObservation
	}
	now := s.options.Now()
	s.edgesMu.Lock()
	defer s.edgesMu.Unlock()
	state := s.edges[sourceID]
	if state == nil || !state.sessionBound || state.controlGeneration != controlGeneration || state.generation != batch.Generation {
		return nil
	}
	applyEventBatchLocked(state, sourceID, batch.Events, now)
	return nil
}

func (s *Service) ReplaceDigest(sourceID string, digest protocol.RouteEdgeDigest) error {
	if sourceID == "" || digest.Generation == 0 {
		return errInvalidRouteObservation
	}
	if len(digest.Edges) > maxRouteEdges {
		return errRouteDigestTooLarge
	}
	now := s.options.Now()
	next := digestEdges(digest.Edges, now)

	s.edgesMu.Lock()
	defer s.edgesMu.Unlock()
	state := s.edges[sourceID]
	if state == nil {
		state = newSourceRouteEdges(digest.Generation)
		s.edges[sourceID] = state
	} else if state.sessionBound {
		return nil
	} else if state.generation == digest.Generation && digest.CoveredThrough < state.lastAppliedSequence {
		return nil
	}
	if state.generation != digest.Generation {
		state.failures = nil
	}
	state.generation = digest.Generation
	state.lastActivityAt = now.Unix()
	state.lastAppliedSequence = digest.CoveredThrough
	state.edges = next
	return nil
}

func (s *Service) ReplaceSessionDigest(sourceID string, controlGeneration uint64, digest protocol.RouteEdgeDigest) error {
	if sourceID == "" || controlGeneration == 0 || digest.Generation == 0 {
		return errInvalidRouteObservation
	}
	if len(digest.Edges) > maxRouteEdges {
		return errRouteDigestTooLarge
	}
	now := s.options.Now()
	next := digestEdges(digest.Edges, now)
	s.edgesMu.Lock()
	defer s.edgesMu.Unlock()
	state := s.edges[sourceID]
	if state == nil || !state.sessionBound || state.controlGeneration != controlGeneration {
		return nil
	}
	if state.generation == 0 {
		state.generation = digest.Generation
	} else if state.generation != digest.Generation {
		return nil
	}
	if digest.CoveredThrough < state.lastAppliedSequence {
		return nil
	}
	state.lastActivityAt = now.Unix()
	state.lastAppliedSequence = digest.CoveredThrough
	state.edges = next
	return nil
}

func digestEdges(edges []protocol.RouteEdgeSnapshot, now time.Time) map[masterEdgeKey]storedRouteEdge {
	next := make(map[masterEdgeKey]storedRouteEdge, len(edges))
	cutoff := now.Add(-routeEdgeTTL).Unix()
	for _, edge := range edges {
		if edge.LastUsedAt > now.Unix() {
			edge.LastUsedAt = now.Unix()
		}
		if edge.TargetAgentID == "" || edge.LastUsedAt <= cutoff {
			continue
		}
		key := masterEdgeKey{target: edge.TargetAgentID, routeID: edge.RouteID, selectorKind: edge.SelectorKind}
		next[key] = storedRouteEdge{
			snapshot: edge, lastEventAt: edge.LastUsedAt, legacySuccessCoveredAt: edge.LastUsedAt,
		}
	}
	return next
}

func (s *Service) RouteEdges(sourceID string) []protocol.RouteEdgeSnapshot {
	if s == nil || sourceID == "" {
		return nil
	}
	s.edgesMu.Lock()
	state := s.edges[sourceID]
	if state == nil {
		s.edgesMu.Unlock()
		return nil
	}
	expireMasterEdges(state, s.options.Now().Add(-routeEdgeTTL).Unix())
	edges := make([]protocol.RouteEdgeSnapshot, 0, len(state.edges))
	for _, edge := range state.edges {
		edges = append(edges, edge.snapshot)
	}
	s.edgesMu.Unlock()
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
	return edges
}

func (s *Service) RouteFailures(sourceID string) []RouteFailureDiagnostic {
	if s == nil || sourceID == "" {
		return nil
	}
	s.edgesMu.Lock()
	defer s.edgesMu.Unlock()
	state := s.edges[sourceID]
	if state == nil {
		return nil
	}
	return append([]RouteFailureDiagnostic(nil), state.failures...)
}

func (s *Service) RouteEdgeSources() []string {
	if s == nil {
		return nil
	}
	s.edgesMu.Lock()
	sources := make([]string, 0, len(s.edges))
	for sourceID, state := range s.edges {
		if sourceID != "" && state != nil && len(state.edges) > 0 {
			sources = append(sources, sourceID)
		}
	}
	s.edgesMu.Unlock()
	sort.Strings(sources)
	return sources
}

func (s *Service) Run(ctx context.Context) {
	if s == nil || ctx == nil {
		return
	}
	ticker := time.NewTicker(routeEdgeSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepExpiredRouteEdges()
		}
	}
}

func (s *Service) sweepExpiredRouteEdges() {
	now := s.options.Now()
	cutoff := now.Add(-routeEdgeTTL).Unix()
	s.edgesMu.Lock()
	for sourceID, state := range s.edges {
		expireMasterEdges(state, cutoff)
		if len(state.edges) == 0 && state.lastActivityAt <= cutoff {
			delete(s.edges, sourceID)
		}
	}
	s.edgesMu.Unlock()
}

func (s *Service) ensureSourceEdgesLocked(sourceID string, generation uint64) *sourceRouteEdges {
	state := s.edges[sourceID]
	if state == nil {
		state = newSourceRouteEdges(generation)
		s.edges[sourceID] = state
		return state
	}
	if state.generation == generation {
		if state.sessionBound {
			return nil
		}
		return state
	}
	return nil
}

func newSourceRouteEdges(generation uint64) *sourceRouteEdges {
	return &sourceRouteEdges{generation: generation, edges: make(map[masterEdgeKey]storedRouteEdge)}
}

func applyEventBatchLocked(state *sourceRouteEdges, sourceID string, events []protocol.RouteEvent, now time.Time) {
	state.lastActivityAt = now.Unix()
	for _, event := range events {
		if event.Sequence == 0 {
			if state.lastAppliedSequence != 0 {
				continue
			}
		} else {
			if event.Sequence <= state.lastAppliedSequence {
				continue
			}
			// behavior change: process sequence, rather than payload guesses, defines the applied boundary.
			state.lastAppliedSequence = event.Sequence
		}
		if applyRouteEvent(state, event, now) {
			recordRouteFailure(state, sourceID, event, now)
		}
	}
	expireMasterEdges(state, now.Add(-routeEdgeTTL).Unix())
}

func applyRouteEvent(state *sourceRouteEdges, event protocol.RouteEvent, now time.Time) bool {
	if event.TargetAgentID == "" {
		return false
	}
	if event.ObservedAt == 0 {
		event.ObservedAt = now.Unix()
	}
	if event.ObservedAt > now.Unix() {
		event.ObservedAt = now.Unix()
	}
	key := masterEdgeKey{target: event.TargetAgentID, routeID: event.RouteID, selectorKind: event.SelectorKind}
	stored, exists := state.edges[key]
	if event.Sequence == 0 && exists {
		if event.ObservedAt < stored.lastEventAt {
			return false
		}
		if event.Result == "success" && event.ObservedAt <= stored.legacySuccessCoveredAt {
			return false
		}
		if event.ObservedAt == stored.lastEventAt && stored.hasLegacyEvent && sameRouteEvent(stored.lastLegacyEvent, event) {
			return false
		}
	}
	if !exists && len(state.edges) >= maxRouteEdges {
		evictOldestMasterEdge(state)
	}
	stored.snapshot.TargetAgentID = event.TargetAgentID
	stored.snapshot.RouteID = event.RouteID
	stored.snapshot.SelectorKind = event.SelectorKind
	stored.snapshot.LastUsedAt = event.ObservedAt
	if exists && stored.lastEventAt > event.ObservedAt {
		stored.snapshot.LastUsedAt = stored.lastEventAt
	}
	stored.snapshot.LastDirectResult = masterEventDirectResult(event)
	stored.snapshot.AddressFingerprint = event.AddressFingerprint
	if event.Result == "success" {
		stored.snapshot.SuccessCount++
		if event.DurationMS > 0 {
			stored.snapshot.LatencyTotalMS += uint64(event.DurationMS)
		}
	}
	if event.ObservedAt > stored.lastEventAt {
		stored.lastEventAt = event.ObservedAt
	}
	if event.Sequence == 0 {
		if event.Result == "success" && event.ObservedAt > stored.legacySuccessCoveredAt {
			stored.legacySuccessCoveredAt = 0
		}
		stored.lastLegacyEvent = event
		stored.hasLegacyEvent = true
	}
	state.edges[key] = stored
	return true
}

func sameRouteEvent(left, right protocol.RouteEvent) bool {
	return left.RequestID == right.RequestID &&
		left.TargetAgentID == right.TargetAgentID &&
		left.RouteID == right.RouteID &&
		left.SelectorKind == right.SelectorKind &&
		left.PathKind == right.PathKind &&
		left.Result == right.Result &&
		left.Stage == right.Stage &&
		left.ReasonCode == right.ReasonCode &&
		left.CommitState == right.CommitState &&
		left.AddressFingerprint == right.AddressFingerprint &&
		left.DurationMS == right.DurationMS &&
		left.ObservedAt == right.ObservedAt &&
		left.Sequence == right.Sequence &&
		slices.Equal(left.Failures, right.Failures)
}

func recordRouteFailure(state *sourceRouteEdges, sourceID string, event protocol.RouteEvent, now time.Time) {
	if event.TargetAgentID == "" {
		return
	}
	occurredAt := event.ObservedAt
	if occurredAt == 0 || occurredAt > now.Unix() {
		occurredAt = now.Unix()
	}
	if len(event.Failures) == 0 {
		if event.Result == "success" && event.ReasonCode == "" {
			return
		}
		appendRouteFailure(state, RouteFailureDiagnostic{
			RequestID: event.RequestID, SourceAgentID: sourceID, TargetAgentID: event.TargetAgentID,
			RouteID: event.RouteID, PathKind: event.PathKind, Stage: event.Stage,
			CommitState: event.CommitState, ReasonCode: event.ReasonCode, OccurredAt: occurredAt,
		})
		return
	}
	for _, attempt := range event.Failures {
		appendRouteFailure(state, RouteFailureDiagnostic{
			RequestID: event.RequestID, SourceAgentID: sourceID, TargetAgentID: event.TargetAgentID,
			RouteID: event.RouteID, PathKind: attempt.PathKind, Stage: attempt.Stage,
			CommitState: attempt.CommitState, ReasonCode: attempt.ReasonCode, OccurredAt: occurredAt,
		})
	}
}

func appendRouteFailure(state *sourceRouteEdges, failure RouteFailureDiagnostic) {
	if len(state.failures) >= maxRouteFailures {
		copy(state.failures, state.failures[len(state.failures)-maxRouteFailures+1:])
		state.failures = state.failures[:maxRouteFailures-1]
	}
	state.failures = append(state.failures, failure)
}

func masterEventDirectResult(event protocol.RouteEvent) string {
	if event.PathKind == "direct" && event.Result == "success" {
		return "success"
	}
	if event.ReasonCode != "" {
		return event.ReasonCode
	}
	return event.Result
}

func evictOldestMasterEdge(state *sourceRouteEdges) {
	var oldestKey masterEdgeKey
	var oldest int64
	first := true
	for key, edge := range state.edges {
		if first || edge.snapshot.LastUsedAt < oldest {
			oldestKey, oldest, first = key, edge.snapshot.LastUsedAt, false
		}
	}
	if !first {
		delete(state.edges, oldestKey)
	}
}

func expireMasterEdges(state *sourceRouteEdges, cutoff int64) {
	for key, edge := range state.edges {
		if edge.snapshot.LastUsedAt <= cutoff {
			delete(state.edges, key)
		}
	}
}
