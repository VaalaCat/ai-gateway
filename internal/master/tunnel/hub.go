package tunnel

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	masteragentauth "github.com/VaalaCat/ai-gateway/internal/master/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	pkgauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

const (
	defaultDrainTimeout        = 30 * time.Second
	handshakeTimeout           = 15 * time.Second
	maxHelloNonceBytes         = 256
	maxHelloMessageBytes int64 = 1024
)

var (
	errHubClosed           = errors.New("master tunnel: hub closed")
	errInvalidTicket       = errors.New("master tunnel: invalid relay ticket")
	errInvalidHello        = errors.New("master tunnel: invalid HELLO")
	errWrongMaster         = errors.New("master tunnel: wrong master")
	errTargetDisabled      = errors.New("master tunnel: target disabled")
	errTargetCapability    = errors.New("master tunnel: target lacks tunnel capability")
	errTargetNotFound      = errors.New("master tunnel: target not found")
	errRelayNotReady       = errors.New("master tunnel: target relay session not ready")
	errSessionNotFound     = errors.New("master tunnel: session not found")
	errGenerationNotFound  = errors.New("master tunnel: generation not found")
	errGenerationExhausted = errors.New("master tunnel: session generation exhausted")
	errHubDraining         = errors.New("master tunnel: hub draining")
)

type AgentSessions struct {
	Active    *Session
	Candidate *Session
	Draining  map[uint64]*Session
}

type SessionSnapshot struct {
	Generation          uint64
	DesiredGeneration   uint64
	Availability        string
	AcceptingNewStreams bool
	Streams             int
	ConnectedAt         int64
	DrainingAt          int64
	RecentErrors        []connectivity.RecentError
}

type AgentLookup interface {
	GetByAgentID(context.Context, string) (*models.Agent, error)
	Capabilities(agentID string) []string
}

type AdmissionGate struct{ enabled atomic.Bool }

func (g *AdmissionGate) Set(enabled bool) { g.enabled.Store(enabled) }
func (g *AdmissionGate) AllowNew() bool   { return g != nil && g.enabled.Load() }
func (g *AdmissionGate) RejectionCode() string {
	if g.AllowNew() {
		return ""
	}
	return wire.ErrorCodeRelayFallbackDisabled
}

type HubOptions struct {
	InstanceID      string
	Signer          *masteragentauth.Signer
	Agents          AgentLookup
	Admission       *AdmissionGate
	Limits          wire.Limits
	DrainTimeout    time.Duration
	Logger          *zap.Logger
	RuntimeSettings func() RuntimeSettings
	Metrics         TunnelMetricRecorder
}

type RuntimeSettings struct {
	Limits       wire.Limits
	DrainTimeout time.Duration
}

type relayRuntimeLookup interface {
	GetRelayRuntime(agentID string) (connectivity.RelayRuntimeFact, bool)
}

type switchKey struct {
	source     *Session
	generation uint64
	streamID   wire.StreamID
}

type closeOwnedMasterConn struct {
	sessionConn
	closeOwner *wire.ConnectionCloseOwner
}

func (c *closeOwnedMasterConn) Close() error {
	return c.closeOwner.Close()
}

type Hub struct {
	opts HubOptions

	mu                            sync.RWMutex
	metricsMu                     sync.Mutex
	publishedSessionMetricVersion uint64
	metricActive                  int
	metricCandidate               int
	metricDraining                int
	metricStreams                 int
	metricVersion                 uint64
	sessions                      map[string]*AgentSessions
	switches                      map[switchKey]*Switch
	connections                   map[*websocket.Conn]*wire.ConnectionCloseOwner
	nextGen                       uint64
	draining                      bool
	changed                       chan struct{}

	ctx              context.Context
	cancel           context.CancelCauseFunc
	done             chan struct{}
	closeOnce        sync.Once
	workers          conc.WaitGroup
	workerMu         sync.Mutex
	handlers         int
	handlerChanged   chan struct{}
	closeConnection  func(*websocket.Conn) error
	writeHandshake   func(*websocket.Conn, int, []byte) error
	setReadDeadline  func(*websocket.Conn, time.Time) error
	setWriteDeadline func(*websocket.Conn, time.Time) error
	handshakeTimeout time.Duration

	afterDrainAdmission        func()
	beforeScheduleDrain        func()
	afterWorkerAdmissionClosed func()
}

