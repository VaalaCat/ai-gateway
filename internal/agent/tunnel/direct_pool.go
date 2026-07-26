package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

const (
	directReasonCapacity    = "direct_session_capacity"
	directPoolSweepInterval = 25 * time.Millisecond
)

var (
	errDirectPoolClosed     = errors.New("direct session pool: closed")
	errDirectPoolDraining   = errors.New("direct session pool: draining")
	errDirectCandidateStale = errors.New("direct session pool: stale candidate")
)

// closeSessionAfterDrain is shared by the outgoing pool and the incoming
// ingress. It stops nothing itself; the caller has already stopped admission.
// It cancels the session once it goes idle or the drain deadline passes, and
// returns after the session is done. Exactly-once close is owned by the
// session (and its ConnectionCloseOwner); this only drives the cancel timing.
func closeSessionAfterDrain(ctx context.Context, session *Session, drainTimeout time.Duration) {
	if session == nil {
		return
	}
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	activity := session.Activity()
	for {
		if session.idle() {
			session.Cancel(errSessionClosed)
		}
		select {
		case <-session.Done():
			return
		case <-timer.C:
			session.Cancel(errSessionClosed)
		case <-activity:
		case <-ctx.Done():
			session.Cancel(context.Cause(ctx))
		}
	}
}

// directOpenError carries only a stable stage/code plus a sanitized endpoint.
// It never exposes the raw URL, ticket, or proxy userinfo.
type directOpenError struct {
	stage    string
	code     string
	endpoint string
	cause    error
}

func (e *directOpenError) Error() string {
	return fmt.Sprintf("direct_tunnel stage=%s code=%s endpoint=%s", e.stage, e.code, e.endpoint)
}

func (e *directOpenError) Unwrap() error          { return e.cause }
func (e *directOpenError) Stage() string          { return e.stage }
func (e *directOpenError) ReasonCode() string     { return e.code }
func (e *directOpenError) CountsForCircuit() bool { return e.countsForCircuit() }

func (e *directOpenError) countsForCircuit() bool {
	if e == nil || e.code == "canceled" {
		return false
	}
	switch e.stage {
	case "dial", "tls", "upgrade", "hello", "ready", "accepted", "confirmed", "session":
		return true
	default:
		return false
	}
}

// IsDirectCapacityRejection reports whether err is a capacity rejection.
// Capacity rejection is not a Direct circuit failure.
func IsDirectCapacityRejection(err error) bool {
	var poolErr *directOpenError
	return errors.As(err, &poolErr) && poolErr.code == directReasonCapacity
}

func directPoolLifecycleError(endpoint string, cause error) error {
	return &directOpenError{stage: "pool", code: "direct_closed", endpoint: endpoint, cause: cause}
}

type directCandidateOpenError struct {
	err   error
	stage string
	code  string
	claim atomic.Bool
}

func (e *directCandidateOpenError) Error() string      { return e.err.Error() }
func (e *directCandidateOpenError) Unwrap() error      { return e.err }
func (e *directCandidateOpenError) Stage() string      { return e.stage }
func (e *directCandidateOpenError) ReasonCode() string { return e.code }
func (e *directCandidateOpenError) CountsForCircuit() bool {
	return e.claim.CompareAndSwap(false, true)
}

func directCandidateFailure(err error, endpoint string) error {
	if err == nil {
		return nil
	}
	var failure interface {
		Stage() string
		ReasonCode() string
		CountsForCircuit() bool
	}
	if !errors.As(err, &failure) {
		err = &directOpenError{stage: "dial", code: "failed", endpoint: endpoint, cause: err}
		failure = err.(interface {
			Stage() string
			ReasonCode() string
			CountsForCircuit() bool
		})
	}
	if !failure.CountsForCircuit() {
		return err
	}
	return &directCandidateOpenError{err: err, stage: failure.Stage(), code: failure.ReasonCode()}
}

type directSessionKey struct {
	TargetAgentID string
	CanonicalURL  string
	Proxy         string
}

type directDesiredFingerprint struct {
	Credential [32]byte
	Limits     [32]byte
}

// DirectPoolSnapshot is a read-only view of pool resource usage. It never
// contains any raw address, ticket, or proxy value.
type DirectPoolSnapshot struct {
	Active                      int
	Candidates                  int
	Draining                    int
	Streams                     int
	Timers                      int
	Sockets                     int
	BufferedBytes               int64
	MaxSessionPeakBufferedBytes int64
}

type DirectRuntimeSettings struct {
	Limits       wire.Limits
	MaxSessions  int
	IdleTimeout  time.Duration
	DrainTimeout time.Duration
}

type DirectSessionPoolOptions struct {
	SourceAgentID         string
	DirectOutboundEnabled func() bool
	Dialer                DirectSessionDialer
	Credentials           agentauthcache.ForwardCredentialReader
	Limits                func() wire.Limits
	MaxSessions           func() int
	IdleTimeout           func() time.Duration
	DrainTimeout          func() time.Duration
	Now                   func() time.Time
	Logger                *zap.Logger
	Metrics               *pkgmetrics.AgentRelayMetrics
	Suppressor            *diagnostics.Suppressor
}

type directPoolSlot struct {
	target       string
	key          directSessionKey
	fingerprint  directDesiredFingerprint
	frozen       agentproxy.DirectSessionTarget
	active       *Session
	lastUsed     time.Time
	candidate    *directDialCandidate
	drainPending bool
	pendingDrain directLifecycleAction
}

type directDialCandidate struct {
	target      string
	key         directSessionKey
	fingerprint directDesiredFingerprint
	frozen      agentproxy.DirectSessionTarget
	credential  agentauthcache.ForwardCredential
	limits      wire.Limits
	done        chan struct{}
	session     *Session
	err         error
}

type DirectSessionPool struct {
	opts            DirectSessionPoolOptions
	runtimeSettings atomic.Pointer[DirectRuntimeSettings]
	logs            *directLogs

	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}

	workers      conc.WaitGroup
	doneOnce     sync.Once
	closeStarted bool
	sweepWake    chan struct{}

	mu             sync.Mutex
	closed         bool
	draining       bool
	slots          map[string]*directPoolSlot
	drains         map[*Session]struct{}
	metricMu       sync.Mutex
	metricSnapshot DirectPoolSnapshot

	beforeAcquireLock         func()
	beforeFinishCandidateLock func()
	beforeSweepLock           atomic.Pointer[func()]
	afterSweep                atomic.Pointer[func()]
}

