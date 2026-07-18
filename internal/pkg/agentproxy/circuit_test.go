package agentproxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDirectCircuitReportsOnlyRealTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	transitions := make([]DirectCircuitTransition, 0, 3)
	c := newDirectCircuit(directCircuitOptions{
		FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }, Limit: 8,
		OnTransition: func(transition DirectCircuitTransition) { transitions = append(transitions, transition) },
	})
	key := directCircuitKey{TargetAgentID: "agent-a", AddressFingerprint: "fp"}
	permit, ok := c.allow(key)
	require.True(t, ok)
	c.transportFailed(permit)
	now = now.Add(time.Second)
	halfOpen, ok := c.allow(key)
	require.True(t, ok)
	c.httpResponded(halfOpen)

	require.Equal(t, []DirectCircuitTransition{
		{TargetAgentID: "agent-a", State: "open"},
		{TargetAgentID: "agent-a", State: "half_open"},
		{TargetAgentID: "agent-a", State: "closed"},
	}, transitions)
}

func TestDirectCircuitOpensAfterThreeFailures(t *testing.T) {
	now := time.Unix(100, 0)
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 3, OpenFor: 30 * time.Second, Now: func() time.Time { return now }, Limit: 8})
	key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	for range 3 {
		permit, ok := c.allow(key)
		require.True(t, ok)
		c.transportFailed(permit)
	}
	_, ok := c.allow(key)
	require.False(t, ok)
}

func TestDirectCircuitAllowsOnlyOneHalfOpenPermit(t *testing.T) {
	now := time.Unix(100, 0)
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }, Limit: 8})
	key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	permit, ok := c.allow(key)
	require.True(t, ok)
	c.transportFailed(permit)
	now = now.Add(time.Second)
	halfOpen, ok := c.allow(key)
	require.True(t, ok)
	require.True(t, halfOpen.halfOpen)
	_, ok = c.allow(key)
	require.False(t, ok)
	c.transportFailed(halfOpen)
}

func TestDirectCircuitCancelledHalfOpenPermitCanBeReacquired(t *testing.T) {
	now := time.Unix(100, 0)
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }, Limit: 8})
	key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	permit, ok := c.allow(key)
	require.True(t, ok)
	c.transportFailed(permit)
	now = now.Add(time.Second)
	halfOpen, ok := c.allow(key)
	require.True(t, ok)
	c.cancelled(halfOpen)
	next, ok := c.allow(key)
	require.True(t, ok)
	require.True(t, next.halfOpen)
}

func TestDirectCircuitHTTPResponseRecoversAndCancellationDoesNotCount(t *testing.T) {
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 2, OpenFor: time.Minute, Limit: 8})
	key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	permit, ok := c.allow(key)
	require.True(t, ok)
	c.transportFailed(permit)
	permit, ok = c.allow(key)
	require.True(t, ok)
	c.cancelled(permit)
	permit, ok = c.allow(key)
	require.True(t, ok)
	c.httpResponded(permit)
	permit, ok = c.allow(key)
	require.True(t, ok)
	c.transportFailed(permit)
	_, ok = c.allow(key)
	require.True(t, ok, "response must clear previous failures")
}

func TestDirectCircuitAdminResetAndBoundedState(t *testing.T) {
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Minute, Limit: 2})
	for _, id := range []string{"a", "b", "c"} {
		key := directCircuitKey{TargetAgentID: id, AddressFingerprint: "fp"}
		permit, ok := c.allow(key)
		require.True(t, ok)
		c.transportFailed(permit)
	}
	require.LessOrEqual(t, c.resourceCount(), 2)
	c.reset("c", "fp")
	_, ok := c.allow(directCircuitKey{TargetAgentID: "c", AddressFingerprint: "fp"})
	require.True(t, ok)
}

func TestDirectCircuitCloseClearsStateAndRejectsLateFailure(t *testing.T) {
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Minute, Limit: 2})
	key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	permit, ok := c.allow(key)
	require.True(t, ok)
	c.transportFailed(permit)
	require.Equal(t, 1, c.resourceCount())
	c.close()
	require.Zero(t, c.resourceCount())
	c.transportFailed(permit)
	require.Zero(t, c.resourceCount(), "late request completion recreated circuit state")
	_, ok = c.allow(key)
	require.False(t, ok)
}

func TestDirectCircuitDoesNotEvictInflightHalfOpenPermit(t *testing.T) {
	now := time.Unix(100, 0)
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }, Limit: 1})
	a := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	b := directCircuitKey{TargetAgentID: "b", AddressFingerprint: "fp"}
	permit, ok := c.allow(a)
	require.True(t, ok)
	c.transportFailed(permit)
	now = now.Add(time.Second)
	halfOpen, ok := c.allow(a)
	require.True(t, ok)
	require.True(t, halfOpen.halfOpen)

	_, ok = c.allow(b)
	require.False(t, ok, "new admission must be denied when every bounded state is active")
	_, ok = c.allow(a)
	require.False(t, ok, "a second half-open permit must never be admitted")
	c.cancelled(halfOpen)
}

