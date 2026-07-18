package diagnostics

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticRingCapsAtTwentyOmitsSuccessAndCopiesSnapshots(t *testing.T) {
	ring := NewRing(20)
	require.False(t, ring.Record(Event{Code: "", Message: "success"}))
	for i := range 25 {
		require.True(t, ring.Record(Event{Code: fmt.Sprintf("failure-%02d", i), Message: "failure"}))
	}
	snapshot := ring.Snapshot()
	require.Len(t, snapshot, 20)
	require.Equal(t, "failure-05", snapshot[0].Code)
	snapshot[0].Message = "mutated"
	require.Equal(t, "failure", ring.Snapshot()[0].Message)
}

func TestDiagnosticRingConcurrentRecordRemainsBounded(t *testing.T) {
	ring := NewRing(0)
	var workers conc.WaitGroup
	for worker := range 64 {
		worker := worker
		workers.Go(func() {
			for event := range 100 {
				ring.Record(Event{Code: "direct_connect", Message: fmt.Sprintf("%d-%d", worker, event)})
			}
		})
	}
	workers.Wait()
	require.Len(t, ring.Snapshot(), 20)
}

func TestSuppressorWindowRecoveryLRUAndIdleExpiry(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	suppressor := NewSuppressor(SuppressorOptions{Window: time.Minute, MaxKeys: 4096})
	key := SuppressionKey{Source: "source", Target: "target", PathKind: "direct", Stage: "dial", ReasonCode: "direct_connect"}

	first := suppressor.Observe(key, start)
	require.True(t, first.Allow)
	require.Nil(t, first.Summary)
	require.False(t, suppressor.Observe(key, start.Add(time.Second)).Allow)

	windowEnd := suppressor.Observe(key, start.Add(time.Minute))
	require.False(t, windowEnd.Allow, "window end emits one summary instead of a duplicate event")
	require.Equal(t, uint64(1), windowEnd.Summary.SuppressedCount)
	require.Equal(t, "window_end", windowEnd.Summary.Kind)

	require.False(t, suppressor.Observe(key, start.Add(time.Minute+time.Second)).Allow)
	recovery := suppressor.Recover(key, start.Add(time.Minute+2*time.Second))
	require.NotNil(t, recovery)
	require.Equal(t, "recovery", recovery.Kind)
	require.Equal(t, uint64(1), recovery.SuppressedCount)
	require.False(t, suppressor.Contains(key))

	for i := range 4097 {
		current := key
		current.Target = fmt.Sprintf("target-%04d", i)
		suppressor.Observe(current, start.Add(3*time.Minute))
	}
	require.Equal(t, 4096, suppressor.Len())
	oldest := key
	oldest.Target = "target-0000"
	require.False(t, suppressor.Contains(oldest))

	fresh := SuppressionKey{Source: "fresh", Target: "target", PathKind: "relay", Stage: "session", ReasonCode: "relay_not_ready"}
	suppressor.Observe(fresh, start.Add(6*time.Minute))
	require.Equal(t, 1, suppressor.Len(), "idle keys expire after two suppression windows")
	require.True(t, suppressor.Contains(fresh))
}

func TestSuppressorKeyUsesAllRouteDimensions(t *testing.T) {
	base := SuppressionKey{Source: "s", Target: "t", PathKind: "direct", Stage: "dial", ReasonCode: "direct_connect"}
	keys := []SuppressionKey{
		base,
		{Source: "s2", Target: "t", PathKind: "direct", Stage: "dial", ReasonCode: "direct_connect"},
		{Source: "s", Target: "t2", PathKind: "direct", Stage: "dial", ReasonCode: "direct_connect"},
		{Source: "s", Target: "t", PathKind: "relay", Stage: "dial", ReasonCode: "direct_connect"},
		{Source: "s", Target: "t", PathKind: "direct", Stage: "auth", ReasonCode: "direct_connect"},
		{Source: "s", Target: "t", PathKind: "direct", Stage: "dial", ReasonCode: "direct_tls"},
	}
	suppressor := NewSuppressor(SuppressorOptions{})
	for _, key := range keys {
		require.True(t, suppressor.Observe(key, time.Unix(1, 0)).Allow)
	}
	require.Len(t, keys, suppressor.Len())
}

