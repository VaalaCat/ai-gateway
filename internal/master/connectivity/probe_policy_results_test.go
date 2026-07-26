package connectivity

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestNormalizeDirectProbeResultPolicyMatrix(t *testing.T) {
	valid := protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "reachable", Identity: "verified",
		Eligible: true, Policy: protocol.ProbeRespectBusinessPolicy,
	}
	tests := []struct {
		name        string
		mutate      func(*protocol.DirectProbeResult)
		wantInvalid bool
	}{
		{name: "respect success"},
		{name: "manual disabled is valid", mutate: func(result *protocol.DirectProbeResult) {
			result.Policy = protocol.ProbeBypassBusinessPolicy
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorSourceDirectOutboundDisabled
			result.Eligible = false
		}},
		{name: "respect target reset is valid", mutate: func(result *protocol.DirectProbeResult) {
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorTargetDirectInboundDisabled
			result.Eligible = false
		}},
		{name: "invalid policy", mutate: func(result *protocol.DirectProbeResult) {
			result.Policy = protocol.ProbePolicy("invalid")
		}, wantInvalid: true},
		{name: "disabled without reason", mutate: func(result *protocol.DirectProbeResult) {
			result.PolicyDisabled = true
			result.Eligible = false
		}, wantInvalid: true},
		{name: "wrong path reason", mutate: func(result *protocol.DirectProbeResult) {
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorSourceRelayOutboundDisabled
			result.Eligible = false
		}, wantInvalid: true},
		{name: "reason without disabled", mutate: func(result *protocol.DirectProbeResult) {
			result.PolicyReasonCode = consts.RouteErrorSourceDirectOutboundDisabled
		}, wantInvalid: true},
		{name: "disabled eligible", mutate: func(result *protocol.DirectProbeResult) {
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorTargetDirectInboundDisabled
		}, wantInvalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid
			if test.mutate != nil {
				test.mutate(&result)
			}
			got := normalizeDirectProbeResult(result)
			if test.wantInvalid {
				require.Equal(t, consts.RouteErrorDirectProbeInvalidResponse, got.ReasonCode)
				require.False(t, got.Eligible)
				return
			}
			require.NotEqual(t, consts.RouteErrorDirectProbeInvalidResponse, got.ReasonCode)
			require.Equal(t, result.PolicyDisabled, got.PolicyDisabled)
			require.Equal(t, result.PolicyReasonCode, got.PolicyReasonCode)
		})
	}
}

func TestNormalizeRelayProbeResultPolicyMatrix(t *testing.T) {
	valid := protocol.RelayProbeResult{
		TargetAgentID: "target", State: protocol.RelayProbeReachable,
		Stage: protocol.RelayProbeStageResponse, Policy: protocol.ProbeRespectBusinessPolicy,
	}
	tests := []struct {
		name        string
		mutate      func(*protocol.RelayProbeResult)
		wantInvalid bool
	}{
		{name: "respect success"},
		{name: "manual reachable but disabled is valid", mutate: func(result *protocol.RelayProbeResult) {
			result.Policy = protocol.ProbeBypassBusinessPolicy
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorTargetRelayInboundDisabled
		}},
		{name: "respect target reset is valid", mutate: func(result *protocol.RelayProbeResult) {
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorTargetRelayInboundDisabled
		}},
		{name: "invalid policy", mutate: func(result *protocol.RelayProbeResult) {
			result.Policy = protocol.ProbePolicy("invalid")
		}, wantInvalid: true},
		{name: "disabled without reason", mutate: func(result *protocol.RelayProbeResult) {
			result.PolicyDisabled = true
		}, wantInvalid: true},
		{name: "wrong path reason", mutate: func(result *protocol.RelayProbeResult) {
			result.PolicyDisabled = true
			result.PolicyReasonCode = consts.RouteErrorTargetDirectInboundDisabled
		}, wantInvalid: true},
		{name: "reason without disabled", mutate: func(result *protocol.RelayProbeResult) {
			result.PolicyReasonCode = consts.RouteErrorSourceRelayOutboundDisabled
		}, wantInvalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid
			if test.mutate != nil {
				test.mutate(&result)
			}
			got := normalizeRelayProbeResult(result, "target")
			if test.wantInvalid {
				require.Equal(t, consts.RouteErrorRelayProbeInvalidResult, got.ReasonCode)
				require.Equal(t, protocol.RelayProbeUnreachable, got.State)
				return
			}
			require.NotEqual(t, consts.RouteErrorRelayProbeInvalidResult, got.ReasonCode)
			require.Equal(t, result.PolicyDisabled, got.PolicyDisabled)
			require.Equal(t, result.PolicyReasonCode, got.PolicyReasonCode)
		})
	}
}
