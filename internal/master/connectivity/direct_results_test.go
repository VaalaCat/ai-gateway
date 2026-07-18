package connectivity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDirectProbeSnapshotCheckingRetainsPreviousResultAndProjectsCompletion(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Name: "Target", Addresses: []protocol.Address{{URL: "https://target", Tag: "wan"}}}
	service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "unreachable", Identity: "unknown",
		CheckedAt: 100, LatencyMS: 8, ReasonCode: "direct_connect",
	}, 1)
	service.MarkDirectProbeChecking("source", 1, target, "fp", 2)
	checking := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
	require.True(t, checking.Checking)
	require.Equal(t, "unreachable", checking.Network)
	require.Equal(t, "direct_connect", checking.LastError.Code)

	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "reachable", Identity: "verified",
		Eligible: true, CheckedAt: 120, LatencyMS: 3,
	}, 2)
	direct := service.Build(models.Agent{AgentID: "source"}).Direct
	require.Equal(t, DirectSummary{State: "reachable", Reachable: 1, Total: 1}, direct.Summary)
	completed := direct.Targets["target"]
	require.False(t, completed.Checking)
	require.True(t, completed.Eligible)
	require.Equal(t, uint64(2), completed.ProbeGeneration)
	require.Equal(t, "verified", completed.Identity)
}

func TestDirectSnapshotGenerationChangesOnlyWithAcceptedContentMutations(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	agent := models.Agent{AgentID: "source"}
	target := ProbeTarget{AgentID: "target", Name: "Target", Addresses: []protocol.Address{{URL: "https://target"}}}

	emptyFirst := service.Build(agent)
	emptySecond := service.Build(agent)
	require.NotEqual(t, emptyFirst.SnapshotSeq, emptySecond.SnapshotSeq)
	require.NotZero(t, emptyFirst.Direct.Generation)
	require.Equal(t, emptyFirst.Direct.Generation, emptySecond.Direct.Generation)

	service.MarkDirectProbeChecking(agent.AgentID, 2, target, "fp-2", 2)
	checking := service.Build(agent)
	require.Greater(t, checking.Direct.Generation, emptySecond.Direct.Generation)
	require.True(t, checking.Direct.Targets[target.AgentID].Checking)

	service.MarkDirectProbeChecking(agent.AgentID, 1, target, "stale-fp", 99)
	rejected := service.Build(agent)
	require.Equal(t, checking.Direct.Generation, rejected.Direct.Generation)
	require.Equal(t, "fp-2", rejected.Direct.Targets[target.AgentID].AddressFingerprint)

	service.FinishDirectProbeWithoutResult(agent.AgentID, 2, target, "fp-2", 2)
	finished := service.Build(agent)
	require.Greater(t, finished.Direct.Generation, rejected.Direct.Generation)
	require.False(t, finished.Direct.Targets[target.AgentID].Checking)
}

func TestDirectProbeSnapshotIsCopyIsolated(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "reachable", Identity: "invalid",
		CheckedAt: 100, ReasonCode: "identity_malformed",
	}, 1)
	first := service.Build(models.Agent{AgentID: "source"}).Direct
	value := first.Targets["target"]
	value.Addresses[0].URL = "mutated"
	value.LastError.Code = "mutated"
	first.Targets["target"] = value
	second := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
	require.Equal(t, "https://target", second.Addresses[0].URL)
	require.Equal(t, "invalid_response", second.Identity)
	require.Equal(t, "identity_malformed", second.LastError.Code)
}

