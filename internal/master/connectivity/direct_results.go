package connectivity

import (
	"reflect"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

func (s *Service) MarkDirectProbeChecking(sourceID string, sourceGeneration uint64, target ProbeTarget, fingerprint string, probeGeneration uint64) {
	if s == nil || sourceID == "" || target.AgentID == "" || fingerprint == "" {
		return
	}
	s.directMu.Lock()
	state, accepted := s.ensureDirectTargetsLocked(sourceID, sourceGeneration)
	if !accepted {
		s.directMu.Unlock()
		return
	}
	targets := state.targets
	snapshot, exists := targets[target.AgentID]
	if exists && probeGeneration < snapshot.ProbeGeneration {
		s.directMu.Unlock()
		return
	}
	if !exists || snapshot.AddressFingerprint != fingerprint {
		previousErrors := snapshot.RecentErrors
		snapshot = directTargetBase(target, fingerprint)
		snapshot.RecentErrors = cloneRecentErrors(previousErrors)
	}
	snapshot.Checking = true
	snapshot.ProbeGeneration = probeGeneration
	if !exists || !reflect.DeepEqual(targets[target.AgentID], snapshot) {
		s.advanceDirectGenerationLocked(state)
	}
	targets[target.AgentID] = snapshot
	s.trimDirectTargetsLocked(targets, target.AgentID)
	s.directMu.Unlock()
}

func (s *Service) ApplyDirectProbeResult(sourceID string, sourceGeneration uint64, target ProbeTarget, result protocol.DirectProbeResult, probeGeneration uint64) {
	if s == nil || sourceID == "" || target.AgentID == "" || result.AddressFingerprint == "" {
		return
	}
	result = normalizeDirectProbeResult(result)
	identity := result.Identity
	snapshot := directTargetBase(target, result.AddressFingerprint)
	snapshot.Network = result.Network
	snapshot.Identity = identity
	snapshot.Eligible = result.Eligible
	snapshot.ProbeGeneration = probeGeneration
	snapshot.CheckedAt = result.CheckedAt
	snapshot.LatencyMS = result.LatencyMS
	s.directMu.Lock()
	state := s.direct[sourceID]
	if state == nil || state.controlGeneration != sourceGeneration {
		s.directMu.Unlock()
		return
	}
	targets := state.targets
	current, exists := targets[target.AgentID]
	if !exists || current.AddressFingerprint != result.AddressFingerprint || current.ProbeGeneration != probeGeneration || !current.Checking {
		s.directMu.Unlock()
		return
	}
	currentReasonCode := ""
	if current.LastError != nil {
		currentReasonCode = current.LastError.Code
	}
	changed := current.Network != result.Network || current.Identity != identity ||
		current.Eligible != result.Eligible || currentReasonCode != result.ReasonCode
	snapshot.RecentErrors = cloneRecentErrors(current.RecentErrors)
	if result.ReasonCode != "" {
		event := RecentError{Code: result.ReasonCode, Stage: "direct_probe", OccurredAt: result.CheckedAt, Count: 1}
		snapshot.RecentErrors = appendRecentError(snapshot.RecentErrors, event)
		snapshot.LastError = &event
	}
	targets[target.AgentID] = snapshot
	s.advanceDirectGenerationLocked(state)
	s.trimDirectTargetsLocked(targets, target.AgentID)
	s.directMu.Unlock()
	if changed {
		s.options.Logger.Info("direct probe state changed",
			zap.String("source", sourceID),
			zap.String("target", target.AgentID),
			zap.String("path_kind", "direct"),
			zap.String("stage", "probe"),
			zap.String("network", result.Network),
			zap.String("identity", identity),
			zap.Bool("eligible", result.Eligible),
			zap.String("reason_code", result.ReasonCode),
		)
	}
}

func normalizeDirectProbeResult(result protocol.DirectProbeResult) protocol.DirectProbeResult {
	valid := true
	switch result.Network {
	case "reachable", "unreachable":
	default:
		result.Network = "unknown"
		valid = false
	}

	identity, identityValid := normalizedDirectIdentity(result.Identity)
	result.Identity = identity
	valid = valid && identityValid && isKnownDirectProbeReasonCode(result.ReasonCode)
	if result.Eligible && (result.Network != "reachable" || identity != "verified" || result.ReasonCode != "") {
		valid = false
	}
	if !valid {
		result.Identity = "invalid_response"
		result.Eligible = false
		result.ReasonCode = "direct_probe_invalid_response"
		return result
	}
	if result.Network != "reachable" || identity != "verified" {
		result.Eligible = false
	}
	return result
}

func normalizedDirectIdentity(identity string) (string, bool) {
	switch identity {
	case "verified", "mismatch", "unknown":
		return identity, true
	case "":
		return "unknown", true
	case "invalid", "unverified":
		return "invalid_response", true
	default:
		return "invalid_response", false
	}
}

func isKnownDirectProbeReasonCode(code string) bool {
	switch code {
	case "",
		"invalid_context",
		"direct_invalid_target",
		"direct_proxy_invalid",
		"direct_invalid_address",
		"direct_dns",
		"direct_connect",
		"direct_tls",
		"http_status",
		"identity_interrupted",
		"identity_too_large",
		"identity_malformed",
		"identity_contract_mismatch",
		"identity_role_mismatch",
		"identity_agent_mismatch",
		"cancelled",
		"request_cancelled",
		"probe_unavailable",
		"probe_call_failed",
		"direct_probe_invalid_response":
		return true
	default:
		return false
	}
}

func publicDirectIdentity(identity string) string {
	result, _ := normalizedDirectIdentity(identity)
	return result
}

func (s *Service) FinishDirectProbeWithoutResult(sourceID string, sourceGeneration uint64, target ProbeTarget, fingerprint string, probeGeneration uint64) {
	if s == nil || sourceID == "" || target.AgentID == "" || fingerprint == "" {
		return
	}
	s.directMu.Lock()
	state := s.direct[sourceID]
	if state == nil || state.controlGeneration != sourceGeneration {
		s.directMu.Unlock()
		return
	}
	targets := state.targets
	snapshot, exists := targets[target.AgentID]
	if !exists || snapshot.AddressFingerprint != fingerprint || snapshot.ProbeGeneration != probeGeneration || !snapshot.Checking {
		s.directMu.Unlock()
		return
	}
	snapshot.Checking = false
	targets[target.AgentID] = snapshot
	s.advanceDirectGenerationLocked(state)
	s.trimDirectTargetsLocked(targets, target.AgentID)
	s.directMu.Unlock()
}

func (s *Service) directSnapshot(sourceID string) DirectSnapshot {
	s.directMu.Lock()
	state := s.direct[sourceID]
	if state == nil || len(state.targets) == 0 {
		s.directMu.Unlock()
		return phaseZeroDirectSnapshot()
	}
	stored := state.targets
	generation := state.generation
	targets := make(map[string]DirectTargetSnapshot, len(stored))
	for targetID, snapshot := range stored {
		snapshot.Addresses = append([]DirectAddressSnapshot(nil), snapshot.Addresses...)
		snapshot.RecentErrors = append([]RecentError(nil), snapshot.RecentErrors...)
		if snapshot.LastError != nil {
			copy := *snapshot.LastError
			snapshot.LastError = &copy
		}
		targets[targetID] = snapshot
	}
	s.directMu.Unlock()
	return DirectSnapshot{Generation: generation, Summary: summarizeDirectTargets(targets), Targets: targets}
}

func (s *Service) currentDirectTarget(sourceID, targetAgentID string) (DirectTargetSnapshot, bool) {
	if s == nil || sourceID == "" || targetAgentID == "" {
		return DirectTargetSnapshot{}, false
	}
	s.directMu.Lock()
	state := s.direct[sourceID]
	if state == nil {
		s.directMu.Unlock()
		return DirectTargetSnapshot{}, false
	}
	snapshot, ok := state.targets[targetAgentID]
	s.directMu.Unlock()
	return snapshot, ok
}

func (s *Service) ensureDirectTargetsLocked(sourceID string, sourceGeneration uint64) (*sourceDirectTargets, bool) {
	state := s.direct[sourceID]
	if state != nil && sourceGeneration < state.controlGeneration {
		return nil, false
	}
	if state == nil || sourceGeneration > state.controlGeneration {
		state = &sourceDirectTargets{
			controlGeneration: sourceGeneration,
			generation:        1,
			targets:           make(map[string]DirectTargetSnapshot),
		}
		s.direct[sourceID] = state
	}
	return state, true
}

func (s *Service) advanceDirectGenerationLocked(state *sourceDirectTargets) {
	state.generation = s.directSequence.Add(1)
}

func (s *Service) trimDirectTargetsLocked(targets map[string]DirectTargetSnapshot, keep string) {
	for len(targets) > maxRouteEdges {
		oldestID := ""
		oldestAt := int64(0)
		for targetID, snapshot := range targets {
			if targetID == keep || oldestID != "" && snapshot.CheckedAt >= oldestAt {
				continue
			}
			oldestID, oldestAt = targetID, snapshot.CheckedAt
		}
		if oldestID == "" {
			return
		}
		delete(targets, oldestID)
	}
}

func directTargetBase(target ProbeTarget, fingerprint string) DirectTargetSnapshot {
	addresses := make([]DirectAddressSnapshot, 0, len(target.Addresses))
	for _, address := range target.Addresses {
		addresses = append(addresses, DirectAddressSnapshot{URL: address.URL, Tag: address.Tag})
	}
	sort.Slice(addresses, func(i, j int) bool {
		if addresses[i].URL != addresses[j].URL {
			return addresses[i].URL < addresses[j].URL
		}
		return addresses[i].Tag < addresses[j].Tag
	})
	return DirectTargetSnapshot{
		TargetAgentID: target.AgentID, TargetName: target.Name, Addresses: addresses,
		Network: "unknown", Identity: "unknown", AddressFingerprint: fingerprint,
		RecentErrors: make([]RecentError, 0),
	}
}

func summarizeDirectTargets(targets map[string]DirectTargetSnapshot) DirectSummary {
	summary := DirectSummary{State: directStateUnknown, Total: len(targets)}
	for _, target := range targets {
		switch {
		case target.Eligible:
			summary.Reachable++
		case target.Network == "unreachable":
			summary.Unreachable++
		case target.Network == "reachable":
			summary.Degraded++
		default:
			summary.Stale++
		}
	}
	if summary.Total == 0 {
		return summary
	}
	if summary.Unreachable > 0 || summary.Degraded > 0 {
		summary.State = "degraded"
	} else if summary.Reachable == summary.Total {
		summary.State = "reachable"
	} else {
		summary.State = "stale"
	}
	return summary
}