func NewDirectSessionPool(opts DirectSessionPoolOptions) *DirectSessionPool {
	if opts.DirectOutboundEnabled == nil {
		opts.DirectOutboundEnabled = func() bool { return true }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	p := &DirectSessionPool{
		opts: opts, logs: newDirectLogs(opts.Logger, opts.Suppressor), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		sweepWake: make(chan struct{}, 1),
		slots:     make(map[string]*directPoolSlot), drains: make(map[*Session]struct{}),
	}
	p.workers.Go(p.sweepLoop)
	return p
}

func (p *DirectSessionPool) ApplyRuntimeSettings(settings DirectRuntimeSettings) {
	if p == nil {
		return
	}
	settings = normalizeDirectRuntimeSettings(settings)
	now := p.opts.Now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.runtimeSettings.Store(&settings)
	notices := p.reconcileLocked(now, settings)
	p.mu.Unlock()
	emitDirectLifecycleNotices(p.logs, notices)
	p.signalChanged()
}

// RuntimeSettings returns the pool's current runtime settings snapshot.
func (p *DirectSessionPool) RuntimeSettings() DirectRuntimeSettings {
	if p == nil {
		return DirectRuntimeSettings{}
	}
	return p.currentRuntimeSettings()
}

var (
	_ agentproxy.DirectAttemptStreamOpener     = (*DirectSessionPool)(nil)
	_ agentproxy.DirectProbeStreamOpener       = (*DirectSessionPool)(nil)
	_ agentproxy.DirectAttemptTransportBuilder = (*DirectSessionPool)(nil)
)

// ---- public opener API --------------------------------------------------

func (p *DirectSessionPool) OpenAttemptStream(
	ctx context.Context, target agentproxy.DirectSessionTarget, req app.AttemptStreamRequest,
) (app.AttemptStream, error) {
	transport, err := p.BuildDirectAttemptTransport(ctx, target)
	if err != nil {
		return nil, err
	}
	reservation, err := transport.AcquireAttemptStream(ctx)
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		return nil, err
	}
	if reservation == nil {
		return nil, &directOpenError{stage: "pool", code: "direct_closed", endpoint: sanitizedDirectEndpoint(target.WebSocketURL)}
	}
	defer reservation.Release()
	return reservation.OpenAttemptStream(ctx, req)
}

func (p *DirectSessionPool) BuildDirectAttemptTransport(
	ctx context.Context, target agentproxy.DirectSessionTarget,
) (agentproxy.DirectAttemptTransport, error) {
	if p == nil || p.opts.DirectOutboundEnabled == nil || !p.opts.DirectOutboundEnabled() {
		return nil, newPathPolicyError(consts.RouteErrorSourceDirectOutboundDisabled)
	}
	return p.buildDirectAttemptTransport(ctx, target)
}

type directAttemptTransport struct {
	pool        *DirectSessionPool
	target      agentproxy.DirectSessionTarget
	key         directSessionKey
	endpoint    string
	fingerprint directDesiredFingerprint
	credential  agentauthcache.ForwardCredential
	limits      wire.Limits
	identity    agentproxy.DirectTransportIdentity
}

func (t *directAttemptTransport) TransportIdentity() agentproxy.DirectTransportIdentity {
	if t == nil {
		return agentproxy.DirectTransportIdentity{}
	}
	return t.identity
}

func (t *directAttemptTransport) AcquireAttemptStream(
	ctx context.Context,
) (agentproxy.DirectAttemptStreamReservation, error) {
	if t == nil || t.pool == nil {
		return nil, &directOpenError{stage: "pool", code: "direct_closed", endpoint: "invalid://invalid" + DirectTunnelPath}
	}
	borrow, err := t.pool.acquireBuilt(ctx, t)
	if err != nil {
		return nil, err
	}
	return &directAttemptStreamReservation{
		session: borrow.session, identity: directTransportIdentity(borrow.key, borrow.fingerprint),
		addressFingerprint: borrow.frozen.AddressFingerprint,
		endpoint:           sanitizedDirectEndpoint(borrow.frozen.WebSocketURL),
		state:              directReservationAcquired, openDone: make(chan struct{}),
	}, nil
}

type directReservationState uint8

const (
	directReservationAcquired directReservationState = iota + 1
	directReservationOpening
	directReservationOpened
	directReservationReleased
)

type directAttemptStreamReservation struct {
	session            *Session
	identity           agentproxy.DirectTransportIdentity
	addressFingerprint string
	endpoint           string

	mu          sync.Mutex
	state       directReservationState
	openDone    chan struct{}
	releaseOnce sync.Once
}

func (r *directAttemptStreamReservation) TransportIdentity() agentproxy.DirectTransportIdentity {
	if r == nil {
		return agentproxy.DirectTransportIdentity{}
	}
	return r.identity
}

func (r *directAttemptStreamReservation) AddressFingerprint() string {
	if r == nil {
		return ""
	}
	return r.addressFingerprint
}

func (r *directAttemptStreamReservation) OpenAttemptStream(
	ctx context.Context, req app.AttemptStreamRequest,
) (stream app.AttemptStream, err error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if r == nil || r.session == nil {
		return nil, directReservationUnavailableError(r)
	}
	r.mu.Lock()
	if r.state != directReservationAcquired {
		r.mu.Unlock()
		return nil, directReservationUnavailableError(r)
	}
	r.state = directReservationOpening
	r.mu.Unlock()
	defer r.finishOpen()
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	return r.session.OpenAttemptStream(ctx, req)
}

func (r *directAttemptStreamReservation) finishOpen() {
	r.mu.Lock()
	if r.state == directReservationOpening {
		r.state = directReservationOpened
		close(r.openDone)
	}
	r.mu.Unlock()
}

func (r *directAttemptStreamReservation) Release() {
	if r == nil {
		return
	}
	r.releaseOnce.Do(func() {
		r.mu.Lock()
		if r.state == directReservationOpening {
			openDone := r.openDone
			r.mu.Unlock()
			<-openDone
			r.mu.Lock()
		}
		r.state = directReservationReleased
		session := r.session
		r.mu.Unlock()
		if session != nil {
			session.releaseAdmission()
		}
	})
}

func directReservationUnavailableError(r *directAttemptStreamReservation) error {
	endpoint := "invalid://invalid" + DirectTunnelPath
	if r != nil && r.endpoint != "" {
		endpoint = r.endpoint
	}
	return &directOpenError{stage: "pool", code: "direct_reservation_released", endpoint: endpoint}
}

type directSessionBorrow struct {
	session     *Session
	key         directSessionKey
	fingerprint directDesiredFingerprint
	frozen      agentproxy.DirectSessionTarget
}