func TestSuppressorOmitsZeroCountWindowAndRecoverySummaries(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	key := SuppressionKey{Source: "source", Target: "target", PathKind: "direct", Stage: "dial", ReasonCode: "direct_connect"}

	t.Run("new window logs current event", func(t *testing.T) {
		suppressor := NewSuppressor(SuppressorOptions{Window: time.Minute})
		require.True(t, suppressor.Observe(key, start).Allow)

		decision := suppressor.Observe(key, start.Add(time.Minute))
		require.True(t, decision.Allow)
		require.Nil(t, decision.Summary)
	})

	t.Run("recovery without suppressed events is silent", func(t *testing.T) {
		suppressor := NewSuppressor(SuppressorOptions{Window: time.Minute})
		require.True(t, suppressor.Observe(key, start).Allow)
		require.Nil(t, suppressor.Recover(key, start.Add(time.Second)))
		require.False(t, suppressor.Contains(key))
	})
}

func TestSuppressorRecoverExpiresIdleKeysAtTwoWindowBoundary(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	window := time.Minute
	key := SuppressionKey{Source: "source", Target: "target", PathKind: "direct", Stage: "dial", ReasonCode: "direct_connect"}

	t.Run("idle at boundary expires without summary", func(t *testing.T) {
		suppressor := NewSuppressor(SuppressorOptions{Window: window})
		require.True(t, suppressor.Observe(key, start).Allow)
		lastSeen := start.Add(time.Second)
		require.False(t, suppressor.Observe(key, lastSeen).Allow)
		require.Nil(t, suppressor.Recover(key, lastSeen.Add(2*window)))
		require.False(t, suppressor.Contains(key))
	})

	t.Run("idle before boundary returns suppressed summary", func(t *testing.T) {
		suppressor := NewSuppressor(SuppressorOptions{Window: window})
		require.True(t, suppressor.Observe(key, start).Allow)
		lastSeen := start.Add(time.Second)
		require.False(t, suppressor.Observe(key, lastSeen).Allow)
		summary := suppressor.Recover(key, lastSeen.Add(2*window-time.Nanosecond))
		require.NotNil(t, summary)
		require.Equal(t, uint64(1), summary.SuppressedCount)
		require.False(t, suppressor.Contains(key))
	})

	t.Run("zero count remains silent", func(t *testing.T) {
		suppressor := NewSuppressor(SuppressorOptions{Window: window})
		require.True(t, suppressor.Observe(key, start).Allow)
		require.Nil(t, suppressor.Recover(key, start.Add(window)))
		require.False(t, suppressor.Contains(key))
	})
}

func TestSuppressorSchedulesFullExpirySweeps(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	suppressor := NewSuppressor(SuppressorOptions{Window: time.Minute, MaxKeys: DefaultSuppressionKeys})
	keys := make([]SuppressionKey, 0, DefaultSuppressionKeys)
	for i := 0; i < DefaultSuppressionKeys; i++ {
		key := SuppressionKey{Source: fmt.Sprintf("source-%d", i), ReasonCode: "relay_not_ready"}
		keys = append(keys, key)
		require.True(t, suppressor.Observe(key, start).Allow)
	}
	for range 100 {
		require.False(t, suppressor.Observe(keys[0], start).Allow)
	}
	require.Equal(t, uint64(0), suppressor.sweepCount, "fresh duplicate observations must not sweep all keys")

	fresh := SuppressionKey{Source: "fresh", ReasonCode: "relay_not_ready"}
	require.True(t, suppressor.Observe(fresh, start.Add(2*time.Minute)).Allow)
	require.Equal(t, uint64(1), suppressor.sweepCount, "the expiry boundary should trigger one full sweep")
	require.Equal(t, 1, suppressor.Len())
	require.False(t, suppressor.Observe(fresh, start.Add(2*time.Minute)).Allow)
	require.Equal(t, uint64(1), suppressor.sweepCount, "duplicates before the next expiry must not resweep")
}

