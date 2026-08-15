package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

const defaultDirectDrainTimeout = 30 * time.Second

const maxDirectIngressSessions = 256

var errDirectIngressClosed = errors.New("direct tunnel ingress closed")

type DirectTunnelIngressOptions struct {
	TargetAgentID          string
	FindAgentByID          func(string) *models.Agent
	LoadAuth               func() agentproxy.ForwardAuthSnapshot
	Limits                 wire.Limits
	TargetHandler          *TargetHandler
	APITargetHandler       APITargetHandler
	WebSocketTargetHandler WebSocketTargetHandler
	Logger                 *zap.Logger
	HandshakeTimeout       time.Duration
	DrainTimeout           time.Duration
	MaxSessions            func() int
	Now                    func() time.Time
	Metrics                *pkgmetrics.AgentRelayMetrics
	Suppressor             *diagnostics.Suppressor
}

type directIngressConnection struct {
	sourceAgentID     string
	owner             *wire.ConnectionCloseOwner
	session           *Session
	acceptedAt        time.Time
	limitsFingerprint [32]byte
	drainPending      bool
	pendingDrain      directLifecycleAction
}

// directSourceSlot holds at most one active session and one in-progress
// handshake (candidate) per verified Source. A new Confirmed handshake for the
// same Source replaces the active; the old active drains its admitted streams.
type directSourceSlot struct {
	active    *directIngressConnection
	candidate *directIngressConnection
}

type DirectTunnelIngress struct {
	opts            DirectTunnelIngressOptions
	limits          wire.Limits
	runtimeSettings atomic.Pointer[DirectRuntimeSettings]
	logs            *directLogs
	initErr         error

	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}

	mu                sync.Mutex
	accepting         bool
	closed            bool
	sources           map[string]*directSourceSlot
	draining          map[*directIngressConnection]struct{}
	changed           chan struct{}
	doneOnce          sync.Once
	finalizeOnce      sync.Once
	supervisorOnce    sync.Once
	workers           conc.WaitGroup
	supervisorStarted bool
	supervisorStopped bool
	metricMu          sync.Mutex
	metricSnapshot    IngressSnapshot

	beforeInstallSessionLock func()
	beforeReserveSourceLock  func()
	beforeSweepLock          atomic.Pointer[func()]
	afterSweep               atomic.Pointer[func()]
}

func NewDirectTunnelIngress(opts DirectTunnelIngressOptions) *DirectTunnelIngress {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = handshakeTimeout
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = defaultDirectDrainTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	limits, err := wire.SelectDirectLimits(opts.Limits, opts.Limits)
	if err == nil && (!validDirectAgentID(opts.TargetAgentID) || opts.FindAgentByID == nil || opts.LoadAuth == nil || opts.TargetHandler == nil) {
		err = errors.New("invalid direct tunnel ingress options")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	ingress := &DirectTunnelIngress{
		opts: opts, limits: limits, initErr: err, logs: newDirectLogs(opts.Logger, opts.Suppressor), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		accepting: err == nil, sources: make(map[string]*directSourceSlot),
		draining: make(map[*directIngressConnection]struct{}), changed: make(chan struct{}, 1),
	}
	return ingress
}

func (i *DirectTunnelIngress) ensureSupervisor() {
	if i == nil {
		return
	}
	i.supervisorOnce.Do(func() {
		i.mu.Lock()
		if i.closed {
			i.supervisorStopped = true
			i.closeDoneIfFinishedLocked()
			i.mu.Unlock()
			return
		}
		i.supervisorStarted = true
		i.workers.Go(i.sweepLoop)
		i.mu.Unlock()
	})
}

func (i *DirectTunnelIngress) maxSessions() int {
	if settings := i.runtimeSettings.Load(); settings != nil {
		return settings.MaxSessions
	}
	if i.opts.MaxSessions == nil {
		return maxDirectIngressSessions
	}
	if value := i.opts.MaxSessions(); value >= 1 {
		return value
	}
	return 1
}

func (i *DirectTunnelIngress) ApplyRuntimeSettings(settings DirectRuntimeSettings) {
	if i == nil {
		return
	}
	i.ensureSupervisor()
	settings = normalizeDirectRuntimeSettings(settings)
	now := i.opts.Now()
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return
	}
	i.runtimeSettings.Store(&settings)
	notices := i.reconcileRuntimeLocked(now, settings)
	i.signalChangedLocked()
	i.mu.Unlock()
	emitDirectLifecycleNotices(i.logs, notices)
	i.publishMetrics()
}

