package tunnel

import (
	"context"
	"sync"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

// drainLeftover closes any healthy session that was built but never consumed
// by a dial (e.g. a dial preempted by shutdown). Such a socket is not owned by
// the pool; closing it here keeps the exactly-once accounting complete.
func (d *blockingDirectDialer) drainLeftover() {
	for {
		select {
		case result := <-d.release:
			if result.session != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = result.session.Close(ctx)
				cancel()
			}
		default:
			return
		}
	}
}

// TestDirectConcurrentCloseClosesEachSocketOnce drives idle eviction,
// replacement, and shutdown concurrently and asserts every hijacked socket is
// closed exactly once. Sessions are only built on the test goroutine because
// websocketPair uses require.
func TestDirectConcurrentCloseClosesEachSocketOnce(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Minute})

	targets := []string{"agent-b", "agent-c", "agent-d"}
	for _, name := range targets {
		inflight := startOpen(pool.DirectSessionPool, t.Context(), target(name))
		dialer.releaseHealthySession()
		require.NoError(t, <-inflight.err)
	}
	require.Eventually(t, func() bool { return pool.Snapshot().Active == 3 }, 2*time.Second, 5*time.Millisecond)

	var wg sync.WaitGroup
	// Concurrent shutdown.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = pool.Close(ctx)
	}()
	// Concurrent idle sweep pressure.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.clock.Advance(2 * time.Minute)
	}()

	// Replacement is driven from the test goroutine (session building is not
	// goroutine-safe). It may or may not win against shutdown.
	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	replacement := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	deadline := time.Now().Add(time.Second)
	for dialer.calls.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if dialer.calls.Load() >= 4 {
		dialer.releaseHealthySession()
	}
	<-replacement.err
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = pool.Close(ctx)
	select {
	case <-pool.Done():
	case <-ctx.Done():
		t.Fatal("pool did not close")
	}
	dialer.drainLeftover()

	snap := pool.Snapshot()
	require.Equal(t, 0, snap.Active)
	require.Equal(t, 0, snap.Draining)
	require.Equal(t, 0, snap.Candidates)
	require.Equal(t, 0, snap.Sockets)
	for i, count := range dialer.closeCounts() {
		require.Equal(t, int64(1), count, "socket %d closed %d times", i, count)
	}
}

func TestDirectSessionIdleStateConcurrentTransitions(t *testing.T) {
	session := newSession(newMemorySessionConn(), 1, testLimits(2), SessionOptions{Direction: SessionDirectionRelay})
	defer session.Close(context.Background())

	var workers conc.WaitGroup
	workers.Go(func() {
		for range 1_000 {
			if session.acquireAdmission() {
				session.releaseAdmission()
			}
		}
	})
	workers.Go(func() {
		for range 1_000 {
			stream := &Stream{session: session, id: testStreamID(1), generation: session.Generation()}
			if session.admitStream(stream) == nil {
				session.removeStream(stream)
			}
		}
	})
	workers.Go(func() {
		for range 1_000 {
			_, _ = session.idleSinceTime()
		}
	})
	workers.Wait()

	_, idle := session.idleSinceTime()
	require.True(t, idle)
}

func TestDirectSessionPoolPromotionDoesNotInheritEndedActivePendingDrain(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})
	settings := pool.currentRuntimeSettings()
	desired := target("agent-b")
	key, endpoint, err := pool.desiredKey(desired)
	require.NoError(t, err)
	fingerprint, _, _, err := pool.desiredFingerprint(endpoint)
	require.NoError(t, err)
	candidate := &directDialCandidate{
		target: desired.TargetAgentID, key: key, fingerprint: fingerprint,
		frozen: desired, done: make(chan struct{}),
	}
	oldSession := dialer.buildSession()
	newSession := dialer.buildSession()
	blockingDrain := dialer.buildSession()
	defer oldSession.Close(context.Background())
	defer newSession.Close(context.Background())
	defer blockingDrain.Close(context.Background())

	pool.mu.Lock()
	slot := &directPoolSlot{target: desired.TargetAgentID, active: oldSession, candidate: candidate}
	pool.slots[desired.TargetAgentID] = slot
	pool.drains[blockingDrain] = struct{}{}
	pool.markDrainPendingLocked(slot, directLifecycleEvicted)
	pool.mu.Unlock()

	pool.onSessionEnd(oldSession)
	require.True(t, pool.finishCandidate(candidate, newSession, nil))
	pool.mu.Lock()
	activeAfterPromotion := slot.active
	pendingAfterPromotion := slot.drainPending
	pool.mu.Unlock()
	require.Same(t, newSession, activeAfterPromotion)
	require.False(t, pendingAfterPromotion, "new active inherited the ended generation's pending drain")

	for range 2 {
		reused, acquireErr := pool.acquire(t.Context(), desired)
		require.NoError(t, acquireErr)
		require.Same(t, newSession, reused)
		reused.releaseAdmission()
	}

	pool.mu.Lock()
	delete(pool.drains, blockingDrain)
	notices := pool.reconcileLocked(pool.clock.Now(), settings)
	activeAfterReconcile := slot.active
	pendingAfterReconcile := slot.drainPending
	_, wronglyDraining := pool.drains[newSession]
	pool.mu.Unlock()
	require.Empty(t, notices)
	require.Same(t, newSession, activeAfterReconcile)
	require.False(t, pendingAfterReconcile)
	require.False(t, wronglyDraining, "next reconcile drained the new generation using stale pending state")
}