func NewHub(opts HubOptions) *Hub {
	if isNilAgentLookup(opts.Agents) {
		opts.Agents = nil
	}
	if opts.Admission == nil {
		opts.Admission = &AdmissionGate{}
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = defaultDrainTimeout
	}
	if normalized, err := wire.NormalizeV1Limits(opts.Limits); err == nil {
		opts.Limits = normalized
	} else {
		opts.Limits = wire.Limits{
			MaxMetadataBytes: wire.MaxV1PayloadBytes, MaxDataBytes: wire.MaxV1PayloadBytes,
			InitialStreamWindow: 256 * 1024, MaxQueuedSessionBytes: 4 * 1024 * 1024, MaxConcurrentStreams: 128,
		}
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	h := &Hub{
		opts: opts, sessions: make(map[string]*AgentSessions), switches: make(map[switchKey]*Switch),
		connections: make(map[*websocket.Conn]*wire.ConnectionCloseOwner), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		changed:         make(chan struct{}, 1),
		handlerChanged:  make(chan struct{}, 1),
		closeConnection: func(conn *websocket.Conn) error { return conn.Close() },
		writeHandshake: func(conn *websocket.Conn, messageType int, payload []byte) error {
			return conn.WriteMessage(messageType, payload)
		},
		setReadDeadline: func(conn *websocket.Conn, deadline time.Time) error {
			return conn.SetReadDeadline(deadline)
		},
		setWriteDeadline: func(conn *websocket.Conn, deadline time.Time) error {
			return conn.SetWriteDeadline(deadline)
		},
		handshakeTimeout: handshakeTimeout,
	}
	h.syncSessionMetrics(sessionMetricSnapshot{})
	h.syncStreamMetrics()
	return h
}

func (h *Hub) HandleWS(c *gin.Context) {
	if !h.beginHandler() {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	defer h.finishHandler()
	handlerCtx, cancel := context.WithCancelCause(c.Request.Context())
	stopOwnerCancel := context.AfterFunc(h.ctx, func() { cancel(context.Cause(h.ctx)) })
	defer func() {
		stopOwnerCancel()
		cancel(context.Canceled)
	}()
	claims, err := h.authenticate(c.GetHeader("Authorization"))
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	agent, err := h.opts.Agents.GetByAgentID(handlerCtx, claims.AgentID)
	if err != nil || agent == nil || agent.Status != consts.StatusEnabled {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	conn, err := relayUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// behavior change: bound untrusted relay messages before protocol parsing.
	conn.SetReadLimit(maxHelloMessageBytes)
	closeOwner := wire.NewConnectionCloseOwner(func() error { return h.closeConnection(conn) })
	if !h.trackConnection(conn, closeOwner) {
		_ = closeOwner.Close()
		return
	}
	defer h.untrackConnection(conn)
	h.serveConn(conn, closeOwner, claims)
}

func (h *Hub) beginHandler() bool {
	h.workerMu.Lock()
	defer h.workerMu.Unlock()
	if h.ctx.Err() != nil {
		return false
	}
	h.handlers++
	return true
}

func (h *Hub) finishHandler() {
	h.workerMu.Lock()
	if h.handlers > 0 {
		h.handlers--
	}
	notify := h.handlers == 0
	h.workerMu.Unlock()
	if notify {
		select {
		case h.handlerChanged <- struct{}{}:
		default:
		}
	}
}

func (h *Hub) waitHandlers() {
	for {
		h.workerMu.Lock()
		active := h.handlers
		h.workerMu.Unlock()
		if active == 0 {
			return
		}
		<-h.handlerChanged
	}
}

func (h *Hub) trackConnection(conn *websocket.Conn, closeOwner *wire.ConnectionCloseOwner) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx.Err() != nil {
		return false
	}
	h.connections[conn] = closeOwner
	return true
}

func (h *Hub) untrackConnection(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.connections, conn)
	h.mu.Unlock()
}

var relayUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, EnableCompression: false}

