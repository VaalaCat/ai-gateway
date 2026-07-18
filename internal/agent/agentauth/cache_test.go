package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

var cacheTestNow = time.Unix(2_000_000_000, 0).UTC()

type cacheTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newCacheTestClock() *cacheTestClock {
	return &cacheTestClock{now: cacheTestNow}
}

func (c *cacheTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *cacheTestClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type cacheTestControl struct {
	mu      sync.Mutex
	calls   map[string]int
	handler func(context.Context, string, any) (json.RawMessage, error)
}

func newCacheTestControl(handler func(context.Context, string, any) (json.RawMessage, error)) *cacheTestControl {
	return &cacheTestControl{calls: make(map[string]int), handler: handler}
}

func (c *cacheTestControl) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.calls[method]++
	handler := c.handler
	c.mu.Unlock()
	if handler == nil {
		return nil, errors.New("unexpected control call")
	}
	return handler(ctx, method, params)
}

func (c *cacheTestControl) Calls(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[method]
}

func TestTicketCacheBootstrapSnapshotIsDefensiveAndCloseClearsOwnedState(t *testing.T) {
	clock := newCacheTestClock()
	key := pkgagentauth.PublicKey{KeyID: "key-a", Algorithm: "EdDSA", Key: make([]byte, 32)}
	control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case consts.RPCAgentAuthBootstrap:
			return jsonResult(protocol.AuthBootstrapResponse{
				MasterInstanceID: "master-a",
				Capabilities: []string{
					protocol.AgentCapabilityTunnelV1,
					protocol.AgentCapabilityForwardV1,
				},
				SigningKeys: []pkgagentauth.PublicKey{key},
			})
		case consts.RPCAgentIssueRelayTicket:
			return ticketJSON("relay-secret", clock.Now().Add(10*time.Minute)), nil
		case consts.RPCAgentIssueForwardTicket:
			return ticketJSON("forward-secret", clock.Now().Add(10*time.Minute)), nil
		default:
			return nil, errors.New("unexpected method")
		}
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))

	first := cache.Bootstrap()
	require.Equal(t, "master-a", first.MasterInstanceID)
	require.Equal(t, []string{protocol.AgentCapabilityForwardV1, protocol.AgentCapabilityTunnelV1}, first.Capabilities)
	require.Equal(t, []pkgagentauth.PublicKey{key}, first.SigningKeys)
	first.MasterInstanceID = "mutated"
	first.Capabilities[0] = "mutated"
	first.SigningKeys[0].Key[0] = 99
	require.Equal(t, BootstrapSnapshot{
		MasterInstanceID: "master-a",
		Capabilities:     []string{protocol.AgentCapabilityForwardV1, protocol.AgentCapabilityTunnelV1},
		SigningKeys:      []pkgagentauth.PublicKey{key},
	}, cache.Bootstrap())

	relay, err := cache.RelayTicket(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("relay-secret"), relay)
	require.Eventually(t, func() bool {
		forward, err := cache.CachedForwardTicket()
		return err == nil && forward == pkgagentauth.ForwardTicket("forward-secret")
	}, time.Second, time.Millisecond, "background owner must cache the first forward ticket")

	done := cache.Done()
	cache.Close()
	cache.Close()
	requireClosed(t, done, "cache close")
	require.Equal(t, done, cache.Done())
	require.Equal(t, BootstrapSnapshot{}, cache.Bootstrap())

	cache.mu.Lock()
	require.Empty(t, cache.relayTickets)
	require.False(t, cache.hasForward)
	require.Empty(t, cache.forward.token)
	require.Empty(t, cache.bootstrap.Capabilities)
	require.Empty(t, cache.bootstrap.SigningKeys)
	cache.mu.Unlock()
}

func TestTicketCacheUsesDefaultRelayTTLRefreshAndNormalizesInvalidFractions(t *testing.T) {
	tests := []struct {
		name     string
		fraction float64
	}{
		{name: "zero default", fraction: 0},
		{name: "negative", fraction: -0.1},
		{name: "one", fraction: 1},
		{name: "above one", fraction: 2},
		{name: "nan", fraction: math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newCacheTestClock()
			var issues atomic.Int32
			control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(method string, _ any) (json.RawMessage, error) {
				require.Equal(t, consts.RPCAgentIssueRelayTicket, method)
				n := issues.Add(1)
				return ticketJSON(fmt.Sprintf("relay-%d", n), cacheTestNow.Add(10*time.Minute)), nil
			})
			cache := NewCache(control, CacheOptions{Now: clock.Now, RelayRefreshFraction: tc.fraction})
			require.NoError(t, cache.Run(context.Background()))
			t.Cleanup(func() { closeTicketCache(t, cache) })

			got, err := cache.RelayTicket(context.Background(), 7)
			require.NoError(t, err)
			require.Equal(t, pkgagentauth.RelayTicket("relay-1"), got)
			clock.Set(cacheTestNow.Add(5*time.Minute - time.Second))
			got, err = cache.RelayTicket(context.Background(), 7)
			require.NoError(t, err)
			require.Equal(t, pkgagentauth.RelayTicket("relay-1"), got)
			clock.Set(cacheTestNow.Add(5 * time.Minute))
			got, err = cache.RelayTicket(context.Background(), 7)
			require.NoError(t, err)
			require.Equal(t, pkgagentauth.RelayTicket("relay-2"), got)
			require.Equal(t, int32(2), issues.Load())
		})
	}
}

func TestTicketCacheRefreshFailureUsesValidTicketWithoutBusyLoopAndRejectsItAtExpiry(t *testing.T) {
	clock := newCacheTestClock()
	marker := "relay-secret-marker"
	var issues atomic.Int32
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		if issues.Add(1) == 1 {
			return ticketJSON("relay-valid", cacheTestNow.Add(10*time.Minute)), nil
		}
		return nil, errors.New(marker)
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })

	got, err := cache.RelayTicket(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("relay-valid"), got)
	clock.Set(cacheTestNow.Add(6 * time.Minute))
	got, err = cache.RelayTicket(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("relay-valid"), got)
	got, err = cache.RelayTicket(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("relay-valid"), got)
	require.Equal(t, int32(2), issues.Load(), "same instant must honor failure backoff")

	clock.Set(cacheTestNow.Add(10 * time.Minute))
	got, err = cache.RelayTicket(context.Background(), 9)
	require.Error(t, err)
	require.Empty(t, got)
	require.NotContains(t, err.Error(), marker)
	require.NotContains(t, err.Error(), "relay-valid")
	require.Equal(t, int32(3), issues.Load())
}

func TestTicketCacheRejectsInvalidTicketResponsesWithoutCaching(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		method     string
		response   protocol.TicketResponse
	}{
		{
			name:       "relay empty token",
			capability: protocol.AgentCapabilityTunnelV1,
			method:     consts.RPCAgentIssueRelayTicket,
			response:   protocol.TicketResponse{ExpiresAt: cacheTestNow.Add(time.Minute).Unix()},
		},
		{
			name:       "relay expires now",
			capability: protocol.AgentCapabilityTunnelV1,
			method:     consts.RPCAgentIssueRelayTicket,
			response:   protocol.TicketResponse{Token: "relay-expired-secret", ExpiresAt: cacheTestNow.Unix()},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newCacheTestClock()
			control := ticketCacheControl(clock, []string{tc.capability}, func(method string, _ any) (json.RawMessage, error) {
				require.Equal(t, tc.method, method)
				return jsonResult(tc.response)
			})
			cache := NewCache(control, CacheOptions{Now: clock.Now})
			require.NoError(t, cache.Run(context.Background()))
			t.Cleanup(func() { closeTicketCache(t, cache) })

			issue := func() error {
				ticket, err := cache.RelayTicket(context.Background(), 0)
				require.Empty(t, ticket)
				require.Error(t, err)
				if tc.response.Token != "" {
					require.NotContains(t, err.Error(), tc.response.Token)
				}
				return err
			}
			for range 2 {
				_ = issue()
			}
			require.Equal(t, 1, control.Calls(tc.method), "invalid responses must enter failure cooldown")
			clock.Set(cacheTestNow.Add(time.Second))
			_ = issue()
			require.Equal(t, 2, control.Calls(tc.method), "invalid responses must not enter the ticket cache")
		})
	}
}

