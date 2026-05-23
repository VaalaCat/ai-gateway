package sync

// EnrichLastSeen overrides each item's last_seen with the value from tracker
// when the in-memory value is fresher (greater) than the DB value.
//
// Used by API handlers that read agent rows from DB but want the latest
// last_seen visible in the UI without waiting for the periodic batch flush.
//
// Generic over item type T. Callers supply:
//   - getID: extract agent_id from the item
//   - getLastSeen: extract the current (DB-side) last_seen from the item
//   - setLastSeen: write the new last_seen back into the item
//
// Nil tracker or empty slice is a no-op.
func EnrichLastSeen[T any](
	tracker *HeartbeatTracker,
	items []T,
	getID func(T) string,
	getLastSeen func(T) int64,
	setLastSeen func(*T, int64),
) {
	if tracker == nil || len(items) == 0 {
		return
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, getID(it))
	}
	mem := tracker.GetMany(ids)
	for i := range items {
		id := getID(items[i])
		memTS, ok := mem[id]
		if !ok {
			continue
		}
		dbTS := getLastSeen(items[i])
		if memTS > dbTS {
			setLastSeen(&items[i], memTS)
		}
	}
}
