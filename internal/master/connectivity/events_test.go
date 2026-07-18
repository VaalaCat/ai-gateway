package connectivity

import (
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDirectProbeStateTransitionsLogInfoOnceWithoutEndpointDetails(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	service := NewService("master", Sources{}, Options{Logger: zap.New(core)})
	target := ProbeTarget{
		AgentID: "target",
		Addresses: []protocol.Address{{
			URL: "https://user:pass@target.example/probe?token=secret",
		}},
	}

	apply := func(generation uint64, result protocol.DirectProbeResult) {
		service.MarkDirectProbeChecking("source", 1, target, "sensitive-fingerprint", generation)
		service.ApplyDirectProbeResult("source", 1, target, result, generation)
	}
	failure := protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "sensitive-fingerprint",
		Network: "unreachable", Identity: "unknown", ReasonCode: "direct_connect",
	}
	apply(1, failure)
	apply(2, failure)
	apply(3, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "sensitive-fingerprint",
		Network: "reachable", Identity: "verified", Eligible: true,
	})
	apply(4, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "sensitive-fingerprint",
		Network: "reachable", Identity: "verified", Eligible: true,
	})

	require.Len(t, logs.All(), 2)
	require.Equal(t, zap.InfoLevel, logs.All()[0].Level)
	require.Equal(t, "direct probe state changed", logs.All()[0].Message)
	require.Equal(t, map[string]any{
		"source": "source", "target": "target", "path_kind": "direct", "stage": "probe",
		"network": "unreachable", "identity": "unknown", "eligible": false, "reason_code": "direct_connect",
	}, logs.All()[0].ContextMap())
	require.Equal(t, map[string]any{
		"source": "source", "target": "target", "path_kind": "direct", "stage": "probe",
		"network": "reachable", "identity": "verified", "eligible": true, "reason_code": "",
	}, logs.All()[1].ContextMap())
	for _, entry := range logs.All() {
		fields := fmt.Sprint(entry.ContextMap())
		require.NotContains(t, fields, "target.example")
		require.NotContains(t, fields, "token=secret")
		require.NotContains(t, fields, "sensitive-fingerprint")
	}
}

func TestDirectEdgeErrorRingCapsAtTwentyAndCopiesSnapshots(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	for generation := uint64(1); generation <= 25; generation++ {
		service.MarkDirectProbeChecking("source", 1, target, "fp", generation)
		service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
			TargetAgentID: target.AgentID, AddressFingerprint: "fp", Network: "unreachable", Identity: "unknown",
			CheckedAt: int64(generation), ReasonCode: "direct_connect",
		}, generation)
	}

	first := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
	require.Len(t, first.RecentErrors, 20)
	require.Equal(t, "direct_connect", first.RecentErrors[0].Code)
	require.Equal(t, int64(6), first.RecentErrors[0].OccurredAt)
	require.Equal(t, int64(25), first.RecentErrors[19].OccurredAt)
	first.RecentErrors[0].Code = "mutated"

	second := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
	require.Equal(t, "direct_connect", second.RecentErrors[0].Code)
}

func TestDirectEdgeSuccessDoesNotEnterOrClearErrorRing(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "fp", Network: "unreachable", Identity: "unknown",
		CheckedAt: 1, ReasonCode: "direct_connect",
	}, 1)
	service.MarkDirectProbeChecking("source", 1, target, "fp", 2)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "fp", Network: "reachable", Identity: "verified",
		Eligible: true, CheckedAt: 2,
	}, 2)

	snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
	require.Len(t, snapshot.RecentErrors, 1)
	require.Equal(t, "direct_connect", snapshot.RecentErrors[0].Code)
	require.Nil(t, snapshot.LastError)
}

func TestDirectEdgeReplacementRejectsOldGenerationErrors(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	service.MarkDirectProbeChecking("source", 1, target, "old", 1)
	service.MarkDirectProbeChecking("source", 2, target, "current", 1)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "old", Network: "unreachable", Identity: "unknown",
		CheckedAt: 1, ReasonCode: "direct_dns",
	}, 1)
	service.ApplyDirectProbeResult("source", 2, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "current", Network: "unreachable", Identity: "unknown",
		CheckedAt: 2, ReasonCode: "direct_connect",
	}, 1)

	snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
	require.Len(t, snapshot.RecentErrors, 1)
	require.Equal(t, "direct_connect", snapshot.RecentErrors[0].Code)
}