func TestTicketCacheRefreshOwnersShareSameRelayGenerationAndSeparateDifferentGenerations(t *testing.T) {
	t.Run("same generation", func(t *testing.T) {
		previousProcs := runtime.GOMAXPROCS(1)
		defer runtime.GOMAXPROCS(previousProcs)
		clock := newCacheTestClock()
		issued := make(chan uint64, 1)
		release := make(chan struct{})
		control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, params any) (json.RawMessage, error) {
			generation := requireRelayGeneration(t, params)
			issued <- generation
			<-release
			return ticketJSON("shared-relay", cacheTestNow.Add(time.Hour)), nil
		})
		cache := NewCache(control, CacheOptions{Now: clock.Now})
		require.NoError(t, cache.Run(context.Background()))
		t.Cleanup(func() { closeTicketCache(t, cache) })

		const callers = 16
		ready := make(chan struct{}, callers)
		start := make(chan struct{})
		results := make(chan relayTicketResult, callers)
		for range callers {
			go func() {
				ready <- struct{}{}
				<-start
				ticket, err := cache.RelayTicket(context.Background(), 11)
				results <- relayTicketResult{ticket: ticket, err: err}
			}()
		}
		for range callers {
			<-ready
		}
		close(start)
		require.Equal(t, uint64(11), receiveWithTimeout(t, issued, "relay issue"))
		close(release)
		for range callers {
			result := receiveWithTimeout(t, results, "relay result")
			require.NoError(t, result.err)
			require.Equal(t, pkgagentauth.RelayTicket("shared-relay"), result.ticket)
		}
		require.Equal(t, 1, control.Calls(consts.RPCAgentIssueRelayTicket))
	})

	t.Run("different generations", func(t *testing.T) {
		clock := newCacheTestClock()
		issued := make(chan uint64, 2)
		release := make(chan struct{})
		control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, params any) (json.RawMessage, error) {
			generation := requireRelayGeneration(t, params)
			issued <- generation
			<-release
			return ticketJSON(fmt.Sprintf("relay-%d", generation), cacheTestNow.Add(time.Hour)), nil
		})
		cache := NewCache(control, CacheOptions{Now: clock.Now})
		require.NoError(t, cache.Run(context.Background()))
		t.Cleanup(func() { closeTicketCache(t, cache) })

		results := make(chan relayTicketResult, 2)
		for _, generation := range []uint64{1, 2} {
			go func() {
				ticket, err := cache.RelayTicket(context.Background(), generation)
				results <- relayTicketResult{ticket: ticket, err: err}
			}()
		}
		generations := []uint64{
			receiveWithTimeout(t, issued, "first generation issue"),
			receiveWithTimeout(t, issued, "second generation issue"),
		}
		sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
		require.Equal(t, []uint64{1, 2}, generations)
		close(release)
		for range 2 {
			result := receiveWithTimeout(t, results, "generation result")
			require.NoError(t, result.err)
			require.NotEmpty(t, result.ticket)
		}
		require.Equal(t, 2, control.Calls(consts.RPCAgentIssueRelayTicket))
	})
}

func TestRelayRefreshOwnershipRechecksStaleDueDecisionBeforeCreatingOwner(t *testing.T) {
	clock := newCacheTestClock()
	var issues atomic.Int32
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(method string, params any) (json.RawMessage, error) {
		require.Equal(t, consts.RPCAgentIssueRelayTicket, method)
		require.Equal(t, uint64(17), requireRelayGeneration(t, params))
		n := issues.Add(1)
		return ticketJSON(fmt.Sprintf("relay-%d", n), clock.Now().Add(10*time.Minute)), nil
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })

	initial, err := cache.RelayTicket(context.Background(), 17)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("relay-1"), initial)
	clock.Set(cacheTestNow.Add(5 * time.Minute))

	firstEntry, firstDue, err := cache.relayEntry(17, clock.Now())
	require.NoError(t, err)
	secondEntry, secondDue, err := cache.relayEntry(17, clock.Now())
	require.NoError(t, err)
	require.True(t, firstDue)
	require.True(t, secondDue)
	require.Equal(t, "relay-1", firstEntry.token)
	require.Equal(t, "relay-1", secondEntry.token)

	firstRefresh, err := cache.waitForRelayRefresh(context.Background(), 17)
	require.NoError(t, err)
	require.Equal(t, "relay-2", firstRefresh.token)

	secondRefresh, err := cache.waitForRelayRefresh(context.Background(), 17)
	require.NoError(t, err)
	require.Equal(t, "relay-2", secondRefresh.token, "stale due decision must reuse the completed refresh")
	require.Equal(t, int32(2), issues.Load(), "initial issue plus one due refresh")
}

func TestCacheNowRunsOutsideMutex(t *testing.T) {
	t.Run("cached forward ticket", func(t *testing.T) {
		var cache *Cache
		var calledWithLock atomic.Bool
		var calls atomic.Int32
		now := func() time.Time {
			calls.Add(1)
			if !cache.mu.TryLock() {
				calledWithLock.Store(true)
				return cacheTestNow
			}
			cache.mu.Unlock()
			return cacheTestNow
		}
		cache = NewCache(nil, CacheOptions{Now: now})
		runCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cache.mu.Lock()
		cache.started = true
		cache.runCtx = runCtx
		cache.bootstrap.Capabilities = []string{protocol.AgentCapabilityForwardV1}
		cache.forward = ticketEntry{
			token:     "forward-cached",
			issuedAt:  cacheTestNow,
			expiresAt: cacheTestNow.Add(7 * 24 * time.Hour),
		}
		cache.hasForward = true
		cache.mu.Unlock()

		ticket, err := cache.CachedForwardTicket()
		require.NoError(t, err)
		require.Equal(t, pkgagentauth.ForwardTicket("forward-cached"), ticket)
		require.Equal(t, int32(1), calls.Load())
		require.False(t, calledWithLock.Load(), "CachedForwardTicket called Now while holding cache.mu")
	})

	t.Run("stale relay owner admission", func(t *testing.T) {
		var cache *Cache
		var calledWithLock atomic.Bool
		var calls atomic.Int32
		now := cacheTestNow.Add(5 * time.Minute)
		clock := func() time.Time {
			calls.Add(1)
			if !cache.mu.TryLock() {
				calledWithLock.Store(true)
				return now
			}
			cache.mu.Unlock()
			return now
		}
		control := newCacheTestControl(func(context.Context, string, any) (json.RawMessage, error) {
			return nil, errors.New("stale admission must not issue another relay ticket")
		})
		cache = NewCache(control, CacheOptions{Now: clock})
		runCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cache.mu.Lock()
		cache.started = true
		cache.runCtx = runCtx
		cache.bootstrap.Capabilities = []string{protocol.AgentCapabilityTunnelV1}
		cache.relayTickets[17] = ticketEntry{
			token:     "relay-refreshed",
			issuedAt:  now,
			expiresAt: now.Add(10 * time.Minute),
		}
		cache.mu.Unlock()

		entry, err := cache.waitForRelayRefresh(context.Background(), 17)
		require.NoError(t, err)
		require.Equal(t, "relay-refreshed", entry.token)
		require.Equal(t, int32(1), calls.Load())
		require.Zero(t, control.Calls(consts.RPCAgentIssueRelayTicket))
		require.False(t, calledWithLock.Load(), "relay owner admission called Now while holding cache.mu")
	})
}

