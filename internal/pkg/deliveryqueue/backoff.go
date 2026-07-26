package deliveryqueue

import (
	"hash/fnv"
	"time"
)

const (
	defaultBackoffBase = time.Second
	defaultBackoffMax  = time.Minute
	defaultJitter      = 0.2
)

// Backoff returns a capped exponential delay with deterministic bounded jitter.
// Deterministic jitter keeps retry schedules stable across tests and snapshots
// while still preventing every item in a failed batch from retrying together.
func Backoff(id string, attempts int, base, max time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		base = defaultBackoffBase
	}
	if max <= 0 {
		max = defaultBackoffMax
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	if attempts < 1 {
		attempts = 1
	}
	delay := base
	for i := 1; i < attempts && delay < max; i++ {
		if delay > max/2 {
			delay = max
			break
		}
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	if jitter == 0 || delay == max {
		return delay
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte{byte(attempts), byte(attempts >> 8)})
	unit := float64(h.Sum32()%2001)/1000 - 1 // [-1, 1]
	adjusted := time.Duration(float64(delay) * (1 + unit*jitter))
	if adjusted > max {
		return max
	}
	if adjusted < 0 {
		return 0
	}
	return adjusted
}
