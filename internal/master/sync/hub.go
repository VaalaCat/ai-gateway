package sync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	pkgtunnel "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

type AgentRuntime = connectivity.AgentRuntimeFact

type AgentTicketSigner interface {
	PublicKey() agentauth.PublicKey
	SignRelay(agentID string, generation uint64) (agentauth.RelayTicket, time.Time, error)
	SignForward(sourceAgentID string) (agentauth.ForwardTicket, time.Time, error)
}

// A new authenticated control generation starts a fresh per-session budget.
const maxCapabilityChangesPerSession = 8

const (
	maxRouteObservationParamsBytes = 1 << 20
	maxAgentControlMessageBytes    = 2 << 20
)

var errControlHubClosed = errors.New("control hub: closed")

type HubOptions struct {
	MasterInstanceID  string
	Capabilities      []string
	AgentTicketSigner AgentTicketSigner
}

type controlRequestHandler func(context.Context, *ws.Conn, string, uint64, *jsonrpc.Request)

type controlSession struct {
	conn                   *ws.Conn
	generation             uint64
	connectedAt            int64
	heartbeatAt            int64
	runtimeReportedAt      int64
	runtime                *connectivity.AgentRuntimeFact
	capabilities           []string
	capabilitySnapshotSent bool
	capabilityChanges      int
	recentErrors           *diagnostics.Ring
}

type controlSessionIdentity struct {
	conn       *ws.Conn
	generation uint64
}

type peerUpdateRecipient struct {
	agentID string
	conn    *ws.Conn
}

type autoAddressVersion struct {
	sessionGeneration uint64
	sequence          uint64
}

// pendingCall 记录在途 RPC 的响应通道与其所属连接,断连时可定向唤醒(spec §6)。
type pendingCall struct {
	ch   chan *jsonrpc.Response
	conn *ws.Conn
}

type Hub struct {
	sessions                   map[string]*controlSession
	nextGeneration             uint64
	nextAutoAddressSequence    uint64
	remoteAddrs                map[string]string               // agentID -> remote IP from WS connection
	autoHTTPAddrs              map[string][]agentproxy.Address // agentID -> auto-detected addresses (memory-only)
	autoAddressVersions        map[string]autoAddressVersion
	mu                         gosync.RWMutex
	App                        dao.AppProvider
	Logger                     *zap.Logger
	Bus                        app.EventBus
	GetVersion                 func() int64
	now                        func() time.Time
	onControlSessionRemoved    func(string, uint64)
	masterInstanceID           string
	masterCapabilities         []string
	agentTicketSigner          AgentTicketSigner
	controlRequestHandlers     map[string]controlRequestHandler
	peerRuntimeUpdatesMu       gosync.Mutex
	nonEmptyCapabilitySessions int
	sendCapabilityUpdate       func(*ws.Conn, protocol.AgentCapabilitiesUpdate) error
	sendDirectAddressesUpdate  func(*ws.Conn, protocol.AgentDirectAddressesUpdate) error
	closePeerUpdateConn        func(*ws.Conn) error

	pending    map[string]pendingCall // requestID -> pending call (response channel + owning conn)
	pendingMu  gosync.Mutex
	nextCallID atomic.Int64

	fetchRegistry               *FetchRegistry
	closeOnce                   gosync.Once
	done                        chan struct{}
	closing                     bool
	sessionChange               chan struct{}
	pendingConnections          map[*ws.Conn]struct{}
	handlerLeases               map[*handlerLease]struct{}
	handlerWorkers              conc.WaitGroup
	activeHandlers              atomic.Int64
	afterPendingTrack           func()
	beforeRouteObservationApply func()

	// Heartbeat captures agent last_seen in memory + skips redundant
	// mergeAgentConfig SELECTs via the ConfigChanged fingerprint. May be nil
	// in tests / pre-wiring; callers must nil-check before use.
	Heartbeat *HeartbeatTracker

	// RouteObservations owns bounded, memory-only source-to-target edges.
	RouteObservations *connectivity.Service

	// SettleUsage 数据面同步结算入口;非 nil 时 HTTP 摄取只在落库成功后才 200 ——
	// ack=已持久化。nil 时回退老的异步 publish 语义(兼容未接线的构造)。
	SettleUsage func(ctx context.Context, agentID string, logs []protocol.UsageLogEntry) error
}

func NewHub(
	application dao.AppProvider,
	logger *zap.Logger,
	bus app.EventBus,
	getVersion func() int64,
	cipher *byokcrypto.Cipher,
	opts HubOptions,
) *Hub {
	h := &Hub{
		sessions:            make(map[string]*controlSession),
		remoteAddrs:         make(map[string]string),
		autoHTTPAddrs:       make(map[string][]agentproxy.Address),
		autoAddressVersions: make(map[string]autoAddressVersion),
		App:                 application,
		Logger:              logger,
		Bus:                 bus,
		GetVersion:          getVersion,
		now:                 time.Now,
		pending:             make(map[string]pendingCall),
		fetchRegistry:       NewFetchRegistry(cipher),
		done:                make(chan struct{}),
		sessionChange:       make(chan struct{}, 1),
		pendingConnections:  make(map[*ws.Conn]struct{}),
		handlerLeases:       make(map[*handlerLease]struct{}),
		masterInstanceID:    opts.MasterInstanceID,
		masterCapabilities:  protocol.NormalizeAgentCapabilities(opts.Capabilities),
		agentTicketSigner:   opts.AgentTicketSigner,
	}
	h.controlRequestHandlers = h.newControlRequestHandlers()
	return h
}

func (h *Hub) installControlSession(agentID string, conn *ws.Conn, remoteAddr string) (uint64, int64, *ws.Conn) {
	return h.installControlSessionWithCheck(agentID, conn, remoteAddr, nil)
}

func (h *Hub) installAuthenticatedControlSession(
	ctx context.Context,
	agentID string,
	secret string,
	conn *ws.Conn,
	remoteAddr string,
) (uint64, int64, *ws.Conn) {
	return h.installControlSessionWithCheck(agentID, conn, remoteAddr, func() bool {
		return h.validAgentCredentials(ctx, agentID, secret)
	})
}

func (h *Hub) installControlSessionWithCheck(
	agentID string,
	conn *ws.Conn,
	remoteAddr string,
	check func() bool,
) (uint64, int64, *ws.Conn) {
	h.peerRuntimeUpdatesMu.Lock()
	if check != nil && !check() {
		h.peerRuntimeUpdatesMu.Unlock()
		return 0, 0, nil
	}
	connectedAt := h.now().Unix()
	h.mu.Lock()
	delete(h.pendingConnections, conn)
	if h.closing {
		h.mu.Unlock()
		h.peerRuntimeUpdatesMu.Unlock()
		return 0, 0, nil
	}
	h.nextGeneration++
	if h.nextGeneration == 0 {
		h.nextGeneration++
	}
	generation := h.nextGeneration
	if h.RouteObservations != nil {
		h.RouteObservations.AdvanceRouteObservationSession(agentID, generation)
	}
	var old *ws.Conn
	hadCapabilities := false
	var withdrawnGeneration uint64
	if current := h.sessions[agentID]; current != nil {
		old = current.conn
		withdrawnGeneration = current.generation
		hadCapabilities = len(current.capabilities) > 0
		if hadCapabilities {
			h.nonEmptyCapabilitySessions--
		}
	}
	h.sessions[agentID] = &controlSession{
		conn:         conn,
		generation:   generation,
		connectedAt:  connectedAt,
		recentErrors: diagnostics.NewRing(diagnostics.DefaultRingCapacity),
	}
	h.remoteAddrs[agentID] = extractIP(remoteAddr)
	directUpdate, withdrewAddress := h.withdrawAutoAddressLocked(agentID, withdrawnGeneration)
	var recipients []peerUpdateRecipient
	if hadCapabilities {
		recipients = h.peerUpdateRecipientsLocked()
	}
	var directRecipients []peerUpdateRecipient
	if withdrewAddress {
		directRecipients = h.directAddressRecipientsLocked()
	}
	h.mu.Unlock()

	failed := make(map[*ws.Conn]struct{})
	if hadCapabilities {
		h.broadcastCapabilityUpdate(recipients, protocol.AgentCapabilitiesUpdate{AgentID: agentID}, failed)
	}
	if withdrewAddress {
		h.broadcastDirectAddressesUpdate(directRecipients, directUpdate, failed)
	}
	h.peerRuntimeUpdatesMu.Unlock()
	h.closeFailedPeerUpdateConnections(failed)
	if withdrewAddress {
		h.logAutoAddressChange(directUpdate, "session_replaced")
	}
	h.signalSessionChange()
	return generation, connectedAt, old
}