func TestSuppressorClockRollbackKeepsStateFreshAndBounded(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	window := time.Minute
	suppressor := NewSuppressor(SuppressorOptions{Window: window, MaxKeys: DefaultSuppressionKeys})
	existing := SuppressionKey{Source: "existing", ReasonCode: "relay_auth"}
	require.True(t, suppressor.Observe(existing, start).Allow)
	require.False(t, suppressor.Observe(existing, start.Add(-10*time.Minute)).Allow)
	require.Equal(t, start, suppressor.states[existing].lastSeen, "clock rollback must not move lastSeen backwards")

	fresh := SuppressionKey{Source: "fresh-during-rollback", ReasonCode: "relay_auth"}
	require.True(t, suppressor.Observe(fresh, start.Add(-10*time.Minute)).Allow)
	require.Equal(t, start, suppressor.states[fresh].lastSeen)
	trigger := SuppressionKey{Source: "trigger", ReasonCode: "relay_auth"}
	require.True(t, suppressor.Observe(trigger, start.Add(window)).Allow)
	require.True(t, suppressor.Contains(fresh), "a key created during rollback must not expire early")

	for i := 0; i < DefaultSuppressionKeys+100; i++ {
		key := SuppressionKey{Source: fmt.Sprintf("rollback-%d", i), ReasonCode: "relay_auth"}
		suppressor.Observe(key, start.Add(-time.Hour))
	}
	require.Equal(t, DefaultSuppressionKeys, suppressor.Len())
}

func TestNilSuppressorAlwaysAllowsWithoutSummary(t *testing.T) {
	var suppressor *Suppressor
	decision := suppressor.Observe(SuppressionKey{}, time.Time{})
	require.True(t, decision.Allow)
	require.Nil(t, decision.Summary)
	require.Nil(t, suppressor.Recover(SuppressionKey{}, time.Time{}))
	require.Zero(t, suppressor.Len())
	require.False(t, suppressor.Contains(SuppressionKey{}))
}

func TestRedactURITextAndPublicRouteError(t *testing.T) {
	require.Equal(t, "https://example.com/path", RedactURI("https://user:password@example.com/path?token=secret#fragment"))
	require.Equal(t, "", RedactURI("not a uri?token=secret"))
	require.Equal(t, "redacted", SanitizeText("Authorization: Bearer secret-token\nstack trace"))
	require.Equal(t, "connection refused", SanitizeText("connection refused"))

	public := protocol.NewPublicRouteError(consts.RouteErrorDirectConnect, "dial", "req-17")
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.JSONEq(t, `{"code":"direct_connect","stage":"dial","request_id":"req-17","message":"route request failed"}`, string(raw))
	require.NotContains(t, string(raw), "secret")
}

func TestPublicRouteErrorCodeSetIsExact(t *testing.T) {
	want := []string{
		"selector_invalid", "target_not_found", "target_disabled", "tag_no_candidate",
		"direct_disabled", "direct_ingress_unsupported", "direct_circuit_open", "direct_auth_unavailable", "direct_dns", "direct_connect", "direct_tls",
		"direct_identity_mismatch", "direct_probe_invalid_response", "direct_commit_uncertain", "direct_response_interrupted",
		"relay_unsupported", "relay_not_configured", "relay_disabled", "relay_fallback_disabled", "relay_not_ready", "relay_overloaded",
		"relay_protocol", "relay_auth", "relay_commit_uncertain", "relay_response_interrupted",
		"request_cancelled", "request_deadline", "body_too_large", "body_store_failed", "stream_window_timeout", "session_closed", "drain_timeout",
	}
	require.Equal(t, want, consts.PublicRouteErrorCodes())
	for _, code := range want {
		require.True(t, consts.IsPublicRouteErrorCode(code), code)
		require.True(t, consts.IsConnectivityProbeErrorCode(code), code)
	}
	for _, code := range []string{
		consts.RouteErrorRelayProbeHTTPStatus,
		consts.RouteErrorRelayProbeBodyTooLarge,
		consts.RouteErrorRelayProbeInvalidResponse,
		consts.RouteErrorRelayProbeInvalidResult,
	} {
		require.False(t, consts.IsPublicRouteErrorCode(code), code)
		require.True(t, consts.IsConnectivityProbeErrorCode(code), code)
	}
	require.False(t, consts.IsPublicRouteErrorCode("internal_stack"))
	require.False(t, consts.IsConnectivityProbeErrorCode("internal_stack"))
	invalid := protocol.NewPublicRouteError("internal_stack", "secret-stage", "request?token=secret")
	require.Equal(t, consts.RouteErrorRelayProtocol, invalid.Code)
	require.Equal(t, "", invalid.RequestID)
}