func TestForwardCacheRunImmediatelyIssuesFirstTicketWithoutRequestTrigger(t *testing.T) {
	clock := newCacheTestClock()
	issued := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityForwardV1}, func(method string, _ any) (json.RawMessage, error) {
		require.Equal(t, consts.RPCAgentIssueForwardTicket, method)
		close(issued)
		<-release
		return ticketJSON("forward-1", cacheTestNow.Add(7*24*time.Hour)), nil
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		closeTicketCache(t, cache)
	})

	receiveWithTimeout(t, issued, "immediate forward issue")
	ticket, err := cache.CachedForwardTicket()
	require.Error(t, err, "a blocked background owner must not make the cache appear populated")
	require.Empty(t, ticket)
	releaseOnce.Do(func() { close(release) })
	require.Eventually(t, func() bool {
		ticket, err = cache.CachedForwardTicket()
		return err == nil && ticket == pkgagentauth.ForwardTicket("forward-1")
	}, time.Second, time.Millisecond)
	require.Equal(t, 1, control.Calls(consts.RPCAgentIssueForwardTicket))
}

func TestForwardCacheCloseCancelsBlockedInitialIssueAndDoneWaitsForReturn(t *testing.T) {
	callStarted := make(chan struct{}, 1)
	callCanceled := make(chan error, 1)
	releaseReturn := make(chan struct{})
	callReturned := make(chan struct{}, 1)
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			close(releaseReturn)
		}
	}
	control := newCacheTestControl(func(ctx context.Context, method string, _ any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityForwardV1}), nil
		}
		if method != consts.RPCAgentIssueForwardTicket {
			return nil, fmt.Errorf("unexpected control method %q", method)
		}
		callStarted <- struct{}{}
		<-ctx.Done()
		callCanceled <- ctx.Err()
		<-releaseReturn
		callReturned <- struct{}{}
		return nil, ctx.Err()
	})
	cache := NewCache(control, CacheOptions{})
	t.Cleanup(func() {
		release()
		cache.Close()
		requireClosed(t, cache.Done(), "forward owner cleanup")
	})

	require.NoError(t, cache.Run(context.Background()))
	receiveWithTimeout(t, callStarted, "initial forward issue")
	cache.Close()
	require.ErrorIs(t, receiveWithTimeout(t, callCanceled, "initial forward issue cancellation"), context.Canceled)
	select {
	case <-cache.Done():
		t.Fatal("Cache.Done closed before the canceled initial forward issue returned")
	default:
	}

	release()
	receiveWithTimeout(t, callReturned, "initial forward issue return")
	requireClosed(t, cache.Done(), "initial forward owner shutdown")
	ticket, err := cache.CachedForwardTicket()
	require.ErrorIs(t, err, errCacheClosed)
	require.Empty(t, ticket)
	require.Equal(t, 1, control.Calls(consts.RPCAgentIssueForwardTicket))
}

func TestForwardCacheRefreshesAtConfiguredAgeBoundary(t *testing.T) {
	tests := []struct {
		name         string
		configured   time.Duration
		refreshAfter time.Duration
	}{
		{name: "default", refreshAfter: 24 * time.Hour},
		{name: "negative normalizes to default", configured: -time.Second, refreshAfter: 24 * time.Hour},
		{name: "custom", configured: 3 * time.Hour, refreshAfter: 3 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newCacheTestClock()
			var issues atomic.Int32
			control := ticketCacheControl(clock, []string{protocol.AgentCapabilityForwardV1}, func(method string, _ any) (json.RawMessage, error) {
				require.Equal(t, consts.RPCAgentIssueForwardTicket, method)
				n := issues.Add(1)
				return ticketJSON(fmt.Sprintf("forward-%d", n), clock.Now().Add(7*24*time.Hour)), nil
			})
			cache := NewCache(control, CacheOptions{
				Now:                 clock.Now,
				ForwardRefreshAfter: tc.configured,
			})
			require.NoError(t, cache.Run(context.Background()))
			t.Cleanup(func() { closeTicketCache(t, cache) })
			requireCachedForwardTicket(t, cache, "forward-1")

			clock.Set(cacheTestNow.Add(tc.refreshAfter - time.Second))
			refreshTicketsForTest(t, cache)
			require.Equal(t, int32(1), issues.Load(), "refresh must not run before the age boundary")
			requireCachedForwardTicket(t, cache, "forward-1")

			clock.Set(cacheTestNow.Add(tc.refreshAfter))
			refreshTicketsForTest(t, cache)
			require.Equal(t, int32(2), issues.Load(), "refresh must run exactly at the age boundary")
			requireCachedForwardTicket(t, cache, "forward-2")
		})
	}
}

func TestForwardCacheFailureCooldownKeepsValidTicketAndRetries(t *testing.T) {
	clock := newCacheTestClock()
	marker := "private-forward-control-marker"
	var issues atomic.Int32
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityForwardV1}, func(method string, _ any) (json.RawMessage, error) {
		require.Equal(t, consts.RPCAgentIssueForwardTicket, method)
		switch issues.Add(1) {
		case 1:
			return ticketJSON("forward-old-secret", cacheTestNow.Add(7*24*time.Hour)), nil
		case 2:
			return nil, errors.New(marker)
		default:
			return ticketJSON("forward-new", clock.Now().Add(7*24*time.Hour)), nil
		}
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	requireCachedForwardTicket(t, cache, "forward-old-secret")

	clock.Set(cacheTestNow.Add(24 * time.Hour))
	refreshTicketsForTest(t, cache)
	require.Equal(t, int32(2), issues.Load())
	ticket, err := cache.CachedForwardTicket()
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.ForwardTicket("forward-old-secret"), ticket)

	refreshTicketsForTest(t, cache)
	clock.Set(cacheTestNow.Add(24*time.Hour + 999*time.Millisecond))
	refreshTicketsForTest(t, cache)
	require.Equal(t, int32(2), issues.Load(), "failure cooldown must prevent a busy loop")

	clock.Set(cacheTestNow.Add(24*time.Hour + time.Second))
	refreshTicketsForTest(t, cache)
	require.Equal(t, int32(3), issues.Load(), "background owner must retry at the cooldown boundary")
	requireCachedForwardTicket(t, cache, "forward-new")
}

func TestForwardRefreshContainsControlPanicAndRecordsCooldown(t *testing.T) {
	marker := "private-forward-panic-secret"
	var nowCalls atomic.Int32
	control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method != consts.RPCAgentIssueForwardTicket {
			return nil, fmt.Errorf("unexpected control method %q", method)
		}
		panic(marker)
	})
	cache := NewCache(control, CacheOptions{Now: func() time.Time {
		nowCalls.Add(1)
		return cacheTestNow
	}})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache.mu.Lock()
	cache.started = true
	cache.runCtx = runCtx
	cache.bootstrap.Capabilities = []string{protocol.AgentCapabilityForwardV1}
	cache.mu.Unlock()

	var refreshErr error
	panicEscaped := false
	func() {
		defer func() {
			panicEscaped = recover() != nil
		}()
		refreshErr = cache.refreshForwardTicket(runCtx)
	}()
	if panicEscaped {
		t.Error("forward refresh panic escaped the Cache boundary")
		return
	}
	require.ErrorIs(t, refreshErr, errTicketRefresh)
	require.NotContains(t, refreshErr.Error(), marker)
	require.Equal(t, int32(1), nowCalls.Load(), "panic path must record the failure cooldown once")
	cache.mu.Lock()
	retryAt := cache.forwardFailure
	cache.mu.Unlock()
	require.Equal(t, cacheTestNow.Add(ticketFailureCooldown), retryAt)
	require.Equal(t, 1, control.Calls(consts.RPCAgentIssueForwardTicket))
}