func (h *Hub) removeControlSession(agentID string, conn *ws.Conn, generation uint64) bool {
	_, removed := h.removeControlSessionWithReason(agentID, &controlSessionIdentity{
		conn:       conn,
		generation: generation,
	}, "control_disconnected")
	return removed
}

func (h *Hub) removeControlSessionWithReason(
	agentID string,
	expected *controlSessionIdentity,
	reason string,
) (*ws.Conn, bool) {
	h.peerRuntimeUpdatesMu.Lock()
	h.mu.Lock()
	current := h.sessions[agentID]
	if current == nil || (expected != nil &&
		(current.conn != expected.conn || current.generation != expected.generation)) {
		h.mu.Unlock()
		h.peerRuntimeUpdatesMu.Unlock()
		return nil, false
	}
	delete(h.sessions, agentID)
	delete(h.remoteAddrs, agentID)
	removedGeneration := current.generation
	directUpdate, withdrewAddress := h.withdrawAutoAddressLocked(agentID, removedGeneration)
	onRemoved := h.onControlSessionRemoved
	hadCapabilities := len(current.capabilities) > 0
	var recipients []peerUpdateRecipient
	if hadCapabilities {
		h.nonEmptyCapabilitySessions--
		recipients = h.peerUpdateRecipientsLocked()
	}
	var directRecipients []peerUpdateRecipient
	if withdrewAddress {
		directRecipients = h.directAddressRecipientsLocked()
	}
	h.mu.Unlock()
	failed := make(map[*ws.Conn]struct{})
	if hadCapabilities {
		h.broadcastCapabilityUpdate(recipients, protocol.AgentCapabilitiesUpdate{AgentID: agentID}, failed)
	}
	if withdrewAddress {
		h.broadcastDirectAddressesUpdate(directRecipients, directUpdate, failed)
	}
	h.peerRuntimeUpdatesMu.Unlock()
	h.closeFailedPeerUpdateConnections(failed)
	if withdrewAddress {
		h.logAutoAddressChange(directUpdate, reason)
	}
	h.signalSessionChange()

	if onRemoved != nil {
		onRemoved(agentID, removedGeneration)
	}
	return current.conn, true
}

// RevokeControlSession removes and closes the current authenticated control
// session for an Agent deleted by the management plane.
func (h *Hub) RevokeControlSession(agentID string) bool {
	if agentID == "" {
		return false
	}
	conn, removed := h.removeControlSessionWithReason(agentID, nil, "agent_revoked")
	if !removed {
		return false
	}
	h.failPendingForConn(conn)
	h.closeFailedPeerUpdateConnections(map[*ws.Conn]struct{}{conn: {}})
	return true
}

func (h *Hub) SetControlSessionRemoved(fn func(string, uint64)) {
	h.mu.Lock()
	h.onControlSessionRemoved = fn
	h.mu.Unlock()
}

