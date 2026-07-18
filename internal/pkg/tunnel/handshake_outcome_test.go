package tunnel

import (
	"sync/atomic"
	"testing"

	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

func TestHandshakeOutcomeOwnerAllowsExactlyOneConcurrentWinner(t *testing.T) {
	owner := NewHandshakeOutcomeOwner()
	start := make(chan struct{})
	winners := make(chan HandshakeOutcome, 4)
	workers := pool.New().WithMaxGoroutines(4)
	for _, outcome := range []HandshakeOutcome{
		HandshakeSucceeded,
		HandshakeTimedOut,
		HandshakeCanceled,
		HandshakeStopped,
	} {
		outcome := outcome
		workers.Go(func() {
			<-start
			if owner.TryOwn(outcome) {
				winners <- outcome
			}
		})
	}
	close(start)
	workers.Wait()
	close(winners)

	var won []HandshakeOutcome
	for outcome := range winners {
		won = append(won, outcome)
	}
	require.Len(t, won, 1)
	require.Equal(t, won[0], owner.Outcome())
}

func TestHandshakeOutcomeOwnerKeepsSuccessfulOwner(t *testing.T) {
	owner := NewHandshakeOutcomeOwner()
	require.Equal(t, HandshakePending, owner.Outcome())
	require.True(t, owner.TryOwn(HandshakeSucceeded))
	require.False(t, owner.TryOwn(HandshakeTimedOut))
	require.False(t, owner.TryOwn(HandshakeCanceled))
	require.Equal(t, HandshakeSucceeded, owner.Outcome())
}

func TestHandshakeOutcomeOwnerRejectsNilAndNonterminalOutcomes(t *testing.T) {
	var nilOwner *HandshakeOutcomeOwner
	require.False(t, nilOwner.TryOwn(HandshakeSucceeded))
	require.Equal(t, HandshakePending, nilOwner.Outcome())

	owner := NewHandshakeOutcomeOwner()
	require.False(t, owner.TryOwn(HandshakePending))
	require.False(t, owner.TryOwn(HandshakeOutcome(255)))
	require.Equal(t, HandshakePending, owner.Outcome())
}

func TestHandshakeOutcomeOwnerKeepsAbortOwner(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome HandshakeOutcome
	}{
		{name: "timed out", outcome: HandshakeTimedOut},
		{name: "canceled", outcome: HandshakeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := NewHandshakeOutcomeOwner()
			require.True(t, owner.TryOwn(tc.outcome))
			require.False(t, owner.TryOwn(HandshakeSucceeded))
			require.False(t, owner.TryOwn(HandshakeStopped))
			require.Equal(t, tc.outcome, owner.Outcome())
		})
	}
}

func TestHandshakeOutcomeOwnerSuccessfulOwnerSuppressesReadyAbortClose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		abort HandshakeOutcome
	}{
		{name: "timed out", abort: HandshakeTimedOut},
		{name: "canceled", abort: HandshakeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := NewHandshakeOutcomeOwner()
			abortReady := make(chan struct{})
			releaseAbort := make(chan struct{})
			abortDone := make(chan struct{})
			var closeCalls atomic.Int32
			go func() {
				defer close(abortDone)
				close(abortReady)
				<-releaseAbort
				if owner.TryOwn(tc.abort) {
					closeCalls.Add(1)
				}
			}()
			<-abortReady
			require.True(t, owner.TryOwn(HandshakeSucceeded))
			close(releaseAbort)
			<-abortDone
			require.Equal(t, HandshakeSucceeded, owner.Outcome())
			require.Zero(t, closeCalls.Load())
		})
	}
}
