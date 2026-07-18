package connectivity

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

const (
	snapshotVersion = "v1"

	controlStateConnected    = "connected"
	controlStateDisconnected = "disconnected"
	controlHealthUnknown     = "unknown"
	controlHealthHealthy     = "healthy"
	controlHealthDegraded    = "degraded"

	relaySupportUnsupported      = "unsupported"
	relaySupportSupported        = "supported"
	relayConfigNotConfigured     = "not_configured"
	relayConfigDisabled          = "disabled"
	relayConfigConfigured        = "configured"
	relayAvailabilityReady       = "ready"
	relayAvailabilityDraining    = "draining"
	relayAvailabilityUnavailable = "unavailable"
	relayConvergenceConverged    = "converged"
	relayConvergenceApplying     = "applying"
	relayConvergenceDegraded     = "degraded"
	directStateUnknown           = "unknown"
)

const (
	defaultHeartbeatDegradedAfter = 90 * time.Second
	defaultRecoverySamples        = 2
	minimumHeartbeatDegradedAfter = 10 * time.Second
	maximumHeartbeatDegradedAfter = 3600 * time.Second
	minimumRecoverySamples        = 1
	maximumRecoverySamples        = 10
)

var fixedOperationOrder = [...]Operation{
	OperationFullSync,
	OperationProbe,
	OperationRelayReconnect,
	OperationRelayDrain,
	OperationRelayDisconnect,
	OperationDirectCircuitReset,
	OperationInterrupt,
}

type snapshotIdentity struct {
	epoch      string
	sequence   uint64
	observedAt int64
}

type healthHysteresis struct {
	generation         uint64
	degraded           bool
	heartbeatHighWater int64
	recoveryCount      int
}

type sourceDirectTargets struct {
	controlGeneration uint64
	generation        uint64
	targets           map[string]DirectTargetSnapshot
}

type sourceRelayTargets struct {
	controlGeneration uint64
	generation        uint64
	targets           map[string]RelayTargetSnapshot
}

type Service struct {
	instanceID string
	sources    Sources
	options    Options

	sequence       atomic.Uint64
	directSequence atomic.Uint64
	relaySequence  atomic.Uint64
	healthMu       sync.Mutex
	health         map[string]*healthHysteresis
	edgesMu        sync.Mutex
	edges          map[string]*sourceRouteEdges
	directMu       sync.Mutex
	direct         map[string]*sourceDirectTargets
	relayProbeMu   sync.Mutex
	relayProbes    map[string]*sourceRelayTargets
}