func (h *Hub) acceptControlHeartbeat(agentID string, conn *ws.Conn, generation uint64, raw json.RawMessage) (protocol.HeartbeatParams, int64, bool) {
	var params protocol.HeartbeatParams
	if err := json.Unmarshal(raw, &params); err != nil {
		h.RecordControlError(agentID, generation, diagnostics.Event{
			Code: "control_protocol", Stage: "heartbeat", Message: err.Error(), At: time.Now(),
		})
		return protocol.HeartbeatParams{}, 0, false
	}
	h.mu.RLock()
	current := h.sessions[agentID]
	isCurrent := current != nil && current.conn == conn && current.generation == generation
	h.mu.RUnlock()
	if !isCurrent {
		return protocol.HeartbeatParams{}, 0, false
	}
	reportedAt := h.now().Unix()
	masterVersion := int64(0)
	if h.GetVersion != nil {
		masterVersion = h.GetVersion()
	}
	runtime := &connectivity.AgentRuntimeFact{
		Uptime:               params.Uptime,
		CachedTokens:         params.CachedTokens,
		CachedChannels:       params.CachedChannels,
		CachedModels:         params.CachedModels,
		CachedGlobalRoutings: params.CachedGlobalRoutings,
		CachedUserRoutings:   params.CachedUserRoutings,
		ActiveConnections:    params.ActiveConnections,
		Version:              params.Version,
		MasterVersion:        masterVersion,
		PendingUsage:         params.PendingUsage,
		CacheStats:           cloneCacheStats(params.CacheStats),
		Relay:                relayRuntimeFact(params.Relay, reportedAt),
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	current = h.sessions[agentID]
	if current == nil || current.conn != conn || current.generation != generation {
		return protocol.HeartbeatParams{}, 0, false
	}
	current.heartbeatAt = reportedAt
	current.runtimeReportedAt = reportedAt
	current.runtime = runtime
	return params, reportedAt, true
}

func (h *Hub) touchCurrentHeartbeatLastSeen(ctx context.Context, agentID string, conn *ws.Conn, generation uint64, reportedAt int64) bool {
	h.mu.RLock()
	current := h.sessions[agentID]
	tracker := h.Heartbeat
	isCurrent := current != nil && current.conn == conn && current.generation == generation
	h.mu.RUnlock()
	if !isCurrent {
		return false
	}
	if tracker != nil {
		tracker.Touch(agentID, reportedAt)
		return true
	}
	dao.NewAdminMutation(dao.NewContextWithContext(h.App, ctx)).Agent().UpdateLastSeen(agentID, reportedAt)
	return true
}

func cloneRuntime(runtime *connectivity.AgentRuntimeFact) *connectivity.AgentRuntimeFact {
	if runtime == nil {
		return nil
	}
	cloned := *runtime
	cloned.CacheStats = cloneCacheStats(runtime.CacheStats)
	cloned.Relay = cloneRelayRuntime(runtime.Relay)
	return &cloned
}

func relayRuntimeFact(runtime *protocol.RelayRuntime, reportedAt int64) *connectivity.RelayRuntimeFact {
	if runtime == nil {
		return nil
	}
	fact := &connectivity.RelayRuntimeFact{
		Support: normalizeRelaySupport(runtime.Support), Config: normalizeRelayConfig(runtime.Config),
		Availability:        normalizeRelayAvailability(runtime.Availability),
		AcceptingNewStreams: runtime.AcceptingNewStreams,
		Convergence:         normalizeRelayConvergence(runtime.Convergence),
		Desired: connectivity.RelayDesiredSnapshot{
			Mode:              normalizeRelayMode(runtime.Desired.Mode),
			ConfiguredURI:     sanitizedRuntimeURI(runtime.Desired.ConfiguredURI),
			EffectiveURI:      sanitizedRuntimeURI(runtime.Desired.EffectiveURI),
			DesiredGeneration: runtime.Desired.DesiredGeneration,
		},
		Active: connectivity.RelayActiveSnapshot{
			URI: sanitizedRuntimeURI(runtime.Active.URI), ActiveGeneration: runtime.Active.ActiveGeneration,
			SessionGeneration: runtime.Active.SessionGeneration, ConnectedAt: runtime.Active.ConnectedAt,
			Streams: runtime.Active.Streams, RetryAt: runtime.Active.RetryAt,
		},
		RecentErrors: make([]connectivity.RecentError, 0, 1),
	}
	for _, event := range runtime.RecentErrors {
		count := event.Count
		if count == 0 {
			count = 1
		}
		fact.RecentErrors = appendBoundedRuntimeError(fact.RecentErrors, connectivity.RecentError{
			Code: diagnostics.SanitizeText(event.Code), Stage: diagnostics.SanitizeText(event.Stage),
			Message: diagnostics.SanitizeText(event.Message), OccurredAt: event.OccurredAt, Count: count,
		})
	}
	if runtime.LastError != "" {
		message := sanitizeRuntimeError(runtime.LastError,
			runtime.Desired.ConfiguredURI, runtime.Desired.EffectiveURI, runtime.Active.URI)
		fact.RecentErrors = appendBoundedRuntimeError(fact.RecentErrors, connectivity.RecentError{
			Code: "relay_runtime_error", Stage: "runtime", Message: message, OccurredAt: reportedAt, Count: 1,
		})
	}
	return fact
}

func appendBoundedRuntimeError(history []connectivity.RecentError, event connectivity.RecentError) []connectivity.RecentError {
	if len(history) >= diagnostics.DefaultRingCapacity {
		history = history[len(history)-diagnostics.DefaultRingCapacity+1:]
	}
	result := make([]connectivity.RecentError, 0, len(history)+1)
	result = append(result, history...)
	return append(result, event)
}

func cloneRelayRuntime(runtime *connectivity.RelayRuntimeFact) *connectivity.RelayRuntimeFact {
	if runtime == nil {
		return nil
	}
	cloned := *runtime
	cloned.RecentErrors = append([]connectivity.RecentError(nil), runtime.RecentErrors...)
	return &cloned
}

func sanitizedRuntimeURI(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := pkgtunnel.ParseRelayURI(raw)
	if err != nil {
		return ""
	}
	return parsed.Sanitized
}

func sanitizeRuntimeError(message string, uris ...string) string {
	for _, raw := range uris {
		parsed, err := pkgtunnel.ParseRelayURI(raw)
		if err != nil {
			continue
		}
		message = strings.ReplaceAll(message, raw, parsed.Sanitized)
		for _, values := range parsed.URI.Query() {
			for _, value := range values {
				if value != "" {
					message = strings.ReplaceAll(message, value, "REDACTED")
				}
			}
		}
	}
	return diagnostics.SanitizeText(message)
}

func normalizeRelaySupport(value string) string {
	if value == "supported" {
		return "supported"
	}
	return "unsupported"
}

func normalizeRelayConfig(value string) string {
	switch value {
	case "configured", "disabled", "not_configured":
		return value
	}
	return "not_configured"
}

func normalizeRelayAvailability(value string) string {
	if value == "available" {
		return "available"
	}
	return "unavailable"
}

func normalizeRelayConvergence(value string) string {
	if value == "converging" {
		return "converging"
	}
	return "converged"
}

func normalizeRelayMode(value string) string {
	switch value {
	case consts.RelayModeCustom, consts.RelayModeDisabled:
		return value
	}
	return consts.RelayModeInherit
}

func cloneCacheStats(stats map[string]protocol.CacheEntityStats) map[string]protocol.CacheEntityStats {
	if stats == nil {
		return nil
	}
	cloned := make(map[string]protocol.CacheEntityStats, len(stats))
	for key, value := range stats {
		if value.Extra != nil {
			value.Extra = make(map[string]int64, len(value.Extra))
			for extraKey, extraValue := range stats[key].Extra {
				value.Extra[extraKey] = extraValue
			}
		}
		cloned[key] = value
	}
	return cloned
}

func (h *Hub) HandleWS(c *gin.Context) {
	handler, ok := h.beginHandler(c.Request.Context())
	if !ok {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	defer handler.Release()
	c.Request = c.Request.WithContext(handler.Context())
	agentID, ok := h.authenticateAgent(handler.Context(), c)
	if !ok {
		return // authenticateAgent 已写 401 响应
	}

	conn, err := ws.Upgrade(c.Writer, c.Request, h.Logger)
	if err != nil {
		h.Logger.Error("ws upgrade failed", zap.Error(err))
		return
	}
	conn.WS.SetReadLimit(maxAgentControlMessageBytes)
	if !h.trackPendingConnection(conn) {
		_ = conn.Close()
		return
	}
	defer h.untrackPendingConnection(conn)
	defer conn.Close()
	if h.afterPendingTrack != nil {
		h.afterPendingTrack()
	}

	generation, connectedAt, old := h.installAuthenticatedControlSession(
		handler.Context(), agentID, c.GetHeader(consts.HeaderXAgentSecret), conn, c.Request.RemoteAddr,
	)
	if generation == 0 {
		_ = conn.Close()
		return
	}
	if old != nil {
		old.Close()
	}

	h.Logger.Info("agent connected", zap.String("agent_id", agentID))

	// Update last_seen
	if h.Heartbeat != nil {
		h.Heartbeat.Touch(agentID, connectedAt)
	} else {
		// Fallback when tracker not wired (e.g. legacy tests).
		dao.NewAdminMutation(dao.NewContextWithContext(h.App, handler.Context())).Agent().UpdateLastSeen(agentID, connectedAt)
	}

	defer func() {
		h.removeControlSession(agentID, conn, generation)
		h.failPendingForConn(conn)
		h.Logger.Info("agent disconnected", zap.String("agent_id", agentID))
	}()

	// Read loop
	for {
		req, resp, err := conn.ReadMessage()
		if err != nil {
			return
		}

		// Handle RPC responses (for Hub.Call)
		if resp != nil && resp.ID != nil {
			key := strconv.FormatInt(*resp.ID, 10)
			h.deliverPendingResponse(conn, key, resp)
			continue
		}

		if req == nil {
			continue
		}

		if !h.dispatchControlRequest(handler.Context(), conn, agentID, generation, req) {
			if req.ID != nil {
				conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrMethodNotFound, "unknown method"))
			}
		}
	}
}

type handlerLease struct {
	done   chan struct{}
	once   gosync.Once
	ctx    context.Context
	cancel context.CancelCauseFunc
	hub    *Hub
}

func (l *handlerLease) Context() context.Context { return l.ctx }

func (l *handlerLease) Cancel(cause error) {
	if l != nil && l.cancel != nil {
		l.cancel(cause)
	}
}

func (l *handlerLease) Release() {
	if l != nil {
		l.once.Do(func() {
			l.cancel(context.Canceled)
			l.hub.mu.Lock()
			delete(l.hub.handlerLeases, l)
			l.hub.mu.Unlock()
			close(l.done)
		})
	}
}

func (h *Hub) beginHandler(parent context.Context) (*handlerLease, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return nil, false
	}
	ctx, cancel := context.WithCancelCause(parent)
	lease := &handlerLease{done: make(chan struct{}), ctx: ctx, cancel: cancel, hub: h}
	h.handlerLeases[lease] = struct{}{}
	h.activeHandlers.Add(1)
	h.handlerWorkers.Go(func() {
		<-lease.done
		h.activeHandlers.Add(-1)
	})
	return lease, true
}

func (h *Hub) trackPendingConnection(conn *ws.Conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	h.pendingConnections[conn] = struct{}{}
	return true
}

func (h *Hub) untrackPendingConnection(conn *ws.Conn) {
	h.mu.Lock()
	delete(h.pendingConnections, conn)
	h.mu.Unlock()
}

func (h *Hub) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("control hub: nil close context")
	}
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closing = true
		leases := make([]*handlerLease, 0, len(h.handlerLeases))
		for lease := range h.handlerLeases {
			leases = append(leases, lease)
		}
		h.mu.Unlock()
		for _, lease := range leases {
			lease.Cancel(errControlHubClosed)
		}
		go h.finalizeClose()
	})
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (h *Hub) Done() <-chan struct{} { return h.done }

func (h *Hub) finalizeClose() {
	h.peerRuntimeUpdatesMu.Lock()
	h.mu.Lock()
	connections := make([]*ws.Conn, 0, len(h.sessions)+len(h.pendingConnections))
	for _, session := range h.sessions {
		connections = append(connections, session.conn)
	}
	for conn := range h.pendingConnections {
		connections = append(connections, conn)
	}
	h.mu.Unlock()
	h.peerRuntimeUpdatesMu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	h.handlerWorkers.Wait()
	close(h.done)
}

