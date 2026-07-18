package tunnel

import (
	"math/rand/v2"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

func (m *Manager) configured() bool {
	return m.desired.Mode != "disabled" && m.desired.EffectiveURI != ""
}

func (m *Manager) completeReconnects(session *Session) {
	remaining := m.reconnects[:0]
	for _, waiter := range m.reconnects {
		if waiter.ctx.Err() != nil {
			continue
		}
		if waiter.desiredGen == m.desiredGen && session != waiter.previous &&
			session.Generation() != waiter.previousGen {
			waiter.reply <- nil
			continue
		}
		remaining = append(remaining, waiter)
	}
	m.reconnects = remaining
}

func (m *Manager) failReconnects(err error) {
	for _, waiter := range m.reconnects {
		if waiter.ctx.Err() == nil {
			waiter.reply <- err
		}
	}
	m.reconnects = nil
}

func (m *Manager) nextBackoffLocked() time.Duration {
	base := m.backoff
	if base < m.opts.BackoffMin {
		base = m.opts.BackoffMin
	}
	jitterRange := base / 5
	delay := base
	if jitterRange > 0 {
		delay += time.Duration(rand.Int64N(int64(jitterRange) + 1))
	}
	if delay > m.opts.BackoffMax {
		delay = m.opts.BackoffMax
	}
	if base >= m.opts.BackoffMax/2 {
		m.backoff = m.opts.BackoffMax
	} else {
		m.backoff = base * 2
	}
	return delay
}

func (m *Manager) refreshSnapshot() {
	snapshot := Snapshot{
		Desired: m.desired, DesiredGeneration: m.desiredGen,
		LastError: m.lastError, Availability: "unavailable",
	}
	if !m.retryAt.IsZero() {
		snapshot.RetryAt = m.retryAt.Unix()
	}
	if m.active.session != nil {
		snapshot.ActiveURI = m.active.uri
		snapshot.ActiveGeneration = m.active.desiredGen
		snapshot.SessionGeneration = m.active.session.Generation()
		snapshot.Availability = "available"
		snapshot.AcceptingNewStreams = m.active.session.acceptsNew()
		snapshot.ConnectedAt = m.active.connected
		snapshot.Streams = m.active.session.StreamCount()
		snapshot.RecentErrors = m.active.session.RecentErrors()
	}
	if m.candidate.session != nil || m.candidate.attempt != 0 {
		snapshot.Candidates = 1
	}
	snapshot.Draining = len(m.draining)
	if (!m.configured() || m.generationDead) && m.active.session == nil && m.candidate.attempt == 0 {
		snapshot.Convergence = "converged"
	} else if m.active.session != nil && m.active.desiredGen == m.desiredGen &&
		m.active.uri == m.desired.EffectiveURI && !m.forceReplace {
		snapshot.Convergence = "converged"
	} else {
		snapshot.Convergence = "converging"
	}
	m.snapshotMu.Lock()
	m.snapshot = snapshot
	m.snapshotMu.Unlock()
}

func (m *Manager) finalize() {
	m.closing = true
	m.closeEnqueueGate()
	m.workerCancel(errManagerClosed)
	m.stopCandidate(errManagerClosed)
	if m.active.session != nil {
		m.active.session.Cancel(errManagerClosed)
	}
	for session := range m.draining {
		session.Cancel(errManagerClosed)
	}
	m.failReconnects(errManagerClosed)
	m.workers.Wait()
	m.active = managerSlot{}
	m.candidate = managerSlot{}
	m.activeRef.Store(nil)
	clear(m.draining)
	m.drainInputs()
	m.refreshSnapshot()
	m.doneOnce.Do(func() { close(m.done) })
}

func (m *Manager) drainInputs() {
	for {
		drained := true
		select {
		case <-m.events:
			drained = false
		default:
		}
		select {
		case <-m.commands:
			drained = false
		default:
		}
		select {
		case <-m.disconnects:
			drained = false
		default:
		}
		if drained {
			return
		}
	}
}

func sanitizeManagerError(err error, rawURI string) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if parsed, parseErr := wire.ParseRelayURI(rawURI); parseErr == nil {
		message = strings.ReplaceAll(message, rawURI, parsed.Sanitized)
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

func sessionGeneration(session *Session) uint64 {
	if session == nil {
		return 0
	}
	return session.Generation()
}

func sessionLimitsAllowed(granted, allowed wire.Limits) bool {
	if _, err := wire.NormalizeV1Limits(allowed); err != nil {
		return true
	}
	return granted.MaxMetadataBytes <= allowed.MaxMetadataBytes &&
		granted.MaxDataBytes <= allowed.MaxDataBytes &&
		granted.InitialStreamWindow <= allowed.InitialStreamWindow &&
		granted.MaxQueuedSessionBytes <= allowed.MaxQueuedSessionBytes &&
		granted.MaxConcurrentStreams <= allowed.MaxConcurrentStreams
}

var _ agentproxy.RelayLink = (*Manager)(nil)
