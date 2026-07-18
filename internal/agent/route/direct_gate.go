package route

import (
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	DirectGateUnreachable = agentproxy.CodeDirectConnect
	DirectGateIdentity    = agentproxy.CodeDirectIdentityMismatch

	defaultDirectGateFreshFor   = 5 * time.Minute
	defaultDirectGateMaxEntries = 4096
)

type DirectGateOptions struct {
	FreshFor   time.Duration
	MaxEntries int
	Now        func() time.Time
}

type directGateKey struct {
	targetAgentID      string
	addressFingerprint string
}

type directGateEntry struct {
	result    protocol.DirectProbeResult
	hasResult bool
	checking  bool
	updatedAt time.Time
}

type DirectGate struct {
	mu         sync.Mutex
	entries    map[directGateKey]directGateEntry
	aliases    map[directGateKey]directGateKey
	freshFor   time.Duration
	maxEntries int
	now        func() time.Time
}

func NewDirectGate(opts DirectGateOptions) *DirectGate {
	freshFor := opts.FreshFor
	if freshFor <= 0 {
		freshFor = defaultDirectGateFreshFor
	}
	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultDirectGateMaxEntries
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &DirectGate{
		entries: make(map[directGateKey]directGateEntry), freshFor: freshFor,
		aliases: make(map[directGateKey]directGateKey), maxEntries: maxEntries, now: now,
	}
}

func (g *DirectGate) Decision(targetAgentID, addressFingerprint string) string {
	if g == nil || targetAgentID == "" || addressFingerprint == "" {
		return ""
	}
	key := directGateKey{targetAgentID: targetAgentID, addressFingerprint: addressFingerprint}
	g.mu.Lock()
	if probeKey, exists := g.aliases[key]; exists {
		key = probeKey
	}
	entry, ok := g.entries[key]
	g.mu.Unlock()
	if !ok || !entry.hasResult || !g.fresh(entry.result.CheckedAt) {
		return ""
	}
	if entry.result.Network == "unreachable" && isDirectNetworkFailure(entry.result.ReasonCode) {
		return entry.result.ReasonCode
	}
	if entry.result.Identity == "mismatch" {
		return DirectGateIdentity
	}
	if entry.result.Identity == "invalid" || entry.result.Identity == "malformed" {
		return agentproxy.CodeDirectProbeInvalidResponse
	}
	return ""
}

func isDirectNetworkFailure(reason string) bool {
	return reason == "direct_dns" || reason == "direct_connect" || reason == "direct_tls"
}

func (g *DirectGate) BindProbeTarget(target protocol.DirectProbeTarget) {
	if g == nil || target.TargetAgentID == "" || target.AddressFingerprint == "" || len(target.Addresses) == 0 {
		return
	}
	routeFingerprint := agentproxy.CanonicalAddressFingerprint(target.Addresses)
	if routeFingerprint == "" {
		return
	}
	routeKey := directGateKey{targetAgentID: target.TargetAgentID, addressFingerprint: routeFingerprint}
	probeKey := directGateKey{targetAgentID: target.TargetAgentID, addressFingerprint: target.AddressFingerprint}
	g.mu.Lock()
	g.aliases[routeKey] = probeKey
	for len(g.aliases) > g.maxEntries {
		for key := range g.aliases {
			if key != routeKey {
				delete(g.aliases, key)
				break
			}
		}
	}
	g.mu.Unlock()
}

func (g *DirectGate) MarkChecking(targetAgentID, addressFingerprint string) {
	if g == nil || targetAgentID == "" || addressFingerprint == "" {
		return
	}
	now := g.now()
	key := directGateKey{targetAgentID: targetAgentID, addressFingerprint: addressFingerprint}
	g.mu.Lock()
	entry := g.entries[key]
	entry.checking = true
	entry.updatedAt = now
	g.entries[key] = entry
	g.evictLocked(key)
	g.mu.Unlock()
}

func (g *DirectGate) ApplyProbeResult(result protocol.DirectProbeResult) {
	if g == nil || result.TargetAgentID == "" || result.AddressFingerprint == "" {
		return
	}
	now := g.now()
	key := directGateKey{targetAgentID: result.TargetAgentID, addressFingerprint: result.AddressFingerprint}
	g.mu.Lock()
	if result.ReasonCode == "cancelled" {
		entry := g.entries[key]
		entry.checking = false
		entry.updatedAt = now
		g.entries[key] = entry
		g.evictLocked(key)
		g.mu.Unlock()
		return
	}
	g.entries[key] = directGateEntry{result: result, hasResult: true, updatedAt: now}
	g.evictLocked(key)
	g.mu.Unlock()
}

func (g *DirectGate) EntryCount() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	count := len(g.entries)
	g.mu.Unlock()
	return count
}

func (g *DirectGate) fresh(checkedAt int64) bool {
	if checkedAt <= 0 {
		return false
	}
	age := g.now().Sub(time.Unix(checkedAt, 0))
	return age >= 0 && age <= g.freshFor
}

func (g *DirectGate) evictLocked(keep directGateKey) {
	for len(g.entries) > g.maxEntries {
		var oldestKey directGateKey
		var oldestTime time.Time
		found := false
		for key, entry := range g.entries {
			if key == keep || found && !entry.updatedAt.Before(oldestTime) {
				continue
			}
			oldestKey, oldestTime, found = key, entry.updatedAt, true
		}
		if !found {
			return
		}
		delete(g.entries, oldestKey)
		for routeKey, probeKey := range g.aliases {
			if probeKey == oldestKey {
				delete(g.aliases, routeKey)
			}
		}
	}
}
