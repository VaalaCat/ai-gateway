package connectivity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	routeTargetStateDisabled    = "disabled"
	routeTargetStateChecking    = "checking"
	routeTargetStateReachable   = "reachable"
	routeTargetStateDegraded    = "degraded"
	routeTargetStateUnreachable = "unreachable"
	routeTargetStateUnknown     = "unknown"
	routeTargetStateUnsupported = "unsupported"
	routeTargetStateUnavailable = "unavailable"
	routeTargetStateStale       = "stale"
	maxJavaScriptSafeInteger    = uint64(1<<53 - 1)
)

func (s *Service) routeTargets(source models.Agent) RouteTargetsSnapshot {
	result := RouteTargetsSnapshot{
		Targets: make(map[string]RouteTargetSnapshot),
		Summaries: RouteTargetSummaries{
			Direct: DirectSummary{State: routeTargetStateUnknown},
			Relay:  RelayPathSummary{State: routeTargetStateUnknown},
		},
	}
	if s == nil || source.AgentID == "" {
		result.Generation = 1
		return result
	}
	direct := s.directSnapshot(source.AgentID)
	relay := s.relayPathSnapshot(source.AgentID)
	targetIDs := make(map[string]struct{})
	addTargetID := func(targetID string) {
		if targetID == "" || targetID == source.AgentID {
			return
		}
		targetIDs[targetID] = struct{}{}
	}
	for _, edge := range s.RouteEdges(source.AgentID) {
		addTargetID(edge.TargetAgentID)
	}
	// behavior change: retained manual probe results remain visible even before a business route creates an edge.
	for targetID := range direct.Targets {
		addTargetID(targetID)
	}
	for targetID := range relay.Targets {
		addTargetID(targetID)
	}
	for targetID := range targetIDs {
		directTarget := direct.Targets[targetID]
		relayTarget := relay.Targets[targetID]
		name := directTarget.TargetName
		if name == "" {
			name = relayTarget.TargetName
		}
		if name == "" {
			name = targetID
		}
		relayTarget.TargetAgentID = targetID
		relayTarget.TargetName = name
		relayTarget.State = s.publicRelayTargetState(source.AgentID, targetID, relayTarget)
		result.Targets[targetID] = RouteTargetSnapshot{
			TargetAgentID: targetID,
			TargetName:    name,
			Direct: publicDirectTarget(
				source.PeerRouteMode,
				s.agentSupports(targetID, protocol.AgentCapabilityDirectIngressV1),
				directTarget,
			),
			Relay: relayTarget,
		}
	}
	result.Summaries = summarizeRouteTargets(result.Targets)
	result.Generation = routeTargetsGeneration(result)
	return result
}

func publicDirectTarget(peerRouteMode string, targetSupportsDirectIngress bool, target DirectTargetSnapshot) RouteDirectTargetSnapshot {
	state := routeTargetStateUnknown
	switch {
	case peerRouteMode == consts.PeerRouteModeRelayOnly:
		state = routeTargetStateDisabled
	case !targetSupportsDirectIngress:
		state = routeTargetStateUnsupported
	case target.Checking:
		state = routeTargetStateChecking
	case target.Eligible:
		state = routeTargetStateReachable
	case target.Network == "reachable":
		state = routeTargetStateDegraded
	case target.Network == "unreachable" && target.CheckedAt > 0:
		state = routeTargetStateUnreachable
	}
	return RouteDirectTargetSnapshot{
		State: state, Addresses: append([]DirectAddressSnapshot(nil), target.Addresses...),
		Network: target.Network, Identity: target.Identity, Eligible: target.Eligible,
		Checking: target.Checking, ProbeGeneration: target.ProbeGeneration,
		AddressFingerprint: target.AddressFingerprint,
		CheckedAt:          target.CheckedAt, LatencyMS: target.LatencyMS, LastError: cloneRecentError(target.LastError),
	}
}