func TestForwardRefreshLoopSurvivesPanicCooldownAndRecovers(t *testing.T) {
	clock := newCacheTestClock()
	marker := "private-forward-loop-panic-secret"
	firstAttempt := make(chan struct{}, 1)
	var attempts atomic.Int32
	control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityForwardV1}), nil
		}
		if method != consts.RPCAgentIssueForwardTicket {
			return nil, fmt.Errorf("unexpected control method %q", method)
		}
		switch attempts.Add(1) {
		case 1:
			firstAttempt <- struct{}{}
			panic(marker)
		case 2:
			return ticketJSON("forward-recovered", clock.Now().Add(7*24*time.Hour)), nil
		default:
			return nil, errors.New("unexpected extra forward issue")
		}
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	receiveWithTimeout(t, firstAttempt, "panicking initial forward issue")
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return cache.forwardFailure.Equal(cacheTestNow.Add(ticketFailureCooldown))
	}, time.Second, time.Millisecond)
	select {
	case <-cache.Done():
		t.Fatal("forward issue panic stopped the refresh loop")
	default:
	}
	require.Equal(t, "master-a", cache.Bootstrap().MasterInstanceID)
	ticket, err := cache.CachedForwardTicket()
	require.ErrorIs(t, err, errTicketRefresh)
	require.Empty(t, ticket)
	require.NotContains(t, err.Error(), marker)

	refreshTicketsForTest(t, cache)
	clock.Set(cacheTestNow.Add(ticketFailureCooldown - time.Millisecond))
	refreshTicketsForTest(t, cache)
	require.Equal(t, int32(1), attempts.Load(), "cooldown must prevent retry before the boundary")

	clock.Set(cacheTestNow.Add(ticketFailureCooldown))
	refreshTicketsForTest(t, cache)
	require.Equal(t, int32(2), attempts.Load(), "refresh must retry exactly at the cooldown boundary")
	requireCachedForwardTicket(t, cache, "forward-recovered")
	cache.Close()
	requireClosed(t, cache.Done(), "forward panic recovery shutdown")
}

func TestCachedForwardTicketRejectsUnavailableAndExpiredSnapshotsWithoutLeaking(t *testing.T) {
	t.Run("cache not running", func(t *testing.T) {
		cache := NewCache(nil, CacheOptions{})
		t.Cleanup(cache.Close)
		ticket, err := cache.CachedForwardTicket()
		require.ErrorIs(t, err, errCacheNotRunning)
		require.Empty(t, ticket)
	})

	t.Run("capability off", func(t *testing.T) {
		control := ticketCacheControl(nil, []string{protocol.AgentCapabilityTunnelV1}, func(string, any) (json.RawMessage, error) {
			return nil, errors.New("must not issue forward ticket")
		})
		cache := NewCache(control, CacheOptions{})
		require.NoError(t, cache.Run(context.Background()))
		t.Cleanup(func() { closeTicketCache(t, cache) })
		ticket, err := cache.CachedForwardTicket()
		require.ErrorIs(t, err, errCapabilityOff)
		require.Empty(t, ticket)
		require.Zero(t, control.Calls(consts.RPCAgentIssueForwardTicket))
	})

	t.Run("missing after refresh failure", func(t *testing.T) {
		marker := "missing-forward-secret-marker"
		control := ticketCacheControl(nil, []string{protocol.AgentCapabilityForwardV1}, func(string, any) (json.RawMessage, error) {
			return nil, errors.New(marker)
		})
		cache := NewCache(control, CacheOptions{})
		require.NoError(t, cache.Run(context.Background()))
		t.Cleanup(func() { closeTicketCache(t, cache) })
		require.Eventually(t, func() bool {
			cache.mu.Lock()
			defer cache.mu.Unlock()
			return !cache.forwardFailure.IsZero()
		}, time.Second, time.Millisecond)
		ticket, err := cache.CachedForwardTicket()
		require.ErrorIs(t, err, errTicketRefresh)
		require.Empty(t, ticket)
		require.NotContains(t, err.Error(), marker)
	})

	t.Run("strict expiry boundary", func(t *testing.T) {
		clock := newCacheTestClock()
		control := ticketCacheControl(clock, []string{protocol.AgentCapabilityForwardV1}, func(string, any) (json.RawMessage, error) {
			return ticketJSON("expired-forward-secret", cacheTestNow.Add(7*24*time.Hour)), nil
		})
		cache := NewCache(control, CacheOptions{Now: clock.Now})
		require.NoError(t, cache.Run(context.Background()))
		t.Cleanup(func() { closeTicketCache(t, cache) })
		requireCachedForwardTicket(t, cache, "expired-forward-secret")
		calls := control.Calls(consts.RPCAgentIssueForwardTicket)

		clock.Set(cacheTestNow.Add(7 * 24 * time.Hour))
		ticket, err := cache.CachedForwardTicket()
		require.ErrorIs(t, err, errTicketRefresh)
		require.Empty(t, ticket)
		require.NotContains(t, err.Error(), "expired-forward-secret")
		require.Equal(t, calls, control.Calls(consts.RPCAgentIssueForwardTicket), "cache reads must not issue tickets")
	})

	t.Run("closed", func(t *testing.T) {
		control := ticketCacheControl(nil, []string{protocol.AgentCapabilityForwardV1}, func(string, any) (json.RawMessage, error) {
			return ticketJSON("closed-forward-secret", time.Now().Add(7*24*time.Hour)), nil
		})
		cache := NewCache(control, CacheOptions{})
		require.NoError(t, cache.Run(context.Background()))
		requireCachedForwardTicket(t, cache, "closed-forward-secret")
		closeTicketCache(t, cache)
		ticket, err := cache.CachedForwardTicket()
		require.ErrorIs(t, err, errCacheClosed)
		require.Empty(t, ticket)
		require.NotContains(t, err.Error(), "closed-forward-secret")
	})
}

func TestCachedForwardTicketRejectsInvalidIssueResponseWithoutCaching(t *testing.T) {
	clock := newCacheTestClock()
	marker := "invalid-forward-secret"
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityForwardV1}, func(method string, _ any) (json.RawMessage, error) {
		require.Equal(t, consts.RPCAgentIssueForwardTicket, method)
		return ticketJSON(marker, clock.Now()), nil
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return !cache.forwardFailure.IsZero()
	}, time.Second, time.Millisecond)

	for range 2 {
		ticket, err := cache.CachedForwardTicket()
		require.ErrorIs(t, err, errTicketRefresh)
		require.Empty(t, ticket)
		require.NotContains(t, err.Error(), marker)
	}
	require.Equal(t, 1, control.Calls(consts.RPCAgentIssueForwardTicket))
	clock.Set(cacheTestNow.Add(time.Second))
	refreshTicketsForTest(t, cache)
	require.Equal(t, 2, control.Calls(consts.RPCAgentIssueForwardTicket))
}