func (p *DirectSessionPool) OpenProbeStream(
	ctx context.Context, target agentproxy.DirectSessionTarget, req app.ProbeStreamRequest,
) (app.ProbeStream, error) {
	session, err := p.acquire(ctx, target)
	if err != nil {
		return nil, err
	}
	stream, err := session.OpenProbeStream(ctx, req)
	session.releaseAdmission()
	if err != nil {
		return nil, err
	}
	return stream, nil
}

// ---- acquisition and singleflight candidate -----------------------------

func (p *DirectSessionPool) lifecycleOpenError(target agentproxy.DirectSessionTarget) error {
	p.mu.Lock()
	closed, draining := p.closed, p.draining
	p.mu.Unlock()
	endpoint := sanitizedDirectEndpoint(target.WebSocketURL)
	switch {
	case closed:
		return directPoolLifecycleError(endpoint, errDirectPoolClosed)
	case draining:
		return directPoolLifecycleError(endpoint, errDirectPoolDraining)
	default:
		return nil
	}
}

func (p *DirectSessionPool) acquire(ctx context.Context, target agentproxy.DirectSessionTarget) (*Session, error) {
	built, err := p.buildDirectAttemptTransport(ctx, target)
	if err != nil {
		return nil, err
	}
	borrow, err := p.acquireBuilt(ctx, built)
	return borrow.session, err
}

func (p *DirectSessionPool) buildDirectAttemptTransport(
	ctx context.Context, target agentproxy.DirectSessionTarget,
) (*directAttemptTransport, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	target = cloneDirectSessionTarget(target)
	if err := p.lifecycleOpenError(target); err != nil {
		return nil, err
	}
	key, endpoint, err := p.desiredKey(target)
	if err != nil {
		return nil, err
	}
	fingerprint, credential, limits, err := p.desiredFingerprint(endpoint)
	if err != nil {
		return nil, err
	}
	return &directAttemptTransport{
		pool: p, target: target, key: key, endpoint: endpoint,
		fingerprint: fingerprint, credential: credential, limits: limits,
		identity: directTransportIdentity(key, fingerprint),
	}, nil
}

func (p *DirectSessionPool) acquireBuilt(ctx context.Context, built *directAttemptTransport) (directSessionBorrow, error) {
	if ctx == nil {
		return directSessionBorrow{}, errNilContext
	}
	if built == nil {
		return directSessionBorrow{}, &directOpenError{stage: "pool", code: "direct_closed", endpoint: "invalid://invalid" + DirectTunnelPath}
	}
	target, key, endpoint := built.target, built.key, built.endpoint
	fingerprint, credential, limits := built.fingerprint, built.credential, built.limits
	for {
		settings := p.currentRuntimeSettings()
		now := p.opts.Now()
		if p.beforeAcquireLock != nil {
			p.beforeAcquireLock()
		}
		p.mu.Lock()
		settings = p.publishedSettingsLocked(settings)
		if p.closed {
			p.mu.Unlock()
			return directSessionBorrow{}, directPoolLifecycleError(endpoint, errDirectPoolClosed)
		}
		if p.draining {
			p.mu.Unlock()
			return directSessionBorrow{}, directPoolLifecycleError(endpoint, errDirectPoolDraining)
		}
		if current := limitsFingerprint(settings.Limits); current != fingerprint.Limits {
			p.mu.Unlock()
			fingerprint.Limits = current
			limits = settings.Limits
			continue
		}
		notices := p.reconcileLocked(now, settings)
		slot := p.slots[target.TargetAgentID]
		if slot == nil {
			slot = &directPoolSlot{target: target.TargetAgentID}
			p.slots[target.TargetAgentID] = slot
		}
		if slot.active != nil && slot.key == key && slot.fingerprint == fingerprint {
			if slot.drainPending {
				p.mu.Unlock()
				emitDirectLifecycleNotices(p.logs, notices)
				return directSessionBorrow{}, &directOpenError{stage: "pool", code: directReasonCapacity, endpoint: endpoint}
			}
			if slot.active.acquireAdmission() {
				slot.lastUsed = now
				session := slot.active
				p.mu.Unlock()
				emitDirectLifecycleNotices(p.logs, notices)
				p.opts.Metrics.IncDirectSessionReuse()
				return directSessionBorrow{
					session: session, key: slot.key, fingerprint: slot.fingerprint, frozen: slot.frozen,
				}, nil
			}
			slot.active = nil
			p.gcSlotLocked(target.TargetAgentID, slot)
			p.mu.Unlock()
			emitDirectLifecycleNotices(p.logs, notices)
			continue
		}
		candidate := slot.candidate
		if candidate != nil && (candidate.key != key || candidate.fingerprint != fingerprint) {
			p.mu.Unlock()
			emitDirectLifecycleNotices(p.logs, notices)
			if waitErr := p.waitCandidateCompletion(ctx, candidate); waitErr != nil {
				return directSessionBorrow{}, waitErr
			}
			continue
		}
		startedCandidate := false
		if candidate == nil {
			capacityNotices, capacityErr := p.ensureCandidateCapacityLocked(slot, endpoint, settings)
			notices = append(notices, capacityNotices...)
			if capacityErr != nil {
				if fallback := p.borrowActiveSlotLocked(slot, now); fallback != nil {
					p.mu.Unlock()
					emitDirectLifecycleNotices(p.logs, notices)
					p.opts.Metrics.IncDirectSessionReuse()
					return *fallback, nil
				}
				p.gcSlotLocked(target.TargetAgentID, slot)
				p.mu.Unlock()
				emitDirectLifecycleNotices(p.logs, notices)
				return directSessionBorrow{}, capacityErr
			}
			candidate = p.startCandidateLocked(slot, target, key, fingerprint, credential, limits)
			startedCandidate = true
		}
		p.mu.Unlock()
		emitDirectLifecycleNotices(p.logs, notices)
		if startedCandidate {
			p.startCandidate(candidate)
		}

		session, waitErr := p.waitCandidate(ctx, candidate)
		if waitErr != nil {
			if ctx.Err() != nil {
				return directSessionBorrow{}, context.Cause(ctx)
			}
			if errors.Is(waitErr, errDirectCandidateStale) {
				continue
			}
			if fallback := p.borrowActiveFallback(target.TargetAgentID); fallback != nil {
				return *fallback, nil
			}
			return directSessionBorrow{}, waitErr
		}
		if session.acquireAdmission() {
			return directSessionBorrow{
				session: session, key: candidate.key, fingerprint: candidate.fingerprint, frozen: candidate.frozen,
			}, nil
		}
		// Promoted session already unhealthy; retry the whole flow.
	}
}

func (p *DirectSessionPool) waitCandidateCompletion(ctx context.Context, candidate *directDialCandidate) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-candidate.done:
		return nil
	case <-p.done:
		return directPoolLifecycleError(sanitizedDirectEndpoint(candidate.frozen.WebSocketURL), errDirectPoolClosed)
	}
}

