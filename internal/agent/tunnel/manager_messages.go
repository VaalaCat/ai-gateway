package tunnel

import (
	"context"
	"errors"
	"math"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"go.uber.org/zap"
)

type candidateResult struct {
	attempt uint64
	gen     uint64
	uri     string
	session *Session
	err     error
}

type sessionEnd struct {
	session    *Session
	desiredGen uint64
	sessionGen uint64
	err        error
}

type managerEvent struct {
	candidate *candidateResult
	ended     *sessionEnd
	drained   *sessionEnd
	handled   chan struct{}
}

type applyCommand struct {
	desired Desired
	reply   chan uint64
}

type runCommand struct{ reply chan error }

type reconnectCommand struct {
	ctx   context.Context
	reply chan error
}

type cancelReconnectCommand struct {
	reply chan error
	cause error
	ack   chan struct{}
}

type drainCommand struct {
	ctx   context.Context
	reply chan error
}

type disconnectCommand struct{ cause error }
type closeCommand struct{ cause error }

type reconnectWaiter struct {
	ctx         context.Context
	reply       chan error
	previous    *Session
	previousGen uint64
	desiredGen  uint64
}

type managerRuntimeSettingsProvider interface {
	managerRuntimeSettings() (wire.Limits, time.Duration)
}

func (m *Manager) handleCommand(raw any) {
	switch command := raw.(type) {
	case runCommand:
		if m.closing {
			command.reply <- errManagerClosed
			return
		}
		m.running = true
		command.reply <- nil
		m.reconcile()
	case applyCommand:
		m.handleApply(command)
	case reconnectCommand:
		m.handleReconnect(command)
	case cancelReconnectCommand:
		remaining := m.reconnects[:0]
		for _, waiter := range m.reconnects {
			if waiter.reply != command.reply {
				remaining = append(remaining, waiter)
			}
		}
		m.reconnects = remaining
		if len(m.reconnects) == 0 && m.forceReplace {
			m.forceReplace = false
			m.stopCandidate(command.cause)
		}
		command.ack <- struct{}{}
	case drainCommand:
		m.handleDrain(command)
	case disconnectCommand:
		if m.candidate.cancel != nil {
			m.candidate.cancel(command.cause)
		}
		if m.candidate.session != nil {
			m.candidate.session.Cancel(command.cause)
		}
		if m.active.session != nil {
			m.active.session.Cancel(command.cause)
		}
	case closeCommand:
		m.closing = true
		m.closeEnqueueGate()
		m.workerCancel(command.cause)
	}
	m.refreshSnapshot()
}

func (m *Manager) handleApply(command applyCommand) {
	m.refreshRuntimeSettings()
	if m.desiredGen == math.MaxUint64 {
		m.generationDead = true
		m.lastError = errDesiredGenerationExhausted.Error()
		m.retryAt = time.Time{}
		m.stopCandidate(errDesiredGenerationExhausted)
		m.detachActiveForDrain(context.Background(), nil)
		m.refreshSnapshot()
		command.reply <- m.desiredGen
		return
	}
	m.desiredGen++
	m.desired = command.desired
	m.generationDead = false
	m.lastError = ""
	m.retryAt = time.Time{}
	m.backoff = m.opts.BackoffMin
	m.forceReplace = false
	m.logRelayState("relay config changed", "config",
		zap.String("mode", m.desired.Mode), zap.Uint64("desired_generation", m.desiredGen))
	m.stopCandidate(errDesiredChanged)
	m.failReconnects(errDesiredChanged)
	if !m.configured() {
		m.detachActiveForDrain(context.Background(), nil)
	}
	m.refreshSnapshot()
	command.reply <- m.desiredGen
	m.reconcile()
}

func (m *Manager) refreshRuntimeSettings() {
	provider, ok := m.opts.Dialer.(managerRuntimeSettingsProvider)
	if !ok {
		return
	}
	limits, drainTimeout := provider.managerRuntimeSettings()
	if normalized, err := wire.NormalizeV1Limits(limits); err == nil {
		m.opts.Limits = normalized
	}
	if drainTimeout > 0 {
		m.opts.DrainTimeout = drainTimeout
	}
}

func (m *Manager) handleReconnect(command reconnectCommand) {
	if !m.running {
		command.reply <- errManagerNotRunning
		return
	}
	if !m.configured() || m.generationDead {
		command.reply <- errRelayNotAvailable
		return
	}
	m.reconnects = append(m.reconnects, reconnectWaiter{
		ctx: command.ctx, reply: command.reply, previous: m.active.session,
		previousGen: sessionGeneration(m.active.session), desiredGen: m.desiredGen,
	})
	m.forceReplace = true
	m.retryAt = time.Time{}
	m.lastError = ""
	m.stopCandidate(errors.New("agent tunnel manager: reconnect requested"))
	m.reconcile()
}

func (m *Manager) handleDrain(command drainCommand) {
	if m.active.session == nil {
		command.reply <- nil
		return
	}
	m.detachActiveForDrain(command.ctx, command.reply)
}

func (m *Manager) handleEvent(event managerEvent) {
	if event.handled != nil {
		defer close(event.handled)
	}
	if event.candidate != nil {
		m.handleCandidateResult(*event.candidate)
	}
	if event.ended != nil {
		m.handleSessionEnd(*event.ended)
	}
	if event.drained != nil {
		if generation, ok := m.draining[event.drained.session]; ok && generation == event.drained.sessionGen {
			delete(m.draining, event.drained.session)
		}
	}
	m.refreshSnapshot()
}