func TestCachedForwardTicketStaysReadOnlyWhileBackgroundRefreshBlocks(t *testing.T) {
	clock := newCacheTestClock()
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	var issues atomic.Int32
	control := newCacheTestControl(func(ctx context.Context, method string, _ any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityForwardV1}), nil
		}
		require.Equal(t, consts.RPCAgentIssueForwardTicket, method)
		switch issues.Add(1) {
		case 1:
			return ticketJSON("forward-1", cacheTestNow.Add(7*24*time.Hour)), nil
		case 2:
			close(refreshStarted)
			select {
			case <-releaseRefresh:
				return ticketJSON("forward-2", clock.Now().Add(7*24*time.Hour)), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			return nil, errors.New("unexpected extra forward issue")
		}
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRefresh) })
		closeTicketCache(t, cache)
	})
	requireCachedForwardTicket(t, cache, "forward-1")

	clock.Set(cacheTestNow.Add(25 * time.Hour))
	receiveWithTimeout(t, refreshStarted, "blocked background forward refresh")
	require.Equal(t, int32(2), issues.Load())

	readers := pool.NewWithResults[forwardTicketResult]().WithMaxGoroutines(20)
	for range 100 {
		readers.Go(func() forwardTicketResult {
			ticket, err := cache.CachedForwardTicket()
			return forwardTicketResult{ticket: ticket, err: err}
		})
	}
	readResults := make(chan []forwardTicketResult, 1)
	go func() { readResults <- readers.Wait() }()
	var snapshots []forwardTicketResult
	select {
	case snapshots = <-readResults:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("100 cached forward reads blocked behind the control RPC")
	}
	for _, result := range snapshots {
		require.NoError(t, result.err)
		require.Equal(t, pkgagentauth.ForwardTicket("forward-1"), result.ticket)
	}
	require.Equal(t, int32(2), issues.Load(), "cached reads must not trigger another control RPC")

	startConcurrentReaders := make(chan struct{})
	concurrentReaders := pool.NewWithResults[forwardTicketResult]()
	for range 32 {
		concurrentReaders.Go(func() forwardTicketResult {
			<-startConcurrentReaders
			var result forwardTicketResult
			for range 100 {
				result.ticket, result.err = cache.CachedForwardTicket()
				if result.err != nil {
					return result
				}
			}
			return result
		})
	}
	close(startConcurrentReaders)
	releaseOnce.Do(func() { close(releaseRefresh) })
	for _, result := range concurrentReaders.Wait() {
		require.NoError(t, result.err)
		require.Contains(t, []pkgagentauth.ForwardTicket{"forward-1", "forward-2"}, result.ticket)
	}
	requireCachedForwardTicket(t, cache, "forward-2")
	require.Equal(t, int32(2), issues.Load())
}

func TestTicketCacheCallerCancellationDoesNotCancelSharedRefresh(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	clock := newCacheTestClock()
	issued := make(chan struct{}, 1)
	underlyingCanceled := make(chan struct{}, 1)
	release := make(chan struct{})
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		issued <- struct{}{}
		select {
		case <-release:
			return ticketJSON("shared-after-cancel", cacheTestNow.Add(time.Hour)), nil
		case <-contextDoneForTest():
			return nil, errors.New("unreachable test context")
		}
	})
	control.handler = func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityTunnelV1}), nil
		}
		issued <- struct{}{}
		select {
		case <-release:
			return ticketJSON("shared-after-cancel", cacheTestNow.Add(time.Hour)), nil
		case <-ctx.Done():
			underlyingCanceled <- struct{}{}
			return nil, ctx.Err()
		}
	}
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan relayTicketResult, 1)
	go func() {
		ticket, err := cache.RelayTicket(firstCtx, 4)
		firstResult <- relayTicketResult{ticket: ticket, err: err}
	}()
	receiveWithTimeout(t, issued, "shared issue")
	cancelFirst()
	first := receiveWithTimeout(t, firstResult, "canceled waiter")
	require.ErrorIs(t, first.err, context.Canceled)
	require.Empty(t, first.ticket)

	secondCalling := make(chan struct{})
	secondResult := make(chan relayTicketResult, 1)
	go func() {
		close(secondCalling)
		ticket, err := cache.RelayTicket(context.Background(), 4)
		secondResult <- relayTicketResult{ticket: ticket, err: err}
	}()
	<-secondCalling
	runtime.Gosched()
	select {
	case <-underlyingCanceled:
		t.Fatal("one canceled waiter canceled the shared Control RPC")
	default:
	}
	close(release)
	second := receiveWithTimeout(t, secondResult, "remaining waiter")
	require.NoError(t, second.err)
	require.Equal(t, pkgagentauth.RelayTicket("shared-after-cancel"), second.ticket)
	require.Equal(t, 1, control.Calls(consts.RPCAgentIssueRelayTicket))
	cache.mu.Lock()
	require.Empty(t, cache.relayFailures, "caller cancellation must not create a failure cooldown")
	cache.mu.Unlock()
}

func TestTicketCacheCloseCancelsBlockedControlCallAndAllWaiters(t *testing.T) {
	clock := newCacheTestClock()
	issued := make(chan struct{}, 1)
	underlyingDone := make(chan error, 1)
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		panic("ticket handler must receive cache context")
	})
	control.handler = func(ctx context.Context, method string, _ any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityTunnelV1}), nil
		}
		issued <- struct{}{}
		<-ctx.Done()
		underlyingDone <- ctx.Err()
		return nil, ctx.Err()
	}
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))

	results := make(chan relayTicketResult, 2)
	for range 2 {
		go func() {
			ticket, err := cache.RelayTicket(context.Background(), 5)
			results <- relayTicketResult{ticket: ticket, err: err}
		}()
	}
	receiveWithTimeout(t, issued, "blocked ticket issue")
	done := cache.Done()
	cache.Close()
	require.ErrorIs(t, receiveWithTimeout(t, underlyingDone, "underlying cancellation"), context.Canceled)
	for range 2 {
		result := receiveWithTimeout(t, results, "canceled cache waiter")
		require.Error(t, result.err)
		require.Empty(t, result.ticket)
	}
	requireClosed(t, done, "cache done")
	cache.mu.Lock()
	require.Empty(t, cache.relayFailures)
	require.True(t, cache.forwardFailure.IsZero())
	cache.mu.Unlock()
}

func TestTicketCacheDoneWaitsForCanceledSharedRefreshWorkerToReturn(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	clock := newCacheTestClock()
	callStarted := make(chan struct{})
	sawCancel := make(chan struct{})
	releaseReturn := make(chan struct{})
	callReturned := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseReturn) })
	}
	control := newCacheTestControl(func(ctx context.Context, method string, _ any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityTunnelV1}), nil
		}
		close(callStarted)
		<-ctx.Done()
		close(sawCancel)
		<-releaseReturn
		close(callReturned)
		return nil, ctx.Err()
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	waiter := make(chan relayTicketResult, 1)
	go func() {
		ticket, err := cache.RelayTicket(context.Background(), 1)
		waiter <- relayTicketResult{ticket: ticket, err: err}
	}()
	<-callStarted
	t.Cleanup(func() {
		release()
		receiveWithTimeout(t, callReturned, "refresh call cleanup")
	})
	cache.Close()
	<-sawCancel
	result := receiveWithTimeout(t, waiter, "canceled refresh waiter")
	require.Error(t, result.err)
	require.Empty(t, result.ticket)
	runtime.Gosched()
	select {
	case <-cache.Done():
		t.Fatal("Cache.Done closed while the shared refresh worker was still running")
	default:
	}

	release()
	<-callReturned
	requireClosed(t, cache.Done(), "joined refresh Cache.Done")
}

func TestTicketCacheCompletedHighCardinalityRefreshesDoNotRetainRefreshWorkers(t *testing.T) {
	const generations = 64
	allStarted := make(chan struct{})
	releaseCalls := make(chan struct{})
	var started atomic.Int32
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseCalls) })
	}
	control := ticketCacheControl(nil, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, params any) (json.RawMessage, error) {
		request, ok := params.(protocol.RelayTicketRequest)
		if !ok {
			return nil, fmt.Errorf("relay params type = %T", params)
		}
		if started.Add(1) == generations {
			close(allStarted)
		}
		<-releaseCalls
		return ticketJSON(fmt.Sprintf("relay-%d", request.DesiredGeneration), cacheTestNow.Add(time.Hour)), nil
	})
	cache := NewCache(control, CacheOptions{Now: func() time.Time { return cacheTestNow }})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() {
		release()
		closeTicketCache(t, cache)
	})
	poolWorkersBefore := concPoolWorkerCount()

	results := make(chan relayTicketResult, generations)
	for generation := uint64(1); generation <= generations; generation++ {
		go func() {
			ticket, err := cache.RelayTicket(context.Background(), generation)
			results <- relayTicketResult{ticket: ticket, err: err}
		}()
	}
	receiveWithTimeout(t, allStarted, "high-cardinality refresh barrier")
	release()
	for range generations {
		result := receiveWithTimeout(t, results, "high-cardinality refresh result")
		require.NoError(t, result.err)
		require.NotEmpty(t, result.ticket)
	}
	require.Equal(t, generations, control.Calls(consts.RPCAgentIssueRelayTicket))

	deadline := time.Now().Add(time.Second)
	for concPoolWorkerCount() > poolWorkersBefore && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	require.LessOrEqual(t, concPoolWorkerCount(), poolWorkersBefore, "completed refresh workers must not remain resident until Cache.Close")
}

