package agentproxy

import (
	"container/list"
	"sync"
	"time"
)

const (
	defaultCircuitFailureThreshold = 3
	defaultCircuitOpenDuration     = 30 * time.Second
	defaultCircuitLimit            = 256
)

type directCircuitKey struct {
	TargetAgentID      string
	AddressFingerprint string
}

type directCircuitOptions struct {
	FailureThreshold int
	OpenFor          time.Duration
	Now              func() time.Time
	Limit            int
	OnTransition     func(DirectCircuitTransition)
}

type DirectCircuitTransition struct {
	TargetAgentID string
	State         string
}

type directCircuitDenyReason uint8

const (
	directCircuitAllowed directCircuitDenyReason = iota
	directCircuitDeniedOpen
	directCircuitDeniedHalfOpen
	directCircuitDeniedCapacity
	directCircuitDeniedClosed
)

type directCircuitPermit struct {
	key        directCircuitKey
	generation uint64
	halfOpen   bool
}

type directCircuitState struct {
	generation  uint64
	active      int
	failures    int
	openedUntil time.Time
	halfOpen    bool
	lru         *list.Element
}

type directCircuit struct {
	mu               sync.Mutex
	failureThreshold int
	openFor          time.Duration
	now              func() time.Time
	limit            int
	states           map[directCircuitKey]*directCircuitState
	lru              list.List
	closed           bool
	nextGeneration   uint64
	onTransition     func(DirectCircuitTransition)
}

func newDirectCircuit(opts directCircuitOptions) *directCircuit {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = defaultCircuitFailureThreshold
	}
	if opts.OpenFor <= 0 {
		opts.OpenFor = defaultCircuitOpenDuration
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultCircuitLimit
	}
	return &directCircuit{
		failureThreshold: opts.FailureThreshold,
		openFor:          opts.OpenFor,
		now:              opts.Now,
		limit:            opts.Limit,
		states:           make(map[directCircuitKey]*directCircuitState),
		onTransition:     opts.OnTransition,
	}
}

func (c *directCircuit) allow(key directCircuitKey) (directCircuitPermit, bool) {
	permit, reason := c.admit(key)
	return permit, reason == directCircuitAllowed
}

func (c *directCircuit) admit(key directCircuitKey) (directCircuitPermit, directCircuitDenyReason) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return directCircuitPermit{}, directCircuitDeniedClosed
	}
	state := c.states[key]
	if state == nil {
		state = c.newStateLocked(key)
		if state == nil {
			c.mu.Unlock()
			return directCircuitPermit{}, directCircuitDeniedCapacity
		}
	}
	c.lru.MoveToFront(state.lru)
	permit := directCircuitPermit{key: key, generation: state.generation}
	if state.openedUntil.IsZero() {
		state.active++
		c.mu.Unlock()
		return permit, directCircuitAllowed
	}
	if c.now().Before(state.openedUntil) {
		c.mu.Unlock()
		return directCircuitPermit{}, directCircuitDeniedOpen
	}
	if state.halfOpen {
		c.mu.Unlock()
		return directCircuitPermit{}, directCircuitDeniedHalfOpen
	}
	state.halfOpen = true
	state.active++
	permit.halfOpen = true
	c.mu.Unlock()
	c.reportTransition(key, "half_open")
	return permit, directCircuitAllowed
}

func (c *directCircuit) transportFailed(permit directCircuitPermit) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	state := c.matchingStateLocked(permit)
	if state == nil {
		c.mu.Unlock()
		return
	}
	if state.active > 0 {
		state.active--
	}
	if permit.halfOpen {
		state.halfOpen = false
	}
	state.failures++
	opened := false
	if permit.halfOpen || state.failures >= c.failureThreshold {
		opened = permit.halfOpen || state.openedUntil.IsZero()
		state.failures = c.failureThreshold
		state.openedUntil = c.now().Add(c.openFor)
	}
	c.mu.Unlock()
	if opened {
		c.reportTransition(permit.key, "open")
	}
}

func (c *directCircuit) cancelled(permit directCircuitPermit) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if state := c.matchingStateLocked(permit); state != nil {
		if state.active > 0 {
			state.active--
		}
		if permit.halfOpen {
			state.halfOpen = false
		}
		if state.active == 0 && state.failures == 0 && state.openedUntil.IsZero() {
			c.removeLocked(permit.key)
		}
	}
	c.mu.Unlock()
}

func (c *directCircuit) httpResponded(permit directCircuitPermit) {
	c.mu.Lock()
	recovered := false
	if !c.closed {
		state := c.matchingStateLocked(permit)
		recovered = state != nil && (!state.openedUntil.IsZero() || state.halfOpen || state.failures > 0)
		if state != nil {
			c.removeLocked(permit.key)
		}
	}
	c.mu.Unlock()
	if recovered {
		c.reportTransition(permit.key, "closed")
	}
}

func (c *directCircuit) reset(targetAgentID, fingerprint string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	reset := make([]directCircuitKey, 0)
	for key := range c.states {
		if key.TargetAgentID == targetAgentID && (fingerprint == "" || key.AddressFingerprint == fingerprint) {
			c.removeLocked(key)
			reset = append(reset, key)
		}
	}
	c.mu.Unlock()
	for _, key := range reset {
		c.reportTransition(key, "reset")
	}
}

func (c *directCircuit) reportTransition(key directCircuitKey, state string) {
	if c.onTransition != nil {
		c.onTransition(DirectCircuitTransition{TargetAgentID: key.TargetAgentID, State: state})
	}
}

func (c *directCircuit) close() {
	c.mu.Lock()
	c.closed = true
	clear(c.states)
	c.lru.Init()
	c.mu.Unlock()
}

func (c *directCircuit) resourceCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.states)
}

func (c *directCircuit) newStateLocked(key directCircuitKey) *directCircuitState {
	for len(c.states) >= c.limit {
		oldest := c.oldestEvictableLocked()
		if oldest == nil {
			return nil
		}
		c.removeLocked(oldest.Value.(directCircuitKey))
	}
	c.nextGeneration++
	if c.nextGeneration == 0 {
		c.nextGeneration++
	}
	state := &directCircuitState{generation: c.nextGeneration}
	state.lru = c.lru.PushFront(key)
	c.states[key] = state
	return state
}

func (c *directCircuit) oldestEvictableLocked() *list.Element {
	for element := c.lru.Back(); element != nil; element = element.Prev() {
		state := c.states[element.Value.(directCircuitKey)]
		if state != nil && state.active == 0 && !state.halfOpen {
			return element
		}
	}
	return nil
}

func (c *directCircuit) matchingStateLocked(permit directCircuitPermit) *directCircuitState {
	state := c.states[permit.key]
	if state == nil || state.generation != permit.generation {
		return nil
	}
	return state
}

func (c *directCircuit) removeLocked(key directCircuitKey) {
	state := c.states[key]
	if state == nil {
		return
	}
	delete(c.states, key)
	c.lru.Remove(state.lru)
}