func (h *Hub) authenticate(header string) (*pkgauth.RelayClaims, error) {
	if h == nil || h.opts.Signer == nil || h.opts.Agents == nil || h.opts.InstanceID == "" {
		return nil, errInvalidTicket
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || strings.TrimSpace(header[len(prefix):]) == "" {
		return nil, errInvalidTicket
	}
	raw := strings.TrimSpace(header[len(prefix):])
	unverified := &pkgauth.RelayClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, unverified); err != nil {
		return nil, errInvalidTicket
	}
	if unverified.MasterInstanceID != h.opts.InstanceID {
		return nil, errWrongMaster
	}
	verifier := pkgauth.NewVerifier(singleKey{h.opts.Signer.PublicKey()})
	claims, err := verifier.VerifyRelay(pkgauth.RelayTicket(raw), unverified.AgentID, h.opts.InstanceID, unverified.DesiredGeneration)
	if err != nil {
		return nil, errInvalidTicket
	}
	return claims, nil
}

type singleKey struct{ key pkgauth.PublicKey }

func (s singleKey) LookupKey(id string) (ed25519.PublicKey, bool) {
	if id != s.key.KeyID || len(s.key.Key) != ed25519.PublicKeySize {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), s.key.Key...), true
}

func (h *Hub) serveConn(conn *websocket.Conn, closeOwner *wire.ConnectionCloseOwner, claims *pkgauth.RelayClaims) {
	if h.setReadDeadline(conn, time.Now().Add(h.handshakeTimeout)) != nil {
		_ = closeOwner.Close()
		return
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage {
		_ = closeOwner.Close()
		return
	}
	var hello wire.Hello
	if json.Unmarshal(payload, &hello) != nil || hello.Nonce == "" || len(hello.Nonce) > maxHelloNonceBytes || hello.DesiredGeneration != claims.DesiredGeneration {
		_ = closeOwner.Close()
		return
	}
	runtimeSettings := h.runtimeSettings()
	conn.SetReadLimit(relayMessageReadLimit(runtimeSettings.Limits))
	generation, err := h.allocateGeneration()
	if err != nil {
		_ = closeOwner.Close()
		return
	}
	proof, err := h.opts.Signer.SignWelcome(pkgauth.WelcomeProofClaims{
		AgentID: claims.AgentID, Nonce: hello.Nonce, MasterInstanceID: h.opts.InstanceID,
		SessionGeneration: generation, DesiredGeneration: hello.DesiredGeneration,
	})
	if err != nil {
		_ = closeOwner.Close()
		return
	}
	welcome, _ := json.Marshal(wire.Welcome{NonceProof: string(proof), MasterInstanceID: h.opts.InstanceID, SessionGeneration: generation, Capabilities: []string{protocol.AgentCapabilityTunnelV1}, Limits: runtimeSettings.Limits})
	ctx, cancel := context.WithCancelCause(h.ctx)
	sessionConn := &closeOwnedMasterConn{sessionConn: conn, closeOwner: closeOwner}
	session := newSession(h, sessionConn, claims.AgentID, generation, hello.DesiredGeneration, runtimeSettings.Limits, ctx, cancel)
	if h.registerUnauthenticated(session) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	if h.setWriteDeadline(conn, time.Now().Add(h.handshakeTimeout)) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	if h.writeHandshake(conn, websocket.TextMessage, welcome) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	messageType, payload, err = conn.ReadMessage()
	var authenticated wire.Authenticated
	if err != nil || messageType != websocket.TextMessage || json.Unmarshal(payload, &authenticated) != nil ||
		authenticated.DesiredGeneration != claims.DesiredGeneration || authenticated.SessionGeneration != generation {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	if h.validateCandidateForConfirmation(session) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	confirmed, _ := json.Marshal(wire.Confirmed{
		DesiredGeneration: claims.DesiredGeneration, SessionGeneration: generation,
	})
	if h.setReadDeadline(conn, time.Time{}) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	if h.setWriteDeadline(conn, time.Time{}) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	confirmedOutcome, confirmedErr := h.writeConfirmedHandshake(session, conn, confirmed)
	if confirmedErr != nil || confirmedOutcome != wire.HandshakeSucceeded {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	if h.commitConfirmedCandidate(session) != nil {
		h.unregister(session)
		session.Cancel(errSessionClosed)
		return
	}
	session.run()
}

func (h *Hub) writeConfirmedHandshake(session *Session, conn *websocket.Conn, payload []byte) (wire.HandshakeOutcome, error) {
	owner := wire.NewHandshakeOutcomeOwner()
	timer := time.NewTimer(h.handshakeTimeout)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-timer.C:
			if owner.TryOwn(wire.HandshakeTimedOut) {
				session.Cancel(errSessionClosed)
			}
		case <-h.ctx.Done():
			if owner.TryOwn(wire.HandshakeCanceled) {
				session.Cancel(context.Cause(h.ctx))
			}
		case <-stop:
		}
	}()
	err := h.writeHandshake(conn, websocket.TextMessage, payload)
	if err == nil {
		owner.TryOwn(wire.HandshakeSucceeded)
	} else {
		owner.TryOwn(wire.HandshakeStopped)
	}
	timer.Stop()
	close(stop)
	<-done
	return owner.Outcome(), err
}

func (h *Hub) registerUnauthenticated(s *Session) error {
	if s == nil {
		return errSessionClosed
	}
	h.mu.Lock()
	if h.ctx.Err() != nil {
		h.mu.Unlock()
		return errHubClosed
	}
	if h.draining {
		h.mu.Unlock()
		return errHubDraining
	}
	set := h.sessions[s.agentID]
	if set == nil {
		set = &AgentSessions{Draining: make(map[uint64]*Session)}
		h.sessions[s.agentID] = set
	}
	if set.Candidate != nil {
		h.mu.Unlock()
		return errDuplicateSession
	}
	set.Candidate = s
	h.transitionSessionMetricLocked(s, sessionMetricCandidate)
	metrics := h.sessionMetricSnapshotLocked()
	h.mu.Unlock()
	h.syncSessionMetrics(metrics)
	return nil
}

func relayMessageReadLimit(limits wire.Limits) int64 {
	payloadLimit := limits.MaxMetadataBytes
	if limits.MaxDataBytes > payloadLimit {
		payloadLimit = limits.MaxDataBytes
	}
	return int64(wire.HeaderSize) + payloadLimit
}

func (h *Hub) allocateGeneration() (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// behavior change: generation exhaustion fails closed instead of wrapping.
	if h.nextGen == math.MaxUint64 {
		return 0, errGenerationExhausted
	}
	h.nextGen++
	return h.nextGen, nil
}

func (h *Hub) register(s *Session) error {
	if s == nil {
		return errSessionClosed
	}
	h.mu.Lock()
	if h.ctx.Err() != nil {
		h.mu.Unlock()
		return errHubClosed
	}
	if h.draining {
		h.mu.Unlock()
		return errHubDraining
	}
	set := h.sessions[s.agentID]
	if set == nil {
		set = &AgentSessions{Draining: make(map[uint64]*Session)}
		h.sessions[s.agentID] = set
	}
	if set.Active == nil {
		set.Active = s
		h.transitionSessionMetricLocked(s, sessionMetricActive)
	} else {
		if set.Candidate != nil {
			h.mu.Unlock()
			return errDuplicateSession
		}
		set.Candidate = s
		h.transitionSessionMetricLocked(s, sessionMetricCandidate)
	}
	metrics := h.sessionMetricSnapshotLocked()
	h.mu.Unlock()
	h.syncSessionMetrics(metrics)
	return nil
}

func (h *Hub) validateCandidateForConfirmation(s *Session) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.sessions[s.agentID]
	if set == nil {
		return errSessionNotFound
	}
	if set.Candidate != s {
		return errGenerationNotFound
	}
	return nil
}

