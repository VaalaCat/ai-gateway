package connectivity

import (
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

func (s *Service) MarkRelayProbeChecking(
	sourceID string,
	sourceControlGeneration uint64,
	target ProbeTarget,
	fingerprint string,
	probeGeneration uint64,
	sourceRelayGeneration uint64,
	targetRelayGeneration uint64,
) {
	if s == nil || sourceID == "" || sourceControlGeneration == 0 || target.AgentID == "" ||
		fingerprint == "" || probeGeneration == 0 || sourceRelayGeneration == 0 || targetRelayGeneration == 0 {
		return
	}
	s.relayProbeMu.Lock()
	state, ok := s.ensureRelayTargetsLocked(sourceID, sourceControlGeneration)
	if !ok {
		s.relayProbeMu.Unlock()
		return
	}
	current := state.targets[target.AgentID]
	snapshot := relayTargetBase(target, fingerprint, sourceRelayGeneration, targetRelayGeneration)
	if current.TargetAgentID != "" {
		snapshot.State = current.State
		snapshot.Stage = current.Stage
		snapshot.CheckedAt = current.CheckedAt
		snapshot.LatencyMS = current.LatencyMS
		snapshot.LastError = cloneRecentError(current.LastError)
	}
	snapshot.Checking = true
	snapshot.ProbeGeneration = probeGeneration
	state.targets[target.AgentID] = snapshot
	s.advanceRelayGenerationLocked(state)
	s.trimRelayTargetsLocked(state.targets, target.AgentID)
	s.relayProbeMu.Unlock()
}

func (s *Service) ApplyRelayProbeResult(
	sourceID string,
	sourceControlGeneration uint64,
	target ProbeTarget,
	fingerprint string,
	probeGeneration uint64,
	sourceRelayGeneration uint64,
	targetRelayGeneration uint64,
	result protocol.RelayProbeResult,
) {
	if s == nil || sourceID == "" || target.AgentID == "" || fingerprint == "" {
		return
	}
	result = normalizeRelayProbeResult(result, target.AgentID)
	if result.State == protocol.RelayProbeUnknown {
		s.FinishRelayProbeWithoutResult(sourceID, sourceControlGeneration, target, fingerprint, probeGeneration)
		return
	}
	if !s.relayGenerationsMatch(sourceID, target.AgentID, sourceRelayGeneration, targetRelayGeneration) {
		s.FinishRelayProbeWithoutResult(sourceID, sourceControlGeneration, target, fingerprint, probeGeneration)
		return
	}

	s.relayProbeMu.Lock()
	state := s.relayProbes[sourceID]
	if state == nil || state.controlGeneration != sourceControlGeneration {
		s.relayProbeMu.Unlock()
		return
	}
	current, exists := state.targets[target.AgentID]
	if !exists || current.RelayFingerprint != fingerprint || current.ProbeGeneration != probeGeneration ||
		current.SourceRelayGeneration != sourceRelayGeneration || current.TargetRelayGeneration != targetRelayGeneration || !current.Checking {
		s.relayProbeMu.Unlock()
		return
	}
	changed := current.State != result.State || current.Stage != result.Stage ||
		relayReasonCode(current.LastError) != result.ReasonCode
	snapshot := relayTargetBase(target, fingerprint, sourceRelayGeneration, targetRelayGeneration)
	snapshot.State = result.State
	snapshot.Stage = result.Stage
	snapshot.ProbeGeneration = probeGeneration
	snapshot.CheckedAt = result.CheckedAt
	snapshot.LatencyMS = result.LatencyMS
	if result.ReasonCode != "" {
		snapshot.LastError = &RecentError{
			Code: result.ReasonCode, Stage: string(result.Stage), OccurredAt: result.CheckedAt, Count: 1,
		}
	}
	state.targets[target.AgentID] = snapshot
	s.advanceRelayGenerationLocked(state)
	s.trimRelayTargetsLocked(state.targets, target.AgentID)
	s.relayProbeMu.Unlock()

	if changed {
		s.options.Logger.Info("relay probe state changed",
			zap.String("source", sourceID),
			zap.String("target", target.AgentID),
			zap.String("path_kind", "relay"),
			zap.String("stage", string(result.Stage)),
			zap.String("state", string(result.State)),
			zap.String("reason_code", result.ReasonCode),
		)
	}
}

func (s *Service) FinishRelayProbeWithoutResult(
	sourceID string,
	sourceControlGeneration uint64,
	target ProbeTarget,
	fingerprint string,
	probeGeneration uint64,
) {
	if s == nil || sourceID == "" || target.AgentID == "" || fingerprint == "" {
		return
	}
	s.relayProbeMu.Lock()
	state := s.relayProbes[sourceID]
	if state == nil || state.controlGeneration != sourceControlGeneration {
		s.relayProbeMu.Unlock()
		return
	}
	snapshot, exists := state.targets[target.AgentID]
	if !exists || snapshot.RelayFingerprint != fingerprint || snapshot.ProbeGeneration != probeGeneration || !snapshot.Checking {
		s.relayProbeMu.Unlock()
		return
	}
	snapshot.Checking = false
	state.targets[target.AgentID] = snapshot
	s.advanceRelayGenerationLocked(state)
	s.trimRelayTargetsLocked(state.targets, target.AgentID)
	s.relayProbeMu.Unlock()
}

func (s *Service) relayPathSnapshot(sourceID string) RelayPathSnapshot {
	if s == nil || sourceID == "" {
		return phaseZeroRelayPathSnapshot()
	}
	s.relayProbeMu.Lock()
	state := s.relayProbes[sourceID]
	if state == nil || len(state.targets) == 0 {
		s.relayProbeMu.Unlock()
		return phaseZeroRelayPathSnapshot()
	}
	targets := make(map[string]RelayTargetSnapshot, len(state.targets))
	for targetID, snapshot := range state.targets {
		snapshot.LastError = cloneRecentError(snapshot.LastError)
		targets[targetID] = snapshot
	}
	generation := state.generation
	s.relayProbeMu.Unlock()
	return RelayPathSnapshot{Generation: generation, Summary: summarizeRelayTargets(targets), Targets: targets}
}

func (s *Service) currentRelayTarget(sourceID, targetAgentID string) (RelayTargetSnapshot, bool) {
	if s == nil || sourceID == "" || targetAgentID == "" {
		return RelayTargetSnapshot{}, false
	}
	s.relayProbeMu.Lock()
	state := s.relayProbes[sourceID]
	if state == nil {
		s.relayProbeMu.Unlock()
		return RelayTargetSnapshot{}, false
	}
	snapshot, ok := state.targets[targetAgentID]
	snapshot.LastError = cloneRecentError(snapshot.LastError)
	s.relayProbeMu.Unlock()
	return snapshot, ok
}

func (s *Service) relayGenerationsMatch(sourceID, targetID string, sourceGeneration, targetGeneration uint64) bool {
	if sourceGeneration == 0 || targetGeneration == 0 {
		return false
	}
	source := s.relayFact(sourceID)
	target := s.relayFact(targetID)
	return source.Active.SessionGeneration == sourceGeneration &&
		target.Active.SessionGeneration == targetGeneration
}

func (s *Service) ensureRelayTargetsLocked(sourceID string, controlGeneration uint64) (*sourceRelayTargets, bool) {
	state := s.relayProbes[sourceID]
	if state != nil && controlGeneration < state.controlGeneration {
		return nil, false
	}
	if state == nil || controlGeneration > state.controlGeneration {
		state = &sourceRelayTargets{
			controlGeneration: controlGeneration,
			generation:        1,
			targets:           make(map[string]RelayTargetSnapshot),
		}
		s.relayProbes[sourceID] = state
	}
	return state, true
}

func (s *Service) advanceRelayGenerationLocked(state *sourceRelayTargets) {
	state.generation = s.relaySequence.Add(1)
}

func (s *Service) trimRelayTargetsLocked(targets map[string]RelayTargetSnapshot, keep string) {
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

func relayTargetBase(target ProbeTarget, fingerprint string, sourceGeneration, targetGeneration uint64) RelayTargetSnapshot {
	return RelayTargetSnapshot{
		TargetAgentID:         target.AgentID,
		TargetName:            target.Name,
		State:                 protocol.RelayProbeUnknown,
		RelayFingerprint:      fingerprint,
		SourceRelayGeneration: sourceGeneration,
		TargetRelayGeneration: targetGeneration,
	}
}

func normalizeRelayProbeResult(result protocol.RelayProbeResult, expectedTargetAgentID string) protocol.RelayProbeResult {
	validState := result.State == protocol.RelayProbeReachable ||
		result.State == protocol.RelayProbeUnreachable ||
		result.State == protocol.RelayProbeUnavailable ||
		result.State == protocol.RelayProbeUnknown
	validStage := result.Stage == "" || result.Stage == protocol.RelayProbeStageOpen ||
		result.Stage == protocol.RelayProbeStageCommit || result.Stage == protocol.RelayProbeStageResponse
	validReason := result.ReasonCode == "" || consts.IsConnectivityProbeErrorCode(result.ReasonCode)
	validTarget := expectedTargetAgentID != "" && result.TargetAgentID == expectedTargetAgentID
	if !validTarget || !validState || !validStage || !validReason || result.State == protocol.RelayProbeReachable && result.ReasonCode != "" {
		result.State = protocol.RelayProbeUnreachable
		result.Stage = protocol.RelayProbeStageResponse
		result.ReasonCode = consts.RouteErrorRelayProbeInvalidResult
	}
	return result
}

func relayReasonCode(lastError *RecentError) string {
	if lastError == nil {
		return ""
	}
	return lastError.Code
}

func cloneRecentError(lastError *RecentError) *RecentError {
	if lastError == nil {
		return nil
	}
	copy := *lastError
	return &copy
}

func summarizeRelayTargets(targets map[string]RelayTargetSnapshot) RelayPathSummary {
	summary := RelayPathSummary{State: string(protocol.RelayProbeUnknown), Total: len(targets)}
	for _, target := range targets {
		switch target.State {
		case protocol.RelayProbeReachable:
			summary.Reachable++
		case protocol.RelayProbeUnreachable:
			summary.Unreachable++
		case protocol.RelayProbeUnavailable:
			summary.Unavailable++
		default:
			summary.Unknown++
		}
	}
	if summary.Total == 0 {
		return summary
	}
	switch {
	case summary.Unreachable > 0 || summary.Unavailable > 0:
		summary.State = "degraded"
	case summary.Reachable == summary.Total:
		summary.State = string(protocol.RelayProbeReachable)
	default:
		summary.State = string(protocol.RelayProbeUnknown)
	}
	return summary
}

func phaseZeroRelayPathSnapshot() RelayPathSnapshot {
	return RelayPathSnapshot{
		Generation: 1,
		Summary:    RelayPathSummary{State: string(protocol.RelayProbeUnknown)},
		Targets:    make(map[string]RelayTargetSnapshot),
	}
}

func sortedRelayTargetIDs(targets map[string]RelayTargetSnapshot) []string {
	result := make([]string, 0, len(targets))
	for targetID := range targets {
		result = append(result, targetID)
	}
	sort.Strings(result)
	return result
}