func (p *DirectSessionPool) waitCandidate(ctx context.Context, candidate *directDialCandidate) (*Session, error) {
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-candidate.done:
		if errors.Is(candidate.err, errDirectPoolClosed) || errors.Is(candidate.err, errDirectPoolDraining) {
			return nil, directPoolLifecycleError(sanitizedDirectEndpoint(candidate.frozen.WebSocketURL), candidate.err)
		}
		return candidate.session, candidate.err
	case <-p.done:
		return nil, directPoolLifecycleError(sanitizedDirectEndpoint(candidate.frozen.WebSocketURL), errDirectPoolClosed)
	}
}

// borrowActiveFallback lets a caller reuse a still-healthy prior active after a
// replacement dial fails, rather than taking down a working connection. The
// fingerprint may be stale; a healthy connection is preferred over an error.
func (p *DirectSessionPool) borrowActiveFallback(target string) *directSessionBorrow {
	now := p.opts.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.borrowActiveSlotLocked(p.slots[target], now)
}

func (p *DirectSessionPool) borrowActiveSlotLocked(slot *directPoolSlot, now time.Time) *directSessionBorrow {
	if slot == nil || slot.active == nil || slot.drainPending || !slot.active.acquireAdmission() {
		return nil
	}
	slot.lastUsed = now
	return &directSessionBorrow{
		session: slot.active, key: slot.key, fingerprint: slot.fingerprint, frozen: slot.frozen,
	}
}

func (p *DirectSessionPool) startCandidateLocked(
	slot *directPoolSlot, target agentproxy.DirectSessionTarget, key directSessionKey,
	fingerprint directDesiredFingerprint, credential agentauthcache.ForwardCredential, limits wire.Limits,
) *directDialCandidate {
	candidate := &directDialCandidate{
		target: target.TargetAgentID, key: key, fingerprint: fingerprint, frozen: target,
		credential: credential, limits: limits, done: make(chan struct{}),
	}
	slot.candidate = candidate
	p.workers.Go(func() { p.runCandidate(candidate) })
	return candidate
}

func (p *DirectSessionPool) startCandidate(candidate *directDialCandidate) {
	p.signalChanged()
	p.logs.DialStarted(directLogEvent{
		SourceAgentID: p.opts.SourceAgentID, TargetAgentID: candidate.target,
		Stage: "dial", ReasonCode: "none", SourceOutbound: true,
	}, candidate.key.CanonicalURL)
}

func (p *DirectSessionPool) runCandidate(candidate *directDialCandidate) {
	session, err := p.dialCandidate(candidate)
	if err != nil || session == nil {
		if err == nil {
			err = errors.New("direct session pool: dial returned nil session")
		}
		p.finishCandidate(candidate, nil, err)
		return
	}
	session.opts.Metrics = p.opts.Metrics
	session.opts.directLogs = p.logs
	session.opts.directSourceAgentID = p.opts.SourceAgentID
	session.opts.directTargetAgentID = candidate.target
	runDone := make(chan struct{})
	p.workers.Go(func() {
		defer close(runDone)
		_ = session.Run(p.ctx)
	})
	select {
	case <-session.started:
	case <-p.ctx.Done():
		session.Cancel(context.Cause(p.ctx))
	}
	initErr := session.initializationError()
	if initErr == nil && p.ctx.Err() != nil {
		initErr = context.Cause(p.ctx)
	}
	if initErr != nil {
		session.Cancel(initErr)
		<-runDone
		p.finishCandidate(candidate, nil, initErr)
		return
	}
	if !p.finishCandidate(candidate, session, nil) {
		session.Cancel(errDirectCandidateStale)
		<-runDone
		p.onSessionEnd(session)
		return
	}
	<-runDone
	p.onSessionEnd(session)
}

func (p *DirectSessionPool) dialCandidate(candidate *directDialCandidate) (*Session, error) {
	if p.opts.Dialer == nil {
		return nil, &directOpenError{
			stage: "pool", code: "policy_disabled",
			endpoint: sanitizedDirectEndpoint(candidate.frozen.WebSocketURL),
		}
	}
	targetURL := ""
	if candidate.frozen.WebSocketURL != nil {
		targetURL = candidate.frozen.WebSocketURL.String()
	}
	return p.opts.Dialer.DialDirectSession(p.ctx, DirectSessionDialRequest{
		SourceAgentID: p.opts.SourceAgentID,
		TargetAgentID: candidate.target,
		TargetURL:     targetURL,
		ProxyURL:      candidate.frozen.ProxyURL,
		Credential:    candidate.credential,
		Limits:        candidate.limits,
	})
}