func (h *Hub) commitConfirmedCandidate(s *Session) error {
	h.mu.Lock()
	set := h.sessions[s.agentID]
	if set == nil || set.Candidate != s {
		h.mu.Unlock()
		return errGenerationNotFound
	}
	old := set.Active
	set.Active, set.Candidate = s, nil
	h.transitionSessionMetricLocked(s, sessionMetricActive)
	drainOld := false
	if old != nil {
		select {
		case <-old.Done():
			h.transitionSessionMetricLocked(old, sessionMetricNone)
		default:
			h.transitionSessionMetricLocked(old, sessionMetricDraining)
			old.accepting.Store(false)
			old.drainingAt.Store(time.Now().Unix())
			set.Draining[old.generation] = old
			drainOld = true
		}
	}
	metrics := h.sessionMetricSnapshotLocked()
	h.mu.Unlock()
	h.syncSessionMetrics(metrics)
	if drainOld {
		if h.beforeScheduleDrain != nil {
			h.beforeScheduleDrain()
		}
		h.scheduleDrain(old)
	}
	return nil
}

func (h *Hub) promote(s *Session) error {
	return h.commitConfirmedCandidate(s)
}

func (h *Hub) scheduleDrain(s *Session) {
	s.drainOnce.Do(func() {
		h.workerMu.Lock()
		if h.ctx.Err() != nil {
			h.workerMu.Unlock()
			return
		}
		h.workers.Go(func() {
			timer := time.NewTimer(h.runtimeSettings().DrainTimeout)
			defer timer.Stop()
			select {
			case <-timer.C:
				s.recordError(diagnostics.Event{Code: wire.ErrorCodeDrainTimeout, Stage: "drain", Message: errDrainTimeout.Error(), At: time.Now()})
				s.Cancel(errDrainTimeout)
			case <-s.Done():
			case <-h.ctx.Done():
			}
		})
		h.workerMu.Unlock()
	})
}