func TestDirectCircuitOldNormalCompletionDoesNotReleaseCurrentHalfOpen(t *testing.T) {
	for _, tc := range []struct {
		name     string
		complete func(*directCircuit, directCircuitPermit)
		advance  bool
	}{
		{name: "cancelled", complete: func(c *directCircuit, permit directCircuitPermit) { c.cancelled(permit) }},
		{name: "transport failed", complete: func(c *directCircuit, permit directCircuitPermit) { c.transportFailed(permit) }, advance: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }, Limit: 2})
			key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
			first, ok := c.allow(key)
			require.True(t, ok)
			oldNormal, ok := c.allow(key)
			require.True(t, ok)
			c.transportFailed(first)
			now = now.Add(time.Second)
			halfOpen, ok := c.allow(key)
			require.True(t, ok)
			require.True(t, halfOpen.halfOpen)

			tc.complete(c, oldNormal)
			if tc.advance {
				now = now.Add(time.Second)
			}
			_, ok = c.allow(key)
			require.False(t, ok, "an old normal completion must not release the active half-open permit")
			c.cancelled(halfOpen)
		})
	}
}

func TestDirectCircuitDeniesNewKeyWhileActiveStatePreservesFailures(t *testing.T) {
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 3, OpenFor: time.Minute, Limit: 1})
	a := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	b := directCircuitKey{TargetAgentID: "b", AddressFingerprint: "fp"}
	for attempt := 0; attempt < 3; attempt++ {
		permit, ok := c.allow(a)
		require.True(t, ok)
		_, ok = c.allow(b)
		require.False(t, ok, "active state must not be evicted or bypassed")
		c.transportFailed(permit)
	}
	_, ok := c.allow(a)
	require.False(t, ok, "three preserved failures must open the original circuit")
}

func TestDirectCircuitAdmissionReturnsTypedDenialReason(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Minute, Limit: 2})
		key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
		permit, reason := c.admit(key)
		require.Equal(t, directCircuitAllowed, reason)
		c.transportFailed(permit)
		_, reason = c.admit(key)
		require.Equal(t, directCircuitDeniedOpen, reason)
	})

	t.Run("half-open busy", func(t *testing.T) {
		now := time.Unix(100, 0)
		c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Second, Now: func() time.Time { return now }, Limit: 2})
		key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
		permit, _ := c.admit(key)
		c.transportFailed(permit)
		now = now.Add(time.Second)
		halfOpen, reason := c.admit(key)
		require.Equal(t, directCircuitAllowed, reason)
		_, reason = c.admit(key)
		require.Equal(t, directCircuitDeniedHalfOpen, reason)
		c.cancelled(halfOpen)
	})

	t.Run("capacity", func(t *testing.T) {
		c := newDirectCircuit(directCircuitOptions{Limit: 1})
		active, reason := c.admit(directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"})
		require.Equal(t, directCircuitAllowed, reason)
		_, reason = c.admit(directCircuitKey{TargetAgentID: "b", AddressFingerprint: "fp"})
		require.Equal(t, directCircuitDeniedCapacity, reason)
		c.cancelled(active)
	})

	t.Run("closed", func(t *testing.T) {
		c := newDirectCircuit(directCircuitOptions{})
		c.close()
		_, reason := c.admit(directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"})
		require.Equal(t, directCircuitDeniedClosed, reason)
	})
}

func TestDirectCircuitIgnoresCompletionFromGenerationBeforeReset(t *testing.T) {
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Minute, Limit: 2})
	key := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	stale, ok := c.allow(key)
	require.True(t, ok)
	c.reset("a", "fp")
	current, ok := c.allow(key)
	require.True(t, ok)

	c.transportFailed(stale)
	probe, ok := c.allow(key)
	require.True(t, ok, "completion from before reset must not open the replacement state")
	c.cancelled(probe)
	c.cancelled(current)
}

func TestDirectCircuitIgnoresCompletionFromEvictedGeneration(t *testing.T) {
	c := newDirectCircuit(directCircuitOptions{FailureThreshold: 1, OpenFor: time.Minute, Limit: 1})
	a := directCircuitKey{TargetAgentID: "a", AddressFingerprint: "fp"}
	b := directCircuitKey{TargetAgentID: "b", AddressFingerprint: "fp"}
	staleA, ok := c.allow(a)
	require.True(t, ok)
	c.transportFailed(staleA)
	permitB, ok := c.allow(b)
	require.True(t, ok)
	c.cancelled(permitB)
	currentA, ok := c.allow(a)
	require.True(t, ok)

	c.transportFailed(staleA)
	probe, ok := c.allow(a)
	require.True(t, ok, "completion from an evicted generation must not mutate the replacement state")
	c.cancelled(probe)
	c.cancelled(currentA)
}