func TestDirectProbeResultNormalizesPublicIdentity(t *testing.T) {
	tests := []struct {
		name     string
		identity string
		want     string
	}{
		{name: "verified", identity: "verified", want: "verified"},
		{name: "mismatch", identity: "mismatch", want: "mismatch"},
		{name: "unknown", identity: "unknown", want: "unknown"},
		{name: "empty is unknown", identity: "", want: "unknown"},
		{name: "invalid", identity: "invalid", want: "invalid_response"},
		{name: "unverified", identity: "unverified", want: "invalid_response"},
		{name: "malformed", identity: "malformed", want: "invalid_response"},
		{name: "unrecognized fails closed", identity: "future_value", want: "invalid_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService("master", Sources{}, Options{})
			target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
			service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
			service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
				TargetAgentID: "target", AddressFingerprint: "fp", Network: "reachable",
				Identity: test.identity, CheckedAt: 100,
			}, 1)

			snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
			require.Equal(t, test.want, snapshot.Identity)
			encoded, err := json.Marshal(snapshot)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"identity":"`+test.want+`"`)
		})
	}
}

func TestDirectProbeResultNormalizesUntrustedDiagnosticFields(t *testing.T) {
	tests := []struct {
		name         string
		network      string
		identity     string
		eligible     bool
		reasonCode   string
		wantNetwork  string
		wantIdentity string
		wantEligible bool
		wantReason   string
	}{
		{
			name: "known mismatch remains actionable", network: "reachable", identity: "mismatch",
			reasonCode: "identity_role_mismatch", wantNetwork: "reachable", wantIdentity: "mismatch",
			wantReason: "identity_role_mismatch",
		},
		{
			name: "unknown code", network: "unreachable", identity: "unknown",
			reasonCode: "future_probe_failure", wantNetwork: "unreachable", wantIdentity: "invalid_response",
			wantReason: "direct_probe_invalid_response",
		},
		{
			name: "invalid code characters", network: "unreachable", identity: "unknown",
			reasonCode: "direct_connect\nforged=true", wantNetwork: "unreachable", wantIdentity: "invalid_response",
			wantReason: "direct_probe_invalid_response",
		},
		{
			name: "oversized code", network: "unreachable", identity: "unknown",
			reasonCode: strings.Repeat("x", 4096), wantNetwork: "unreachable", wantIdentity: "invalid_response",
			wantReason: "direct_probe_invalid_response",
		},
		{
			name: "unknown network", network: "online", identity: "verified", eligible: true,
			wantNetwork: "unknown", wantIdentity: "invalid_response", wantReason: "direct_probe_invalid_response",
		},
		{
			name: "contradictory eligibility", network: "reachable", identity: "mismatch", eligible: true,
			reasonCode: "identity_agent_mismatch", wantNetwork: "reachable", wantIdentity: "invalid_response",
			wantReason: "direct_probe_invalid_response",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, logs := observer.New(zap.InfoLevel)
			service := NewService("master", Sources{}, Options{Logger: zap.New(core)})
			target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
			service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
			service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
				TargetAgentID: "target", AddressFingerprint: "fp", Network: test.network,
				Identity: test.identity, Eligible: test.eligible, CheckedAt: 100, ReasonCode: test.reasonCode,
			}, 1)

			snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
			require.Equal(t, test.wantNetwork, snapshot.Network)
			require.Equal(t, test.wantIdentity, snapshot.Identity)
			require.Equal(t, test.wantEligible, snapshot.Eligible)
			require.NotNil(t, snapshot.LastError)
			require.Equal(t, test.wantReason, snapshot.LastError.Code)

			entries := logs.All()
			require.Len(t, entries, 1)
			fields := entries[0].ContextMap()
			require.Equal(t, test.wantNetwork, fields["network"])
			require.Equal(t, test.wantIdentity, fields["identity"])
			require.Equal(t, test.wantReason, fields["reason_code"])
		})
	}
}

func TestDirectProbeResultRejectsOlderFingerprintCompletion(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	service.MarkDirectProbeChecking("source", 1, target, "old-fp", 1)
	service.MarkDirectProbeChecking("source", 1, target, "new-fp", 2)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "new-fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 200,
	}, 2)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "old-fp", Network: "unreachable",
		Identity: "unknown", CheckedAt: 300, ReasonCode: "direct_connect",
	}, 1)

	snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
	require.Equal(t, "new-fp", snapshot.AddressFingerprint)
	require.Equal(t, uint64(2), snapshot.ProbeGeneration)
	require.True(t, snapshot.Eligible)
	require.Equal(t, int64(200), snapshot.CheckedAt)
}

func TestDirectProbeResultRejectsOlderGenerationForSameFingerprint(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
	service.MarkDirectProbeChecking("source", 1, target, "fp", 2)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 200,
	}, 2)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "unreachable",
		Identity: "unknown", CheckedAt: 300, ReasonCode: "direct_connect",
	}, 1)

	snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
	require.Equal(t, uint64(2), snapshot.ProbeGeneration)
	require.True(t, snapshot.Eligible)
	require.Equal(t, int64(200), snapshot.CheckedAt)
}

func TestDirectProbeCancelRejectsOlderGenerationWhileNewProbeChecks(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}
	service.MarkDirectProbeChecking("source", 1, target, "fp", 1)
	service.MarkDirectProbeChecking("source", 1, target, "fp", 2)
	service.FinishDirectProbeWithoutResult("source", 1, target, "fp", 1)

	checking := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
	require.True(t, checking.Checking)
	require.Equal(t, uint64(2), checking.ProbeGeneration)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: "target", AddressFingerprint: "fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 200,
	}, 2)
	completed := service.Build(models.Agent{AgentID: "source"}).Direct.Targets["target"]
	require.False(t, completed.Checking)
	require.True(t, completed.Eligible)
}

func TestDirectProbeMarkUsesPerTargetGenerationHighWater(t *testing.T) {
	t.Run("stale start after newer completion is ignored", func(t *testing.T) {
		service := NewService("master", Sources{}, Options{})
		target := ProbeTarget{AgentID: "target-a", Addresses: []protocol.Address{{URL: "https://a"}}}
		markAndCompleteProbeForTest(service, "source", target, "new-fp", 2, 200)

		service.MarkDirectProbeChecking("source", 1, target, "old-fp", 1)
		service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
			TargetAgentID: target.AgentID, AddressFingerprint: "old-fp", Network: "unreachable",
			Identity: "unknown", CheckedAt: 300, ReasonCode: "direct_connect",
		}, 1)

		snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
		require.Equal(t, "new-fp", snapshot.AddressFingerprint)
		require.Equal(t, uint64(2), snapshot.ProbeGeneration)
		require.True(t, snapshot.Eligible)
		require.Equal(t, int64(200), snapshot.CheckedAt)
	})

	t.Run("matching and newer generations start normally", func(t *testing.T) {
		service := NewService("master", Sources{}, Options{})
		target := ProbeTarget{AgentID: "target-a", Addresses: []protocol.Address{{URL: "https://a"}}}
		markAndCompleteProbeForTest(service, "source", target, "fp-2", 2, 200)
		markAndCompleteProbeForTest(service, "source", target, "fp-2", 2, 220)
		matching := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
		require.Equal(t, uint64(2), matching.ProbeGeneration)
		require.Equal(t, int64(220), matching.CheckedAt)

		markAndCompleteProbeForTest(service, "source", target, "fp-3", 3, 300)
		newer := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
		require.Equal(t, "fp-3", newer.AddressFingerprint)
		require.Equal(t, uint64(3), newer.ProbeGeneration)
		require.Equal(t, int64(300), newer.CheckedAt)
	})

	t.Run("target high water marks are independent", func(t *testing.T) {
		service := NewService("master", Sources{}, Options{})
		targetA := ProbeTarget{AgentID: "target-a", Addresses: []protocol.Address{{URL: "https://a"}}}
		targetB := ProbeTarget{AgentID: "target-b", Addresses: []protocol.Address{{URL: "https://b"}}}
		targetC := ProbeTarget{AgentID: "target-c", Addresses: []protocol.Address{{URL: "https://c"}}}
		markAndCompleteProbeForTest(service, "source", targetA, "fp-a", 9, 900)
		markAndCompleteProbeForTest(service, "source", targetB, "fp-b", 1, 100)
		markAndCompleteProbeForTest(service, "source", targetB, "fp-b-7", 7, 700)
		markAndCompleteProbeForTest(service, "source", targetC, "fp-c-7", 7, 710)

		targets := service.Build(models.Agent{AgentID: "source"}).Direct.Targets
		require.Equal(t, uint64(9), targets[targetA.AgentID].ProbeGeneration)
		require.Equal(t, uint64(7), targets[targetB.AgentID].ProbeGeneration)
		require.Equal(t, uint64(7), targets[targetC.AgentID].ProbeGeneration)
		require.Equal(t, "fp-b-7", targets[targetB.AgentID].AddressFingerprint)
		require.Equal(t, "fp-c-7", targets[targetC.AgentID].AddressFingerprint)
	})
}

func TestDirectProbeStateRejectsStaleSourceGenerationLifecycle(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}}}

	service.MarkDirectProbeChecking("source", 2, target, "gen2-fp", 7)
	service.ApplyDirectProbeResult("source", 2, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "gen2-fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 200,
	}, 7)
	service.Forget("source", 1)

	service.MarkDirectProbeChecking("source", 1, target, "gen1-late-fp", 99)
	service.ApplyDirectProbeResult("source", 1, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "gen1-late-fp", Network: "unreachable",
		Identity: "unknown", CheckedAt: 300, ReasonCode: "direct_connect",
	}, 99)
	snapshot := service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
	require.Equal(t, "gen2-fp", snapshot.AddressFingerprint)
	require.Equal(t, int64(200), snapshot.CheckedAt)
	require.True(t, snapshot.Eligible)

	service.MarkDirectProbeChecking("source", 2, target, "gen2-fp", 8)
	service.FinishDirectProbeWithoutResult("source", 1, target, "gen2-fp", 8)
	snapshot = service.Build(models.Agent{AgentID: "source"}).Direct.Targets[target.AgentID]
	require.True(t, snapshot.Checking)
	require.Equal(t, uint64(8), snapshot.ProbeGeneration)

	service.Forget("source", 2)
	require.Empty(t, service.Build(models.Agent{AgentID: "source"}).Direct.Targets)
}

func markAndCompleteProbeForTest(service *Service, sourceID string, target ProbeTarget, fingerprint string, generation uint64, checkedAt int64) {
	service.MarkDirectProbeChecking(sourceID, 1, target, fingerprint, generation)
	service.ApplyDirectProbeResult(sourceID, 1, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: fingerprint, Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: checkedAt,
	}, generation)
}