func (h *Hub) ResourceCounts() app.ResourceCounts {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return app.ResourceCounts{
		ControlHandlers: h.activeHandlers.Load(),
		ControlSockets:  int64(len(h.sessions) + len(h.pendingConnections)),
		ControlSessions: int64(len(h.sessions)),
	}
}

func (h *Hub) isClosing() bool {
	h.mu.RLock()
	closing := h.closing
	h.mu.RUnlock()
	return closing
}

func (h *Hub) signalSessionChange() {
	select {
	case h.sessionChange <- struct{}{}:
	default:
	}
}

func (h *Hub) newControlRequestHandlers() map[string]controlRequestHandler {
	return map[string]controlRequestHandler{
		consts.RPCSyncFullSync: func(ctx context.Context, conn *ws.Conn, _ string, _ uint64, req *jsonrpc.Request) {
			h.handleFullSync(ctx, conn, req)
		},
		consts.RPCSyncGetVersion: func(_ context.Context, conn *ws.Conn, _ string, _ uint64, req *jsonrpc.Request) {
			h.handleGetVersion(conn, req)
		},
		consts.RPCSyncFetchEntity: func(ctx context.Context, conn *ws.Conn, _ string, _ uint64, req *jsonrpc.Request) {
			h.handleFetchEntity(ctx, conn, req)
		},
		consts.RPCReportUsage: func(ctx context.Context, _ *ws.Conn, agentID string, _ uint64, req *jsonrpc.Request) {
			h.handleUsageReport(ctx, agentID, req)
		},
		consts.RPCAgentHeartbeat:          h.handleHeartbeat,
		consts.RPCAgentAuthBootstrap:      h.handleAuthBootstrap,
		consts.RPCAgentIssueRelayTicket:   h.handleIssueRelayTicket,
		consts.RPCAgentIssueForwardTicket: h.handleIssueForwardTicket,
		consts.RPCAgentRouteTelemetry:     h.handleRouteTelemetry,
		consts.RPCAgentRouteDigest:        h.handleRouteDigest,
		consts.RPCSyncAgentCapabilities:   h.handleAgentCapabilities,
	}
}

func (h *Hub) handleRouteTelemetry(_ context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	if h.RouteObservations == nil {
		return
	}
	if len(req.Params) > maxRouteObservationParamsBytes {
		return
	}
	var batch protocol.RouteTelemetryBatch
	if decodeControlParams(req.Params, &batch) != nil {
		return
	}
	if h.beforeRouteObservationApply != nil {
		h.beforeRouteObservationApply()
	}
	h.withCurrentControlSession(agentID, conn, generation, func() {
		_ = h.RouteObservations.ApplySessionEvents(agentID, generation, batch)
	})
}

func (h *Hub) handleRouteDigest(_ context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	if h.RouteObservations == nil {
		return
	}
	if len(req.Params) > maxRouteObservationParamsBytes {
		return
	}
	var digest protocol.RouteEdgeDigest
	if decodeControlParams(req.Params, &digest) != nil {
		return
	}
	if h.beforeRouteObservationApply != nil {
		h.beforeRouteObservationApply()
	}
	h.withCurrentControlSession(agentID, conn, generation, func() {
		_ = h.RouteObservations.ReplaceSessionDigest(agentID, generation, digest)
	})
}

func (h *Hub) withCurrentControlSession(agentID string, conn *ws.Conn, generation uint64, apply func()) bool {
	if generation == 0 || apply == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	current := h.sessions[agentID]
	if current == nil || current.conn != conn || current.generation != generation {
		return false
	}
	// The callback is memory-only and must not perform socket I/O or wait.
	apply()
	return true
}

func (h *Hub) dispatchControlRequest(ctx context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) bool {
	handler := h.controlRequestHandlers[req.Method]
	if handler == nil {
		return false
	}
	handler(ctx, conn, agentID, generation, req)
	return true
}

// authenticateAgent 校验 X-Vaala-Agent-ID/Secret 头;失败时写 401 并返回 ok=false。
// 与 HandleWS / HandleUsageHTTP 共用,保持鉴权单一实现。
func (h *Hub) authenticateAgent(ctx context.Context, c *gin.Context) (string, bool) {
	agentID := c.GetHeader(consts.HeaderXAgentID)
	secret := c.GetHeader(consts.HeaderXAgentSecret)
	if agentID == "" || secret == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing credentials"})
		return "", false
	}
	if !h.validAgentCredentials(ctx, agentID, secret) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid agent credentials"})
		return "", false
	}
	return agentID, true
}

func (h *Hub) validAgentCredentials(ctx context.Context, agentID, secret string) bool {
	if h == nil || ctx == nil || agentID == "" || secret == "" || context.Cause(ctx) != nil {
		return false
	}
	daoCtx := dao.NewContextWithContext(h.App, ctx)
	agent, err := dao.NewAdminQuery(daoCtx).Agent().GetByAgentID(agentID)
	return err == nil && agent.Secret == secret && agent.Status == consts.StatusEnabled
}

func (h *Hub) handleAuthBootstrap(_ context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	if !paramsAreEmpty(req.Params) {
		h.sendControlError(conn, req, jsonrpc.ErrInvalidParams, "invalid params")
		return
	}
	if !h.isCurrentControlSession(agentID, conn, generation) ||
		h.masterInstanceID == "" ||
		isNilAgentTicketSigner(h.agentTicketSigner) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "agent auth unavailable")
		return
	}
	key := cloneAgentPublicKey(h.agentTicketSigner.PublicKey())
	if !validAgentPublicKey(key) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "agent auth unavailable")
		return
	}
	h.sendControlResult(conn, req, protocol.AuthBootstrapResponse{
		MasterInstanceID: h.masterInstanceID,
		Capabilities:     append([]string(nil), h.masterCapabilities...),
		SigningKeys:      []agentauth.PublicKey{key},
	})
}

func validAgentPublicKey(key agentauth.PublicKey) bool {
	return key.KeyID != "" &&
		len(key.KeyID) <= protocol.AgentAuthKeyIDMaxLength &&
		key.Algorithm == protocol.AgentAuthAlgorithmEdDSA &&
		len(key.Key) == ed25519.PublicKeySize
}

func (h *Hub) handleIssueRelayTicket(_ context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	var params protocol.RelayTicketRequest
	if err := decodeControlParams(req.Params, &params); err != nil {
		h.sendControlError(conn, req, jsonrpc.ErrInvalidParams, "invalid params")
		return
	}
	if !h.isCurrentControlSession(agentID, conn, generation) || isNilAgentTicketSigner(h.agentTicketSigner) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "ticket issuance unavailable")
		return
	}
	ticket, expiresAt, err := h.agentTicketSigner.SignRelay(agentID, params.DesiredGeneration)
	if err != nil || ticket == "" || !expiresAt.After(h.now()) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "ticket issuance failed")
		return
	}
	h.sendControlResult(conn, req, protocol.TicketResponse{Token: string(ticket), ExpiresAt: expiresAt.Unix()})
}

func (h *Hub) handleIssueForwardTicket(_ context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	if !paramsAreEmpty(req.Params) {
		h.sendControlError(conn, req, jsonrpc.ErrInvalidParams, "invalid params")
		return
	}
	if !h.isCurrentControlSession(agentID, conn, generation) || isNilAgentTicketSigner(h.agentTicketSigner) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "ticket issuance unavailable")
		return
	}
	ticket, expiresAt, err := h.agentTicketSigner.SignForward(agentID)
	if err != nil || ticket == "" || !expiresAt.After(h.now()) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "ticket issuance failed")
		return
	}
	h.sendControlResult(conn, req, protocol.TicketResponse{Token: string(ticket), ExpiresAt: expiresAt.Unix()})
}

func (h *Hub) handleAgentCapabilities(_ context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	var params protocol.AgentCapabilitiesUpdate
	if err := decodeControlParams(req.Params, &params); err != nil {
		h.sendControlError(conn, req, jsonrpc.ErrInvalidParams, "invalid params")
		return
	}
	if !h.publishCurrentCapabilities(agentID, conn, generation, params.Capabilities) {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "control session changed")
		return
	}
	h.sendControlResult(conn, req, map[string]bool{"ok": true})
}