// finishCandidate publishes the dial result. It confirms slot/pool generation
// still matches before promoting; late results are closed by the caller.
func (p *DirectSessionPool) finishCandidate(candidate *directDialCandidate, session *Session, err error) bool {
	sampledSettings := p.currentRuntimeSettings()
	now := p.opts.Now()
	if p.beforeFinishCandidateLock != nil {
		p.beforeFinishCandidateLock()
	}
	p.mu.Lock()
	settings := p.publishedSettingsLocked(sampledSettings)
	slot := p.slots[candidate.target]
	staleSettings := limitsFingerprint(settings.Limits) != candidate.fingerprint.Limits ||
		p.occupiedSlotCountLocked() > settings.MaxSessions
	if p.closed || staleSettings || slot == nil || slot.candidate != candidate {
		if slot != nil && slot.candidate == candidate {
			slot.candidate = nil
			p.gcSlotLocked(candidate.target, slot)
		}
		endpoint := sanitizedDirectEndpoint(candidate.frozen.WebSocketURL)
		if p.closed {
			candidate.err = directPoolLifecycleError(endpoint, errDirectPoolClosed)
		} else {
			candidate.err = directPoolLifecycleError(endpoint, errDirectCandidateStale)
		}
		p.mu.Unlock()
		close(candidate.done)
		reason := directMetricReason(candidate.err)
		p.opts.Metrics.IncDirectSessionDial(pkgmetrics.DirectDialFailed, reason)
		p.logs.DialFailed(directLogEvent{
			SourceAgentID: p.opts.SourceAgentID, TargetAgentID: candidate.target,
			Stage: "dial", ReasonCode: string(reason), SourceOutbound: true,
		}, endpoint, candidate.err)
		p.signalChanged()
		return false
	}
	slot.candidate = nil
	if err != nil || session == nil {
		candidate.err = directCandidateFailure(err, sanitizedDirectEndpoint(candidate.frozen.WebSocketURL))
		p.gcSlotLocked(candidate.target, slot)
		p.mu.Unlock()
		close(candidate.done)
		reason := directMetricReason(candidate.err)
		p.opts.Metrics.IncDirectSessionDial(pkgmetrics.DirectDialFailed, reason)
		p.logs.DialFailed(directLogEvent{
			SourceAgentID: p.opts.SourceAgentID, TargetAgentID: candidate.target,
			Stage: "dial", ReasonCode: string(reason), SourceOutbound: true,
		}, candidate.key.CanonicalURL, candidate.err)
		p.signalChanged()
		return false
	}
	// behavior change: idle timeout starts when the promoted session actually becomes idle.
	session.setActivityNow(p.opts.Now, now)
	var notices []directLifecycleNotice
	if slot.active != nil {
		var moved bool
		notices, moved = p.tryMoveActiveToDrainingLocked(slot, settings, directLifecycleReplaced)
		if !moved {
			candidate.err = &directOpenError{
				stage: "pool", code: directReasonCapacity,
				endpoint: sanitizedDirectEndpoint(candidate.frozen.WebSocketURL),
			}
			p.mu.Unlock()
			close(candidate.done)
			p.opts.Metrics.IncDirectSessionDial(pkgmetrics.DirectDialFailed, pkgmetrics.DirectReasonCapacity)
			p.logs.DialFailed(directLogEvent{
				SourceAgentID: p.opts.SourceAgentID, TargetAgentID: candidate.target,
				Stage: "pool", ReasonCode: directReasonCapacity, SourceOutbound: true,
			}, candidate.key.CanonicalURL, candidate.err)
			p.signalChanged()
			return false
		}
	}
	slot.active = session
	// behavior change: pending drain state belongs to the prior active generation.
	slot.drainPending = false
	slot.pendingDrain = directLifecycleReplaced
	slot.key = candidate.key
	slot.fingerprint = candidate.fingerprint
	slot.frozen = candidate.frozen
	slot.lastUsed = now
	session.opts.Metrics = p.opts.Metrics
	session.opts.directLogs = p.logs
	session.opts.directSourceAgentID = p.opts.SourceAgentID
	session.opts.directTargetAgentID = candidate.target
	candidate.session = session
	if p.draining {
		notices = append(notices, p.moveActiveToDrainingLocked(slot, directLifecycleDraining, settings.DrainTimeout)...)
	}
	p.mu.Unlock()
	emitDirectLifecycleNotices(p.logs, notices)
	close(candidate.done)
	p.opts.Metrics.IncDirectSessionDial(pkgmetrics.DirectDialSucceeded, pkgmetrics.DirectReasonNone)
	p.logs.Ready(directLogEvent{
		SourceAgentID: p.opts.SourceAgentID, TargetAgentID: candidate.target,
		Stage: "dial", ReasonCode: "none", SessionGeneration: session.Generation(), SourceOutbound: true,
	}, candidate.key.CanonicalURL)
	p.signalChanged()
	return true
}

func (p *DirectSessionPool) onSessionEnd(session *Session) {
	p.mu.Lock()
	for _, slot := range p.slots {
		if slot.active == session {
			slot.active = nil
			// behavior change: do not carry this generation's pending action into a candidate promotion.
			slot.drainPending = false
			slot.pendingDrain = directLifecycleReplaced
			break
		}
	}
	p.gcAllSlotsLocked()
	p.mu.Unlock()
	emitDirectLifecycleNotices(p.logs, []directLifecycleNotice{{
		action: directLifecycleClosed,
		event: directLogEvent{
			SourceAgentID: p.opts.SourceAgentID, TargetAgentID: session.opts.directTargetAgentID,
			Stage: "close", ReasonCode: "closed", SessionGeneration: session.Generation(), SourceOutbound: true,
		},
	}})
	p.signalChanged()
}

// ---- capacity, eviction, idle sweep -------------------------------------

func (p *DirectSessionPool) ensureCandidateCapacityLocked(
	slot *directPoolSlot, endpoint string, settings DirectRuntimeSettings,
) ([]directLifecycleNotice, error) {
	maxSessions := settings.MaxSessions
	if slot != nil && slot.active != nil {
		if len(p.drains) >= maxSessions {
			return nil, &directOpenError{stage: "pool", code: directReasonCapacity, endpoint: endpoint}
		}
		return nil, nil
	}
	var notices []directLifecycleNotice
	for p.occupiedSlotCountLocked() >= maxSessions {
		victim := p.oldestIdleActiveLocked()
		if victim == nil {
			victim = p.oldestActiveLocked()
		}
		if victim == nil {
			return notices, &directOpenError{stage: "pool", code: directReasonCapacity, endpoint: endpoint}
		}
		movedNotices, moved := p.tryMoveActiveToDrainingLocked(victim, settings, directLifecycleEvicted)
		if !moved {
			return notices, &directOpenError{stage: "pool", code: directReasonCapacity, endpoint: endpoint}
		}
		notices = append(notices, movedNotices...)
	}
	return notices, nil
}

func (p *DirectSessionPool) reconcileLocked(now time.Time, settings DirectRuntimeSettings) []directLifecycleNotice {
	// behavior change: pending is a reconcile decision, not a terminal state.
	// Keep admission stopped while recomputing, then reactivate only survivors.
	previousPending := make([]struct {
		slot    *directPoolSlot
		session *Session
	}, 0)
	for _, slot := range p.slots {
		if slot.active == nil || !slot.drainPending {
			continue
		}
		previousPending = append(previousPending, struct {
			slot    *directPoolSlot
			session *Session
		}{slot: slot, session: slot.active})
		slot.drainPending = false
		slot.pendingDrain = directLifecycleReplaced
	}
	zeroFingerprint := [32]byte{}
	desiredLimits := limitsFingerprint(settings.Limits)
	for _, slot := range p.slots {
		if slot.active == nil || slot.fingerprint.Limits == zeroFingerprint || slot.fingerprint.Limits == desiredLimits {
			continue
		}
		p.markDrainPendingLocked(slot, directLifecycleDraining)
	}
	idleTimeout := settings.IdleTimeout
	if idleTimeout > 0 {
		for _, slot := range p.slots {
			if slot.active == nil {
				continue
			}
			idleSince, idle := slot.active.idleSinceTime()
			if idle && now.Sub(idleSince) >= idleTimeout {
				p.markDrainPendingLocked(slot, directLifecycleEvicted)
			}
		}
	}
	for p.projectedOccupiedSlotCountLocked() > settings.MaxSessions {
		victim := p.oldestIdleDrainCandidateLocked()
		if victim == nil {
			victim = p.oldestActiveDrainCandidateLocked()
		}
		if victim == nil {
			break
		}
		p.markDrainPendingLocked(victim, directLifecycleEvicted)
	}
	for _, previous := range previousPending {
		if previous.slot.active != previous.session || previous.slot.drainPending {
			continue
		}
		if !previous.session.tryActivate() {
			p.markDrainPendingLocked(previous.slot, directLifecycleDraining)
		}
	}
	var notices []directLifecycleNotice
	for _, slot := range p.slots {
		if !slot.drainPending || slot.active == nil || len(p.drains) >= settings.MaxSessions {
			continue
		}
		action := slot.pendingDrain
		movedNotices, moved := p.tryMoveActiveToDrainingLocked(slot, settings, action)
		if moved {
			notices = append(notices, movedNotices...)
		}
	}
	p.gcAllSlotsLocked()
	return notices
}

