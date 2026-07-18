package connectivity

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type probePath string

const (
	// Direct intentionally remains the zero value so existing queued jobs retain their identity.
	probePathDirect probePath = ""
	probePathRelay  probePath = "relay"
)

func (p probePath) publicName() string {
	if p == probePathRelay {
		return "relay"
	}
	return "direct"
}

type RelayProbeCaller interface {
	CallRelayProbe(ctx context.Context, sourceID string, sourceGeneration uint64, target protocol.RelayProbeTarget) (protocol.RelayProbeResult, error)
}

type probeRunResult struct {
	succeeded bool
	callErr   error
}

type ProbeRunner interface {
	Path() probePath
	Run(ctx context.Context, job probeJob) probeRunResult
}

type DirectProbeRunner struct {
	caller  ProbeCaller
	service *Service
}

func (r *DirectProbeRunner) Path() probePath { return probePathDirect }

func (r *DirectProbeRunner) Run(ctx context.Context, job probeJob) probeRunResult {
	now := job.startedAt
	if now.IsZero() && r.service != nil {
		now = r.service.options.Now()
	}
	if r.service != nil {
		r.service.MarkDirectProbeChecking(job.key.sourceID, job.sourceGeneration, job.target, job.key.fingerprint, job.probeGeneration)
	}
	result := protocol.DirectProbeResult{
		TargetAgentID: job.target.AgentID, AddressFingerprint: job.key.fingerprint,
		Network: "unreachable", Identity: "unknown", CheckedAt: now.Unix(), ReasonCode: "probe_unavailable",
	}
	var callErr error
	if r.caller == nil {
		callErr = errors.New("probe scheduler: caller is required")
	} else {
		result, callErr = r.caller.CallDirectProbe(ctx, job.key.sourceID, job.sourceGeneration, protocol.DirectProbeTarget{
			TargetAgentID: job.target.AgentID, Addresses: append([]protocol.Address(nil), job.target.Addresses...),
			EffectiveProxy: job.target.EffectiveProxy, AddressFingerprint: job.key.fingerprint,
			TargetGeneration: job.target.ControlGeneration,
		})
	}
	if result.TargetAgentID == "" {
		result.TargetAgentID = job.target.AgentID
	}
	if result.AddressFingerprint == "" {
		result.AddressFingerprint = job.key.fingerprint
	}
	if result.CheckedAt == 0 && r.service != nil {
		result.CheckedAt = r.service.options.Now().Unix()
	}
	if callErr != nil && result.ReasonCode == "" {
		result.Network, result.Identity, result.ReasonCode = "unreachable", "unknown", "probe_call_failed"
	}
	if r.service != nil && (callErr != nil || result.ReasonCode == "cancelled" || result.ReasonCode == consts.RouteErrorRequestCancelled) {
		r.service.FinishDirectProbeWithoutResult(job.key.sourceID, job.sourceGeneration, job.target, job.key.fingerprint, job.probeGeneration)
	} else if r.service != nil {
		r.service.ApplyDirectProbeResult(job.key.sourceID, job.sourceGeneration, job.target, result, job.probeGeneration)
	}
	return probeRunResult{succeeded: callErr == nil && result.Eligible, callErr: callErr}
}

type RelayProbeRunner struct {
	caller  RelayProbeCaller
	service *Service
}

func (r *RelayProbeRunner) Path() probePath { return probePathRelay }

func (r *RelayProbeRunner) Run(ctx context.Context, job probeJob) probeRunResult {
	now := job.startedAt
	if now.IsZero() && r.service != nil {
		now = r.service.options.Now()
	}
	if r.service != nil {
		r.service.MarkRelayProbeChecking(
			job.key.sourceID, job.sourceGeneration, job.target, job.key.fingerprint, job.probeGeneration,
			job.sourceRelayGeneration, job.targetRelayGeneration,
		)
	}
	result := protocol.RelayProbeResult{
		TargetAgentID: job.target.AgentID, State: protocol.RelayProbeUnavailable,
		Stage: protocol.RelayProbeStageOpen, CheckedAt: now.Unix(), ReasonCode: consts.RouteErrorRelayNotReady,
	}
	var callErr error
	if r.caller == nil {
		callErr = errors.New("relay probe scheduler: caller is required")
	} else {
		result, callErr = r.caller.CallRelayProbe(ctx, job.key.sourceID, job.sourceGeneration, protocol.RelayProbeTarget{
			TargetAgentID:         job.target.AgentID,
			SourceRelayGeneration: job.sourceRelayGeneration,
			TargetRelayGeneration: job.targetRelayGeneration,
		})
	}
	if result.CheckedAt == 0 && r.service != nil {
		result.CheckedAt = r.service.options.Now().Unix()
	}
	if callErr != nil && result.ReasonCode == "" {
		result.State = protocol.RelayProbeUnavailable
		result.Stage = protocol.RelayProbeStageOpen
		result.ReasonCode = consts.RouteErrorRelayNotReady
	}
	withoutResult := callErr != nil || result.State == protocol.RelayProbeUnknown ||
		result.ReasonCode == consts.RouteErrorRequestCancelled
	if r.service != nil && withoutResult {
		r.service.FinishRelayProbeWithoutResult(
			job.key.sourceID, job.sourceGeneration, job.target, job.key.fingerprint, job.probeGeneration,
		)
	} else if r.service != nil {
		r.service.ApplyRelayProbeResult(
			job.key.sourceID, job.sourceGeneration, job.target, job.key.fingerprint, job.probeGeneration,
			job.sourceRelayGeneration, job.targetRelayGeneration, result,
		)
	}
	return probeRunResult{succeeded: callErr == nil && result.State == protocol.RelayProbeReachable, callErr: callErr}
}
