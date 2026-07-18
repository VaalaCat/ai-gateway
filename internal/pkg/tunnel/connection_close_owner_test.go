package tunnel

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

func TestConnectionCloseOwnerClosesExactlyOnceConcurrently(t *testing.T) {
	wantErr := errors.New("injected close error")
	var closeCalls atomic.Int32
	owner := NewConnectionCloseOwner(func() error {
		closeCalls.Add(1)
		return wantErr
	})
	results := make(chan error, 8)
	workers := pool.New().WithMaxGoroutines(8)
	for range 8 {
		workers.Go(func() { results <- owner.Close() })
	}
	workers.Wait()
	close(results)

	for err := range results {
		require.ErrorIs(t, err, wantErr)
	}
	require.EqualValues(t, 1, closeCalls.Load())
}

func TestConnectionCloseOwnerKeepsSuccessfulCloseResult(t *testing.T) {
	var closeCalls atomic.Int32
	owner := NewConnectionCloseOwner(func() error {
		closeCalls.Add(1)
		return nil
	})
	require.NoError(t, owner.Close())
	require.NoError(t, owner.Close())
	require.EqualValues(t, 1, closeCalls.Load())
}

func TestConnectionCloseOwnerAllowsNilOwnerAndCallback(t *testing.T) {
	var nilOwner *ConnectionCloseOwner
	require.NoError(t, nilOwner.Close())
	require.NoError(t, NewConnectionCloseOwner(nil).Close())
}
