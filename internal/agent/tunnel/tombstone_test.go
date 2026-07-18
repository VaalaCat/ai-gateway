package tunnel

import (
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestTombstoneStoreExpiresEntriesAtTTL(t *testing.T) {
	now := time.Unix(100, 0)
	store := newTombstoneStore(2, time.Second, func() time.Time { return now })
	id := testStreamID(1)
	store.Add(id)
	require.True(t, store.Contains(id))
	now = now.Add(time.Second)
	require.False(t, store.Contains(id))
}

func TestTombstoneStoreEvictsOldestAtLimit(t *testing.T) {
	now := time.Unix(100, 0)
	store := newTombstoneStore(2, time.Minute, func() time.Time { return now })
	store.Add(testStreamID(1))
	now = now.Add(time.Millisecond)
	store.Add(testStreamID(2))
	now = now.Add(time.Millisecond)
	store.Add(testStreamID(3))
	require.False(t, store.Contains(testStreamID(1)))
	require.True(t, store.Contains(testStreamID(2)))
	require.True(t, store.Contains(testStreamID(3)))
}

func TestTombstoneStoreZeroLimitStoresNothing(t *testing.T) {
	store := newTombstoneStore(0, time.Minute, time.Now)
	store.Add(wire.StreamID{1})
	require.False(t, store.Contains(wire.StreamID{1}))
}