func (i *DirectTunnelIngress) currentRuntimeSettings() DirectRuntimeSettings {
	if settings := i.runtimeSettings.Load(); settings != nil {
		return *settings
	}
	return normalizeDirectRuntimeSettings(DirectRuntimeSettings{
		Limits: i.limits, MaxSessions: i.maxSessions(), IdleTimeout: 0, DrainTimeout: i.opts.DrainTimeout,
	})
}

func (i *DirectTunnelIngress) publishedSettingsLocked(fallback DirectRuntimeSettings) DirectRuntimeSettings {
	if published := i.runtimeSettings.Load(); published != nil {
		return *published
	}
	return fallback
}

func (i *DirectTunnelIngress) reconcileRuntimeLocked(now time.Time, settings DirectRuntimeSettings) []directLifecycleNotice {
	// behavior change: pending is a reconcile decision, not a terminal state.
	// Keep admission stopped while recomputing, then reactivate only survivors.
	previousPending := make([]struct {
		slot    *directSourceSlot
		tracked *directIngressConnection
	}, 0)
	for _, slot := range i.sources {
		if slot.active == nil || !slot.active.drainPending {
			continue
		}
		previousPending = append(previousPending, struct {
			slot    *directSourceSlot
			tracked *directIngressConnection
		}{slot: slot, tracked: slot.active})
		slot.active.drainPending = false
		slot.active.pendingDrain = directLifecycleReplaced
	}
	zeroFingerprint := [32]byte{}
	desiredLimits := limitsFingerprint(settings.Limits)
	for _, slot := range i.sources {
		active := slot.active
		if active == nil || active.limitsFingerprint == zeroFingerprint || active.limitsFingerprint == desiredLimits {
			continue
		}
		i.markDrainPendingLocked(active, directLifecycleDraining)
	}
	for _, slot := range i.sources {
		active := slot.active
		if active == nil || active.session == nil || settings.IdleTimeout <= 0 {
			continue
		}
		idleSince, idle := active.session.idleSinceTime()
		if !idle || now.Sub(idleSince) < settings.IdleTimeout {
			continue
		}
		i.markDrainPendingLocked(active, directLifecycleEvicted)
	}
	for i.projectedOccupiedSourceCountLocked() > settings.MaxSessions {
		victim := i.oldestDrainCandidateLocked()
		if victim == nil {
			break
		}
		i.markDrainPendingLocked(victim, directLifecycleEvicted)
	}
	for _, previous := range previousPending {
		if previous.slot.active != previous.tracked || previous.tracked.drainPending {
			continue
		}
		if !previous.tracked.session.tryActivate() {
			i.markDrainPendingLocked(previous.tracked, directLifecycleDraining)
		}
	}
	var notices []directLifecycleNotice
	for source, slot := range i.sources {
		active := slot.active
		if active == nil || !active.drainPending || len(i.draining) >= settings.MaxSessions {
			continue
		}
		action := active.pendingDrain
		movedNotices, moved := i.tryStartDrainLocked(active, settings, action)
		if moved {
			slot.active = nil
			notices = append(notices, movedNotices...)
			i.gcSourceLocked(source, slot)
		}
	}
	return notices
}

func (i *DirectTunnelIngress) markDrainPendingLocked(
	tracked *directIngressConnection, action directLifecycleAction,
) {
	if tracked == nil || tracked.session == nil {
		return
	}
	// behavior change: sessions waiting for a bounded drain slot reject new OPENs.
	tracked.session.setAccepting(false)
	if !tracked.drainPending || action == directLifecycleEvicted {
		tracked.pendingDrain = action
	}
	tracked.drainPending = true
}

func (i *DirectTunnelIngress) projectedOccupiedSourceCountLocked() int {
	count := 0
	for _, slot := range i.sources {
		// behavior change: a replacement candidate shares its active's slot;
		// only a candidate without an active reserves separate projected capacity.
		if slot.active != nil && !slot.active.drainPending || slot.active == nil && slot.candidate != nil {
			count++
		}
	}
	return count
}

