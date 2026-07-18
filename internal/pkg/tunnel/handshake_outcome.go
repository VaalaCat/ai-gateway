package tunnel

import "sync/atomic"

type HandshakeOutcome uint32

const (
	HandshakePending HandshakeOutcome = iota
	HandshakeSucceeded
	HandshakeTimedOut
	HandshakeCanceled
	HandshakeStopped
)

type HandshakeOutcomeOwner struct {
	outcome atomic.Uint32
}

func NewHandshakeOutcomeOwner() *HandshakeOutcomeOwner {
	return &HandshakeOutcomeOwner{}
}

func (o *HandshakeOutcomeOwner) TryOwn(outcome HandshakeOutcome) bool {
	if o == nil || outcome <= HandshakePending || outcome > HandshakeStopped {
		return false
	}
	return o.outcome.CompareAndSwap(uint32(HandshakePending), uint32(outcome))
}

func (o *HandshakeOutcomeOwner) Outcome() HandshakeOutcome {
	if o == nil {
		return HandshakePending
	}
	return HandshakeOutcome(o.outcome.Load())
}