func (h *Hub) runtimeSettings() RuntimeSettings {
	settings := RuntimeSettings{Limits: h.opts.Limits, DrainTimeout: h.opts.DrainTimeout}
	if h.opts.RuntimeSettings == nil {
		return settings
	}
	configured := h.opts.RuntimeSettings()
	if normalized, err := wire.NormalizeV1Limits(configured.Limits); err == nil {
		settings.Limits = normalized
	}
	if configured.DrainTimeout > 0 {
		settings.DrainTimeout = configured.DrainTimeout
	}
	return settings
}

func (h *Hub) unregister(s *Session) {
	if s == nil {
		return
	}
	h.mu.Lock()
	set := h.sessions[s.agentID]
	removed := false
	if set != nil {
		if set.Active == s && set.Active.generation == s.generation {
			set.Active = nil
			removed = true
		}
		if set.Candidate == s && set.Candidate.generation == s.generation {
			set.Candidate = nil
			removed = true
		}
		if set.Draining[s.generation] == s {
			delete(set.Draining, s.generation)
			removed = true
		}
		if set.Active == nil && set.Candidate == nil && len(set.Draining) == 0 {
			delete(h.sessions, s.agentID)
		}
	}
	if removed {
		h.transitionSessionMetricLocked(s, sessionMetricNone)
	}
	metrics := h.sessionMetricSnapshotLocked()
	h.mu.Unlock()
	if removed {
		h.syncSessionMetrics(metrics)
	}
}

