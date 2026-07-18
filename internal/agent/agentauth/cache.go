package agentauth

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
)

const (
	defaultRelayRefreshFraction = 0.5
	defaultForwardRefreshAfter  = 24 * time.Hour
	refreshLoopInterval         = time.Second
	maxRelayTicketEntries       = 2
	ticketFailureCooldown       = time.Second
)

var (
	errCacheClosed      = errors.New("agent auth cache is closed")
	errCacheNotRunning  = errors.New("agent auth cache is not running")
	errCacheAlreadyRun  = errors.New("agent auth cache already ran")
	errCapabilityOff    = errors.New("agent auth capability is disabled")
	errTicketRefresh    = errors.New("agent auth ticket refresh failed")
	errInvalidBootstrap = errors.New("agent auth bootstrap response is invalid")
)

type ControlClient interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

type CacheOptions struct {
	Now                  func() time.Time
	RelayRefreshFraction float64
	ForwardRefreshAfter  time.Duration
}

type BootstrapSnapshot struct {
	MasterInstanceID string
	Capabilities     []string
	SigningKeys      []pkgagentauth.PublicKey
}

type ticketEntry struct {
	token      string
	issuedAt   time.Time
	expiresAt  time.Time
	retryAfter time.Time
}

type ticketRefreshResult struct {
	entry ticketEntry
	err   error
}

type ticketRefreshCall struct {
	done   chan struct{}
	result ticketRefreshResult
}

type Cache struct {
	mu             sync.Mutex
	client         ControlClient
	opts           CacheOptions
	bootstrap      BootstrapSnapshot
	relayTickets   map[uint64]ticketEntry
	relayFailures  map[uint64]time.Time
	forward        ticketEntry
	hasForward     bool
	forwardFailure time.Time
	refreshCalls   map[string]*ticketRefreshCall
	workers        conc.WaitGroup
	cancel         context.CancelFunc
	runCtx         context.Context
	done           chan struct{}
	doneOnce       sync.Once
	started        bool
	closed         bool
}

func NewCache(client ControlClient, opts CacheOptions) *Cache {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RelayRefreshFraction <= 0 || opts.RelayRefreshFraction >= 1 || math.IsNaN(opts.RelayRefreshFraction) {
		opts.RelayRefreshFraction = defaultRelayRefreshFraction
	}
	if opts.ForwardRefreshAfter <= 0 {
		opts.ForwardRefreshAfter = defaultForwardRefreshAfter
	}
	return &Cache{
		client:        client,
		opts:          opts,
		relayTickets:  make(map[uint64]ticketEntry),
		relayFailures: make(map[uint64]time.Time),
		refreshCalls:  make(map[string]*ticketRefreshCall),
		done:          make(chan struct{}),
	}
}

func (c *Cache) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("agent auth cache context is required")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errCacheClosed
	}
	if c.started {
		c.mu.Unlock()
		return errCacheAlreadyRun
	}
	c.started = true
	c.runCtx, c.cancel = context.WithCancel(ctx)
	runCtx := c.runCtx
	c.mu.Unlock()

	if err := runCtx.Err(); err != nil {
		c.finish()
		return err
	}
	if err := c.loadBootstrap(runCtx); err != nil {
		if runErr := runCtx.Err(); runErr != nil {
			c.finish()
			return runErr
		}
		c.clearBootstrap()
	}
	if err := runCtx.Err(); err != nil {
		c.finish()
		return err
	}

	go c.runRefreshLoop(runCtx)
	return nil
}