func (i *DirectTunnelIngress) oldestDrainCandidateLocked() *directIngressConnection {
	var victim *directIngressConnection
	for _, slot := range i.sources {
		active := slot.active
		if active == nil || active.drainPending {
			continue
		}
		if victim == nil || active.acceptedAt.Before(victim.acceptedAt) {
			victim = active
		}
	}
	return victim
}

func (i *DirectTunnelIngress) sweepLoop() {
	ticker := time.NewTicker(directPoolSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-i.ctx.Done():
			return
		case <-ticker.C:
		}
		if context.Cause(i.ctx) != nil {
			return
		}
		settings := i.currentRuntimeSettings()
		now := i.opts.Now()
		if hook := i.beforeSweepLock.Load(); hook != nil {
			(*hook)()
		}
		i.mu.Lock()
		settings = i.publishedSettingsLocked(settings)
		var notices []directLifecycleNotice
		if !i.closed {
			notices = i.reconcileRuntimeLocked(now, settings)
		}
		i.signalChangedLocked()
		i.mu.Unlock()
		emitDirectLifecycleNotices(i.logs, notices)
		i.publishMetrics()
		if hook := i.afterSweep.Load(); hook != nil {
			(*hook)()
		}
	}
}

func (i *DirectTunnelIngress) Handle(c *gin.Context) {
	if c == nil || i == nil {
		return
	}
	i.ensureSupervisor()
	if !i.localTarget(c) {
		i.reject(c, http.StatusForbidden, "direct_target_mismatch")
		return
	}
	if !i.acceptsNew() {
		i.reject(c, http.StatusForbidden, "direct_ingress_draining")
		return
	}
	ticket, ok := directBearer(c.Request)
	if !ok {
		i.reject(c, http.StatusUnauthorized, "direct_ticket_invalid")
		return
	}
	claims, err := agentproxy.VerifyForwardTicket(i.opts.LoadAuth(), ticket)
	if err != nil || !i.validClaims(claims) {
		i.reject(c, http.StatusUnauthorized, "direct_ticket_invalid")
		return
	}
	if !i.sourceEnabled(claims.SourceAgentID) {
		i.reject(c, http.StatusForbidden, "direct_source_unavailable")
		return
	}
	// behavior change: a direct socket requires the local target to be enabled now.
	if !i.targetStatusEnabled() {
		i.reject(c, http.StatusForbidden, "direct_target_unavailable")
		return
	}
	runtimeSettings := i.currentRuntimeSettings()
	tracked, status, code, notices := i.reserveSource(claims.SourceAgentID, runtimeSettings)
	emitDirectLifecycleNotices(i.logs, notices)
	if tracked == nil {
		i.reject(c, status, code)
		return
	}
	i.publishMetrics()
	defer func() {
		i.releaseSource(tracked)
		i.publishMetrics()
	}()

	conn, err := directUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		i.rejectUpgraded(c.Request, claims.SourceAgentID, "handshake", "protocol", err)
		return
	}
	owner := wire.NewConnectionCloseOwner(conn.Close)
	if !i.installOwner(tracked, owner) {
		i.rejectUpgraded(c.Request, claims.SourceAgentID, "session", "direct_closed", errDirectIngressClosed)
		_ = owner.Close()
		return
	}
	generation, negotiated, handshakeFingerprint, err := i.acceptHandshake(conn)
	if err != nil {
		i.rejectUpgraded(c.Request, claims.SourceAgentID, "handshake", "protocol", err)
		_ = owner.Close()
		return
	}
	tracked.limitsFingerprint = handshakeFingerprint
	owned := &closeOwnedClientConn{sessionConn: conn, closeOwner: owner}
	session := newSession(owned, generation, negotiated, SessionOptions{
		Direction: SessionDirectionDirectIncoming, IngressKind: agentproxy.IngressKindDirectTunnel,
		BoundSourceAgentID: claims.SourceAgentID, AdmissionDeadline: claims.ExpiresAt.Time,
		SourceEnabled: i.sourceEnabled, TargetStatusEnabled: i.targetStatusEnabled,
		TargetHandler: i.opts.TargetHandler, APITargetHandler: i.opts.APITargetHandler,
		WebSocketTargetHandler: i.opts.WebSocketTargetHandler,
		Logger:                 i.opts.Logger, Metrics: i.opts.Metrics,
		directLogs: i.logs, directSourceAgentID: claims.SourceAgentID, directTargetAgentID: i.opts.TargetAgentID,
	})
	installed, notices := i.installSession(tracked, session, i.opts.Now(), i.currentRuntimeSettings())
	emitDirectLifecycleNotices(i.logs, notices)
	if !installed {
		i.rejectUpgraded(c.Request, claims.SourceAgentID, "session", "direct_closed", errDirectIngressClosed)
		_ = session.Close(context.Background())
		return
	}
	i.publishMetrics()
	i.logs.IngressAccepted(directLogEvent{
		SourceAgentID: claims.SourceAgentID, TargetAgentID: i.opts.TargetAgentID,
		Stage: "handshake", ReasonCode: "none", SessionGeneration: generation, TargetInbound: true,
	}, requestDirectEndpoint(c.Request))
	_ = session.Run(i.ctx)
}

