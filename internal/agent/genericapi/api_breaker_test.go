package genericapi

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

type staticAPIBreakerSettings struct {
	value settings.AgentSettings
}

func (s staticAPIBreakerSettings) Settings() settings.AgentSettings { return s.value }

type mutableAPIBreakerSettings struct {
	value atomic.Pointer[settings.AgentSettings]
}

func newMutableAPIBreakerSettings(value settings.AgentSettings) *mutableAPIBreakerSettings {
	result := &mutableAPIBreakerSettings{}
	result.Set(value)
	return result
}

func (s *mutableAPIBreakerSettings) Settings() settings.AgentSettings {
	if value := s.value.Load(); value != nil {
		return *value
	}
	return settings.AgentSettings{}
}

func (s *mutableAPIBreakerSettings) Set(value settings.AgentSettings) {
	copy := value
	s.value.Store(&copy)
}

func testAPIBreakerSettings(enabled int, threshold int) staticAPIBreakerSettings {
	return staticAPIBreakerSettings{value: settings.AgentSettings{
		BreakerEnabled: enabled, BreakerThreshold: threshold, BreakerCooldownMs: 60_000,
	}}
}

func dispatchedAPIResult(status int) *apiattempt.APIExecutionResult {
	return &apiattempt.APIExecutionResult{
		UpstreamStatus: status, ProviderDispatchKnown: true, ProviderDispatched: true,
	}
}

func acquireAPIBreakerPermit(t *testing.T, registry *APIBreakerRegistry, upstreamID uint) APIBreakerPermit {
	t.Helper()
	require.True(t, registry.Healthy(upstreamID))
	permit, ok := registry.TryAcquire(upstreamID)
	require.True(t, ok)
	require.NotNil(t, permit)
	return permit
}

func TestAPIBreakerRecordsTransportAndConfiguredStatusesOnly(t *testing.T) {
	t.Run("default status classification", func(t *testing.T) {
		registry := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 2), nil)
		for _, status := range []int{httpStatusBadRequest, httpStatusUnauthorized, httpStatusNotFound} {
			upstreamID := uint(status)
			for i := 0; i < 3; i++ {
				acquireAPIBreakerPermit(t, registry, upstreamID).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(status)})
			}
			require.True(t, registry.Healthy(upstreamID), "ordinary business 4xx %d must not open breaker", status)
		}

		for _, status := range []int{httpStatusTooManyRequests, httpStatusInternalServerError, httpStatusServiceUnavailable} {
			upstreamID := uint(status)
			for i := 0; i < 2; i++ {
				acquireAPIBreakerPermit(t, registry, upstreamID).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(status)})
			}
			require.False(t, registry.Healthy(upstreamID), "configured status %d must open breaker", status)
		}
	})

	t.Run("transport failure counts", func(t *testing.T) {
		registry := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 1), nil)
		acquireAPIBreakerPermit(t, registry, 1).Finish(APIBreakerCompletion{
			Result: dispatchedAPIResult(0), Err: errors.New("dial failed"),
		})
		require.False(t, registry.Healthy(1))
	})

	t.Run("explicit predicate overrides defaults", func(t *testing.T) {
		registry := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 1), func(status int) bool {
			return status == 418
		})
		acquireAPIBreakerPermit(t, registry, 1).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusInternalServerError)})
		require.True(t, registry.Healthy(1), "injected predicate must be authoritative")

		acquireAPIBreakerPermit(t, registry, 2).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(418)})
		require.False(t, registry.Healthy(2))
	})
}

func TestAPIBreakerCompletionDistinguishesClientAbortFromUpstreamTimeout(t *testing.T) {
	clientAbortCases := []struct {
		name   string
		reason APIClientAbortReason
		err    error
	}{
		{name: "wrapped client cancel", reason: APIClientAbortCanceled, err: fmt.Errorf("client left: %w", context.Canceled)},
		{name: "wrapped client deadline", reason: APIClientAbortDeadlineExceeded, err: fmt.Errorf("client timed out: %w", context.DeadlineExceeded)},
	}
	for index, test := range clientAbortCases {
		t.Run(test.name, func(t *testing.T) {
			registry := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 1), nil)
			upstreamID := uint(index + 1)
			acquireAPIBreakerPermit(t, registry, upstreamID).Finish(APIBreakerCompletion{
				Result: dispatchedAPIResult(0), Err: test.err, ClientAbort: test.reason,
			})
			require.True(t, registry.Healthy(upstreamID))
		})
	}

	upstreamFailures := []struct {
		name string
		err  error
	}{
		{name: "wrapped upstream timeout", err: fmt.Errorf("response header: %w", context.DeadlineExceeded)},
		{name: "wrapped upstream cancellation", err: fmt.Errorf("transport stopped: %w", context.Canceled)},
	}
	for index, test := range upstreamFailures {
		t.Run(test.name, func(t *testing.T) {
			registry := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 1), nil)
			upstreamID := uint(index + 10)
			acquireAPIBreakerPermit(t, registry, upstreamID).Finish(APIBreakerCompletion{
				Result: dispatchedAPIResult(0), Err: test.err,
			})
			require.False(t, registry.Healthy(upstreamID), "without an explicit client-abort fact the transport failure must count")
		})
	}
}