func (h *Hub) Snapshot(agentID string) (SessionSnapshot, bool) {
	h.mu.RLock()
	set := h.sessions[agentID]
	var s *Session
	if set != nil {
		s = set.Active
	}
	h.mu.RUnlock()
	if s == nil {
		return SessionSnapshot{}, false
	}
	return s.snapshot(), true
}

func (h *Hub) GetRelayRuntime(agentID string) (connectivity.RelayRuntimeFact, bool) {
	if h == nil || h.opts.Agents == nil {
		return connectivity.RelayRuntimeFact{}, false
	}
	agent, err := h.opts.Agents.GetByAgentID(h.ctx, agentID)
	if err != nil || agent == nil {
		return connectivity.RelayRuntimeFact{}, false
	}
	fact := connectivity.RelayRuntimeFact{
		Support: "unsupported", Config: "not_configured", Availability: "unavailable",
		Convergence: "converged", RecentErrors: make([]connectivity.RecentError, 0),
	}
	if runtimeSource, ok := h.opts.Agents.(relayRuntimeLookup); ok {
		if reported, found := runtimeSource.GetRelayRuntime(agentID); found {
			fact = cloneRelayRuntimeFact(reported)
		}
	}
	for _, capability := range h.opts.Agents.Capabilities(agentID) {
		if capability == protocol.AgentCapabilityTunnelV1 {
			fact.Support = "supported"
			break
		}
	}
	if fact.Support != "supported" {
		return fact, true
	}
	reportedActive := fact.Active
	reportedAccepting := fact.AcceptingNewStreams
	snapshot, active := h.Snapshot(agentID)
	if !active {
		fact.Availability = "unavailable"
		fact.AcceptingNewStreams = false
		fact.Active = connectivity.RelayActiveSnapshot{}
		if fact.Config == "configured" {
			fact.Convergence = "converging"
		}
		return fact, true
	}
	fact.Active = connectivity.RelayActiveSnapshot{
		ActiveGeneration:  snapshot.DesiredGeneration,
		SessionGeneration: snapshot.Generation,
		ConnectedAt:       snapshot.ConnectedAt,
		Streams:           snapshot.Streams,
	}
	activeTupleMatches := reportedActive.ActiveGeneration == snapshot.DesiredGeneration &&
		reportedActive.SessionGeneration == snapshot.Generation
	if activeTupleMatches {
		fact.Active.URI = reportedActive.URI
		fact.Active.RetryAt = reportedActive.RetryAt
	}
	fact.Availability = "available"
	fact.AcceptingNewStreams = snapshot.AcceptingNewStreams
	fact.RecentErrors = mergeRecentErrors(fact.RecentErrors, snapshot.RecentErrors)
	desiredGenerationMatches := fact.Desired.DesiredGeneration == snapshot.DesiredGeneration
	uriMatches := reportedActive.URI != "" && reportedActive.URI == fact.Desired.EffectiveURI
	acceptingReady := reportedAccepting && snapshot.AcceptingNewStreams
	if activeTupleMatches && desiredGenerationMatches && uriMatches && acceptingReady {
		fact.Convergence = "converged"
	} else {
		fact.Convergence = "converging"
	}
	return fact, true
}

func mergeRecentErrors(groups ...[]connectivity.RecentError) []connectivity.RecentError {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	result := make([]connectivity.RecentError, 0, total)
	for _, group := range groups {
		result = append(result, group...)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].OccurredAt < result[j].OccurredAt })
	if len(result) > diagnostics.DefaultRingCapacity {
		result = append([]connectivity.RecentError(nil), result[len(result)-diagnostics.DefaultRingCapacity:]...)
	}
	return result
}

func cloneRelayRuntimeFact(fact connectivity.RelayRuntimeFact) connectivity.RelayRuntimeFact {
	fact.RecentErrors = append([]connectivity.RecentError(nil), fact.RecentErrors...)
	return fact
}