func TestTicketCacheRefreshPanicCompletesOwnerWaitersAndClose(t *testing.T) {
	const waiterCount = 16
	const refreshKey = "relay:panic"
	control := ticketCacheControl(nil, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		return nil, errors.New("unexpected ticket call")
	})
	cache := NewCache(control, CacheOptions{Now: func() time.Time { return cacheTestNow }})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() {
		cache.Close()
		requireClosed(t, cache.Done(), "panic test cache cleanup")
	})

	cache.mu.Lock()
	sharedCtx := cache.runCtx
	call := &ticketRefreshCall{done: make(chan struct{})}
	cache.refreshCalls[refreshKey] = call
	cache.mu.Unlock()
	waiterResults := make(chan error, waiterCount)
	for range waiterCount {
		go func() {
			_, err := cache.waitForRefreshResult(context.Background(), sharedCtx, call)
			waiterResults <- err
		}()
	}

	panicEscaped := false
	func() {
		defer func() {
			panicEscaped = recover() != nil
		}()
		cache.runRefreshCall(sharedCtx, refreshKey, call, func(context.Context) (ticketEntry, error) {
			panic("private-ticket-secret https://control.invalid/issue?token=private")
		})
	}()
	cache.mu.Lock()
	ownerRetained := cache.refreshCalls[refreshKey] == call
	resultErr := call.result.err
	cache.mu.Unlock()
	doneClosed := false
	select {
	case <-call.done:
		doneClosed = true
	default:
	}
	if panicEscaped {
		t.Error("refresh panic escaped the Cache boundary")
	}
	if ownerRetained {
		t.Error("refresh panic retained the per-key owner")
	}
	if !doneClosed {
		t.Error("refresh panic left waiters blocked")
	}
	if !errors.Is(resultErr, errTicketRefresh) {
		t.Error("refresh panic result was not the fixed sanitized error")
	}
	if resultErr != nil {
		if strings.Contains(resultErr.Error(), "private-ticket-secret") ||
			strings.Contains(resultErr.Error(), "control.invalid") ||
			strings.Contains(resultErr.Error(), "token=private") {
			t.Error("refresh panic result exposed private panic content")
		}
	}

	// Repair only the pre-fix state so the RED test can terminate without leaking waiters.
	if ownerRetained || !doneClosed {
		cache.mu.Lock()
		if cache.refreshCalls[refreshKey] == call {
			delete(cache.refreshCalls, refreshKey)
		}
		call.result = ticketRefreshResult{err: errTicketRefresh}
		cache.mu.Unlock()
		if !doneClosed {
			close(call.done)
		}
	}
	for range waiterCount {
		require.ErrorIs(t, receiveWithTimeout(t, waiterResults, "panic refresh waiter"), errTicketRefresh)
	}

	entry, err := cache.waitForRefresh(context.Background(), refreshKey, nil, func(context.Context) (ticketEntry, error) {
		return ticketEntry{token: "recovered-ticket", issuedAt: cacheTestNow, expiresAt: cacheTestNow.Add(time.Hour)}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "recovered-ticket", entry.token, "same key must create a fresh owner after panic cleanup")

	closeReturned := make(chan struct{})
	go func() {
		cache.Close()
		close(closeReturned)
	}()
	receiveWithTimeout(t, closeReturned, "Cache.Close after refresh panic")
	requireClosed(t, cache.Done(), "Cache.Done after refresh panic")
}

func TestTicketCacheConcurrentSameKeyUsesSingleTrackedRefreshOwner(t *testing.T) {
	const callers = 64
	callStarted := make(chan struct{})
	releaseCall := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseCall) })
	}
	control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON([]string{protocol.AgentCapabilityTunnelV1}), nil
		}
		startedOnce.Do(func() { close(callStarted) })
		<-releaseCall
		return ticketJSON("shared-ticket", cacheTestNow.Add(time.Hour)), nil
	})
	cache := NewCache(control, CacheOptions{Now: func() time.Time { return cacheTestNow }})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() {
		release()
		closeTicketCache(t, cache)
	})

	results := make(chan relayTicketResult, callers)
	for range callers {
		go func() {
			ticket, err := cache.RelayTicket(context.Background(), 7)
			results <- relayTicketResult{ticket: ticket, err: err}
		}()
	}
	receiveWithTimeout(t, callStarted, "shared refresh call")
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return len(cache.refreshCalls) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 1, control.Calls(consts.RPCAgentIssueRelayTicket))

	release()
	for range callers {
		result := receiveWithTimeout(t, results, "shared refresh result")
		require.NoError(t, result.err)
		require.Equal(t, pkgagentauth.RelayTicket("shared-ticket"), result.ticket)
	}
}

func TestTicketCacheUsesOnlyTrackedPerKeyRefreshOwners(t *testing.T) {
	_, hasDuplicateSingleflight := reflect.TypeOf(Cache{}).FieldByName("refreshes")
	require.False(t, hasDuplicateSingleflight, "refreshCalls already owns per-key coalescing")
}

func TestWaitForRefreshResultRejectsCompletedTicketAfterCacheCancellation(t *testing.T) {
	newCompletedCall := func() *ticketRefreshCall {
		call := &ticketRefreshCall{
			done: make(chan struct{}),
			result: ticketRefreshResult{entry: ticketEntry{
				token:     "must-not-escape",
				expiresAt: cacheTestNow.Add(time.Hour),
			}},
		}
		close(call.done)
		return call
	}

	t.Run("closed before owner context cancellation propagates", func(t *testing.T) {
		sharedCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cache := NewCache(nil, CacheOptions{})
		cache.mu.Lock()
		cache.started = true
		cache.runCtx = sharedCtx
		cache.closed = true
		cache.mu.Unlock()

		entry, err := cache.waitForRefreshResult(context.Background(), sharedCtx, newCompletedCall())
		require.ErrorIs(t, err, errCacheClosed)
		require.Empty(t, entry.token)
	})

	t.Run("owner context already canceled", func(t *testing.T) {
		sharedCtx, cancel := context.WithCancel(context.Background())
		cache := NewCache(nil, CacheOptions{})
		cache.mu.Lock()
		cache.started = true
		cache.runCtx = sharedCtx
		cache.mu.Unlock()
		cancel()

		for range 100 {
			entry, err := cache.waitForRefreshResult(context.Background(), sharedCtx, newCompletedCall())
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, entry.token)
		}
	})
}