func (c *Cache) RelayTicket(ctx context.Context, desiredGeneration uint64) (pkgagentauth.RelayTicket, error) {
	if ctx == nil {
		return "", errors.New("agent auth ticket context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	now := c.opts.Now()
	entry, refresh, err := c.relayEntry(desiredGeneration, now)
	if err != nil {
		return "", err
	}
	if !refresh {
		return pkgagentauth.RelayTicket(entry.token), nil
	}

	result, err := c.waitForRelayRefresh(ctx, desiredGeneration)
	if err == nil {
		return pkgagentauth.RelayTicket(result.token), nil
	}
	if callerErr := ctx.Err(); callerErr != nil {
		return "", callerErr
	}
	if cacheErr := c.runContextErr(); cacheErr != nil {
		return "", cacheErr
	}
	if fallback, ok := c.relayFallback(desiredGeneration, c.opts.Now()); ok {
		return pkgagentauth.RelayTicket(fallback.token), nil
	}
	return "", errTicketRefresh
}

func (c *Cache) CachedForwardTicket() (pkgagentauth.ForwardTicket, error) {
	now := c.opts.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.availableLocked(protocol.AgentCapabilityForwardV1); err != nil {
		return "", err
	}
	if !c.hasForward || !c.forward.validAt(now) {
		c.forward.token = ""
		c.forward = ticketEntry{}
		c.hasForward = false
		return "", errTicketRefresh
	}
	return pkgagentauth.ForwardTicket(c.forward.token), nil
}

func (c *Cache) Bootstrap() BootstrapSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneBootstrap(c.bootstrap)
}

func (c *Cache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	cancel := c.cancel
	closeWithoutOwner := !c.started
	if closeWithoutOwner {
		c.clearLocked()
	}
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if closeWithoutOwner {
		c.closeDone()
	}
}

func (c *Cache) Done() <-chan struct{} {
	return c.done
}

func (c *Cache) loadBootstrap(ctx context.Context) error {
	if isNilControlClient(c.client) {
		return errInvalidBootstrap
	}
	raw, err := c.client.Call(ctx, consts.RPCAgentAuthBootstrap, nil)
	if err != nil {
		return errInvalidBootstrap
	}
	var response protocol.AuthBootstrapResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return errInvalidBootstrap
	}
	if response.MasterInstanceID == "" || !validSigningKeys(response.SigningKeys) {
		return errInvalidBootstrap
	}
	snapshot := BootstrapSnapshot{
		MasterInstanceID: response.MasterInstanceID,
		Capabilities:     protocol.NormalizeAgentCapabilities(response.Capabilities),
		SigningKeys:      clonePublicKeys(response.SigningKeys),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.runCtx != ctx || ctx.Err() != nil {
		zeroPublicKeys(snapshot.SigningKeys)
		return errCacheClosed
	}
	c.clearBootstrapLocked()
	c.bootstrap = snapshot
	return nil
}

func (c *Cache) runRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(refreshLoopInterval)
	defer ticker.Stop()
	defer c.finish()
	c.refreshDueTickets(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshDueTickets(ctx)
		}
	}
}

func (c *Cache) refreshDueTickets(ctx context.Context) {
	now := c.opts.Now()
	c.mu.Lock()
	c.pruneExpiredRelayLocked(now)
	generations := make([]uint64, 0, len(c.relayTickets))
	for generation, entry := range c.relayTickets {
		if entry.refreshDue(now, c.opts.RelayRefreshFraction) {
			generations = append(generations, generation)
		}
	}
	refreshForward := c.forwardRefreshDueLocked(now)
	c.mu.Unlock()

	sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
	for _, generation := range generations {
		if ctx.Err() != nil {
			return
		}
		_, _ = c.RelayTicket(ctx, generation)
	}
	if refreshForward && ctx.Err() == nil {
		_ = c.refreshForwardTicket(ctx)
	}
}

func (c *Cache) relayEntry(generation uint64, now time.Time) (ticketEntry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.relayEntryLocked(generation, now)
}

func (c *Cache) relayEntryLocked(generation uint64, now time.Time) (ticketEntry, bool, error) {
	if err := c.availableLocked(protocol.AgentCapabilityTunnelV1); err != nil {
		return ticketEntry{}, false, err
	}
	c.pruneExpiredRelayLocked(now)
	failureActive := c.relayFailureActiveLocked(generation, now)
	entry, ok := c.relayTickets[generation]
	if !ok {
		if failureActive {
			return ticketEntry{}, false, errTicketRefresh
		}
		return ticketEntry{}, true, nil
	}
	if failureActive {
		return entry, false, nil
	}
	return entry, entry.refreshDue(now, c.opts.RelayRefreshFraction), nil
}

func (c *Cache) forwardRefreshDueLocked(now time.Time) bool {
	if c.availableLocked(protocol.AgentCapabilityForwardV1) != nil || c.forwardFailureActiveLocked(now) {
		return false
	}
	if !c.hasForward || !c.forward.validAt(now) {
		return true
	}
	return !now.Before(c.forward.issuedAt.Add(c.opts.ForwardRefreshAfter))
}