func NewService(instanceID string, sources Sources, opts Options) *Service {
	if opts.HeartbeatDegradedAfter < minimumHeartbeatDegradedAfter || opts.HeartbeatDegradedAfter > maximumHeartbeatDegradedAfter {
		opts.HeartbeatDegradedAfter = defaultHeartbeatDegradedAfter
	}
	if opts.RecoverySamples < minimumRecoverySamples || opts.RecoverySamples > maximumRecoverySamples {
		opts.RecoverySamples = defaultRecoverySamples
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	service := &Service{
		instanceID:  instanceID,
		sources:     sources,
		options:     opts,
		health:      make(map[string]*healthHysteresis),
		edges:       make(map[string]*sourceRouteEdges),
		direct:      make(map[string]*sourceDirectTargets),
		relayProbes: make(map[string]*sourceRelayTargets),
	}
	service.directSequence.Store(1)
	service.relaySequence.Store(1)
	return service
}

func (s *Service) Build(agent models.Agent) ConnectionSnapshot {
	identity := s.nextIdentity()
	return s.build(agent, identity)
}

func (s *Service) BuildMany(agents []models.Agent) SnapshotBatch {
	identity := s.nextIdentity()
	items := make(map[string]ConnectionSnapshot, len(agents))
	for _, agent := range agents {
		items[agent.AgentID] = s.build(agent, identity)
	}
	return SnapshotBatch{
		SnapshotEpoch: identity.epoch,
		SnapshotSeq:   identity.sequence,
		ObservedAt:    identity.observedAt,
		Items:         items,
	}
}

func (s *Service) Authorize(agent models.Agent, op Operation) (OperationLease, error) {
	if !isKnownOperation(op) {
		return OperationLease{}, fmt.Errorf("unknown operation %q", op)
	}
	control, hasControl := s.controlFact(agent.AgentID)
	relay := s.relayFact(agent.AgentID)
	return s.authorize(agent, op, control, hasControl, relay)
}

func (s *Service) Forget(agentID string, generation uint64) {
	s.healthMu.Lock()
	if state := s.health[agentID]; state != nil && state.generation <= generation {
		delete(s.health, agentID)
	}
	s.healthMu.Unlock()
	s.edgesMu.Lock()
	if state := s.edges[agentID]; state != nil && state.sessionBound && state.controlGeneration <= generation {
		state.sessionBound = false
	}
	s.edgesMu.Unlock()
	s.directMu.Lock()
	if state := s.direct[agentID]; state != nil && state.controlGeneration <= generation {
		delete(s.direct, agentID)
	}
	s.directMu.Unlock()
	s.relayProbeMu.Lock()
	if state := s.relayProbes[agentID]; state != nil && state.controlGeneration <= generation {
		delete(s.relayProbes, agentID)
	}
	s.relayProbeMu.Unlock()
}

func (s *Service) nextIdentity() snapshotIdentity {
	return snapshotIdentity{
		epoch:      s.instanceID,
		sequence:   s.sequence.Add(1),
		observedAt: s.options.Now().Unix(),
	}
}

func (s *Service) build(agent models.Agent, identity snapshotIdentity) ConnectionSnapshot {
	control, hasControl := s.controlFact(agent.AgentID)
	relay := s.relayFact(agent.AgentID)
	routeTargets := s.routeTargets(agent)
	return ConnectionSnapshot{
		Version:           snapshotVersion,
		SnapshotEpoch:     identity.epoch,
		SnapshotSeq:       identity.sequence,
		ObservedAt:        identity.observedAt,
		AgentID:           agent.AgentID,
		AdminStatus:       agent.Status,
		Control:           s.controlSnapshot(agent, control, hasControl, identity.observedAt),
		Relay:             relaySnapshot(relay),
		Direct:            s.directSnapshot(agent.AgentID),
		RelayPaths:        s.relayPathSnapshot(agent.AgentID),
		TargetSummaries:   routeTargets.Summaries,
		RouteTargets:      routeTargets,
		AllowedOperations: s.allowedOperations(agent, control, hasControl, relay),
	}
}

func (s *Service) controlFact(agentID string) (ControlSessionFact, bool) {
	if s.sources.Control == nil {
		return ControlSessionFact{}, false
	}
	fact, ok := s.sources.Control.GetControlSession(agentID)
	if !ok || fact.Generation == 0 {
		return ControlSessionFact{}, false
	}
	return fact, true
}

func (s *Service) relayFact(agentID string) RelayRuntimeFact {
	if s.sources.Relay != nil {
		if fact, ok := s.sources.Relay.GetRelayRuntime(agentID); ok {
			fact.RecentErrors = append([]RecentError{}, fact.RecentErrors...)
			return fact
		}
	}
	return RelayRuntimeFact{
		Support:      relaySupportUnsupported,
		Config:       relayConfigNotConfigured,
		Availability: relayAvailabilityUnavailable,
		Convergence:  relayConvergenceConverged,
		RecentErrors: make([]RecentError, 0),
	}
}

func (s *Service) controlSnapshot(agent models.Agent, fact ControlSessionFact, hasSession bool, observedAt int64) ControlSnapshot {
	health, baseReasons := s.controlHealth(agent.AgentID, fact, hasSession, observedAt)
	reasons := baseReasons
	if s.sources.Health != nil {
		reasons = appendUniqueReasons(reasons, s.sources.Health.ReasonCodes(agent.AgentID))
	}
	if !hasSession {
		return ControlSnapshot{
			State:       controlStateDisconnected,
			Health:      health,
			ReasonCodes: reasons,
			LastSeen:    agent.LastSeen,
		}
	}
	return ControlSnapshot{
		State:             controlStateConnected,
		Health:            health,
		ReasonCodes:       reasons,
		SessionGeneration: fact.Generation,
		ConnectedAt:       fact.ConnectedAt,
		HeartbeatAt:       fact.HeartbeatAt,
		RuntimeReportedAt: fact.RuntimeReportedAt,
		LastSeen:          agent.LastSeen,
	}
}

func (s *Service) controlHealth(agentID string, fact ControlSessionFact, hasSession bool, observedAt int64) (string, []string) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()

	if !hasSession {
		delete(s.health, agentID)
		return controlHealthUnknown, []string{ControlReasonHeartbeatMissing}
	}

	state, ok := s.health[agentID]
	if ok && state.generation != fact.Generation {
		delete(s.health, agentID)
		state = nil
		ok = false
	}

	if fact.HeartbeatAt == 0 {
		if fact.ConnectedAt != 0 && ageAtLeast(observedAt, fact.ConnectedAt, s.options.HeartbeatDegradedAfter) {
			state = s.ensureHealthStateLocked(agentID, fact.Generation)
			state.degraded = true
			state.recoveryCount = 0
			return controlHealthDegraded, []string{ControlReasonHeartbeatMissing}
		}
		return controlHealthUnknown, []string{ControlReasonHeartbeatMissing}
	}

	if ageAtLeast(observedAt, fact.HeartbeatAt, s.options.HeartbeatDegradedAfter) {
		state = s.ensureHealthStateLocked(agentID, fact.Generation)
		state.degraded = true
		if fact.HeartbeatAt > state.heartbeatHighWater {
			state.heartbeatHighWater = fact.HeartbeatAt
		}
		state.recoveryCount = 0
		return controlHealthDegraded, []string{ControlReasonHeartbeatStale}
	}
	if !ok || !state.degraded {
		delete(s.health, agentID)
		return controlHealthHealthy, make([]string, 0)
	}

	if fact.HeartbeatAt > state.heartbeatHighWater {
		state.heartbeatHighWater = fact.HeartbeatAt
		state.recoveryCount++
	}
	if state.recoveryCount < s.options.RecoverySamples {
		return controlHealthDegraded, []string{ControlReasonHeartbeatRecovering}
	}
	delete(s.health, agentID)
	return controlHealthHealthy, make([]string, 0)
}