func (p *DirectSessionPool) markDrainPendingLocked(slot *directPoolSlot, action directLifecycleAction) {
	if slot == nil || slot.active == nil {
		return
	}
	// behavior change: cap-bounded reconcile stops admission immediately, even
	// when the session must wait for a draining registry slot.
	slot.active.setAccepting(false)
	if !slot.drainPending || action == directLifecycleEvicted {
		slot.pendingDrain = action
	}
	slot.drainPending = true
}

func (p *DirectSessionPool) projectedOccupiedSlotCountLocked() int {
	count := 0
	for _, slot := range p.slots {
		// behavior change: a replacement candidate shares its active's slot;
		// only a candidate without an active reserves separate projected capacity.
		if slot.active != nil && !slot.drainPending || slot.active == nil && slot.candidate != nil {
			count++
		}
	}
	return count
}

func (p *DirectSessionPool) oldestIdleDrainCandidateLocked() *directPoolSlot {
	var oldest *directPoolSlot
	var oldestIdleSince time.Time
	for _, slot := range p.slots {
		if slot.active == nil || slot.drainPending {
			continue
		}
		idleSince, idle := slot.active.idleSinceTime()
		if !idle {
			continue
		}
		if oldest == nil || idleSince.Before(oldestIdleSince) ||
			idleSince.Equal(oldestIdleSince) && slot.lastUsed.Before(oldest.lastUsed) {
			oldest = slot
			oldestIdleSince = idleSince
		}
	}
	return oldest
}

func (p *DirectSessionPool) oldestActiveDrainCandidateLocked() *directPoolSlot {
	var oldest *directPoolSlot
	for _, slot := range p.slots {
		if slot.active == nil || slot.drainPending {
			continue
		}
		if oldest == nil || slot.lastUsed.Before(oldest.lastUsed) {
			oldest = slot
		}
	}
	return oldest
}

// tryMoveActiveToDrainingLocked is the cap-aware transition used by every
// non-terminal replacement and reconcile path.
func (p *DirectSessionPool) tryMoveActiveToDrainingLocked(
	slot *directPoolSlot, settings DirectRuntimeSettings, action directLifecycleAction,
) ([]directLifecycleNotice, bool) {
	if slot == nil || slot.active == nil || len(p.drains) >= settings.MaxSessions {
		return nil, false
	}
	// behavior change: a full draining registry preserves the healthy active.
	return p.moveActiveToDrainingLocked(slot, action, settings.DrainTimeout), true
}

func (p *DirectSessionPool) moveActiveToDrainingLocked(
	slot *directPoolSlot, action directLifecycleAction, drainTimeout time.Duration,
) []directLifecycleNotice {
	session := slot.active
	if session == nil {
		return nil
	}
	slot.active = nil
	slot.drainPending = false
	slot.pendingDrain = directLifecycleReplaced
	session.setAccepting(false)
	if _, exists := p.drains[session]; exists {
		return nil
	}
	p.drains[session] = struct{}{}
	p.workers.Go(func() { p.watchDrain(session, drainTimeout) })
	event := directLogEvent{
		SourceAgentID: p.opts.SourceAgentID, TargetAgentID: slot.target,
		Stage: "drain", ReasonCode: "closed", SessionGeneration: session.Generation(), SourceOutbound: true,
	}
	notices := make([]directLifecycleNotice, 0, 2)
	if action != directLifecycleDraining {
		actionEvent := event
		actionEvent.Stage = map[directLifecycleAction]string{directLifecycleReplaced: "replace", directLifecycleEvicted: "evict"}[action]
		notices = append(notices, directLifecycleNotice{action: action, event: actionEvent})
	}
	return append(notices, directLifecycleNotice{action: directLifecycleDraining, event: event})
}

func (p *DirectSessionPool) watchDrain(session *Session, drainTimeout time.Duration) {
	closeSessionAfterDrain(p.ctx, session, drainTimeout)
	p.mu.Lock()
	delete(p.drains, session)
	p.mu.Unlock()
	p.signalChanged()
}

func (p *DirectSessionPool) sweepLoop() {
	ticker := time.NewTicker(directPoolSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.sweepWake:
		case <-ticker.C:
		}
		settings := p.currentRuntimeSettings()
		now := p.opts.Now()
		if hook := p.beforeSweepLock.Load(); hook != nil {
			(*hook)()
		}
		p.mu.Lock()
		settings = p.publishedSettingsLocked(settings)
		var notices []directLifecycleNotice
		if !p.closed {
			notices = p.reconcileLocked(now, settings)
		}
		p.mu.Unlock()
		emitDirectLifecycleNotices(p.logs, notices)
		p.publishMetrics()
		if hook := p.afterSweep.Load(); hook != nil {
			(*hook)()
		}
	}
}

// ---- lifecycle ----------------------------------------------------------

func (p *DirectSessionPool) Drain(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	settings := p.currentRuntimeSettings()
	p.mu.Lock()
	settings = p.publishedSettingsLocked(settings)
	if p.closed {
		p.mu.Unlock()
		return errDirectPoolClosed
	}
	p.draining = true
	var notices []directLifecycleNotice
	for _, slot := range p.slots {
		if slot.active != nil {
			notices = append(notices, p.moveActiveToDrainingLocked(slot, directLifecycleDraining, settings.DrainTimeout)...)
		}
	}
	p.mu.Unlock()
	emitDirectLifecycleNotices(p.logs, notices)
	p.signalChanged()

	for {
		p.mu.Lock()
		waits := make([]<-chan struct{}, 0, len(p.drains)+len(p.slots))
		for _, slot := range p.slots {
			if slot.candidate != nil {
				waits = append(waits, slot.candidate.done)
			}
		}
		for session := range p.drains {
			waits = append(waits, session.Done())
		}
		p.mu.Unlock()
		if len(waits) == 0 {
			return nil
		}
		for _, wait := range waits {
			select {
			case <-wait:
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-p.done:
				return nil
			}
		}
	}
}