func (s *Service) publicRelayTargetState(sourceID, targetID string, target RelayTargetSnapshot) protocol.RelayProbeState {
	if _, connected := s.controlFact(sourceID); !connected {
		return protocol.RelayProbeState(routeTargetStateUnknown)
	}
	if !s.agentSupports(sourceID, protocol.AgentCapabilityRelayHTTPPingV1) ||
		!s.agentSupports(targetID, protocol.AgentCapabilityRelayHTTPPingV1) {
		return protocol.RelayProbeState(routeTargetStateUnsupported)
	}
	sourceRelay := s.relayFact(sourceID)
	targetRelay := s.relayFact(targetID)
	if sourceRelay.Availability != "available" || !sourceRelay.AcceptingNewStreams ||
		targetRelay.Availability != "available" || !targetRelay.AcceptingNewStreams {
		return protocol.RelayProbeState(routeTargetStateUnavailable)
	}
	if target.TargetAgentID == "" || target.CheckedAt == 0 {
		if target.Checking {
			return protocol.RelayProbeState(routeTargetStateChecking)
		}
		return protocol.RelayProbeState(routeTargetStateUnknown)
	}
	if target.SourceRelayGeneration != sourceRelay.Active.SessionGeneration ||
		target.TargetRelayGeneration != targetRelay.Active.SessionGeneration {
		return protocol.RelayProbeState(routeTargetStateStale)
	}
	return target.State
}

func (s *Service) agentSupports(agentID, capability string) bool {
	source, ok := s.sources.Control.(interface{ Capabilities(string) []string })
	if !ok {
		return false
	}
	for _, current := range source.Capabilities(agentID) {
		if current == capability {
			return true
		}
	}
	return false
}

func summarizeRouteTargets(targets map[string]RouteTargetSnapshot) RouteTargetSummaries {
	summaries := RouteTargetSummaries{
		Direct: DirectSummary{State: routeTargetStateUnknown, Total: len(targets)},
		Relay:  RelayPathSummary{State: routeTargetStateUnknown, Total: len(targets)},
	}
	for _, target := range targets {
		switch target.Direct.State {
		case routeTargetStateReachable:
			summaries.Direct.Reachable++
		case routeTargetStateDegraded:
			summaries.Direct.Degraded++
		case routeTargetStateUnreachable:
			summaries.Direct.Unreachable++
		default:
			summaries.Direct.Stale++
		}
		switch string(target.Relay.State) {
		case routeTargetStateReachable:
			summaries.Relay.Reachable++
		case routeTargetStateUnreachable:
			summaries.Relay.Unreachable++
		case routeTargetStateUnavailable:
			summaries.Relay.Unavailable++
		case routeTargetStateUnsupported:
			summaries.Relay.Unsupported++
		case routeTargetStateStale:
			summaries.Relay.Stale++
		default:
			summaries.Relay.Unknown++
		}
	}
	if len(targets) == 0 {
		return summaries
	}
	summaries.Direct.State = summarizeDirectState(summaries.Direct)
	summaries.Relay.State = summarizeRelayState(summaries.Relay)
	return summaries
}

func summarizeDirectState(summary DirectSummary) string {
	switch {
	case summary.Unreachable > 0 || summary.Degraded > 0:
		return routeTargetStateDegraded
	case summary.Reachable == summary.Total:
		return routeTargetStateReachable
	default:
		return routeTargetStateUnknown
	}
}

func summarizeRelayState(summary RelayPathSummary) string {
	switch {
	case summary.Unreachable > 0 || summary.Unavailable > 0:
		return routeTargetStateDegraded
	case summary.Reachable == summary.Total:
		return routeTargetStateReachable
	default:
		return routeTargetStateUnknown
	}
}

func routeTargetsGeneration(snapshot RouteTargetsSnapshot) uint64 {
	targets := make([]RouteTargetSnapshot, 0, len(snapshot.Targets))
	for _, target := range snapshot.Targets {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].TargetAgentID < targets[j].TargetAgentID })
	raw, _ := json.Marshal(struct {
		Summaries RouteTargetSummaries
		Targets   []RouteTargetSnapshot
	}{Summaries: snapshot.Summaries, Targets: targets})
	digest := sha256.Sum256(raw)
	generation := binary.BigEndian.Uint64(digest[:8]) & maxJavaScriptSafeInteger
	if generation == 0 {
		return 1
	}
	return generation
}