func (c *Cache) availableLocked(capability string) error {
	if !c.started {
		return errCacheNotRunning
	}
	if c.closed || c.runCtx == nil || c.runCtx.Err() != nil {
		return errCacheClosed
	}
	if !hasCapability(c.bootstrap.Capabilities, capability) {
		return errCapabilityOff
	}
	return nil
}

func (c *Cache) waitForRefresh(
	callerCtx context.Context,
	key string,
	refreshStillNeeded func() (ticketEntry, bool, error),
	refresh func(context.Context) (ticketEntry, error),
) (ticketEntry, error) {
	c.mu.Lock()
	if err := callerCtx.Err(); err != nil {
		c.mu.Unlock()
		return ticketEntry{}, err
	}
	if c.closed || c.runCtx == nil || c.runCtx.Err() != nil {
		c.mu.Unlock()
		return ticketEntry{}, errCacheClosed
	}
	sharedCtx := c.runCtx
	call := c.refreshCalls[key]
	var start chan struct{}
	if call == nil {
		if refreshStillNeeded != nil {
			entry, needed, err := refreshStillNeeded()
			if err != nil || !needed {
				c.mu.Unlock()
				return entry, err
			}
		}
		call = &ticketRefreshCall{done: make(chan struct{})}
		c.refreshCalls[key] = call
		start = make(chan struct{})
		c.workers.Go(func() {
			<-start
			c.runRefreshCall(sharedCtx, key, call, refresh)
		})
	}
	c.mu.Unlock()
	if start != nil {
		close(start)
	}

	return c.waitForRefreshResult(callerCtx, sharedCtx, call)
}

func (c *Cache) waitForRelayRefresh(callerCtx context.Context, generation uint64) (ticketEntry, error) {
	now := c.opts.Now()
	return c.waitForRefresh(
		callerCtx,
		relayRefreshKey(generation),
		func() (ticketEntry, bool, error) {
			return c.relayEntryLocked(generation, now)
		},
		func(sharedCtx context.Context) (ticketEntry, error) {
			return c.issueRelayTicket(sharedCtx, generation)
		},
	)
}

func relayRefreshKey(generation uint64) string {
	return fmt.Sprintf("relay:%d", generation)
}

func (c *Cache) waitForRefreshResult(
	callerCtx context.Context,
	sharedCtx context.Context,
	call *ticketRefreshCall,
) (ticketEntry, error) {
	select {
	case <-callerCtx.Done():
		return ticketEntry{}, callerCtx.Err()
	case <-sharedCtx.Done():
		return ticketEntry{}, sharedCtx.Err()
	case <-call.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := callerCtx.Err(); err != nil {
			return ticketEntry{}, err
		}
		if c.closed || c.runCtx != sharedCtx {
			return ticketEntry{}, errCacheClosed
		}
		if err := sharedCtx.Err(); err != nil {
			return ticketEntry{}, err
		}
		return call.result.entry, call.result.err
	}
}

func (c *Cache) runRefreshCall(
	ctx context.Context,
	key string,
	call *ticketRefreshCall,
	refresh func(context.Context) (ticketEntry, error),
) {
	result := ticketRefreshResult{err: errTicketRefresh}
	defer func() {
		if recover() != nil {
			result = ticketRefreshResult{err: errTicketRefresh}
		}
		c.completeRefreshCall(key, call, result)
	}()

	entry, err := refresh(ctx)
	if err != nil {
		return
	}
	result = ticketRefreshResult{entry: entry}
}

func (c *Cache) completeRefreshCall(key string, call *ticketRefreshCall, result ticketRefreshResult) {
	c.mu.Lock()
	if c.refreshCalls[key] == call {
		delete(c.refreshCalls, key)
	}
	call.result = result
	c.mu.Unlock()
	close(call.done)
}

