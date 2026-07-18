package tunnel

import (
	"context"
	"errors"
	"time"
)

func (m *Manager) detachActiveForDrain(ctx context.Context, reply chan error) {
	session := m.active.session
	if session == nil {
		if reply != nil {
			reply <- nil
		}
		return
	}
	session.setAccepting(false)
	m.active = managerSlot{}
	m.activeRef.CompareAndSwap(session, nil)
	m.startDrain(session, ctx, reply)
}

func (m *Manager) startDrain(session *Session, waiterCtx context.Context, reply chan error) {
	if session == nil {
		if reply != nil {
			reply <- nil
		}
		return
	}
	if _, exists := m.draining[session]; exists {
		if reply != nil {
			reply <- nil
		}
		return
	}
	m.draining[session] = session.Generation()
	m.workers.Add(1)
	drainTimeout := m.opts.DrainTimeout
	go m.drainSession(session, waiterCtx, reply, drainTimeout)
}

func (m *Manager) drainSession(session *Session, waiterCtx context.Context, reply chan error, drainTimeout time.Duration) {
	defer m.workers.Done()
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	activity := session.Activity()
	replied := false
	finishReply := func(err error) {
		if reply != nil && !replied {
			replied = true
			reply <- err
		}
	}
	for {
		if session.idle() {
			session.Cancel(errSessionClosed)
		}
		select {
		case <-session.Done():
			finishReply(nil)
			m.deliverEvent(m.workerCtx, managerEvent{drained: &sessionEnd{session: session, sessionGen: session.Generation()}})
			return
		case <-timer.C:
			session.Cancel(errors.New("agent tunnel manager: drain timeout"))
		case <-activity:
		case <-waiterCtx.Done():
			finishReply(context.Cause(waiterCtx))
			waiterCtx = context.Background()
		case <-m.workerCtx.Done():
			session.Cancel(context.Cause(m.workerCtx))
		}
	}
}
