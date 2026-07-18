package tunnel

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

func (m *Manager) reconcile() {
	if !m.running || m.closing || m.generationDead || !m.configured() || !m.retryAt.IsZero() {
		return
	}
	if m.candidate.attempt != 0 {
		return
	}
	if !m.forceReplace && m.active.session != nil && m.active.desiredGen == m.desiredGen && m.active.uri == m.desired.EffectiveURI {
		return
	}
	m.attempt++
	attempt := m.attempt
	dialCtx, cancel := context.WithCancelCause(m.workerCtx)
	m.candidate = managerSlot{uri: m.desired.EffectiveURI, desiredGen: m.desiredGen, attempt: attempt, cancel: cancel}
	m.workers.Add(1)
	go m.dialCandidate(dialCtx, attempt, m.desiredGen, m.desired.EffectiveURI)
}

func (m *Manager) dialCandidate(ctx context.Context, attempt, generation uint64, uri string) {
	defer m.workers.Done()
	if m.opts.Dialer == nil || m.opts.Tickets == nil {
		m.deliverEvent(ctx, managerEvent{candidate: &candidateResult{
			attempt: attempt, gen: generation, uri: uri,
			err: errors.New("agent tunnel manager: missing dial dependencies"),
		}})
		return
	}
	ticket, err := m.opts.Tickets.RelayTicket(ctx, generation)
	if err != nil {
		m.deliverEvent(ctx, managerEvent{candidate: &candidateResult{attempt: attempt, gen: generation, uri: uri, err: err}})
		return
	}
	session, err := m.opts.Dialer.Dial(ctx, uri, ticket, generation)
	if err != nil || session == nil {
		if err == nil {
			err = errors.New("agent tunnel manager: dial returned nil session")
		}
		m.deliverEvent(ctx, managerEvent{candidate: &candidateResult{attempt: attempt, gen: generation, uri: uri, err: err}})
		return
	}
	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(ctx) }()
	select {
	case <-session.started:
		if initErr := session.initializationError(); initErr != nil {
			m.deliverEvent(ctx, managerEvent{candidate: &candidateResult{attempt: attempt, gen: generation, uri: uri, session: session, err: initErr}})
		} else {
			m.deliverEvent(ctx, managerEvent{candidate: &candidateResult{attempt: attempt, gen: generation, uri: uri, session: session}})
		}
	case <-ctx.Done():
		session.Cancel(context.Cause(ctx))
	}
	var runErr error
	select {
	case runErr = <-runDone:
	case <-ctx.Done():
		session.Cancel(context.Cause(ctx))
		runErr = <-runDone
	}
	m.deliverEvent(ctx, managerEvent{ended: &sessionEnd{
		session: session, desiredGen: generation, sessionGen: session.Generation(), err: runErr,
	}})
}

func (m *Manager) deliverEvent(ctx context.Context, event managerEvent) {
	if !m.beginEnqueue() {
		return
	}
	defer m.endEnqueue()
	select {
	case m.events <- event:
	case <-ctx.Done():
	case <-m.enqueueDone:
	}
}