func (h *Hub) publishCurrentCapabilities(
	agentID string,
	conn *ws.Conn,
	generation uint64,
	capabilities []string,
) bool {
	normalized := protocol.NormalizeAgentCapabilities(capabilities)
	h.peerRuntimeUpdatesMu.Lock()
	h.mu.Lock()
	current := h.sessions[agentID]
	if current == nil || current.conn != conn || current.generation != generation {
		h.mu.Unlock()
		h.peerRuntimeUpdatesMu.Unlock()
		return false
	}
	changed := !slices.Equal(current.capabilities, normalized)
	if changed && current.capabilityChanges >= maxCapabilityChangesPerSession {
		h.mu.Unlock()
		h.peerRuntimeUpdatesMu.Unlock()
		if h.Logger != nil {
			h.Logger.Warn("capability change budget exceeded; closing source", zap.String("agent", agentID))
		}
		h.closeFailedPeerUpdateConnections(map[*ws.Conn]struct{}{conn: {}})
		return false
	}
	var snapshot []protocol.AgentCapabilitiesUpdate
	var directAddressSnapshot []protocol.AgentDirectAddressesUpdate
	if !current.capabilitySnapshotSent {
		current.capabilitySnapshotSent = true
		snapshot = h.capabilitySnapshotLocked(agentID)
		directAddressSnapshot = h.directAddressSnapshotLocked(agentID)
	}
	if changed {
		hadCapabilities := len(current.capabilities) > 0
		hasCapabilities := len(normalized) > 0
		if hadCapabilities != hasCapabilities {
			if hasCapabilities {
				h.nonEmptyCapabilitySessions++
			} else {
				h.nonEmptyCapabilitySessions--
			}
		}
		current.capabilities = append([]string(nil), normalized...)
		current.capabilityChanges++
	}
	var recipients []peerUpdateRecipient
	if changed {
		recipients = h.peerUpdateRecipientsLocked()
	}
	h.mu.Unlock()

	failed := make(map[*ws.Conn]struct{})
	for _, update := range snapshot {
		h.enqueueCapabilityUpdate(agentID, conn, update, failed)
	}
	for _, update := range directAddressSnapshot {
		h.enqueueDirectAddressesUpdate(agentID, conn, update, failed)
	}
	if changed {
		h.broadcastCapabilityUpdate(recipients, protocol.AgentCapabilitiesUpdate{
			AgentID:      agentID,
			Capabilities: append([]string(nil), normalized...),
		}, failed)
	}
	h.peerRuntimeUpdatesMu.Unlock()
	h.closeFailedPeerUpdateConnections(failed)
	return true
}

func (h *Hub) capabilitySnapshotLocked(excludeAgentID string) []protocol.AgentCapabilitiesUpdate {
	if h.nonEmptyCapabilitySessions == 0 {
		return nil
	}
	updates := make([]protocol.AgentCapabilitiesUpdate, 0, h.nonEmptyCapabilitySessions)
	for agentID, current := range h.sessions {
		if agentID == excludeAgentID || len(current.capabilities) == 0 {
			continue
		}
		updates = append(updates, protocol.AgentCapabilitiesUpdate{
			AgentID:      agentID,
			Capabilities: append([]string(nil), current.capabilities...),
		})
	}
	slices.SortFunc(updates, func(a, b protocol.AgentCapabilitiesUpdate) int {
		return strings.Compare(a.AgentID, b.AgentID)
	})
	return updates
}

func (h *Hub) peerUpdateRecipientsLocked() []peerUpdateRecipient {
	recipients := make([]peerUpdateRecipient, 0, len(h.sessions))
	for agentID, current := range h.sessions {
		recipients = append(recipients, peerUpdateRecipient{agentID: agentID, conn: current.conn})
	}
	slices.SortFunc(recipients, func(a, b peerUpdateRecipient) int {
		return strings.Compare(a.agentID, b.agentID)
	})
	return recipients
}

func (h *Hub) directAddressRecipientsLocked() []peerUpdateRecipient {
	recipients := make([]peerUpdateRecipient, 0, len(h.sessions))
	for agentID, current := range h.sessions {
		if !current.capabilitySnapshotSent {
			continue
		}
		recipients = append(recipients, peerUpdateRecipient{agentID: agentID, conn: current.conn})
	}
	slices.SortFunc(recipients, func(a, b peerUpdateRecipient) int {
		return strings.Compare(a.agentID, b.agentID)
	})
	return recipients
}

func (h *Hub) broadcastCapabilityUpdate(
	recipients []peerUpdateRecipient,
	update protocol.AgentCapabilitiesUpdate,
	failed map[*ws.Conn]struct{},
) {
	for _, recipient := range recipients {
		h.enqueueCapabilityUpdate(recipient.agentID, recipient.conn, update, failed)
	}
}

func (h *Hub) broadcastDirectAddressesUpdate(
	recipients []peerUpdateRecipient,
	update protocol.AgentDirectAddressesUpdate,
	failed map[*ws.Conn]struct{},
) {
	for _, recipient := range recipients {
		h.enqueueDirectAddressesUpdate(recipient.agentID, recipient.conn, update, failed)
	}
}

func (h *Hub) enqueueCapabilityUpdate(
	agentID string,
	conn *ws.Conn,
	update protocol.AgentCapabilitiesUpdate,
	failed map[*ws.Conn]struct{},
) {
	if _, alreadyFailed := failed[conn]; alreadyFailed {
		return
	}
	var err error
	if h.sendCapabilityUpdate != nil {
		err = h.sendCapabilityUpdate(conn, update)
	} else {
		err = conn.SendNotification(consts.RPCSyncAgentCapabilities, update)
	}
	if err == nil {
		return
	}
	failed[conn] = struct{}{}
	if h.Logger != nil {
		h.Logger.Warn("capability update enqueue failed; closing recipient", zap.String("agent", agentID), zap.Error(err))
	}
}

func (h *Hub) enqueueDirectAddressesUpdate(
	agentID string,
	conn *ws.Conn,
	update protocol.AgentDirectAddressesUpdate,
	failed map[*ws.Conn]struct{},
) {
	if _, alreadyFailed := failed[conn]; alreadyFailed {
		return
	}
	var err error
	if h.sendDirectAddressesUpdate != nil {
		err = h.sendDirectAddressesUpdate(conn, update)
	} else {
		err = conn.SendNotification(consts.RPCSyncAutoAddrUpdate, update)
	}
	if err == nil {
		return
	}
	failed[conn] = struct{}{}
	if h.Logger != nil {
		h.Logger.Warn("direct address update enqueue failed; closing recipient", zap.String("agent", agentID), zap.Error(err))
	}
}

func (h *Hub) closeFailedPeerUpdateConnections(failed map[*ws.Conn]struct{}) {
	for conn := range failed {
		if h.closePeerUpdateConn != nil {
			_ = h.closePeerUpdateConn(conn)
			continue
		}
		_ = conn.Close()
	}
}

func (h *Hub) Capabilities(agentID string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	current := h.sessions[agentID]
	if current == nil {
		return nil
	}
	return append([]string(nil), current.capabilities...)
}

func (h *Hub) isCurrentControlSession(agentID string, conn *ws.Conn, generation uint64) bool {
	if generation == 0 {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	current := h.sessions[agentID]
	return current != nil && current.conn == conn && current.generation == generation
}

func (h *Hub) sendControlResult(conn *ws.Conn, req *jsonrpc.Request, result any) {
	if req.ID == nil {
		return
	}
	response, err := jsonrpc.NewResponse(req.ID, result)
	if err != nil {
		h.sendControlError(conn, req, jsonrpc.ErrInternal, "encode response failed")
		return
	}
	_ = conn.SendResponse(response)
}

func (h *Hub) sendControlError(conn *ws.Conn, req *jsonrpc.Request, code int, message string) {
	if req.ID != nil {
		_ = conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, code, message))
	}
}