func (p *DirectSessionPool) Close(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	p.startClose()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (p *DirectSessionPool) startClose() {
	p.mu.Lock()
	first := !p.closeStarted
	p.closeStarted = true
	p.closed = true
	p.mu.Unlock()
	if first {
		p.cancel(errDirectPoolClosed)
		go p.finalize()
	}
}

func (p *DirectSessionPool) finalize() {
	p.workers.Wait()
	p.mu.Lock()
	clear(p.slots)
	clear(p.drains)
	p.mu.Unlock()
	p.publishMetrics()
	p.doneOnce.Do(func() { close(p.done) })
}

func (p *DirectSessionPool) Done() <-chan struct{} { return p.done }

func (p *DirectSessionPool) Snapshot() DirectPoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := DirectPoolSnapshot{}
	for _, slot := range p.slots {
		if slot.active != nil {
			snapshot.Active++
			snapshot.Streams += slot.active.StreamCount()
			current, peak := slot.active.bufferedByteSnapshot()
			snapshot.BufferedBytes += current
			snapshot.MaxSessionPeakBufferedBytes = max(snapshot.MaxSessionPeakBufferedBytes, peak)
		}
		if slot.candidate != nil {
			snapshot.Candidates++
		}
	}
	snapshot.Draining = len(p.drains)
	for session := range p.drains {
		snapshot.Streams += session.StreamCount()
		current, peak := session.bufferedByteSnapshot()
		snapshot.BufferedBytes += current
		snapshot.MaxSessionPeakBufferedBytes = max(snapshot.MaxSessionPeakBufferedBytes, peak)
	}
	// Candidates are excluded: a dialing candidate's socket is owned by the dial
	// goroutine until the session is constructed, so it is not yet a pool socket.
	snapshot.Sockets = snapshot.Active + snapshot.Draining
	snapshot.Timers = snapshot.Draining
	if !p.closed {
		snapshot.Timers++
	}
	return snapshot
}

// ---- helpers ------------------------------------------------------------

func (p *DirectSessionPool) signalChanged() {
	p.publishMetrics()
	select {
	case p.sweepWake <- struct{}{}:
	default:
	}
}

func (p *DirectSessionPool) publishMetrics() {
	if p == nil || p.opts.Metrics == nil {
		return
	}
	p.metricMu.Lock()
	snapshot := p.Snapshot()
	previous := p.metricSnapshot
	p.metricSnapshot = snapshot
	p.opts.Metrics.AddDirectSessions(pkgmetrics.DirectSessionConnecting, float64(snapshot.Candidates-previous.Candidates))
	p.opts.Metrics.AddDirectSessions(pkgmetrics.DirectSessionActive, float64(snapshot.Active-previous.Active))
	p.opts.Metrics.AddDirectSessions(pkgmetrics.DirectSessionDraining, float64(snapshot.Draining-previous.Draining))
	p.metricMu.Unlock()
}

func directMetricReason(err error) pkgmetrics.DirectReasonCode {
	if err == nil {
		return pkgmetrics.DirectReasonNone
	}
	switch {
	case errors.Is(err, context.Canceled):
		return pkgmetrics.DirectReasonCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return pkgmetrics.DirectReasonTimeout
	case errors.Is(err, errDirectPoolClosed), errors.Is(err, errDirectPoolDraining), errors.Is(err, errSessionClosed):
		return pkgmetrics.DirectReasonClosed
	case IsDirectCapacityRejection(err):
		return pkgmetrics.DirectReasonCapacity
	}
	var failure interface {
		Stage() string
		ReasonCode() string
	}
	if errors.As(err, &failure) {
		switch failure.Stage() {
		case "credentials", "auth":
			return pkgmetrics.DirectReasonAuth
		case "protocol", "handshake", "result":
			return pkgmetrics.DirectReasonProtocol
		case "policy":
			return pkgmetrics.DirectReasonPolicy
		case "dial", "target", "source":
			return pkgmetrics.DirectReasonUnavailable
		}
	}
	return pkgmetrics.DirectReasonOther
}

func (p *DirectSessionPool) gcSlotLocked(target string, slot *directPoolSlot) {
	if slot.active == nil && slot.candidate == nil {
		delete(p.slots, target)
	}
}

func (p *DirectSessionPool) gcAllSlotsLocked() {
	for target, slot := range p.slots {
		if slot.active == nil && slot.candidate == nil {
			delete(p.slots, target)
		}
	}
}

func (p *DirectSessionPool) activeCountLocked() int {
	count := 0
	for _, slot := range p.slots {
		if slot.active != nil {
			count++
		}
	}
	return count
}

func (p *DirectSessionPool) candidateCountLocked() int {
	count := 0
	for _, slot := range p.slots {
		if slot.candidate != nil {
			count++
		}
	}
	return count
}

func (p *DirectSessionPool) occupiedSlotCountLocked() int {
	count := 0
	for _, slot := range p.slots {
		if slot.active != nil || slot.candidate != nil {
			count++
		}
	}
	return count
}

func (p *DirectSessionPool) oldestIdleActiveLocked() *directPoolSlot {
	var oldest *directPoolSlot
	var oldestIdleSince time.Time
	for _, slot := range p.slots {
		if slot.active == nil || slot.drainPending {
			continue
		}
		idleSince, idle := slot.active.idleSinceTime()
		if !idle {
			continue
		}
		if oldest == nil || idleSince.Before(oldestIdleSince) ||
			idleSince.Equal(oldestIdleSince) && slot.lastUsed.Before(oldest.lastUsed) {
			oldest = slot
			oldestIdleSince = idleSince
		}
	}
	return oldest
}

func (p *DirectSessionPool) oldestActiveLocked() *directPoolSlot {
	var oldest *directPoolSlot
	for _, slot := range p.slots {
		if slot.active == nil || slot.drainPending {
			continue
		}
		if oldest == nil || slot.lastUsed.Before(oldest.lastUsed) {
			oldest = slot
		}
	}
	return oldest
}

func (p *DirectSessionPool) desiredKey(target agentproxy.DirectSessionTarget) (directSessionKey, string, error) {
	if target.WebSocketURL == nil {
		return directSessionKey{}, "invalid://invalid" + DirectTunnelPath, &directOpenError{stage: "target", code: "invalid_url", endpoint: "invalid://invalid" + DirectTunnelPath}
	}
	endpoint := sanitizedDirectEndpoint(target.WebSocketURL)
	if !validDirectAgentID(target.TargetAgentID) {
		return directSessionKey{}, endpoint, &directOpenError{stage: "target", code: "invalid_target", endpoint: endpoint}
	}
	return directSessionKey{
		TargetAgentID: target.TargetAgentID,
		CanonicalURL:  directCanonicalURL(target),
		Proxy:         directProxyFingerprint(target.ProxyURL),
	}, endpoint, nil
}

