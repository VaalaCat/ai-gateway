package resilience

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

func TestAutoBanTrackerThresholdAndRevisionSemantics(t *testing.T) {
	key := BreakerKey{Source: state.SourceAdmin, ID: 7}
	failure := state.AttemptResult{Err: &common.UpstreamError{Status: 500}}
	success := state.AttemptResult{}

	t.Run("third consecutive failure emits once per revision", func(t *testing.T) {
		tracker := NewAutoBanTracker()
		observation := AutoBanObservation{Key: key, Enabled: true, Threshold: 3, Revision: 4, Result: failure}
		require.Nil(t, tracker.Observe(observation))
		require.Nil(t, tracker.Observe(observation))
		require.Equal(t, &attemptproxy.ChannelAutoDisableTrigger{
			Source: attemptproxy.SourceAdmin, ChannelID: 7, Revision: 4,
			Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
		}, tracker.Observe(observation))
		observation.Result = success
		require.Nil(t, tracker.Observe(observation), "success must not rearm the same revision")
		observation.Result = failure
		for range 4 {
			require.Nil(t, tracker.Observe(observation), "same revision must emit only once")
		}
	})

	t.Run("success resets an unreported streak", func(t *testing.T) {
		tracker := NewAutoBanTracker()
		observation := AutoBanObservation{Key: key, Enabled: true, Threshold: 3, Revision: 4, Result: failure}
		require.Nil(t, tracker.Observe(observation))
		observation.Result = success
		require.Nil(t, tracker.Observe(observation))
		observation.Result = failure
		require.Nil(t, tracker.Observe(observation))
		require.Nil(t, tracker.Observe(observation))
		require.NotNil(t, tracker.Observe(observation))
	})

	t.Run("revision and threshold changes discard old streak", func(t *testing.T) {
		tracker := NewAutoBanTracker()
		observation := AutoBanObservation{Key: key, Enabled: true, Threshold: 3, Revision: 4, Result: failure}
		require.Nil(t, tracker.Observe(observation))
		require.Nil(t, tracker.Observe(observation))
		observation.Revision = 5
		require.Nil(t, tracker.Observe(observation))
		observation.Threshold = 2
		require.Nil(t, tracker.Observe(observation))
		require.NotNil(t, tracker.Observe(observation))
	})

	t.Run("threshold below one normalizes to one", func(t *testing.T) {
		tracker := NewAutoBanTracker()
		trigger := tracker.Observe(AutoBanObservation{Key: key, Enabled: true, Threshold: 0, Result: failure})
		require.NotNil(t, trigger)
	})
}

func TestAutoBanTrackerReusesBreakerClassification(t *testing.T) {
	tests := []struct {
		name   string
		result state.AttemptResult
		count  bool
	}{
		{name: "success", result: state.AttemptResult{}},
		{name: "invalid request", result: state.AttemptResult{Err: &common.UpstreamError{Status: 400, ProviderErrorType: "invalid_request_error"}}},
		{name: "not found", result: state.AttemptResult{Err: &common.UpstreamError{Status: 404}}},
		{name: "canceled", result: state.AttemptResult{Err: context.Canceled}},
		{name: "written", result: state.AttemptResult{Err: errors.New("mid-stream"), Written: true}},
		{name: "network", result: state.AttemptResult{Err: errors.New("network")}, count: true},
		{name: "rate limited", result: state.AttemptResult{Err: &common.UpstreamError{Status: 429}}, count: true},
		{name: "server error", result: state.AttemptResult{Err: &common.UpstreamError{Status: 500}}, count: true},
		{name: "unauthorized", result: state.AttemptResult{Err: &common.UpstreamError{Status: 401}}, count: true},
		{name: "forbidden", result: state.AttemptResult{Err: &common.UpstreamError{Status: 403}}, count: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker := NewAutoBanTracker()
			trigger := tracker.Observe(AutoBanObservation{
				Key: BreakerKey{Source: state.SourceAdmin, ID: 7}, Enabled: true, Threshold: 1, Result: test.result,
			})
			if test.count {
				require.NotNil(t, trigger)
			} else {
				require.Nil(t, trigger)
			}
		})
	}
}

func TestAutoBanTrackerDisabledRemovesEntry(t *testing.T) {
	tracker := NewAutoBanTracker()
	observation := AutoBanObservation{
		Key: BreakerKey{Source: state.SourceAdmin, ID: 7}, Enabled: true, Threshold: 2,
		Result: state.AttemptResult{Err: errors.New("network")},
	}
	require.Nil(t, tracker.Observe(observation))
	require.Equal(t, 1, tracker.Len())
	observation.Enabled = false
	require.Nil(t, tracker.Observe(observation))
	require.Zero(t, tracker.Len())
}

