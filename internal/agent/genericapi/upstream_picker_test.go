package genericapi

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

type staticAPIUpstreamIndex struct {
	values    []protocol.SyncedAPIUpstream
	byBackend map[uint][]protocol.SyncedAPIUpstream
}

func (s staticAPIUpstreamIndex) UpstreamsForBackend(backendID uint) []protocol.SyncedAPIUpstream {
	values := s.values
	if s.byBackend != nil {
		values = s.byBackend[backendID]
	}
	return append([]protocol.SyncedAPIUpstream(nil), values...)
}

type staticAPIBreakerFinder struct {
	mu          sync.Mutex
	unhealthy   map[uint]bool
	denyAcquire map[uint]bool
	acquired    []uint
}

func (f *staticAPIBreakerFinder) Healthy(upstreamID uint) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.unhealthy[upstreamID]
}

func (f *staticAPIBreakerFinder) TryAcquire(upstreamID uint) (APIBreakerPermit, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denyAcquire[upstreamID] {
		return nil, false
	}
	f.acquired = append(f.acquired, upstreamID)
	return noopAPIBreakerPermit{}, true
}

type noopAPIBreakerPermit struct{}

func (noopAPIBreakerPermit) Finish(APIBreakerCompletion) {}

func TestAPIUpstreamPickerUsesHighestHealthyPriorityThenStableRequestWeight(t *testing.T) {
	breakers := &staticAPIBreakerFinder{unhealthy: map[uint]bool{4: true}}
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: []protocol.SyncedAPIUpstream{
		{ID: 4, BackendID: 7, BaseURL: "https://open-highest.example", Priority: 99, Weight: 100, Status: 1},
		{ID: 1, BackendID: 7, BaseURL: "https://low.example", Priority: 1, Weight: 100, Status: 1},
		{ID: 2, BackendID: 7, BaseURL: "https://one.example", Priority: 9, Weight: 1, Status: 1},
		{ID: 3, BackendID: 7, BaseURL: "http://three.example", Priority: 9, Weight: 3, Status: 1},
	}}, breakers)

	counts := map[uint]int{}
	for i := 0; i < 4000; i++ {
		requestID := fmt.Sprintf("request-%d", i)
		first, err := picker.Pick(7, apiattempt.APIProtocolHTTP, requestID)
		require.NoError(t, err)
		require.NotEqual(t, uint(1), first.Upstream.ID, "lower priority must never be selected")
		require.NotEqual(t, uint(4), first.Upstream.ID, "breaker-open priority must be excluded before choosing the healthy priority")
		counts[first.Upstream.ID]++
		for repeat := 0; repeat < 3; repeat++ {
			again, pickErr := picker.Pick(7, apiattempt.APIProtocolHTTP, requestID)
			require.NoError(t, pickErr)
			require.Equal(t, first.Upstream.ID, again.Upstream.ID, "same request ID must freeze the same weighted choice")
		}
	}

	require.InDelta(t, 0.25, float64(counts[2])/4000, 0.04)
	require.InDelta(t, 0.75, float64(counts[3])/4000, 0.04)
}

func TestAPIUpstreamPickerReturnsUnavailableWhenAllOpenOrDisabled(t *testing.T) {
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: []protocol.SyncedAPIUpstream{
		{ID: 1, BackendID: 7, BaseURL: "https://disabled.example", Priority: 10, Weight: 1, Status: 0},
		{ID: 2, BackendID: 7, BaseURL: "https://open.example", Priority: 9, Weight: 1, Status: 1},
	}}, &staticAPIBreakerFinder{unhealthy: map[uint]bool{2: true}})

	_, err := picker.Pick(7, apiattempt.APIProtocolHTTP, "request-1")
	require.ErrorIs(t, err, ErrExecutionUnavailable)
}

func TestAPIUpstreamPickerFailsClosedOnMalformedSelectionInputs(t *testing.T) {
	valid := protocol.SyncedAPIUpstream{ID: 1, BackendID: 7, BaseURL: "https://valid.example", Priority: 1, Weight: 1, Status: 1}
	tests := []struct {
		name      string
		backendID uint
		protocol  apiattempt.APIProtocol
		requestID string
		values    []protocol.SyncedAPIUpstream
	}{
		{name: "zero backend", protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{valid}},
		{name: "empty request id", backendID: 7, protocol: apiattempt.APIProtocolHTTP, values: []protocol.SyncedAPIUpstream{valid}},
		{name: "unknown protocol", backendID: 7, protocol: "grpc", requestID: "request", values: []protocol.SyncedAPIUpstream{valid}},
		{name: "invalid base URL", backendID: 7, protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{{ID: 1, BackendID: 7, BaseURL: "ftp://invalid.example", Weight: 1, Status: 1}}},
		{name: "wrong backend projection", backendID: 7, protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{{ID: 1, BackendID: 8, BaseURL: "https://invalid.example", Weight: 1, Status: 1}}},
		{name: "zero weight", backendID: 7, protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{{ID: 1, BackendID: 7, BaseURL: "https://invalid.example", Weight: 0, Status: 1}}},
		{name: "negative weight", backendID: 7, protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{{ID: 1, BackendID: 7, BaseURL: "https://invalid.example", Weight: -1, Status: 1}}},
		{name: "duplicate id", backendID: 7, protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{valid, valid}},
		{name: "weight sum overflow", backendID: 7, protocol: apiattempt.APIProtocolHTTP, requestID: "request", values: []protocol.SyncedAPIUpstream{
			{ID: 1, BackendID: 7, BaseURL: "https://one.example", Weight: math.MaxInt, Status: 1},
			{ID: 2, BackendID: 7, BaseURL: "https://two.example", Weight: math.MaxInt, Status: 1},
			{ID: 3, BackendID: 7, BaseURL: "https://three.example", Weight: math.MaxInt, Status: 1},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: test.values}, &staticAPIBreakerFinder{})
			_, err := picker.Pick(test.backendID, test.protocol, test.requestID)
			require.ErrorIs(t, err, ErrExecutionUnavailable)
		})
	}
}