func decodeControlParams(raw json.RawMessage, dst any) error {
	if paramsAreEmpty(raw) {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func paramsAreEmpty(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}"))
}

func cloneAgentPublicKey(key agentauth.PublicKey) agentauth.PublicKey {
	key.Key = append([]byte(nil), key.Key...)
	return key
}

func isNilAgentTicketSigner(signer AgentTicketSigner) bool {
	if signer == nil {
		return true
	}
	value := reflect.ValueOf(signer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Call sends an RPC request to a connected agent and waits for response.
func (h *Hub) Call(agentID string, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	h.mu.RLock()
	current := h.sessions[agentID]
	var conn *ws.Conn
	if current != nil {
		conn = current.conn
	}
	h.mu.RUnlock()
	if conn == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	return h.callConn(agentID, conn, method, params, timeout)
}

func (h *Hub) CallSession(agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	h.mu.RLock()
	current := h.sessions[agentID]
	if generation == 0 || current == nil || current.generation != generation {
		h.mu.RUnlock()
		return nil, fmt.Errorf("%w: agent %s generation %d is not current", connectivity.ErrConnectionGenerationChanged, agentID, generation)
	}
	conn := current.conn
	h.mu.RUnlock()
	return h.callConn(agentID, conn, method, params, timeout)
}

func (h *Hub) CallSessionContext(ctx context.Context, agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("agent session call: nil context")
	}
	h.mu.RLock()
	current := h.sessions[agentID]
	if generation == 0 || current == nil || current.generation != generation {
		h.mu.RUnlock()
		return nil, fmt.Errorf("%w: agent %s generation %d is not current", connectivity.ErrConnectionGenerationChanged, agentID, generation)
	}
	conn := current.conn
	h.mu.RUnlock()
	return h.callConnContext(ctx, agentID, conn, method, params, timeout)
}

func (h *Hub) callConn(agentID string, conn *ws.Conn, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	return h.callConnContext(nil, agentID, conn, method, params, timeout)
}

func (h *Hub) callConnContext(ctx context.Context, agentID string, conn *ws.Conn, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if ctx != nil {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
	}
	id := h.nextCallID.Add(1)
	req, err := jsonrpc.NewRequest(method, params, &id)
	if err != nil {
		return nil, err
	}

	ch := make(chan *jsonrpc.Response, 1)
	key := strconv.FormatInt(id, 10)
	pc := pendingCall{ch: ch, conn: conn}
	h.pendingMu.Lock()
	h.pending[key] = pc
	h.pendingMu.Unlock()
	defer h.claimPendingCall(key, pc)

	err = conn.WriteJSON(req)
	if err != nil {
		return nil, fmt.Errorf("send to agent %s failed: %w", agentID, err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}
	var resp *jsonrpc.Response
	select {
	case resp = <-ch:
	case <-ctxDone:
		return nil, context.Cause(ctx)
	case <-timer.C:
		if h.claimPendingCall(key, pc) {
			return nil, fmt.Errorf("agent %s rpc timeout after %s", agentID, timeout)
		}
		resp = <-ch
	}
	if resp == nil {
		return nil, fmt.Errorf("agent %s disconnected during call", agentID)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("agent rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func (h *Hub) deliverPendingResponse(conn *ws.Conn, key string, resp *jsonrpc.Response) bool {
	h.pendingMu.Lock()
	pc, ok := h.pending[key]
	if !ok || pc.conn != conn {
		h.pendingMu.Unlock()
		return false
	}
	delete(h.pending, key)
	h.pendingMu.Unlock()

	select {
	case pc.ch <- resp:
	default:
	}
	return true
}

func (h *Hub) claimPendingCall(key string, expected pendingCall) bool {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	current, ok := h.pending[key]
	if !ok || current.conn != expected.conn || current.ch != expected.ch {
		return false
	}
	delete(h.pending, key)
	return true
}

// failPendingForConn 唤醒并移除归属 conn 的全部在途调用(投 nil 哨兵)。
// 在 pendingMu 内只删除条目，解锁后再非阻塞投递；读循环并发路径查不到已删 key，不会双写。
func (h *Hub) failPendingForConn(conn *ws.Conn) int {
	h.pendingMu.Lock()
	wake := make([]chan *jsonrpc.Response, 0)
	for key, pc := range h.pending {
		if pc.conn != conn {
			continue
		}
		delete(h.pending, key)
		wake = append(wake, pc.ch)
	}
	h.pendingMu.Unlock()

	for _, ch := range wake {
		select {
		case ch <- nil:
		default:
		}
	}
	if len(wake) > 0 && h.Logger != nil {
		// ④ 诊断打点
		h.Logger.Info("cleared pending calls for disconnected agent conn", zap.Int("pending_cleared", len(wake)))
	}
	return len(wake)
}

// NotifyAgent 向指定 agent 单向推送一条通知(无响应)。agent 离线则静默丢弃。
func (h *Hub) NotifyAgent(agentID, method string, params any) {
	h.mu.RLock()
	current := h.sessions[agentID]
	var conn *ws.Conn
	if current != nil {
		conn = current.conn
	}
	h.mu.RUnlock()
	if conn == nil {
		return
	}
	if err := conn.SendNotification(method, params); err != nil {
		h.Logger.Warn("notify agent failed",
			zap.String("agent_id", agentID), zap.String("method", method), zap.Error(err))
	}
}

// GetOnlineAgentIDs returns IDs of currently connected agents.
func (h *Hub) GetOnlineAgentIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		ids = append(ids, id)
	}
	return ids
}

// GetRuntime returns cached runtime info for an agent, or nil if not available.
func (h *Hub) GetRuntime(agentID string) *AgentRuntime {
	h.mu.RLock()
	defer h.mu.RUnlock()
	current := h.sessions[agentID]
	if current == nil {
		return nil
	}
	return cloneRuntime(current.runtime)
}

func (h *Hub) GetControlSession(agentID string) (connectivity.ControlSessionFact, bool) {
	h.mu.RLock()
	current := h.sessions[agentID]
	if current == nil || current.generation == 0 {
		h.mu.RUnlock()
		return connectivity.ControlSessionFact{}, false
	}
	fact := connectivity.ControlSessionFact{
		Generation: current.generation, ConnectedAt: current.connectedAt, HeartbeatAt: current.heartbeatAt,
		RuntimeReportedAt: current.runtimeReportedAt, Runtime: cloneRuntime(current.runtime),
	}
	ring := current.recentErrors
	h.mu.RUnlock()
	fact.RecentErrors = controlRecentErrors(ring.Snapshot())
	return fact, true
}

func (h *Hub) RecordControlError(agentID string, generation uint64, event diagnostics.Event) bool {
	if h == nil || agentID == "" || generation == 0 {
		return false
	}
	h.mu.RLock()
	current := h.sessions[agentID]
	if current == nil || current.generation != generation {
		h.mu.RUnlock()
		return false
	}
	ring := current.recentErrors
	h.mu.RUnlock()
	return ring.Record(event)
}

func controlRecentErrors(events []diagnostics.Event) []connectivity.RecentError {
	result := make([]connectivity.RecentError, 0, len(events))
	for _, event := range events {
		result = append(result, connectivity.RecentError{
			Code: event.Code, Stage: event.Stage, Message: event.Message, OccurredAt: event.At.Unix(), Count: 1,
		})
	}
	return result
}

func (h *Hub) IsCurrentControlSession(agentID string, generation uint64) bool {
	if generation == 0 {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	current := h.sessions[agentID]
	return current != nil && current.generation == generation
}

// IsOnline checks if an agent is currently connected.
func (h *Hub) IsOnline(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[agentID] != nil
}

// GetAgentAddresses returns merged addresses for an agent.
// Priority: manual config (DB) > auto-detected (memory).
func (h *Hub) GetAgentAddresses(agentID string, dbHTTPAddrs string) []agentproxy.Address {
	if dbHTTPAddrs != "" && dbHTTPAddrs != "[]" {
		addrs := agentproxy.ParseAddresses(dbHTTPAddrs)
		if len(addrs) > 0 {
			return addrs
		}
	}

	h.mu.RLock()
	autoAddrs := h.autoHTTPAddrs[agentID]
	h.mu.RUnlock()

	if len(autoAddrs) > 0 {
		result := make([]agentproxy.Address, len(autoAddrs))
		copy(result, autoAddrs)
		return result
	}

	return nil
}

func (h *Hub) nextAutoAddressSequenceLocked() uint64 {
	h.nextAutoAddressSequence++
	if h.nextAutoAddressSequence == 0 {
		h.nextAutoAddressSequence++
	}
	return h.nextAutoAddressSequence
}

func (h *Hub) replaceAutoAddressLocked(
	agentID string,
	sessionGeneration uint64,
	addresses []agentproxy.Address,
) protocol.AgentDirectAddressesUpdate {
	cloned := make([]protocol.Address, len(addresses))
	copy(cloned, addresses)
	sequence := h.nextAutoAddressSequenceLocked()
	if len(addresses) == 0 {
		delete(h.autoHTTPAddrs, agentID)
		delete(h.autoAddressVersions, agentID)
	} else {
		h.autoHTTPAddrs[agentID] = append([]agentproxy.Address(nil), addresses...)
		h.autoAddressVersions[agentID] = autoAddressVersion{
			sessionGeneration: sessionGeneration,
			sequence:          sequence,
		}
	}
	return protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: h.masterInstanceID, AgentID: agentID,
		SessionGeneration: sessionGeneration, Sequence: sequence, HTTPAddresses: cloned,
	}
}

func (h *Hub) withdrawAutoAddressLocked(agentID string, sessionGeneration uint64) (protocol.AgentDirectAddressesUpdate, bool) {
	if len(h.autoHTTPAddrs[agentID]) == 0 || sessionGeneration == 0 {
		return protocol.AgentDirectAddressesUpdate{}, false
	}
	return h.replaceAutoAddressLocked(agentID, sessionGeneration, nil), true
}

func (h *Hub) directAddressSnapshotLocked(excludeAgentID string) []protocol.AgentDirectAddressesUpdate {
	updates := make([]protocol.AgentDirectAddressesUpdate, 0, len(h.autoHTTPAddrs))
	for agentID, addresses := range h.autoHTTPAddrs {
		if agentID == excludeAgentID || len(addresses) == 0 {
			continue
		}
		version, ok := h.autoAddressVersions[agentID]
		if !ok || version.sessionGeneration == 0 || version.sequence == 0 {
			continue
		}
		cloned := make([]protocol.Address, len(addresses))
		copy(cloned, addresses)
		updates = append(updates, protocol.AgentDirectAddressesUpdate{
			MasterInstanceID: h.masterInstanceID, AgentID: agentID,
			SessionGeneration: version.sessionGeneration, Sequence: version.sequence, HTTPAddresses: cloned,
		})
	}
	slices.SortFunc(updates, func(a, b protocol.AgentDirectAddressesUpdate) int {
		return strings.Compare(a.AgentID, b.AgentID)
	})
	return updates
}

func (h *Hub) logAutoAddressChange(update protocol.AgentDirectAddressesUpdate, reason string) {
	if h.Logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("agent_id", update.AgentID), zap.String("reason", reason),
		zap.Uint64("session_generation", update.SessionGeneration), zap.Uint64("sequence", update.Sequence),
	}
	if len(update.HTTPAddresses) == 0 {
		h.Logger.Info("auto-detected agent address withdrawn", fields...)
		return
	}
	fields = append(fields, zap.String("url", update.HTTPAddresses[0].URL))
	h.Logger.Info("auto-detected agent address updated", fields...)
}

func (h *Hub) handleFullSync(ctx context.Context, conn *ws.Conn, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	var params protocol.FullSyncRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, err.Error()))
		return
	}
	keysetAgents := params.Entity == events.EntityAgent && params.Page == 0
	keysetAgentRoutes := params.Entity == events.EntityAgentRoute && params.Page == 0
	keyset := keysetAgents || keysetAgentRoutes
	if params.PageSize <= 0 || params.PageSize > protocol.FullSyncMaxPageSize {
		params.PageSize = protocol.FullSyncMaxPageSize
	}
	if !keyset && params.Page <= 0 {
		params.Page = 1
	}

	daoCtx := dao.NewContextWithContext(h.App, ctx)
	q := dao.NewAdminQuery(daoCtx)

	var items []byte
	var total int64
	var keysetHasMore bool
	var lastID uint
	var snapshotMaxID uint
	var baseVersion int64
	var snapshotContract string

	switch params.Entity {
	case events.EntityToken:
		records, t, _ := q.Token().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.TokenListFilter{})
		total = t
		items, _ = json.Marshal(records)
	case events.EntityChannel:
		records, t, _ := q.Channel().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.ChannelListFilter{})
		total = t
		items, _ = json.Marshal(records)
	case events.EntityModelV1:
		records, t, _ := q.ModelConfig().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.ModelConfigListFilter{})
		total = t
		items, _ = json.Marshal(records)
	case events.EntitySetting:
		records, _ := q.Setting().GetAll()
		total = int64(len(records))
		items, _ = json.Marshal(records)
	case events.EntityAgent:
		if keysetAgents {
			snapshotMaxID = params.SnapshotMaxID
			baseVersion = params.BaseVersion
			if snapshotMaxID == 0 {
				// behavior change: freeze the configured snapshot base before Agent DB I/O.
				baseVersion = h.GetVersion()
				var err error
				snapshotMaxID, err = q.Agent().MaxID()
				if err != nil {
					conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
					return
				}
			}

			records, err := q.Agent().ListKeyset(params.AfterID, snapshotMaxID, params.PageSize)
			if err != nil {
				conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
				return
			}
			total, err = q.Agent().CountThroughID(snapshotMaxID)
			if err != nil {
				conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
				return
			}
			if len(records) > 0 {
				lastID = records[len(records)-1].ID
			}
			keysetHasMore = len(records) == params.PageSize && lastID < snapshotMaxID
			snapshotContract = protocol.AgentFullSyncSnapshotContractV1
			redactAgentSecrets(records)
			items, _ = json.Marshal(records)
			break
		}

		records, t, err := q.Agent().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.AgentListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		redactAgentSecrets(records)
		items, _ = json.Marshal(records)
	case events.EntityAgentRoute:
		if keysetAgentRoutes {
			snapshotMaxID = params.SnapshotMaxID
			baseVersion = params.BaseVersion
			if snapshotMaxID == 0 {
				// The version is captured first so every later route mutation has a
				// replayable version greater than this snapshot's base.
				baseVersion = h.GetVersion()
				var err error
				snapshotMaxID, err = q.AgentRoute().MaxID()
				if err != nil {
					conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
					return
				}
			}

			records, err := q.AgentRoute().ListKeyset(params.AfterID, snapshotMaxID, params.PageSize)
			if err != nil {
				conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
				return
			}
			total, err = q.AgentRoute().CountThroughID(snapshotMaxID)
			if err != nil {
				conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
				return
			}
			if len(records) > 0 {
				lastID = records[len(records)-1].ID
			}
			keysetHasMore = len(records) == params.PageSize && lastID < snapshotMaxID
			items, _ = json.Marshal(records)
			break
		}

		records, t, err := q.AgentRoute().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.AgentRouteListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	case events.EntityRequestLimiter:
		// ListAll：limiter 全量随每次 full-sync 整体替换缓存，分页会让 >PageSize 条
		// 只剩最后一页，其余规则在 agent 上静默丢失（对齐下方 LimiterBinding 写法）。
		records, err := q.RequestLimiter().ListAll()
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = int64(len(records))
		items, _ = json.Marshal(records)
	case events.EntityLimiterBinding:
		records, err := q.LimiterBinding().ListAll()
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = int64(len(records))
		items, _ = json.Marshal(records)

	case events.EntityModelRouting:
		records, t, err := q.ModelRouting().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.ModelRoutingListFilter{Scope: models.RoutingScopeGlobal},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	case events.EntityUserGroup:
		records, t, err := q.UserGroup().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.UserGroupListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	case events.EntityUser:
		users, t, err := q.User().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.UserListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		synced := make([]protocol.SyncedUser, len(users))
		for i, u := range users {
			gid := u.GroupID
			if gid == 0 {
				gid = 1
			}
			synced[i] = protocol.SyncedUser{ID: u.ID, GroupID: gid}
		}
		items, _ = json.Marshal(synced)

	case events.EntityScript:
		records, t, err := q.AdminScript().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, "",
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	default:
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, "unknown entity"))
		return
	}

	hasMore := keysetHasMore
	if !keyset {
		offset := (params.Page - 1) * params.PageSize
		hasMore = int64(offset+params.PageSize) < total
	}
	resp := protocol.FullSyncResponse{
		Items:            items,
		Total:            total,
		Page:             params.Page,
		HasMore:          hasMore,
		Version:          h.GetVersion(),
		Keyset:           keyset,
		LastID:           lastID,
		SnapshotMaxID:    snapshotMaxID,
		BaseVersion:      baseVersion,
		SnapshotContract: snapshotContract,
	}
	rpcResp, _ := jsonrpc.NewResponse(req.ID, resp)
	conn.SendResponse(rpcResp)
}

