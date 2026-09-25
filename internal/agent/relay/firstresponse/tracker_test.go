package firstresponse

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrackerPrefersFirstEventAndNeverOverwrites(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)

	tracker.observeFirstByte(startedAt.Add(40 * time.Millisecond))
	tracker.observeFirstByte(startedAt.Add(60 * time.Millisecond))
	require.Equal(t, 40, tracker.Milliseconds())

	tracker.observeFirstEvent(startedAt.Add(120 * time.Millisecond))
	tracker.observeFirstEvent(startedAt.Add(180 * time.Millisecond))
	require.Equal(t, 120, tracker.Milliseconds())
}

func TestTrackerClampsObservedSubMillisecondValue(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)

	tracker.observeFirstByte(startedAt.Add(100 * time.Microsecond))

	require.Equal(t, 1, tracker.Milliseconds())
}

func TestTrackerConcurrentObserveAndRead(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	var workers sync.WaitGroup

	for milliseconds := 1; milliseconds <= 32; milliseconds++ {
		workers.Add(1)
		go func(delay time.Duration) {
			defer workers.Done()
			tracker.observeFirstByte(startedAt.Add(delay))
			_ = tracker.Milliseconds()
		}(time.Duration(milliseconds) * time.Millisecond)
	}

	workers.Wait()
	require.GreaterOrEqual(t, tracker.Milliseconds(), 1)
	require.LessOrEqual(t, tracker.Milliseconds(), 32)
}

func TestNilTrackerHasNoObservedResponse(t *testing.T) {
	var tracker *Tracker

	require.Zero(t, tracker.Milliseconds())
}
