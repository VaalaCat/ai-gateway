package genericapi

import (
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type APIUpstreamIndex interface {
	UpstreamsForBackend(backendID uint) []protocol.SyncedAPIUpstream
}

type APIBreakerFinder interface {
	Healthy(upstreamID uint) bool
	TryAcquire(upstreamID uint) (APIBreakerPermit, bool)
}

type RequestHash func(requestID string) uint64

type APIUpstreamPicker struct {
	index    APIUpstreamIndex
	breakers APIBreakerFinder
	hash     RequestHash
}

// APIUpstreamLease freezes the selected upstream and owns its breaker
// completion permit. Finish is idempotent through the bound permit.
type APIUpstreamLease struct {
	Upstream protocol.SyncedAPIUpstream
	permit   APIBreakerPermit
}

func (l *APIUpstreamLease) valid() bool {
	return l != nil && l.Upstream.ID != 0 && l.permit != nil
}

func (l *APIUpstreamLease) Finish(completion APIBreakerCompletion) {
	if l == nil || l.permit == nil {
		return
	}
	l.permit.Finish(completion)
}

func NewAPIUpstreamPicker(index APIUpstreamIndex, breakers APIBreakerFinder) *APIUpstreamPicker {
	return &APIUpstreamPicker{index: index, breakers: breakers, hash: stableRequestHash}
}

func (p *APIUpstreamPicker) Pick(backendID uint, requestedProtocol apiattempt.APIProtocol, requestID string) (*APIUpstreamLease, error) {
	if p == nil || p.index == nil || p.breakers == nil || backendID == 0 || requestID == "" || !knownAPIProtocol(requestedProtocol) {
		return nil, unavailableUpstream("invalid picker input")
	}

	candidates, err := validAPIUpstreamCandidates(p.index.UpstreamsForBackend(backendID), backendID)
	if err != nil || len(candidates) == 0 {
		return nil, unavailableUpstream("no valid upstream candidate")
	}
	healthy := candidates[:0]
	for _, candidate := range candidates {
		if p.breakers.Healthy(candidate.ID) {
			healthy = append(healthy, candidate)
		}
	}
	if len(healthy) == 0 {
		return nil, unavailableUpstream("all upstream breakers unavailable")
	}
	sort.Slice(healthy, func(i, j int) bool {
		if healthy[i].Priority != healthy[j].Priority {
			return healthy[i].Priority > healthy[j].Priority
		}
		return healthy[i].ID < healthy[j].ID
	})

	hash := p.hash
	if hash == nil {
		hash = stableRequestHash
	}
	requestHash := hash(requestID)
	topEnd := 1
	for topEnd < len(healthy) && healthy[topEnd].Priority == healthy[0].Priority {
		topEnd++
	}
	picked, ok := stableWeightedIndex(healthy[:topEnd], requestHash)
	if !ok {
		return nil, unavailableUpstream("invalid upstream weight total")
	}
	candidate := healthy[picked]
	permit, ok := p.breakers.TryAcquire(candidate.ID)
	if !ok {
		return nil, unavailableUpstream("selected upstream changed health before dispatch")
	}
	return &APIUpstreamLease{Upstream: clonePickedAPIUpstream(candidate), permit: permit}, nil
}

func validAPIUpstreamCandidates(values []protocol.SyncedAPIUpstream, backendID uint) ([]protocol.SyncedAPIUpstream, error) {
	seen := make(map[uint]struct{}, len(values))
	candidates := make([]protocol.SyncedAPIUpstream, 0, len(values))
	for _, value := range values {
		if value.ID == 0 {
			return nil, fmt.Errorf("zero API upstream id")
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return nil, fmt.Errorf("duplicate API upstream id %d", value.ID)
		}
		seen[value.ID] = struct{}{}
		if value.Status != consts.StatusEnabled {
			continue
		}
		if value.BackendID != backendID {
			return nil, fmt.Errorf("API upstream %d belongs to backend %d", value.ID, value.BackendID)
		}
		if value.Weight <= 0 {
			return nil, fmt.Errorf("API upstream %d has non-positive weight", value.ID)
		}
		if !validGenericAPIBaseURL(value.BaseURL) {
			return nil, fmt.Errorf("API upstream %d has invalid base URL", value.ID)
		}
		candidates = append(candidates, value)
	}
	return candidates, nil
}

func knownAPIProtocol(value apiattempt.APIProtocol) bool {
	return value == apiattempt.APIProtocolHTTP || value == apiattempt.APIProtocolWebSocket
}

func validGenericAPIBaseURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func stableWeightedIndex(values []protocol.SyncedAPIUpstream, hash uint64) (int, bool) {
	var total uint64
	for _, value := range values {
		weight := uint64(value.Weight)
		if weight == 0 || math.MaxUint64-total < weight {
			return 0, false
		}
		total += weight
	}
	if total == 0 {
		return 0, false
	}
	point := hash % total
	for index, value := range values {
		weight := uint64(value.Weight)
		if point < weight {
			return index, true
		}
		point -= weight
	}
	return 0, false
}

func stableRequestHash(requestID string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(requestID))
	return hash.Sum64()
}

func clonePickedAPIUpstream(value protocol.SyncedAPIUpstream) protocol.SyncedAPIUpstream {
	if value.HeaderOverride == nil {
		return value
	}
	cloned := make(map[string]string, len(value.HeaderOverride))
	for key, header := range value.HeaderOverride {
		cloned[key] = header
	}
	value.HeaderOverride = cloned
	return value
}

func unavailableUpstream(detail string) error {
	return fmt.Errorf("%w: %s", ErrExecutionUnavailable, detail)
}