func redactAgentSecrets(agents []models.Agent) {
	// behavior change: FullSync carries configured DB data only; runtime addresses use versioned notifications.
	for i := range agents {
		agents[i].Secret = ""
	}
}

func (h *Hub) handleGetVersion(conn *ws.Conn, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	resp, _ := jsonrpc.NewResponse(req.ID, protocol.GetVersionResponse{Version: h.GetVersion()})
	conn.SendResponse(resp)
}

func (h *Hub) handleUsageReport(ctx context.Context, agentID string, req *jsonrpc.Request) {
	var report protocol.UsageReport
	if err := json.Unmarshal(req.Params, &report); err != nil {
		h.Logger.Error("invalid usage report", zap.Error(err))
		return
	}
	if report.AgentID == "" {
		report.AgentID = agentID
	}
	if err := events.PublishUsageReported(ctx, h.Bus, report); err != nil {
		h.Logger.Error("publish usage.reported failed", zap.Error(err))
	}
}

func (h *Hub) handleHeartbeat(ctx context.Context, conn *ws.Conn, agentID string, generation uint64, req *jsonrpc.Request) {
	params, reportedAt, accepted := h.acceptControlHeartbeat(agentID, conn, generation, req.Params)
	if !accepted {
		return
	}
	if params.Capabilities != nil && !h.publishCurrentCapabilities(agentID, conn, generation, params.Capabilities) {
		return
	}
	if !h.touchCurrentHeartbeatLastSeen(ctx, agentID, conn, generation, reportedAt) {
		return
	}

	// Update DB if agent reported new addresses/tags/proxy. Skip the SELECT
	// inside mergeAgentConfig when no relevant config field changed; nil-tracker
	// fallback preserves legacy behavior.
	if h.IsCurrentControlSession(agentID, generation) {
		changed := h.Heartbeat == nil || h.Heartbeat.ConfigChanged(agentID, generation, params)
		if changed && h.IsCurrentControlSession(agentID, generation) {
			h.mergeAgentConfig(ctx, agentID, params)
		}
	}
	h.updateAutoDetectedAddress(agentID, conn, generation, params.ListenPort)

	// Check version drift
	currentVersion := int64(0)
	if h.GetVersion != nil {
		currentVersion = h.GetVersion()
	}
	if params.Version > 0 && params.Version < currentVersion-10 {
		// Significant drift, request full sync
		conn.SendNotification(consts.RPCSyncRequestFullSync, nil)
	}

	// Send RPC response if this is a Call (not Notify)
	if req.ID != nil {
		resp, _ := jsonrpc.NewResponse(req.ID, map[string]bool{"ok": true})
		conn.SendResponse(resp)
	}
}