func (c *Cache) issueRelayTicket(ctx context.Context, generation uint64) (entry ticketEntry, err error) {
	defer func() {
		if err != nil {
			c.recordRelayFailure(ctx, generation)
		}
	}()
	raw, err := c.callTicket(ctx, consts.RPCAgentIssueRelayTicket, protocol.RelayTicketRequest{
		DesiredGeneration: generation,
	})
	if err != nil {
		return ticketEntry{}, err
	}
	entry, err = c.decodeTicket(raw)
	if err != nil {
		return ticketEntry{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.availableLocked(protocol.AgentCapabilityTunnelV1); err != nil {
		return ticketEntry{}, err
	}
	c.pruneExpiredRelayLocked(entry.issuedAt)
	c.relayTickets[generation] = entry
	delete(c.relayFailures, generation)
	c.trimRelayTicketsLocked()
	return entry, nil
}

func (c *Cache) issueForwardTicket(ctx context.Context) (entry ticketEntry, err error) {
	defer func() {
		if err != nil {
			c.recordForwardFailure(ctx)
		}
	}()
	raw, err := c.callTicket(ctx, consts.RPCAgentIssueForwardTicket, nil)
	if err != nil {
		return ticketEntry{}, err
	}
	entry, err = c.decodeTicket(raw)
	if err != nil {
		return ticketEntry{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.availableLocked(protocol.AgentCapabilityForwardV1); err != nil {
		return ticketEntry{}, err
	}
	c.forward = entry
	c.hasForward = true
	c.forwardFailure = time.Time{}
	return entry, nil
}

func (c *Cache) refreshForwardTicket(ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = errTicketRefresh
			c.recordForwardFailure(ctx)
		}
	}()
	_, err = c.issueForwardTicket(ctx)
	return err
}

func (c *Cache) callTicket(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if isNilControlClient(c.client) {
		return nil, errTicketRefresh
	}
	raw, err := c.client.Call(ctx, method, params)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errTicketRefresh
	}
	return raw, nil
}

func (c *Cache) decodeTicket(raw json.RawMessage) (ticketEntry, error) {
	var response protocol.TicketResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ticketEntry{}, errTicketRefresh
	}
	now := c.opts.Now()
	expiresAt := time.Unix(response.ExpiresAt, 0)
	if response.Token == "" || !expiresAt.After(now) {
		return ticketEntry{}, errTicketRefresh
	}
	return ticketEntry{token: response.Token, issuedAt: now, expiresAt: expiresAt}, nil
}

func (c *Cache) relayFallback(generation uint64, now time.Time) (ticketEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.runCtx == nil || c.runCtx.Err() != nil {
		return ticketEntry{}, false
	}
	entry, ok := c.relayTickets[generation]
	if !ok || !entry.validAt(now) {
		delete(c.relayTickets, generation)
		return ticketEntry{}, false
	}
	entry.retryAfter = ticketRetryAfter(now, entry.expiresAt)
	c.relayTickets[generation] = entry
	return entry, true
}

func (c *Cache) pruneExpiredRelayLocked(now time.Time) {
	for generation, entry := range c.relayTickets {
		if !entry.validAt(now) {
			entry.token = ""
			delete(c.relayTickets, generation)
		}
	}
}

