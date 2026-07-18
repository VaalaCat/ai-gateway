package route

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestDirectGateFreshProbeDecisionMatrix(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name, network, identity, reason, want string
		eligible                              bool
	}{
		{name: "dns unreachable", network: "unreachable", identity: "unknown", reason: "direct_dns", want: "direct_dns"},
		{name: "tcp unreachable", network: "unreachable", identity: "unknown", reason: "direct_connect", want: "direct_connect"},
		{name: "tls unreachable", network: "unreachable", identity: "unknown", reason: "direct_tls", want: "direct_tls"},
		{name: "cancelled is unknown", network: "unreachable", identity: "unknown", reason: "cancelled"},
		{name: "identity mismatch", network: "reachable", identity: "mismatch", reason: "identity_agent_mismatch", want: "direct_identity_mismatch"},
		{name: "identity invalid", network: "reachable", identity: "invalid", reason: "identity_malformed", want: "direct_probe_invalid_response"},
		{name: "malformed identity", network: "reachable", identity: "malformed", reason: "identity_malformed", want: "direct_probe_invalid_response"},
		{name: "verified", network: "reachable", identity: "verified", eligible: true},
		{name: "degraded response", network: "reachable", identity: "unverified", reason: "http_status"},
		{name: "unknown", network: "unknown", identity: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := NewDirectGate(DirectGateOptions{FreshFor: 5 * time.Minute, MaxEntries: 8, Now: func() time.Time { return now }})
			gate.ApplyProbeResult(protocol.DirectProbeResult{
				TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: test.network,
				Identity: test.identity, Eligible: test.eligible, ReasonCode: test.reason, CheckedAt: now.Unix(),
			})
			require.Equal(t, test.want, gate.Decision("agent-b", "fp-b"))
		})
	}
}

func TestDirectGateStaleAndCheckingRetainPreviousDecision(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := NewDirectGate(DirectGateOptions{FreshFor: time.Minute, MaxEntries: 8, Now: func() time.Time { return now }})
	gate.ApplyProbeResult(protocol.DirectProbeResult{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: "unreachable",
		Identity: "unknown", CheckedAt: now.Unix(), ReasonCode: "direct_connect",
	})
	require.Equal(t, "direct_connect", gate.Decision("agent-b", "fp-b"))

	gate.MarkChecking("agent-b", "fp-b")
	require.Equal(t, "direct_connect", gate.Decision("agent-b", "fp-b"), "checking must retain previous gate")

	now = now.Add(time.Minute + time.Second)
	require.Empty(t, gate.Decision("agent-b", "fp-b"), "stale failure must attempt direct")
	gate.MarkChecking("agent-c", "fp-c")
	require.Empty(t, gate.Decision("agent-c", "fp-c"), "checking without a previous result is unknown")
}

func TestDirectGateCancelledProbePreservesPreviousCompletedResult(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := NewDirectGate(DirectGateOptions{FreshFor: time.Minute, MaxEntries: 8, Now: func() time.Time { return now }})
	previous := protocol.DirectProbeResult{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: "unreachable",
		Identity: "unknown", CheckedAt: now.Unix(), ReasonCode: "direct_connect",
	}
	gate.ApplyProbeResult(previous)
	gate.MarkChecking("agent-b", "fp-b")
	gate.ApplyProbeResult(protocol.DirectProbeResult{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: "unreachable",
		Identity: "unknown", CheckedAt: now.Add(time.Second).Unix(), ReasonCode: "cancelled",
	})

	key := directGateKey{targetAgentID: "agent-b", addressFingerprint: "fp-b"}
	gate.mu.Lock()
	entry := gate.entries[key]
	gate.mu.Unlock()
	require.True(t, entry.hasResult)
	require.False(t, entry.checking)
	require.Equal(t, previous, entry.result)
	require.Equal(t, "direct_connect", gate.Decision("agent-b", "fp-b"))
}

func TestDirectGateFirstCancelledProbeRemainsUnknown(t *testing.T) {
	gate := NewDirectGate(DirectGateOptions{})
	gate.MarkChecking("agent-b", "fp-b")
	gate.ApplyProbeResult(protocol.DirectProbeResult{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: "unreachable",
		Identity: "unknown", CheckedAt: time.Now().Unix(), ReasonCode: "cancelled",
	})

	key := directGateKey{targetAgentID: "agent-b", addressFingerprint: "fp-b"}
	gate.mu.Lock()
	entry := gate.entries[key]
	gate.mu.Unlock()
	require.False(t, entry.hasResult)
	require.False(t, entry.checking)
	require.Empty(t, gate.Decision("agent-b", "fp-b"))
}

func TestDirectGateFingerprintIsolationAndBoundedState(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := NewDirectGate(DirectGateOptions{FreshFor: time.Minute, MaxEntries: 2, Now: func() time.Time { return now }})
	applyUnreachableGateResult(gate, "agent-b", "old", now)
	require.Equal(t, "direct_connect", gate.Decision("agent-b", "old"))
	require.Empty(t, gate.Decision("agent-b", "new"), "old fingerprint must not gate new addresses")

	now = now.Add(time.Second)
	applyUnreachableGateResult(gate, "agent-c", "fp-c", now)
	now = now.Add(time.Second)
	applyUnreachableGateResult(gate, "agent-d", "fp-d", now)
	require.LessOrEqual(t, gate.EntryCount(), 2)
	require.Empty(t, gate.Decision("agent-b", "old"), "oldest entry must be evicted")
	require.Equal(t, "direct_connect", gate.Decision("agent-d", "fp-d"))
}

func applyUnreachableGateResult(gate *DirectGate, target, fingerprint string, checkedAt time.Time) {
	gate.ApplyProbeResult(protocol.DirectProbeResult{
		TargetAgentID: target, AddressFingerprint: fingerprint, Network: "unreachable",
		Identity: "unknown", CheckedAt: checkedAt.Unix(), ReasonCode: "direct_connect",
	})
}