func (h *Hub) Drain(agentID string, generation uint64) error {
	h.mu.Lock()
	s, err := h.findSessionLocked(agentID, generation)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	changed := h.transitionSessionMetricLocked(s, sessionMetricDraining)
	s.accepting.Store(false)
	s.drainingAt.CompareAndSwap(0, time.Now().Unix())
	metrics := h.sessionMetricSnapshotLocked()
	h.mu.Unlock()
	if changed {
		h.syncSessionMetrics(metrics)
	}
	h.scheduleDrain(s)
	return nil
}

func (h *Hub) Disconnect(agentID string, generation uint64, cause error) error {
	s, err := h.findSession(agentID, generation)
	if err != nil {
		return err
	}
	s.Cancel(cause)
	return nil
}

func (h *Hub) findSession(agentID string, generation uint64) (*Session, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.findSessionLocked(agentID, generation)
}

func (h *Hub) findSessionLocked(agentID string, generation uint64) (*Session, error) {
	set := h.sessions[agentID]
	if set == nil {
		return nil, errSessionNotFound
	}
	for _, s := range []*Session{set.Active, set.Candidate, set.Draining[generation]} {
		if s != nil && s.generation == generation {
			return s, nil
		}
	}
	return nil, errGenerationNotFound
}

func (h *Hub) validateTarget(ctx context.Context, id string) error {
	if id == "" || h.opts.Agents == nil {
		return errors.Join(errSessionNotFound, errTargetNotFound)
	}
	agent, err := h.opts.Agents.GetByAgentID(ctx, id)
	if err != nil || agent == nil {
		return errors.Join(errSessionNotFound, errTargetNotFound)
	}
	if agent.Status != consts.StatusEnabled {
		return errTargetDisabled
	}
	for _, capability := range h.opts.Agents.Capabilities(id) {
		if capability == protocol.AgentCapabilityTunnelV1 {
			return nil
		}
	}
	return errTargetCapability
}

func (h *Hub) activeTarget(ctx context.Context, id string) (*Session, error) {
	if err := h.validateTarget(ctx, id); err != nil {
		return nil, err
	}
	h.mu.RLock()
	set := h.sessions[id]
	var target *Session
	if set != nil && set.Active != nil && set.Active.accepting.Load() {
		target = set.Active
	}
	h.mu.RUnlock()
	if target == nil {
		return nil, errors.Join(errSessionNotFound, errRelayNotReady)
	}
	return target, nil
}

func (h *Hub) addSwitch(sw *Switch) error {
	key := switchKey{source: sw.source, generation: sw.sourceGeneration, streamID: sw.id}
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return errHubDraining
	}
	if h.switches[key] != nil {
		h.mu.Unlock()
		return errDuplicateStreamID
	}
	h.switches[key] = sw
	h.metricStreams++
	h.mu.Unlock()
	h.syncStreamMetrics()
	h.notifyChanged()
	return nil
}

func (h *Hub) attachSwitch(sw *Switch) error {
	return h.withSwitchAttachment(sw, func() error {
		if err := h.addSwitch(sw); err != nil {
			return err
		}
		if err := sw.source.addLeg(sw.id, sw); err != nil {
			return err
		}
		return sw.target.addLeg(sw.id, sw)
	})
}

func (h *Hub) withSwitchAttachment(sw *Switch, attach func() error) error {
	if !sw.beginAttachment() {
		return errSessionClosed
	}
	err := attach()
	if err == nil {
		err = sw.attachmentStatus()
	}
	sw.attachments.Done()
	if err != nil {
		return h.rollbackSwitch(sw, err)
	}
	return nil
}

func (h *Hub) rollbackSwitch(sw *Switch, cause error) error {
	sw.source.removeLeg(sw.id, sw, sw.sourceGeneration)
	sw.target.removeLeg(sw.id, sw, sw.targetGeneration)
	h.removeSwitch(sw)
	sw.Cancel(cause)
	<-sw.Done()
	return cause
}