func TestAPIBreakerDisabledAndNotDispatchedCompletionsAreNeutral(t *testing.T) {
	disabled := NewAPIBreakerRegistry(testAPIBreakerSettings(0, 1), nil)
	for i := 0; i < 3; i++ {
		acquireAPIBreakerPermit(t, disabled, 7).Finish(APIBreakerCompletion{
			Result: dispatchedAPIResult(httpStatusServiceUnavailable), Err: errors.New("dial failed"),
		})
	}
	require.Empty(t, disabled.SnapshotBreakers())

	enabled := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 1), nil)
	acquireAPIBreakerPermit(t, enabled, 8).Finish(APIBreakerCompletion{
		Result: &apiattempt.APIExecutionResult{ProviderDispatchKnown: true, ProviderDispatched: false},
		Err:    errors.New("local build failed"),
	})
	require.True(t, enabled.Healthy(8))
}

func TestAPIBreakerHalfOpenAllowsOneProbeAtThresholdFiveAndNeutralRecovers(t *testing.T) {
	zeroCooldown := testAPIBreakerSettings(1, 5)
	zeroCooldown.value.BreakerCooldownMs = 0
	registry := NewAPIBreakerRegistry(zeroCooldown, nil)
	for i := 0; i < 5; i++ {
		acquireAPIBreakerPermit(t, registry, 9).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusServiceUnavailable)})
	}

	const workers = 64
	permits := make(chan APIBreakerPermit, workers)
	workersPool := pool.New().WithMaxGoroutines(workers)
	for i := 0; i < workers; i++ {
		workersPool.Go(func() {
			if permit, ok := registry.TryAcquire(9); ok {
				permits <- permit
			}
		})
	}
	workersPool.Wait()
	close(permits)
	require.Len(t, permits, 1, "API half-open must have one concurrent probe even when breaker threshold is five")
	permit := <-permits
	before := registry.SnapshotBreakers()
	require.Len(t, before, 1)
	require.Equal(t, "half-open", before[0].State)

	permit.Finish(APIBreakerCompletion{
		Result:      dispatchedAPIResult(0),
		Err:         fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
		ClientAbort: APIClientAbortDeadlineExceeded,
	})
	after := registry.SnapshotBreakers()
	require.Len(t, after, 1)
	require.Equal(t, before[0].Failures, after[0].Failures, "neutral completion must not increment failure metrics")

	recovered, ok := registry.TryAcquire(9)
	require.True(t, ok, "neutral half-open completion must re-enter cooldown and become probeable")
	recovered.Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusOK)})
}

func TestAPIUpstreamLeaseFinishesSelectedBreakerExactlyOnce(t *testing.T) {
	registry := NewAPIBreakerRegistry(testAPIBreakerSettings(1, 2), nil)
	picker := NewAPIUpstreamPicker(staticAPIUpstreamIndex{values: []protocol.SyncedAPIUpstream{
		{ID: 10, BackendID: 7, BaseURL: "https://selected.example", Priority: 1, Weight: 1, Status: 1},
	}}, registry)

	first, err := picker.Pick(7, apiattempt.APIProtocolHTTP, "request-one")
	require.NoError(t, err)
	result := dispatchedAPIResult(httpStatusServiceUnavailable)
	result.APIUpstreamID = 999
	first.Finish(APIBreakerCompletion{Result: result})
	first.Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusOK)})
	require.Equal(t, uint(10), result.APIUpstreamID, "lease must stamp the frozen upstream instead of trusting a caller ID")

	snapshot := registry.SnapshotBreakers()
	require.Len(t, snapshot, 1)
	require.Equal(t, uint(10), snapshot[0].APIUpstreamID)
	require.Equal(t, 1, snapshot[0].Failures, "duplicate Finish must not double count")
	require.Equal(t, "closed", snapshot[0].State)

	second, err := picker.Pick(7, apiattempt.APIProtocolHTTP, "request-two")
	require.NoError(t, err)
	second.Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusServiceUnavailable)})
	require.False(t, registry.Healthy(10))
}

func TestAPIBreakerDeleteClearAndDisabledSnapshotRemoveStaleState(t *testing.T) {
	initial := testAPIBreakerSettings(1, 1).value
	settingsReader := newMutableAPIBreakerSettings(initial)
	registry := NewAPIBreakerRegistry(settingsReader, nil)
	for _, upstreamID := range []uint{1, 2} {
		acquireAPIBreakerPermit(t, registry, upstreamID).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusServiceUnavailable)})
	}

	registry.Delete(1)
	snapshot := registry.SnapshotBreakers()
	require.Len(t, snapshot, 1)
	require.Equal(t, uint(2), snapshot[0].APIUpstreamID)
	permit, ok := registry.TryAcquire(1)
	require.True(t, ok, "re-created upstream must start with a fresh breaker")
	permit.Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusOK)})

	registry.Clear()
	require.Empty(t, registry.SnapshotBreakers())

	acquireAPIBreakerPermit(t, registry, 3).Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusServiceUnavailable)})
	disabled := initial
	disabled.BreakerEnabled = 0
	settingsReader.Set(disabled)
	require.Empty(t, registry.SnapshotBreakers(), "observing disabled settings must clear stale runtime state")
	settingsReader.Set(initial)
	reEnabled, ok := registry.TryAcquire(3)
	require.True(t, ok, "disabled then enabled must not restore the old open state")
	reEnabled.Finish(APIBreakerCompletion{Result: dispatchedAPIResult(httpStatusOK)})
}

const (
	httpStatusOK                  = 200
	httpStatusBadRequest          = 400
	httpStatusUnauthorized        = 401
	httpStatusNotFound            = 404
	httpStatusTooManyRequests     = 429
	httpStatusInternalServerError = 500
	httpStatusServiceUnavailable  = 503
)