func (s *Service) ensureHealthStateLocked(agentID string, generation uint64) *healthHysteresis {
	if state, ok := s.health[agentID]; ok && state.generation == generation {
		return state
	}
	delete(s.health, agentID)
	state := &healthHysteresis{generation: generation}
	s.health[agentID] = state
	return state
}

func ageAtLeast(observedAt, eventAt int64, threshold time.Duration) bool {
	return time.Unix(observedAt, 0).Sub(time.Unix(eventAt, 0)) >= threshold
}

func appendUniqueReasons(base, extra []string) []string {
	result := append([]string{}, base...)
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, reason := range result {
		seen[reason] = struct{}{}
	}
	for _, reason := range extra {
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
	}
	return result
}

func relaySnapshot(fact RelayRuntimeFact) RelaySnapshot {
	return RelaySnapshot{
		Support:             fact.Support,
		Config:              fact.Config,
		Availability:        publicRelayAvailability(fact.Availability, fact.AcceptingNewStreams),
		AcceptingNewStreams: fact.AcceptingNewStreams,
		Convergence:         publicRelayConvergence(fact.Convergence),
		Desired:             fact.Desired,
		Active:              fact.Active,
		RecentErrors:        append([]RecentError{}, fact.RecentErrors...),
	}
}

func publicRelayAvailability(availability string, accepting bool) string {
	if availability != "available" {
		return relayAvailabilityUnavailable
	}
	if accepting {
		return relayAvailabilityReady
	}
	return relayAvailabilityDraining
}

func publicRelayConvergence(convergence string) string {
	switch convergence {
	case relayConvergenceConverged:
		return relayConvergenceConverged
	case "converging":
		return relayConvergenceApplying
	case relayConvergenceDegraded:
		return relayConvergenceDegraded
	default:
		return relayConvergenceDegraded
	}
}

func phaseZeroDirectSnapshot() DirectSnapshot {
	return DirectSnapshot{Generation: 1, Summary: DirectSummary{State: directStateUnknown}}
}

func (s *Service) allowedOperations(agent models.Agent, control ControlSessionFact, hasControl bool, relay RelayRuntimeFact) []OperationStatus {
	statuses := make([]OperationStatus, 0, len(fixedOperationOrder))
	for _, operation := range fixedOperationOrder {
		_, err := s.authorize(agent, operation, control, hasControl, relay)
		status := OperationStatus{Operation: operation, Allowed: err == nil}
		if err != nil {
			var denied *OperationDeniedError
			if errors.As(err, &denied) {
				status.DenialCode = denied.DenialCode
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (s *Service) authorize(agent models.Agent, op Operation, control ControlSessionFact, hasControl bool, relay RelayRuntimeFact) (OperationLease, error) {
	if !isKnownOperation(op) {
		return OperationLease{}, fmt.Errorf("unknown operation %q", op)
	}
	if agent.Status != consts.StatusEnabled {
		return OperationLease{}, deny(op, DenialAgentDisabled)
	}
	if op == OperationRelayDrain || op == OperationRelayDisconnect {
		if relay.Active.SessionGeneration == 0 {
			return OperationLease{}, deny(op, DenialRelaySessionUnavailable)
		}
		return s.lease(control, hasControl, relay), nil
	}
	if !hasControl {
		return OperationLease{}, deny(op, DenialControlDisconnected)
	}

	switch op {
	case OperationFullSync, OperationProbe, OperationDirectCircuitReset, OperationInterrupt:
		return s.lease(control, true, relay), nil
	case OperationRelayReconnect:
		if relay.Support != relaySupportSupported {
			return OperationLease{}, deny(op, DenialRelayUnsupported)
		}
		switch relay.Config {
		case relayConfigDisabled:
			return OperationLease{}, deny(op, DenialRelayDisabled)
		case relayConfigConfigured:
			return s.lease(control, true, relay), nil
		default:
			return OperationLease{}, deny(op, DenialRelayNotConfigured)
		}
	default:
		return OperationLease{}, fmt.Errorf("unknown operation %q", op)
	}
}

func isKnownOperation(op Operation) bool {
	switch op {
	case OperationFullSync,
		OperationProbe,
		OperationRelayReconnect,
		OperationRelayDrain,
		OperationRelayDisconnect,
		OperationDirectCircuitReset,
		OperationInterrupt:
		return true
	default:
		return false
	}
}

func (s *Service) lease(control ControlSessionFact, hasControl bool, relay RelayRuntimeFact) OperationLease {
	lease := OperationLease{
		SnapshotEpoch:   s.instanceID,
		RelayGeneration: relay.Active.SessionGeneration,
	}
	if hasControl {
		lease.ControlGeneration = control.Generation
	}
	return lease
}

func deny(operation Operation, code string) error {
	return &OperationDeniedError{Operation: operation, DenialCode: code}
}