func (p *DirectSessionPool) desiredFingerprint(endpoint string) (directDesiredFingerprint, agentauthcache.ForwardCredential, wire.Limits, error) {
	if p.opts.Credentials == nil {
		return directDesiredFingerprint{}, agentauthcache.ForwardCredential{}, wire.Limits{}, &directOpenError{stage: "credentials", code: "unavailable", endpoint: endpoint}
	}
	credential, err := p.opts.Credentials.CachedForwardCredential()
	if err != nil || credential.Ticket == "" {
		return directDesiredFingerprint{}, agentauthcache.ForwardCredential{}, wire.Limits{}, &directOpenError{stage: "credentials", code: "unavailable", endpoint: endpoint}
	}
	limits := normalizeDirectRuntimeSettings(DirectRuntimeSettings{Limits: p.limits()}).Limits
	return directDesiredFingerprint{
		Credential: sha256.Sum256([]byte(credential.Ticket)),
		Limits:     limitsFingerprint(limits),
	}, credential, limits, nil
}

func (p *DirectSessionPool) limits() wire.Limits {
	if settings := p.runtimeSettings.Load(); settings != nil {
		return settings.Limits
	}
	if p.opts.Limits == nil {
		return wire.Limits{}
	}
	return p.opts.Limits()
}

func (p *DirectSessionPool) maxSessions() int {
	if settings := p.runtimeSettings.Load(); settings != nil {
		return settings.MaxSessions
	}
	if p.opts.MaxSessions == nil {
		return 0
	}
	value := p.opts.MaxSessions()
	if value < 1 {
		value = 1
	}
	return value
}

func (p *DirectSessionPool) idleTimeout() time.Duration {
	if settings := p.runtimeSettings.Load(); settings != nil {
		return settings.IdleTimeout
	}
	if p.opts.IdleTimeout == nil {
		return 0
	}
	return p.opts.IdleTimeout()
}

func (p *DirectSessionPool) drainTimeout() time.Duration {
	if settings := p.runtimeSettings.Load(); settings != nil {
		return settings.DrainTimeout
	}
	if p.opts.DrainTimeout == nil {
		return defaultDirectDrainTimeout
	}
	if value := p.opts.DrainTimeout(); value > 0 {
		return value
	}
	return defaultDirectDrainTimeout
}

func (p *DirectSessionPool) currentRuntimeSettings() DirectRuntimeSettings {
	return normalizeDirectRuntimeSettings(DirectRuntimeSettings{
		Limits: p.limits(), MaxSessions: p.maxSessions(), IdleTimeout: p.idleTimeout(), DrainTimeout: p.drainTimeout(),
	})
}

func (p *DirectSessionPool) publishedSettingsLocked(fallback DirectRuntimeSettings) DirectRuntimeSettings {
	if published := p.runtimeSettings.Load(); published != nil {
		return *published
	}
	return fallback
}

func normalizeDirectRuntimeSettings(settings DirectRuntimeSettings) DirectRuntimeSettings {
	if normalized, err := wire.SelectDirectLimits(settings.Limits, settings.Limits); err == nil {
		settings.Limits = normalized
	} else {
		settings.Limits, _ = wire.SelectDirectLimits(wire.Limits{}, wire.Limits{})
	}
	if settings.MaxSessions < 1 {
		settings.MaxSessions = 1
	}
	if settings.IdleTimeout < 0 {
		settings.IdleTimeout = 0
	}
	if settings.DrainTimeout <= 0 {
		settings.DrainTimeout = defaultDirectDrainTimeout
	}
	return settings
}

func directCanonicalURL(target agentproxy.DirectSessionTarget) string {
	source := target.WebSocketURL
	canonical := &url.URL{Scheme: strings.ToLower(source.Scheme), Host: source.Host, Path: DirectTunnelPath}
	query := url.Values{}
	query.Set("target_agent_id", target.TargetAgentID)
	canonical.RawQuery = query.Encode()
	return canonical.String()
}

func directProxyFingerprint(proxy *url.URL) string {
	if proxy == nil {
		return ""
	}
	// behavior change: hash the complete effective proxy, including credentials
	// and query, while keeping that canonical preimage out of keys and logs.
	sum := sha256.Sum256([]byte(canonicalEffectiveProxyURL(proxy)))
	return hex.EncodeToString(sum[:])
}

func directTransportIdentity(
	key directSessionKey, fingerprint directDesiredFingerprint,
) agentproxy.DirectTransportIdentity {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(key.CanonicalURL))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(key.Proxy))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(fingerprint.Credential[:])
	var identity agentproxy.DirectTransportIdentity
	copy(identity[:], hasher.Sum(nil))
	return identity
}

func cloneDirectSessionTarget(target agentproxy.DirectSessionTarget) agentproxy.DirectSessionTarget {
	if target.WebSocketURL != nil {
		cloned := *target.WebSocketURL
		target.WebSocketURL = &cloned
	}
	if target.ProxyURL != nil {
		cloned := *target.ProxyURL
		target.ProxyURL = &cloned
	}
	return target
}

func canonicalEffectiveProxyURL(proxy *url.URL) string {
	scheme := strings.ToLower(proxy.Scheme)
	if scheme == "socks5h" {
		scheme = "socks5"
	}
	canonical := &url.URL{
		Scheme: scheme,
		Host:   canonicalProxyHost(proxy, scheme),
		User:   proxy.User,
		Path:   proxy.Path,
	}
	if query, err := url.ParseQuery(proxy.RawQuery); err == nil {
		canonical.RawQuery = query.Encode()
	} else {
		canonical.RawQuery = proxy.RawQuery
	}
	return canonical.String()
}

func canonicalProxyHost(proxy *url.URL, scheme string) string {
	hostname := strings.ToLower(proxy.Hostname())
	port := proxy.Port()
	if numericPort, err := strconv.ParseUint(port, 10, 16); err == nil {
		port = strconv.FormatUint(numericPort, 10)
	}
	if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(hostname, port)
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

func limitsFingerprint(limits wire.Limits) [32]byte {
	canonical := fmt.Sprintf("%d|%d|%d|%d|%d",
		limits.MaxMetadataBytes, limits.MaxDataBytes, limits.InitialStreamWindow,
		limits.MaxQueuedSessionBytes, limits.MaxConcurrentStreams)
	return sha256.Sum256([]byte(canonical))
}