func (h *Hub) mergeAgentConfig(ctx context.Context, agentID string, params protocol.HeartbeatParams) {
	daoCtx := dao.NewContextWithContext(h.App, ctx)
	q := dao.NewAdminQuery(daoCtx)

	agent, err := q.Agent().GetByAgentID(agentID)
	if err != nil {
		return
	}
	updates := map[string]any{}

	if agent.HTTPAddresses == "" || agent.HTTPAddresses == "[]" {
		if len(params.HTTPAddresses) > 0 && string(params.HTTPAddresses) != "null" {
			updates["http_addresses"] = string(params.HTTPAddresses)
		}
	}

	if agent.Tags == "" && params.Tags != "" {
		updates["tags"] = params.Tags
	}
	if agent.ProxyURL == "" && params.ProxyURL != "" {
		updates["proxy_url"] = params.ProxyURL
	}

	if len(updates) > 0 {
		dao.NewAdminMutation(daoCtx).Agent().Update(agent.ID, updates)
	}
}

func (h *Hub) updateAutoDetectedAddress(agentID string, conn *ws.Conn, generation uint64, listenPort int) {
	h.peerRuntimeUpdatesMu.Lock()
	h.mu.Lock()
	current := h.sessions[agentID]
	if current == nil || current.conn != conn || current.generation != generation {
		h.mu.Unlock()
		h.peerRuntimeUpdatesMu.Unlock()
		return
	}

	var update protocol.AgentDirectAddressesUpdate
	changed := false
	if listenPort <= 0 {
		update, changed = h.withdrawAutoAddressLocked(agentID, generation)
	} else if ip := h.remoteAddrs[agentID]; ip != "" {
		newAddresses := []agentproxy.Address{{
			URL: fmt.Sprintf("http://%s:%d", ip, listenPort), Tag: "auto-detected",
		}}
		if !addressesEqual(h.autoHTTPAddrs[agentID], newAddresses) {
			update = h.replaceAutoAddressLocked(agentID, generation, newAddresses)
			changed = true
		}
	}
	if !changed {
		h.mu.Unlock()
		h.peerRuntimeUpdatesMu.Unlock()
		return
	}
	recipients := h.directAddressRecipientsLocked()
	h.mu.Unlock()

	failed := make(map[*ws.Conn]struct{})
	h.broadcastDirectAddressesUpdate(recipients, update, failed)
	h.peerRuntimeUpdatesMu.Unlock()
	h.closeFailedPeerUpdateConnections(failed)

	if len(update.HTTPAddresses) == 0 {
		h.logAutoAddressChange(update, "listen_port_zero")
		return
	}
	h.logAutoAddressChange(update, "listen_port_reported")
}

func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// Broadcast sends a notification to all connected agents.
// 复制 conns snapshot 后释放 RLock，再 fan-out。
// 每条 conn 的 SendNotification 是非阻塞 enqueue（见 ws.Conn.WriteJSON），
// 病 conn 队列满会自己 Close，不会拖累其他 conn 或新接入。
func (h *Hub) Broadcast(method string, params any) {
	h.mu.RLock()
	conns := make(map[string]*ws.Conn, len(h.sessions))
	for id, current := range h.sessions {
		conns[id] = current.conn
	}
	h.mu.RUnlock()

	for agentID, conn := range conns {
		if err := conn.SendNotification(method, params); err != nil {
			h.Logger.Warn("broadcast enqueue failed",
				zap.String("agent", agentID), zap.Error(err))
		}
	}
}

// ConnectedAgents returns the number of connected agents
func (h *Hub) ConnectedAgents() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

func addressesEqual(a, b []agentproxy.Address) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// handleFetchEntity 处理 sync.fetchEntity 请求。
// 主流程：参数解码 → 实体路由 → handler 调用 → 响应编码。
// 业务分支不在此函数；新增实体走 FetchRegistry 注册。
func (h *Hub) handleFetchEntity(ctx context.Context, conn *ws.Conn, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	var p protocol.FetchEntityRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, err.Error()))
		return
	}
	handler, ok := h.fetchRegistry.Resolve(p.Entity)
	if !ok {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, "unknown entity"))
		return
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, ctx))
	data, side, found, err := handler.Fetch(ctx, q, p.Key)
	if err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
		return
	}
	resp, _ := jsonrpc.NewResponse(req.ID, protocol.FetchEntityResponse{
		Found: found,
		Data:  data,
		Side:  side,
	})
	conn.SendResponse(resp)
}
