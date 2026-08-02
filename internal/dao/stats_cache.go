package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

const (
	statsCacheCapacity = 256
	statsCacheTTL      = 15 * time.Second
)

// QueryKey contains every dimension that can change a cached statistics result.
type QueryKey struct {
	Name    string
	From    int64
	To      int64
	Scope   string
	UserID  uint
	TokenID uint
	Model   string
	Dim     string
	Metric  string
	Stat    string
	Gran    string
	TopN    int
}

type statsCacheEntry struct {
	expiresAt time.Time
	valueType reflect.Type
	payload   []byte
}

// StatsCache coalesces identical in-flight statistics queries and retains only
// a small, short-lived set of successful results.
type StatsCache struct {
	entries *lru.Cache[QueryKey, statsCacheEntry]
	ttl     time.Duration
	now     func() time.Time
	group   singleflight.Group

	flightMu   sync.Mutex
	flights    map[string]*statsLoadFlight
	flightSeq  uint64
	generation uint64
}

type statsLoadFlight struct {
	ctx        context.Context
	cancel     context.CancelFunc
	key        string
	generation uint64
	waiters    int
	finished   bool
}

func NewStatsCache() *StatsCache {
	return newStatsCache(statsCacheCapacity, statsCacheTTL, time.Now)
}

func newStatsCache(capacity int, ttl time.Duration, now func() time.Time) *StatsCache {
	entries, err := lru.New[QueryKey, statsCacheEntry](capacity)
	if err != nil {
		panic(fmt.Sprintf("create stats cache: %v", err))
	}
	return &StatsCache{entries: entries, ttl: ttl, now: now, flights: make(map[string]*statsLoadFlight)}
}

// Get returns an isolated copy of a cached value. Failed and canceled loads are
// never inserted. A canceled waiter does not need to wait for another caller's
// shared load to finish.
func (c *StatsCache) Get(ctx context.Context, key QueryKey, load func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if value, ok := c.get(key); ok {
		return value, nil
	}

	baseKey := c.singleflightKey(key)
	flight := c.joinFlight(ctx, baseKey)
	defer c.leaveFlight(baseKey, flight)
	result := c.group.DoChan(flight.key, func() (any, error) {
		defer c.finishFlight(baseKey, flight)
		if entry, ok := c.getEntry(key); ok {
			return entry, nil
		}
		value, err := load(flight.ctx)
		if err != nil {
			return nil, err
		}
		if err := flight.ctx.Err(); err != nil {
			return nil, err
		}
		entry, err := c.makeEntry(value)
		if err != nil {
			return nil, err
		}
		c.storeEntryIfCurrent(key, entry, flight.generation)
		return entry, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case loaded := <-result:
		if loaded.Err != nil {
			return nil, loaded.Err
		}
		entry, ok := loaded.Val.(statsCacheEntry)
		if !ok {
			return nil, fmt.Errorf("stats cache flight returned %T", loaded.Val)
		}
		return decodeStatsCacheEntry(entry)
	}
}

// Clear invalidates completed entries and every current in-flight generation.
// A load that ignores its canceled context is still prevented from storing later.
func (c *StatsCache) Clear() {
	c.flightMu.Lock()
	c.generation++
	for _, flight := range c.flights {
		flight.cancel()
	}
	c.flights = make(map[string]*statsLoadFlight)
	c.entries.Purge()
	c.flightMu.Unlock()
}

func (c *StatsCache) get(key QueryKey) (any, bool) {
	entry, ok := c.getEntry(key)
	if !ok {
		return nil, false
	}
	value, err := decodeStatsCacheEntry(entry)
	if err != nil {
		c.entries.Remove(key)
		return nil, false
	}
	return value, true
}

func (c *StatsCache) getEntry(key QueryKey) (statsCacheEntry, bool) {
	entry, ok := c.entries.Get(key)
	if !ok {
		return statsCacheEntry{}, false
	}
	if !c.now().Before(entry.expiresAt) {
		c.entries.Remove(key)
		return statsCacheEntry{}, false
	}
	return entry, true
}

func (c *StatsCache) joinFlight(ctx context.Context, baseKey string) *statsLoadFlight {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	if flight := c.flights[baseKey]; flight != nil {
		flight.waiters++
		return flight
	}
	sharedCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.flightSeq++
	flight := &statsLoadFlight{
		ctx: sharedCtx, cancel: cancel, waiters: 1,
		key: fmt.Sprintf("%s#%d", baseKey, c.flightSeq), generation: c.generation,
	}
	c.flights[baseKey] = flight
	return flight
}

func (c *StatsCache) storeEntryIfCurrent(key QueryKey, entry statsCacheEntry, generation uint64) {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	if c.generation == generation {
		c.entries.Add(key, entry)
	}
}

func (c *StatsCache) leaveFlight(baseKey string, flight *statsLoadFlight) {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	flight.waiters--
	if flight.waiters == 0 && !flight.finished {
		if c.flights[baseKey] == flight {
			delete(c.flights, baseKey)
		}
		flight.cancel()
	}
}

func (c *StatsCache) finishFlight(baseKey string, flight *statsLoadFlight) {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	flight.finished = true
	if c.flights[baseKey] == flight {
		delete(c.flights, baseKey)
	}
	flight.cancel()
}

func (c *StatsCache) makeEntry(value any) (statsCacheEntry, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return statsCacheEntry{}, fmt.Errorf("encode stats cache value: %w", err)
	}
	return statsCacheEntry{
		expiresAt: c.now().Add(c.ttl),
		valueType: reflect.TypeOf(value),
		payload:   payload,
	}, nil
}

func decodeStatsCacheEntry(entry statsCacheEntry) (any, error) {
	if entry.valueType == nil {
		return nil, nil
	}
	target := reflect.New(entry.valueType)
	if err := json.Unmarshal(entry.payload, target.Interface()); err != nil {
		return nil, fmt.Errorf("decode stats cache value: %w", err)
	}
	return target.Elem().Interface(), nil
}

func (c *StatsCache) singleflightKey(key QueryKey) string {
	payload, _ := json.Marshal(key)
	return string(payload)
}