func TestTicketCacheBlockedBootstrapCloseAndLifecycleBoundaries(t *testing.T) {
	t.Run("close blocked bootstrap", func(t *testing.T) {
		started := make(chan struct{})
		control := newCacheTestControl(func(ctx context.Context, method string, _ any) (json.RawMessage, error) {
			require.Equal(t, consts.RPCAgentAuthBootstrap, method)
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		cache := NewCache(control, CacheOptions{})
		runResult := make(chan error, 1)
		go func() { runResult <- cache.Run(context.Background()) }()
		<-started
		cache.Close()
		require.ErrorIs(t, receiveWithTimeout(t, runResult, "blocked Run"), context.Canceled)
		requireClosed(t, cache.Done(), "blocked-bootstrap cache done")
	})

	t.Run("close before run and double close", func(t *testing.T) {
		control := newCacheTestControl(nil)
		cache := NewCache(control, CacheOptions{})
		cache.Close()
		cache.Close()
		requireClosed(t, cache.Done(), "close-before-run done")
		require.Error(t, cache.Run(context.Background()))
		require.Zero(t, control.Calls(consts.RPCAgentAuthBootstrap))
	})

	t.Run("run context cancel and second run", func(t *testing.T) {
		control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			require.Equal(t, consts.RPCAgentAuthBootstrap, method)
			return bootstrapJSON(nil), nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		cache := NewCache(control, CacheOptions{})
		require.NoError(t, cache.Run(ctx))
		require.Error(t, cache.Run(ctx))
		cancel()
		requireClosed(t, cache.Done(), "Run context cancellation")
		cache.Close()
	})
}

func TestTicketCacheOldMasterOrMalformedBootstrapDisablesSessionOnce(t *testing.T) {
	tests := []struct {
		name     string
		response json.RawMessage
		err      error
	}{
		{name: "old master method not found", err: errors.New("rpc error -32601: unknown method")},
		{name: "malformed response", response: json.RawMessage(`{"master_instance_id":`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
				require.Equal(t, consts.RPCAgentAuthBootstrap, method)
				return tc.response, tc.err
			})
			cache := NewCache(control, CacheOptions{})
			require.NoError(t, cache.Run(context.Background()))
			t.Cleanup(func() { closeTicketCache(t, cache) })
			for range 3 {
				relay, err := cache.RelayTicket(context.Background(), 0)
				require.Error(t, err)
				require.Empty(t, relay)
				forward, err := cache.CachedForwardTicket()
				require.Error(t, err)
				require.Empty(t, forward)
			}
			require.Equal(t, 1, control.Calls(consts.RPCAgentAuthBootstrap))
			require.Zero(t, control.Calls(consts.RPCAgentIssueRelayTicket))
			require.Zero(t, control.Calls(consts.RPCAgentIssueForwardTicket))
			require.Equal(t, BootstrapSnapshot{}, cache.Bootstrap())
		})
	}
}

func TestAuthBootstrapRejectsInvalidSigningKeySetsAndDisablesTickets(t *testing.T) {
	validKey := pkgagentauth.PublicKey{
		KeyID:     "key-a",
		Algorithm: "EdDSA",
		Key:       make([]byte, 32),
	}
	tests := []struct {
		name string
		keys []pkgagentauth.PublicKey
	}{
		{name: "empty key id", keys: []pkgagentauth.PublicKey{{Algorithm: "EdDSA", Key: make([]byte, 32)}}},
		{name: "overlong key id", keys: []pkgagentauth.PublicKey{{KeyID: strings.Repeat("k", 129), Algorithm: "EdDSA", Key: make([]byte, 32)}}},
		{name: "wrong algorithm", keys: []pkgagentauth.PublicKey{{KeyID: "key-a", Algorithm: "RS256", Key: make([]byte, 32)}}},
		{name: "short public key", keys: []pkgagentauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: make([]byte, 31)}}},
		{name: "long public key", keys: []pkgagentauth.PublicKey{{KeyID: "key-a", Algorithm: "EdDSA", Key: make([]byte, 33)}}},
		{name: "duplicate key id", keys: []pkgagentauth.PublicKey{validKey, validKey}},
		{name: "too many keys", keys: func() []pkgagentauth.PublicKey {
			keys := make([]pkgagentauth.PublicKey, 9)
			for i := range keys {
				keys[i] = validKey
				keys[i].KeyID = fmt.Sprintf("key-%d", i)
			}
			return keys
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
				switch method {
				case consts.RPCAgentAuthBootstrap:
					return jsonResult(protocol.AuthBootstrapResponse{
						MasterInstanceID: "master-a",
						Capabilities: []string{
							protocol.AgentCapabilityTunnelV1,
							protocol.AgentCapabilityForwardV1,
						},
						SigningKeys: tc.keys,
					})
				default:
					return ticketJSON("must-not-be-issued", cacheTestNow.Add(time.Hour)), nil
				}
			})
			cache := NewCache(control, CacheOptions{})
			require.NoError(t, cache.Run(context.Background()))
			t.Cleanup(func() { closeTicketCache(t, cache) })
			require.Equal(t, BootstrapSnapshot{}, cache.Bootstrap())
			relay, relayErr := cache.RelayTicket(context.Background(), 0)
			require.Error(t, relayErr)
			require.Empty(t, relay)
			forward, forwardErr := cache.CachedForwardTicket()
			require.Error(t, forwardErr)
			require.Empty(t, forward)
			require.Zero(t, control.Calls(consts.RPCAgentIssueRelayTicket))
			require.Zero(t, control.Calls(consts.RPCAgentIssueForwardTicket))
			for _, key := range tc.keys {
				if key.KeyID != "" {
					require.NotContains(t, relayErr.Error(), key.KeyID)
					require.NotContains(t, forwardErr.Error(), key.KeyID)
				}
			}
		})
	}
}

func TestAuthBootstrapAcceptsEightUniqueEdDSASigningKeys(t *testing.T) {
	keys := make([]pkgagentauth.PublicKey, 8)
	for i := range keys {
		keys[i] = pkgagentauth.PublicKey{
			KeyID:     fmt.Sprintf("key-%d", i),
			Algorithm: "EdDSA",
			Key:       make([]byte, 32),
		}
	}
	control := newCacheTestControl(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		require.Equal(t, consts.RPCAgentAuthBootstrap, method)
		return jsonResult(protocol.AuthBootstrapResponse{
			MasterInstanceID: "master-a",
			SigningKeys:      keys,
		})
	})
	cache := NewCache(control, CacheOptions{})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	require.Equal(t, keys, cache.Bootstrap().SigningKeys)
}

func TestTicketCacheRelayMapKeepsDeterministicHighestTwoGenerations(t *testing.T) {
	clock := newCacheTestClock()
	var sequence atomic.Int32
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, params any) (json.RawMessage, error) {
		generation := requireRelayGeneration(t, params)
		n := sequence.Add(1)
		return ticketJSON(fmt.Sprintf("relay-%d-%d", generation, n), cacheTestNow.Add(time.Hour)), nil
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })

	for _, generation := range []uint64{1, 3, 2} {
		ticket, err := cache.RelayTicket(context.Background(), generation)
		require.NoError(t, err)
		require.NotEmpty(t, ticket)
	}
	cache.mu.Lock()
	keys := make([]uint64, 0, len(cache.relayTickets))
	for generation := range cache.relayTickets {
		keys = append(keys, generation)
	}
	cache.mu.Unlock()
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	require.Equal(t, []uint64{2, 3}, keys)

	for _, generation := range []uint64{2, 3} {
		_, err := cache.RelayTicket(context.Background(), generation)
		require.NoError(t, err)
	}
	ticket, err := cache.RelayTicket(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("relay-1-4"), ticket)
	require.Equal(t, 4, control.Calls(consts.RPCAgentIssueRelayTicket))
	cache.mu.Lock()
	require.Len(t, cache.relayTickets, 2)
	_, hasTwo := cache.relayTickets[2]
	_, hasThree := cache.relayTickets[3]
	cache.mu.Unlock()
	require.True(t, hasTwo)
	require.True(t, hasThree)
}