func (c *Cache) trimRelayTicketsLocked() {
	if len(c.relayTickets) <= maxRelayTicketEntries {
		return
	}
	generations := make([]uint64, 0, len(c.relayTickets))
	for generation := range c.relayTickets {
		generations = append(generations, generation)
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
	for _, generation := range generations[:len(generations)-maxRelayTicketEntries] {
		entry := c.relayTickets[generation]
		entry.token = ""
		delete(c.relayTickets, generation)
	}
}

func (c *Cache) recordRelayFailure(ctx context.Context, generation uint64) {
	if ctx.Err() != nil {
		return
	}
	retryAt := c.opts.Now().Add(ticketFailureCooldown)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.runCtx != ctx || ctx.Err() != nil {
		return
	}
	c.relayFailures[generation] = retryAt
	c.trimRelayFailuresLocked()
}

func (c *Cache) recordForwardFailure(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	retryAt := c.opts.Now().Add(ticketFailureCooldown)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.runCtx != ctx || ctx.Err() != nil {
		return
	}
	c.forwardFailure = retryAt
}

func (c *Cache) relayFailureActiveLocked(generation uint64, now time.Time) bool {
	retryAt, ok := c.relayFailures[generation]
	if !ok {
		return false
	}
	if now.Before(retryAt) {
		return true
	}
	delete(c.relayFailures, generation)
	return false
}

func (c *Cache) forwardFailureActiveLocked(now time.Time) bool {
	if c.forwardFailure.IsZero() {
		return false
	}
	if now.Before(c.forwardFailure) {
		return true
	}
	c.forwardFailure = time.Time{}
	return false
}

func (c *Cache) trimRelayFailuresLocked() {
	if len(c.relayFailures) <= maxRelayTicketEntries {
		return
	}
	generations := make([]uint64, 0, len(c.relayFailures))
	for generation := range c.relayFailures {
		generations = append(generations, generation)
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
	for _, generation := range generations[:len(generations)-maxRelayTicketEntries] {
		delete(c.relayFailures, generation)
	}
}

func (c *Cache) runContextErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runCtx != nil && c.runCtx.Err() != nil {
		return c.runCtx.Err()
	}
	if c.closed {
		return errCacheClosed
	}
	return nil
}

func (c *Cache) clearBootstrap() {
	c.mu.Lock()
	c.clearBootstrapLocked()
	c.mu.Unlock()
}

func (c *Cache) finish() {
	c.mu.Lock()
	c.closed = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.workers.Wait()
	c.mu.Lock()
	c.clearLocked()
	c.mu.Unlock()
	c.closeDone()
}

func (c *Cache) clearLocked() {
	c.clearBootstrapLocked()
	for generation, entry := range c.relayTickets {
		entry.token = ""
		delete(c.relayTickets, generation)
	}
	clear(c.relayFailures)
	c.forward.token = ""
	c.forward = ticketEntry{}
	c.hasForward = false
	c.forwardFailure = time.Time{}
}

func (c *Cache) clearBootstrapLocked() {
	zeroPublicKeys(c.bootstrap.SigningKeys)
	c.bootstrap.MasterInstanceID = ""
	c.bootstrap.Capabilities = nil
	c.bootstrap.SigningKeys = nil
}

func (c *Cache) closeDone() {
	c.doneOnce.Do(func() { close(c.done) })
}

func (e ticketEntry) validAt(now time.Time) bool {
	return e.token != "" && e.expiresAt.After(now)
}

func (e ticketEntry) refreshDue(now time.Time, fraction float64) bool {
	if !e.validAt(now) || now.Before(e.retryAfter) {
		return false
	}
	ttl := e.expiresAt.Sub(e.issuedAt)
	refreshAt := e.issuedAt.Add(time.Duration(float64(ttl) * fraction))
	return !now.Before(refreshAt)
}

func ticketRetryAfter(now, expiresAt time.Time) time.Time {
	delay := expiresAt.Sub(now) / 4
	if delay < time.Second {
		delay = time.Second
	}
	retryAt := now.Add(delay)
	if retryAt.After(expiresAt) {
		return expiresAt
	}
	return retryAt
}

func cloneBootstrap(snapshot BootstrapSnapshot) BootstrapSnapshot {
	return BootstrapSnapshot{
		MasterInstanceID: snapshot.MasterInstanceID,
		Capabilities:     append([]string(nil), snapshot.Capabilities...),
		SigningKeys:      clonePublicKeys(snapshot.SigningKeys),
	}
}

func clonePublicKeys(keys []pkgagentauth.PublicKey) []pkgagentauth.PublicKey {
	if len(keys) == 0 {
		return nil
	}
	cloned := make([]pkgagentauth.PublicKey, len(keys))
	for i, key := range keys {
		cloned[i] = key
		cloned[i].Key = append([]byte(nil), key.Key...)
	}
	return cloned
}

func zeroPublicKeys(keys []pkgagentauth.PublicKey) {
	for i := range keys {
		for j := range keys[i].Key {
			keys[i].Key[j] = 0
		}
		keys[i].Key = nil
	}
}

func validSigningKeys(keys []pkgagentauth.PublicKey) bool {
	if len(keys) == 0 || len(keys) > protocol.AgentAuthSigningKeysMaxCount {
		return false
	}
	keyIDs := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key.KeyID == "" || len(key.KeyID) > protocol.AgentAuthKeyIDMaxLength ||
			key.Algorithm != protocol.AgentAuthAlgorithmEdDSA ||
			len(key.Key) != ed25519.PublicKeySize {
			return false
		}
		if _, duplicate := keyIDs[key.KeyID]; duplicate {
			return false
		}
		keyIDs[key.KeyID] = struct{}{}
	}
	return true
}

func hasCapability(capabilities []string, wanted string) bool {
	index := sort.SearchStrings(capabilities, wanted)
	return index < len(capabilities) && capabilities[index] == wanted
}

func isNilControlClient(client ControlClient) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