func (m *Manager) handleCandidateResult(result candidateResult) {
	current := !m.closing && result.attempt == m.candidate.attempt && result.gen == m.candidate.desiredGen &&
		result.uri == m.candidate.uri && result.gen == m.desiredGen
	if !current {
		if result.session != nil {
			result.session.Cancel(errors.New("agent tunnel manager: stale candidate"))
		}
		return
	}
	m.candidate.session = result.session
	if result.err != nil {
		m.candidate = managerSlot{}
		m.lastError = sanitizeManagerError(result.err, result.uri)
		failureCode := candidateFailureCode(result.err)
		m.logRelayFailure("candidate", failureCode, result.err, failureCode == "relay_protocol" || failureCode == "relay_auth")
		m.retryAt = m.opts.Now().Add(m.nextBackoffLocked())
		if result.session != nil {
			result.session.Cancel(result.err)
		}
		m.failReconnects(errors.New(m.lastError))
		return
	}
	if !sessionLimitsAllowed(result.session.limits, m.opts.Limits) {
		m.candidate = managerSlot{}
		m.lastError = "agent tunnel manager: negotiated limits exceed configured limits"
		m.logRelayFailure("candidate", "relay_protocol", errors.New(m.lastError), true)
		m.retryAt = m.opts.Now().Add(m.nextBackoffLocked())
		result.session.Cancel(errors.New(m.lastError))
		m.failReconnects(errors.New(m.lastError))
		return
	}
	select {
	case <-result.session.Done():
		m.candidate = managerSlot{}
		m.lastError = sanitizeManagerError(result.session.cause(), result.uri)
		m.logRelayFailure("candidate", "session_closed", result.session.cause(), false)
		m.retryAt = m.opts.Now().Add(m.nextBackoffLocked())
		m.failReconnects(errors.New(m.lastError))
		return
	default:
	}
	if m.beforeCandidateActivation != nil {
		m.beforeCandidateActivation()
	}
	if !result.session.tryActivate() {
		m.candidate = managerSlot{}
		m.lastError = sanitizeManagerError(result.session.cause(), result.uri)
		m.logRelayFailure("candidate", "session_closed", result.session.cause(), false)
		m.retryAt = m.opts.Now().Add(m.nextBackoffLocked())
		m.failReconnects(errors.New(m.lastError))
		return
	}
	if m.candidate.session != result.session || m.candidate.attempt != result.attempt ||
		m.candidate.desiredGen != result.gen || m.closing {
		result.session.Cancel(errors.New("agent tunnel manager: candidate promotion guard failed"))
		return
	}
	old := m.active.session
	if old != nil {
		old.setAccepting(false)
	}
	m.active = managerSlot{
		session: result.session, uri: result.uri, desiredGen: result.gen,
		attempt: result.attempt, connected: m.opts.Now().Unix(),
	}
	m.activeRef.Store(result.session)
	m.candidate = managerSlot{}
	m.lastError = ""
	m.retryAt = time.Time{}
	m.backoff = m.opts.BackoffMin
	m.forceReplace = false
	m.recoverRelayFailure("candidate", "relay_not_ready")
	m.recoverRelayFailure("candidate", "relay_protocol")
	m.recoverRelayFailure("candidate", "relay_auth")
	m.recoverRelayFailure("candidate", "session_closed")
	m.logRelayState("relay session active", "session", zap.Uint64("session_generation", result.session.Generation()))
	m.refreshSnapshot()
	m.completeReconnects(result.session)
	if old != nil && old != result.session {
		m.startDrain(old, context.Background(), nil)
	}
}

func (m *Manager) handleSessionEnd(ended sessionEnd) {
	if generation, ok := m.draining[ended.session]; ok && generation == ended.sessionGen {
		delete(m.draining, ended.session)
	}
	if m.candidate.session == ended.session && m.candidate.desiredGen == ended.desiredGen {
		m.candidate = managerSlot{}
	}
	if m.active.session != ended.session || m.active.desiredGen != ended.desiredGen ||
		ended.session == nil || ended.session.Generation() != ended.sessionGen {
		return
	}
	m.active = managerSlot{}
	m.activeRef.CompareAndSwap(ended.session, nil)
	if !m.closing && m.configured() && m.desiredGen == ended.desiredGen {
		m.lastError = sanitizeManagerError(ended.err, m.desired.EffectiveURI)
		m.logRelayFailure("session", "session_closed", ended.err, true)
		m.retryAt = m.opts.Now().Add(m.nextBackoffLocked())
	}
}

func (m *Manager) stopCandidate(cause error) {
	if m.candidate.cancel != nil {
		m.candidate.cancel(cause)
	}
	if m.candidate.session != nil {
		m.candidate.session.Cancel(cause)
	}
	m.candidate = managerSlot{}
}