func TestAutoBanTrackerConcurrentThresholdEmitsOneTrigger(t *testing.T) {
	tracker := NewAutoBanTracker()
	workers := pool.NewWithResults[*attemptproxy.ChannelAutoDisableTrigger]().WithMaxGoroutines(16)
	for range 64 {
		workers.Go(func() *attemptproxy.ChannelAutoDisableTrigger {
			return tracker.Observe(AutoBanObservation{
				Key: BreakerKey{Source: state.SourceAdmin, ID: 7}, Enabled: true, Threshold: 3, Revision: 4,
				Result: state.AttemptResult{Err: errors.New("network")},
			})
		})
	}
	var triggers []*attemptproxy.ChannelAutoDisableTrigger
	for _, trigger := range workers.Wait() {
		if trigger != nil {
			triggers = append(triggers, trigger)
		}
	}
	require.Len(t, triggers, 1)
}

func TestAutoBanTrackerSweepKeepsRenewedEntry(t *testing.T) {
	tracker := NewAutoBanTracker()
	key := BreakerKey{Source: state.SourceAdmin, ID: 7}
	observation := AutoBanObservation{Key: key, Enabled: true, Threshold: 3, Result: state.AttemptResult{Err: errors.New("network")}}
	require.Nil(t, tracker.Observe(observation))
	entry, ok := tracker.entries.Load(key)
	require.True(t, ok)

	// Hold the entry lock so Observe must retain tracker.mu while waiting to renew it.
	entry.mu.Lock()
	entry.exp = time.Now().Add(-time.Second).UnixNano()
	observation.Result = state.AttemptResult{Err: &common.UpstreamError{Status: 404}}

	workers := pool.New()
	observeStarted := make(chan struct{})
	observeResult := make(chan *attemptproxy.ChannelAutoDisableTrigger, 1)
	workers.Go(func() {
		close(observeStarted)
		observeResult <- tracker.Observe(observation)
	})
	<-observeStarted

	// TryLock=false is the deterministic barrier proving Observe owns tracker.mu
	// and is blocked on entry.mu. A timeout only prevents a broken lock order from
	// hanging the test; it is not used to create the ordering.
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observeOwnsTracker := false
	for !observeOwnsTracker {
		if tracker.mu.TryLock() {
			tracker.mu.Unlock()
			runtime.Gosched()
			select {
			case <-deadline.Done():
				entry.mu.Unlock()
				workers.Wait()
				require.FailNow(t, "Observe never retained tracker.mu while waiting for entry.mu")
			default:
			}
			continue
		}
		observeOwnsTracker = true
	}

	sweepStarted := make(chan struct{})
	workers.Go(func() {
		close(sweepStarted)
		// This lock acquisition queues the sweep worker behind Observe before it
		// evaluates expiry, without adding a production-only hook.
		tracker.mu.Lock()
		tracker.mu.Unlock()
		tracker.sweep(time.Now())
	})
	<-sweepStarted

	entry.mu.Unlock()
	workers.Wait()
	require.Nil(t, <-observeResult)
	require.Equal(t, 1, tracker.Len())
}

func TestAutoBanTrackerSourceIDsDoNotCollide(t *testing.T) {
	tracker := NewAutoBanTracker()
	failure := state.AttemptResult{Err: errors.New("network")}
	require.Nil(t, tracker.Observe(AutoBanObservation{
		Key: BreakerKey{Source: state.SourceAdmin, ID: 7}, Enabled: true, Threshold: 2, Result: failure,
	}))
	require.Nil(t, tracker.Observe(AutoBanObservation{
		Key: BreakerKey{Source: state.SourcePrivate, ID: 7}, Enabled: true, Threshold: 2, Result: failure,
	}))
	require.Equal(t, 2, tracker.Len())
	require.NotNil(t, tracker.Observe(AutoBanObservation{
		Key: BreakerKey{Source: state.SourceAdmin, ID: 7}, Enabled: true, Threshold: 2, Result: failure,
	}))
	require.NotNil(t, tracker.Observe(AutoBanObservation{
		Key: BreakerKey{Source: state.SourcePrivate, ID: 7}, Enabled: true, Threshold: 2, Result: failure,
	}))
}