func (i *DirectTunnelIngress) localTarget(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	values, ok := c.Request.URL.Query()["target_agent_id"]
	return ok && len(values) == 1 && values[0] == i.opts.TargetAgentID
}

func (i *DirectTunnelIngress) acceptsNew() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.initErr == nil && i.accepting && !i.closed
}

func directBearer(request *http.Request) (agentauth.ForwardTicket, bool) {
	if request == nil {
		return "", false
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.SplitN(values[0], " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || strings.TrimSpace(parts[1]) != parts[1] {
		return "", false
	}
	return agentauth.ForwardTicket(parts[1]), true
}

func (i *DirectTunnelIngress) validClaims(claims *agentauth.ForwardClaims) bool {
	return claims != nil && validDirectAgentID(claims.SourceAgentID) && claims.ExpiresAt != nil &&
		i.opts.Now().Before(claims.ExpiresAt.Time)
}

func (i *DirectTunnelIngress) sourceEnabled(sourceAgentID string) bool {
	if i == nil || i.opts.FindAgentByID == nil || !validDirectAgentID(sourceAgentID) {
		return false
	}
	source := i.opts.FindAgentByID(sourceAgentID)
	return source != nil && source.AgentID == sourceAgentID && source.Status == consts.StatusEnabled
}

func (i *DirectTunnelIngress) targetStatusEnabled() bool {
	if i == nil || i.opts.FindAgentByID == nil || !validDirectAgentID(i.opts.TargetAgentID) {
		return false
	}
	target := i.opts.FindAgentByID(i.opts.TargetAgentID)
	return target != nil && target.AgentID == i.opts.TargetAgentID && target.Status == consts.StatusEnabled
}

// reserveSource registers an in-progress handshake (candidate) for a verified
// Source. A second concurrent handshake for the same Source is rejected; a new
// Source is admitted only if active+candidate and draining are below their
// caps, evicting the oldest idle active when necessary.
func (i *DirectTunnelIngress) reserveSource(
	sourceAgentID string, settings DirectRuntimeSettings,
) (*directIngressConnection, int, string, []directLifecycleNotice) {
	if i.beforeReserveSourceLock != nil {
		i.beforeReserveSourceLock()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	settings = i.publishedSettingsLocked(settings)
	if i.closed || !i.accepting {
		return nil, http.StatusForbidden, "direct_ingress_draining", nil
	}
	slot := i.sources[sourceAgentID]
	if slot == nil {
		slot = &directSourceSlot{}
		i.sources[sourceAgentID] = slot
	}
	if slot.candidate != nil {
		i.gcSourceLocked(sourceAgentID, slot)
		return nil, http.StatusConflict, "direct_source_duplicate", nil
	}
	if slot.active != nil && len(i.draining) >= settings.MaxSessions {
		i.gcSourceLocked(sourceAgentID, slot)
		return nil, http.StatusServiceUnavailable, directReasonCapacity, nil
	}
	// Replacing this Source's own active never needs new capacity. A brand new
	// active does.
	var notices []directLifecycleNotice
	if slot.active == nil && i.occupiedSourceCountLocked() >= settings.MaxSessions {
		var evicted bool
		notices, evicted = i.evictOldestIdleActiveLocked(settings)
		if !evicted {
			i.gcSourceLocked(sourceAgentID, slot)
			return nil, http.StatusServiceUnavailable, directReasonCapacity, notices
		}
	}
	tracked := &directIngressConnection{sourceAgentID: sourceAgentID}
	slot.candidate = tracked
	i.signalChangedLocked()
	return tracked, 0, "", notices
}

func (i *DirectTunnelIngress) installOwner(tracked *directIngressConnection, owner *wire.ConnectionCloseOwner) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	slot := i.sources[tracked.sourceAgentID]
	if slot == nil || slot.candidate != tracked || i.closed {
		return false
	}
	tracked.owner = owner
	return true
}

// installSession promotes a Confirmed candidate to the active session for its
// Source, draining any prior active. The prior active stops admitting new
// FrameOpen but keeps its already-admitted streams until idle or deadline.
func (i *DirectTunnelIngress) installSession(
	tracked *directIngressConnection, session *Session, now time.Time, settings DirectRuntimeSettings,
) (bool, []directLifecycleNotice) {
	if i.beforeInstallSessionLock != nil {
		i.beforeInstallSessionLock()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	settings = i.publishedSettingsLocked(settings)
	slot := i.sources[tracked.sourceAgentID]
	if slot == nil || slot.candidate != tracked || i.closed || !i.accepting ||
		tracked.limitsFingerprint != limitsFingerprint(settings.Limits) ||
		i.occupiedSourceCountLocked() > settings.MaxSessions ||
		(slot.active != nil && len(i.draining) >= settings.MaxSessions) {
		return false, nil
	}
	old := slot.active
	var notices []directLifecycleNotice
	if old != nil {
		var moved bool
		notices, moved = i.tryStartDrainLocked(old, settings, directLifecycleReplaced)
		if !moved {
			return false, nil
		}
	}
	tracked.session = session
	tracked.acceptedAt = now
	// behavior change: acceptedAt is not the idle timeout origin for a busy session.
	session.setActivityNow(i.opts.Now, now)
	slot.candidate = nil
	slot.active = tracked
	i.signalChangedLocked()
	return true, notices
}

func (i *DirectTunnelIngress) releaseSource(tracked *directIngressConnection) {
	i.mu.Lock()
	if slot := i.sources[tracked.sourceAgentID]; slot != nil {
		if slot.candidate == tracked {
			slot.candidate = nil
		}
		if slot.active == tracked {
			slot.active = nil
		}
		i.gcSourceLocked(tracked.sourceAgentID, slot)
	}
	delete(i.draining, tracked)
	i.signalChangedLocked()
	i.closeDoneIfFinishedLocked()
	i.mu.Unlock()
	if tracked.session != nil {
		emitDirectLifecycleNotices(i.logs, []directLifecycleNotice{{
			action: directLifecycleClosed,
			event: directLogEvent{
				SourceAgentID: tracked.sourceAgentID, TargetAgentID: i.opts.TargetAgentID,
				Stage: "close", ReasonCode: "closed", SessionGeneration: tracked.session.Generation(), TargetInbound: true,
			},
		}})
	}
}

func (i *DirectTunnelIngress) gcSourceLocked(sourceAgentID string, slot *directSourceSlot) {
	if slot.active == nil && slot.candidate == nil {
		delete(i.sources, sourceAgentID)
	}
}

func (i *DirectTunnelIngress) activeAndCandidateCountLocked() int {
	count := 0
	for _, slot := range i.sources {
		if slot.active != nil {
			count++
		}
		if slot.candidate != nil {
			count++
		}
	}
	return count
}

func (i *DirectTunnelIngress) occupiedSourceCountLocked() int {
	count := 0
	for _, slot := range i.sources {
		if slot.active != nil || slot.candidate != nil {
			count++
		}
	}
	return count
}

// evictOldestIdleActiveLocked drains the oldest idle active to free a slot for
// a new Source. It refuses when no active is idle or draining is already full.
func (i *DirectTunnelIngress) evictOldestIdleActiveLocked(
	settings DirectRuntimeSettings,
) ([]directLifecycleNotice, bool) {
	var victim *directIngressConnection
	var victimSlot *directSourceSlot
	var victimIdleSince time.Time
	for _, slot := range i.sources {
		active := slot.active
		if active == nil || active.session == nil || active.drainPending {
			continue
		}
		idleSince, idle := active.session.idleSinceTime()
		if !idle {
			continue
		}
		if victim == nil || idleSince.Before(victimIdleSince) ||
			idleSince.Equal(victimIdleSince) && active.acceptedAt.Before(victim.acceptedAt) {
			victim = active
			victimSlot = slot
			victimIdleSince = idleSince
		}
	}
	if victim == nil {
		return nil, false
	}
	notices, moved := i.tryStartDrainLocked(victim, settings, directLifecycleEvicted)
	if !moved {
		return nil, false
	}
	victimSlot.active = nil
	return notices, true
}

// tryStartDrainLocked is the cap-aware transition for every non-terminal
// ingress replacement and reconcile path.
func (i *DirectTunnelIngress) tryStartDrainLocked(
	tracked *directIngressConnection, settings DirectRuntimeSettings, action directLifecycleAction,
) ([]directLifecycleNotice, bool) {
	if tracked == nil || tracked.session == nil || len(i.draining) >= settings.MaxSessions {
		return nil, false
	}
	// behavior change: a full draining registry preserves the healthy active.
	return i.startDrainLocked(tracked, settings.DrainTimeout, action), true
}

// startDrainLocked stops new admission on a session and spawns a watcher that
// closes it once idle or the drain deadline passes. The connection's own Handle
// goroutine removes it from the registry after its session ends.
func (i *DirectTunnelIngress) startDrainLocked(
	tracked *directIngressConnection, drainTimeout time.Duration, action directLifecycleAction,
) []directLifecycleNotice {
	if tracked == nil || tracked.session == nil {
		return nil
	}
	if _, exists := i.draining[tracked]; exists {
		return nil
	}
	tracked.drainPending = false
	tracked.pendingDrain = directLifecycleReplaced
	tracked.session.setAccepting(false)
	i.draining[tracked] = struct{}{}
	i.workers.Go(func() { closeSessionAfterDrain(i.ctx, tracked.session, drainTimeout) })
	event := directLogEvent{
		SourceAgentID: tracked.sourceAgentID, TargetAgentID: i.opts.TargetAgentID,
		Stage: "drain", ReasonCode: "closed", SessionGeneration: tracked.session.Generation(), TargetInbound: true,
	}
	notices := make([]directLifecycleNotice, 0, 2)
	if action != directLifecycleDraining {
		actionEvent := event
		if action == directLifecycleReplaced {
			actionEvent.Stage = "replace"
		} else {
			actionEvent.Stage = "evict"
		}
		notices = append(notices, directLifecycleNotice{action: action, event: actionEvent})
	}
	return append(notices, directLifecycleNotice{action: directLifecycleDraining, event: event})
}

func (i *DirectTunnelIngress) reject(c *gin.Context, status int, code string) {
	i.logs.IngressRejected(directLogEvent{
		TargetAgentID: i.opts.TargetAgentID, Stage: "ingress", ReasonCode: normalizeDirectLogReason(code), TargetInbound: true,
	}, requestDirectEndpoint(c.Request), errors.New(code))
	c.AbortWithStatusJSON(status, gin.H{"code": code})
}

func (i *DirectTunnelIngress) rejectUpgraded(request *http.Request, sourceAgentID, stage, code string, cause error) {
	i.logs.IngressRejected(directLogEvent{
		SourceAgentID: sourceAgentID, TargetAgentID: i.opts.TargetAgentID,
		Stage: stage, ReasonCode: normalizeDirectLogReason(code), TargetInbound: true,
	}, requestDirectEndpoint(request), cause)
}

func requestDirectEndpoint(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	scheme := "ws"
	if request.TLS != nil {
		scheme = "wss"
	}
	return scheme + "://" + request.Host + request.URL.RequestURI()
}

func (i *DirectTunnelIngress) acceptHandshake(conn *websocket.Conn) (uint64, wire.Limits, [32]byte, error) {
	// behavior change: all four target handshake messages share one total budget.
	deadline := i.handshakeDeadline()
	runtimeSettings := i.currentRuntimeSettings()
	conn.SetReadLimit(directHandshakeReadLimit)
	var hello wire.DirectHello
	if err := readDirectText(i.ctx, conn, deadline, &hello); err != nil || hello.ProtocolVersion != wire.ProtocolVersion {
		return 0, wire.Limits{}, [32]byte{}, errors.New("invalid direct HELLO")
	}
	negotiated, err := wire.SelectDirectLimits(hello.Limits, runtimeSettings.Limits)
	if err != nil {
		return 0, wire.Limits{}, [32]byte{}, errors.New("invalid direct HELLO limits")
	}
	generation, err := newDirectSessionGeneration()
	if err != nil {
		return 0, wire.Limits{}, [32]byte{}, errors.New("direct generation failed")
	}
	if err := writeDirectText(i.ctx, conn, deadline, wire.DirectReady{
		ProtocolVersion: wire.ProtocolVersion, TargetAgentID: i.opts.TargetAgentID,
		SessionGeneration: generation, Limits: negotiated,
	}); err != nil {
		return 0, wire.Limits{}, [32]byte{}, errors.New("send direct READY failed")
	}
	var accepted wire.DirectAccepted
	if err := readDirectText(i.ctx, conn, deadline, &accepted); err != nil || accepted.SessionGeneration != generation {
		return 0, wire.Limits{}, [32]byte{}, errors.New("invalid direct ACCEPTED")
	}
	if err := writeDirectText(i.ctx, conn, deadline, wire.DirectConfirmed{SessionGeneration: generation}); err != nil {
		return 0, wire.Limits{}, [32]byte{}, errors.New("send direct CONFIRMED failed")
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return 0, wire.Limits{}, [32]byte{}, errors.New("clear direct read deadline failed")
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		return 0, wire.Limits{}, [32]byte{}, errors.New("clear direct write deadline failed")
	}
	conn.SetReadLimit(sessionMessageReadLimit(negotiated))
	return generation, negotiated, limitsFingerprint(runtimeSettings.Limits), nil
}

func (i *DirectTunnelIngress) handshakeDeadline() time.Time {
	deadline := i.opts.Now().Add(i.opts.HandshakeTimeout)
	if contextDeadline, ok := i.ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}

func newDirectSessionGeneration() (uint64, error) {
	var raw [8]byte
	for range 2 {
		if _, err := rand.Read(raw[:]); err != nil {
			return 0, err
		}
		if generation := binary.BigEndian.Uint64(raw[:]); generation != 0 {
			return generation, nil
		}
	}
	return 0, errors.New("zero direct session generation")
}

func (i *DirectTunnelIngress) Drain(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	settings := i.currentRuntimeSettings()
	i.mu.Lock()
	settings = i.publishedSettingsLocked(settings)
	i.accepting = false
	var notices []directLifecycleNotice
	for source, slot := range i.sources {
		if slot.active == nil {
			continue
		}
		active := slot.active
		slot.active = nil
		notices = append(notices, i.startDrainLocked(active, settings.DrainTimeout, directLifecycleDraining)...)
		i.gcSourceLocked(source, slot)
	}
	i.signalChangedLocked()
	i.mu.Unlock()
	emitDirectLifecycleNotices(i.logs, notices)
	i.publishMetrics()
	drainTimeout := settings.DrainTimeout
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	for {
		if i.connectionCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
			i.closeConnections()
			timer.Reset(drainTimeout)
		case <-i.changed:
		}
	}
}

func (i *DirectTunnelIngress) Close(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	if i == nil {
		return nil
	}
	i.Cancel()
	select {
	case <-i.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (i *DirectTunnelIngress) StopAdmission() {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.accepting = false
	i.signalChangedLocked()
	i.mu.Unlock()
}

func (i *DirectTunnelIngress) Cancel() {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.accepting = false
	firstClose := !i.closed
	i.closed = true
	if firstClose {
		i.cancel(errDirectIngressClosed)
		if !i.supervisorStarted {
			i.supervisorStopped = true
		}
		i.finalizeOnce.Do(func() { go i.finalize() })
	}
	owners := i.connectionOwnersLocked()
	i.closeDoneIfFinishedLocked()
	i.signalChangedLocked()
	i.mu.Unlock()
	for _, owner := range owners {
		_ = owner.Close()
	}
	i.publishMetrics()
}

func (i *DirectTunnelIngress) finalize() {
	i.workers.Wait()
	i.mu.Lock()
	i.supervisorStopped = true
	i.closeDoneIfFinishedLocked()
	i.mu.Unlock()
	i.publishMetrics()
}

func (i *DirectTunnelIngress) closeConnections() {
	i.mu.Lock()
	owners := i.connectionOwnersLocked()
	i.mu.Unlock()
	for _, owner := range owners {
		_ = owner.Close()
	}
}

func (i *DirectTunnelIngress) connectionOwnersLocked() []*wire.ConnectionCloseOwner {
	owners := make([]*wire.ConnectionCloseOwner, 0, len(i.sources)+len(i.draining))
	appendOwner := func(tracked *directIngressConnection) {
		if tracked != nil && tracked.owner != nil {
			owners = append(owners, tracked.owner)
		}
	}
	for _, slot := range i.sources {
		appendOwner(slot.active)
		appendOwner(slot.candidate)
	}
	for tracked := range i.draining {
		appendOwner(tracked)
	}
	return owners
}

func (i *DirectTunnelIngress) connectionCountLocked() int {
	return i.activeAndCandidateCountLocked() + len(i.draining)
}

func (i *DirectTunnelIngress) connectionCount() int {
	if i == nil {
		return 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.connectionCountLocked()
}

// IngressSnapshot reports incoming direct session resource usage.
type IngressSnapshot struct {
	Active                      int
	Candidates                  int
	Draining                    int
	Streams                     int
	Sockets                     int
	Timers                      int
	BufferedBytes               int64
	MaxSessionPeakBufferedBytes int64
}

func (i *DirectTunnelIngress) Snapshot() IngressSnapshot {
	if i == nil {
		return IngressSnapshot{}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	snapshot := IngressSnapshot{}
	for _, slot := range i.sources {
		if slot.active != nil {
			snapshot.Active++
			if slot.active.session != nil {
				snapshot.Streams += slot.active.session.StreamCount()
				current, peak := slot.active.session.bufferedByteSnapshot()
				snapshot.BufferedBytes += current
				snapshot.MaxSessionPeakBufferedBytes = max(snapshot.MaxSessionPeakBufferedBytes, peak)
			}
		}
		if slot.candidate != nil {
			snapshot.Candidates++
		}
	}
	snapshot.Draining = len(i.draining)
	for tracked := range i.draining {
		if tracked.session != nil {
			snapshot.Streams += tracked.session.StreamCount()
			current, peak := tracked.session.bufferedByteSnapshot()
			snapshot.BufferedBytes += current
			snapshot.MaxSessionPeakBufferedBytes = max(snapshot.MaxSessionPeakBufferedBytes, peak)
		}
	}
	snapshot.Sockets = snapshot.Active + snapshot.Draining + snapshot.Candidates
	snapshot.Timers = snapshot.Draining
	if i.supervisorStarted && !i.closed && !i.supervisorStopped {
		snapshot.Timers++
	}
	return snapshot
}

func (i *DirectTunnelIngress) candidateCountLocked() int {
	count := 0
	for _, slot := range i.sources {
		if slot.candidate != nil {
			count++
		}
	}
	return count
}

func (i *DirectTunnelIngress) signalChangedLocked() {
	select {
	case i.changed <- struct{}{}:
	default:
	}
}

func (i *DirectTunnelIngress) closeDoneIfFinishedLocked() {
	if i.closed && i.connectionCountLocked() == 0 && i.supervisorStopped {
		i.doneOnce.Do(func() { close(i.done) })
	}
}

func (i *DirectTunnelIngress) Done() <-chan struct{} {
	if i == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return i.done
}

func (i *DirectTunnelIngress) publishMetrics() {
	if i == nil || i.opts.Metrics == nil {
		return
	}
	i.metricMu.Lock()
	snapshot := i.Snapshot()
	previous := i.metricSnapshot
	i.metricSnapshot = snapshot
	i.opts.Metrics.AddDirectSessions(pkgmetrics.DirectSessionConnecting, float64(snapshot.Candidates-previous.Candidates))
	i.opts.Metrics.AddDirectSessions(pkgmetrics.DirectSessionActive, float64(snapshot.Active-previous.Active))
	i.opts.Metrics.AddDirectSessions(pkgmetrics.DirectSessionDraining, float64(snapshot.Draining-previous.Draining))
	i.metricMu.Unlock()
}

var directUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, EnableCompression: false}