func (h *Hub) removeSwitch(sw *Switch) {
	key := switchKey{source: sw.source, generation: sw.sourceGeneration, streamID: sw.id}
	h.mu.Lock()
	removed := false
	if h.switches[key] == sw {
		delete(h.switches, key)
		h.metricStreams--
		removed = true
	}
	h.mu.Unlock()
	if removed {
		h.syncStreamMetrics()
		h.notifyChanged()
	}
}

func (h *Hub) DrainAll(ctx context.Context) error {
	if ctx == nil {
		return errors.New("master tunnel: nil context")
	}
	h.opts.Admission.Set(false)
	h.mu.Lock()
	h.draining = true
	candidates := make([]*Session, 0)
	metricsChanged := false
	for _, set := range h.sessions {
		for _, session := range appendSession(nil, set.Active, set.Candidate) {
			session.accepting.Store(false)
			metricsChanged = h.transitionSessionMetricLocked(session, sessionMetricDraining) || metricsChanged
		}
		for _, session := range set.Draining {
			session.accepting.Store(false)
			metricsChanged = h.transitionSessionMetricLocked(session, sessionMetricDraining) || metricsChanged
		}
		if set.Candidate != nil {
			candidates = append(candidates, set.Candidate)
		}
	}
	pending := len(h.switches)
	metrics := h.sessionMetricSnapshotLocked()
	h.mu.Unlock()
	if metricsChanged {
		h.syncSessionMetrics(metrics)
	}
	for _, candidate := range candidates {
		candidate.Cancel(errHubDraining)
	}
	if h.afterDrainAdmission != nil {
		h.afterDrainAdmission()
	}
	for pending > 0 {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-h.done:
			return errHubClosed
		case <-h.changed:
			h.mu.RLock()
			pending = len(h.switches)
			h.mu.RUnlock()
		}
	}
	return nil
}

func (h *Hub) notifyChanged() {
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

func (h *Hub) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("master tunnel: nil context")
	}
	h.closeOnce.Do(func() { go h.finalizeClose() })
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (h *Hub) finalizeClose() {
	h.cancel(errHubClosed)
	h.mu.RLock()
	all := make([]*Session, 0)
	connections := make([]*wire.ConnectionCloseOwner, 0, len(h.connections))
	for _, closeOwner := range h.connections {
		connections = append(connections, closeOwner)
	}
	for _, set := range h.sessions {
		all = appendSession(all, set.Active, set.Candidate)
		for _, s := range set.Draining {
			all = appendSession(all, s)
		}
	}
	h.mu.RUnlock()
	for _, closeOwner := range connections {
		_ = closeOwner.Close()
	}
	for _, s := range all {
		s.Cancel(errHubClosed)
	}
	for _, s := range all {
		<-s.Done()
	}
	h.workerMu.Lock()
	h.workerMu.Unlock()
	if h.afterWorkerAdmissionClosed != nil {
		h.afterWorkerAdmissionClosed()
	}
	h.waitHandlers()
	h.workers.Wait()
	close(h.done)
}

func appendSession(dst []*Session, sessions ...*Session) []*Session {
	for _, s := range sessions {
		if s == nil {
			continue
		}
		found := false
		for _, current := range dst {
			if current == s {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}

func (h *Hub) Done() <-chan struct{} { return h.done }

func (h *Hub) ResourceCounts() app.ResourceCounts {
	var counts app.ResourceCounts
	h.mu.RLock()
	defer h.mu.RUnlock()
	counts.RelayStreams = int64(len(h.switches))
	counts.RelaySockets = int64(len(h.connections))
	for _, set := range h.sessions {
		if set.Active != nil {
			counts.RelayActive++
		}
		if set.Candidate != nil {
			counts.RelayCandidates++
		}
		counts.RelayDraining += int64(len(set.Draining))
	}
	return counts
}

func isNilAgentLookup(value AgentLookup) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	return (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil()
}