func TestAPIUpstreamPickerAcceptsHTTPBaseURLsForWebSocketAndIsRaceSafe(t *testing.T) {
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: []protocol.SyncedAPIUpstream{
		{ID: 1, BackendID: 7, BaseURL: "https://ws.example", Priority: 1, Weight: 1, Status: 1},
	}}, &staticAPIBreakerFinder{})

	const workers = 64
	ids := make(chan uint, workers)
	workersPool := pool.New().WithMaxGoroutines(workers)
	for i := 0; i < workers; i++ {
		workersPool.Go(func() {
			got, err := picker.Pick(7, apiattempt.APIProtocolWebSocket, "stable-websocket")
			require.NoError(t, err)
			ids <- got.Upstream.ID
		})
	}
	workersPool.Wait()
	close(ids)
	for id := range ids {
		require.Equal(t, uint(1), id)
	}
}

func TestAPIUpstreamPickerWeightsOnlyTheHealthySet(t *testing.T) {
	breakers := &staticAPIBreakerFinder{unhealthy: map[uint]bool{1: true, 2: true}}
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: []protocol.SyncedAPIUpstream{
		{ID: 1, BackendID: 7, BaseURL: "https://open-one.example", Priority: 9, Weight: 10_000, Status: 1},
		{ID: 2, BackendID: 7, BaseURL: "https://open-two.example", Priority: 9, Weight: 20_000, Status: 1},
		{ID: 3, BackendID: 7, BaseURL: "https://healthy-one.example", Priority: 9, Weight: 1, Status: 1},
		{ID: 4, BackendID: 7, BaseURL: "https://healthy-three.example", Priority: 9, Weight: 3, Status: 1},
	}}, breakers)

	counts := map[uint]int{}
	for index := 0; index < 4000; index++ {
		requestID := fmt.Sprintf("healthy-%d", index)
		selection, err := picker.Pick(7, apiattempt.APIProtocolHTTP, requestID)
		require.NoError(t, err)
		counts[selection.Upstream.ID]++
		repeated, repeatErr := picker.Pick(7, apiattempt.APIProtocolHTTP, requestID)
		require.NoError(t, repeatErr)
		require.Equal(t, selection.Upstream.ID, repeated.Upstream.ID)
	}

	require.Zero(t, counts[1])
	require.Zero(t, counts[2])
	require.InDelta(t, 0.25, float64(counts[3])/4000, 0.04)
	require.InDelta(t, 0.75, float64(counts[4])/4000, 0.04)
}

func TestAPIUpstreamPickerFailsClosedWhenChosenPermitChangesBeforeAcquire(t *testing.T) {
	breakers := &staticAPIBreakerFinder{denyAcquire: map[uint]bool{1: true}}
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: []protocol.SyncedAPIUpstream{
		{ID: 1, BackendID: 7, BaseURL: "https://first.example", Priority: 9, Weight: 1, Status: 1},
		{ID: 2, BackendID: 7, BaseURL: "https://second.example", Priority: 1, Weight: 1, Status: 1},
	}}, breakers)
	picker.hash = func(string) uint64 { return 0 }

	selection, err := picker.Pick(7, apiattempt.APIProtocolHTTP, "race")
	require.Nil(t, selection)
	require.ErrorIs(t, err, ErrExecutionUnavailable)
	require.Empty(t, breakers.acquired, "a concurrent health change must not trigger a second pick")
}

func TestAPIUpstreamPickerNeverMixesBackendPools(t *testing.T) {
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{byBackend: map[uint][]protocol.SyncedAPIUpstream{
		10: {{ID: 101, BackendID: 10, BaseURL: "https://current.example", Weight: 1, Status: 1}},
		20: {{ID: 201, BackendID: 20, BaseURL: "https://archive.example", Weight: 1, Status: 1}},
	}}, &staticAPIBreakerFinder{})

	current, err := picker.Pick(10, apiattempt.APIProtocolHTTP, "current")
	require.NoError(t, err)
	require.Equal(t, uint(101), current.Upstream.ID)
	archive, err := picker.Pick(20, apiattempt.APIProtocolHTTP, "archive")
	require.NoError(t, err)
	require.Equal(t, uint(201), archive.Upstream.ID)
}