func TestTicketCacheFirstFailureCooldownBoundaryAndSuccessRecovery(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		method     string
		issue      func(*Cache) (string, error)
	}{
		{
			name:       "relay",
			capability: protocol.AgentCapabilityTunnelV1,
			method:     consts.RPCAgentIssueRelayTicket,
			issue: func(cache *Cache) (string, error) {
				ticket, err := cache.RelayTicket(context.Background(), 9)
				return string(ticket), err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newCacheTestClock()
			var attempts atomic.Int32
			control := ticketCacheControl(clock, []string{tc.capability}, func(method string, _ any) (json.RawMessage, error) {
				require.Equal(t, tc.method, method)
				if attempts.Add(1) == 1 {
					return nil, errors.New("private-control-marker")
				}
				return ticketJSON("recovered-ticket", clock.Now().Add(time.Hour)), nil
			})
			cache := NewCache(control, CacheOptions{Now: clock.Now})
			require.NoError(t, cache.Run(context.Background()))
			t.Cleanup(func() { closeTicketCache(t, cache) })

			for _, elapsed := range []time.Duration{0, 999 * time.Millisecond} {
				clock.Set(cacheTestNow.Add(elapsed))
				ticket, err := tc.issue(cache)
				require.Error(t, err)
				require.Empty(t, ticket)
				require.NotContains(t, err.Error(), "private-control-marker")
			}
			require.Equal(t, 1, control.Calls(tc.method))

			clock.Set(cacheTestNow.Add(time.Second))
			ticket, err := tc.issue(cache)
			require.NoError(t, err)
			require.Equal(t, "recovered-ticket", ticket)
			require.Equal(t, 2, control.Calls(tc.method))
			ticket, err = tc.issue(cache)
			require.NoError(t, err)
			require.Equal(t, "recovered-ticket", ticket)
			require.Equal(t, 2, control.Calls(tc.method), "success must clear negative state and populate the ticket cache")
		})
	}
}

func TestTicketCacheCanceledCallerDoesNotIssueOrRecordFailure(t *testing.T) {
	control := ticketCacheControl(nil, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		return nil, errors.New("must not be called")
	})
	cache := NewCache(control, CacheOptions{})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	relay, err := cache.RelayTicket(ctx, 3)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, relay)
	require.Never(t, func() bool {
		return control.Calls(consts.RPCAgentIssueRelayTicket) != 0
	}, 50*time.Millisecond, time.Millisecond)
	cache.mu.Lock()
	require.Empty(t, cache.relayFailures)
	require.True(t, cache.forwardFailure.IsZero())
	cache.mu.Unlock()
}

func TestTicketCacheExpiredTicketFailureStartsCooldown(t *testing.T) {
	clock := newCacheTestClock()
	var attempts atomic.Int32
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		if attempts.Add(1) == 1 {
			return ticketJSON("short-ticket", cacheTestNow.Add(time.Second)), nil
		}
		return nil, errors.New("expired-refresh-failed")
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	ticket, err := cache.RelayTicket(context.Background(), 4)
	require.NoError(t, err)
	require.Equal(t, pkgagentauth.RelayTicket("short-ticket"), ticket)

	clock.Set(cacheTestNow.Add(time.Second))
	for range 2 {
		ticket, err = cache.RelayTicket(context.Background(), 4)
		require.Error(t, err)
		require.Empty(t, ticket)
	}
	require.Equal(t, 2, control.Calls(consts.RPCAgentIssueRelayTicket))
}

func TestTicketCacheRelayFailureCooldownKeepsHighestTwoGenerations(t *testing.T) {
	clock := newCacheTestClock()
	control := ticketCacheControl(clock, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		return nil, errors.New("relay unavailable")
	})
	cache := NewCache(control, CacheOptions{Now: clock.Now})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	for _, generation := range []uint64{1, 2, 3} {
		_, err := cache.RelayTicket(context.Background(), generation)
		require.Error(t, err)
	}
	for _, generation := range []uint64{2, 3} {
		_, err := cache.RelayTicket(context.Background(), generation)
		require.Error(t, err)
	}
	require.Equal(t, 3, control.Calls(consts.RPCAgentIssueRelayTicket))
	_, err := cache.RelayTicket(context.Background(), 1)
	require.Error(t, err)
	require.Equal(t, 4, control.Calls(consts.RPCAgentIssueRelayTicket), "oldest generation cooldown must be evicted")
}

func TestTicketCacheRunOwnsProactiveRefreshLoop(t *testing.T) {
	now := time.Now()
	var issues atomic.Int32
	refreshed := make(chan struct{}, 1)
	control := ticketCacheControl(nil, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		n := issues.Add(1)
		if n == 2 {
			refreshed <- struct{}{}
		}
		return ticketJSON(fmt.Sprintf("relay-%d", n), now.Add(2*time.Second)), nil
	})
	cache := NewCache(control, CacheOptions{})
	require.NoError(t, cache.Run(context.Background()))
	_, err := cache.RelayTicket(context.Background(), 8)
	require.NoError(t, err)
	receiveWithTimeout(t, refreshed, "proactive refresh")
	cache.Close()
	requireClosed(t, cache.Done(), "proactive refresh cache close")
	require.GreaterOrEqual(t, issues.Load(), int32(2))
}

type relayTicketResult struct {
	ticket pkgagentauth.RelayTicket
	err    error
}

type forwardTicketResult struct {
	ticket pkgagentauth.ForwardTicket
	err    error
}

func ticketCacheControl(
	clock *cacheTestClock,
	capabilities []string,
	issue func(string, any) (json.RawMessage, error),
) *cacheTestControl {
	return newCacheTestControl(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == consts.RPCAgentAuthBootstrap {
			return bootstrapJSON(capabilities), nil
		}
		return issue(method, params)
	})
}

func bootstrapJSON(capabilities []string) json.RawMessage {
	data, err := jsonResult(protocol.AuthBootstrapResponse{
		MasterInstanceID: "master-a",
		Capabilities:     capabilities,
		SigningKeys: []pkgagentauth.PublicKey{{
			KeyID:     "key-a",
			Algorithm: "EdDSA",
			Key:       make([]byte, 32),
		}},
	})
	if err != nil {
		panic(err)
	}
	return data
}

func ticketJSON(token string, expiresAt time.Time) json.RawMessage {
	data, err := jsonResult(protocol.TicketResponse{Token: token, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		panic(err)
	}
	return data
}

func jsonResult(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	return json.RawMessage(data), err
}

func requireRelayGeneration(t *testing.T, params any) uint64 {
	t.Helper()
	request, ok := params.(protocol.RelayTicketRequest)
	require.True(t, ok, "relay params type = %T", params)
	return request.DesiredGeneration
}

func refreshTicketsForTest(t *testing.T, cache *Cache) {
	t.Helper()
	cache.mu.Lock()
	ctx := cache.runCtx
	cache.mu.Unlock()
	require.NotNil(t, ctx)
	cache.refreshDueTickets(ctx)
}

func requireCachedForwardTicket(t *testing.T, cache *Cache, want pkgagentauth.ForwardTicket) {
	t.Helper()
	require.Eventually(t, func() bool {
		ticket, err := cache.CachedForwardTicket()
		return err == nil && ticket == want
	}, time.Second, time.Millisecond)
}

func closeTicketCache(t *testing.T, cache *Cache) {
	t.Helper()
	cache.Close()
	requireClosed(t, cache.Done(), "ticket cache cleanup")
}

func requireClosed(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatalf("%s did not complete", operation)
	}
}

func receiveWithTimeout[T any](t *testing.T, ch <-chan T, operation string) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-ch:
		return value
	case <-timer.C:
		t.Fatalf("%s did not complete", operation)
		var zero T
		return zero
	}
}

func contextDoneForTest() <-chan struct{} {
	return make(chan struct{})
}

func concPoolWorkerCount() int {
	buffer := make([]byte, 2<<20)
	n := runtime.Stack(buffer, true)
	return strings.Count(string(buffer[:n]), "github.com/sourcegraph/conc/pool.(*Pool).worker")
}

func TestTicketCacheErrorsNeverContainControlSecrets(t *testing.T) {
	marker := "Authorization-secret-private-key-ticket"
	control := ticketCacheControl(nil, []string{protocol.AgentCapabilityTunnelV1}, func(_ string, _ any) (json.RawMessage, error) {
		return nil, errors.New(marker)
	})
	cache := NewCache(control, CacheOptions{})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() { closeTicketCache(t, cache) })
	_, err := cache.RelayTicket(context.Background(), 1)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), marker))
	require.False(t, strings.Contains(err.Error(), "Authorization"))
}
