package tunnel

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"go.uber.org/zap"
)

const (
	defaultManagerDrainTimeout = 30 * time.Second
	defaultManagerBackoffMin   = time.Second
	defaultManagerBackoffMax   = 30 * time.Second
)

var (
	errManagerClosed              = errors.New("agent tunnel manager: closed")
	errManagerNotRunning          = errors.New("agent tunnel manager: not running")
	errRelayNotAvailable          = errors.New("agent tunnel manager: relay unavailable")
	errRelayNotAccepting          = errors.New("agent tunnel manager: relay not accepting new streams")
	errManagerRunRepeated         = errors.New("agent tunnel manager: Run called more than once")
	errDesiredGenerationExhausted = errors.New("agent tunnel manager: desired generation exhausted")
	errDesiredChanged             = errors.New("agent tunnel manager: desired configuration changed")
)

type Desired struct {
	Mode          string
	ConfiguredURI string
	EffectiveURI  string
}

type TicketProvider interface {
	RelayTicket(ctx context.Context, desiredGeneration uint64) (agentauth.RelayTicket, error)
}

type Dialer interface {
	Dial(ctx context.Context, rawURI string, ticket agentauth.RelayTicket, desiredGeneration uint64) (*Session, error)
}

type ManagerOptions struct {
	SourceID     string
	Dialer       Dialer
	Tickets      TicketProvider
	Limits       wire.Limits
	DrainTimeout time.Duration
	BackoffMin   time.Duration
	BackoffMax   time.Duration
	Logger       *zap.Logger
	Now          func() time.Time
	Suppressor   *diagnostics.Suppressor
}

type Snapshot struct {
	Desired             Desired
	DesiredGeneration   uint64
	ActiveURI           string
	ActiveGeneration    uint64
	SessionGeneration   uint64
	Availability        string
	AcceptingNewStreams bool
	Convergence         string
	ConnectedAt         int64
	Streams             int
	Candidates          int
	Draining            int
	LastError           string
	RetryAt             int64
	RecentErrors        []diagnostics.Event
}

type managerSlot struct {
	session    *Session
	uri        string
	desiredGen uint64
	attempt    uint64
	connected  int64
	cancel     context.CancelCauseFunc
}

type Manager struct {
	opts ManagerOptions

	commands    chan any
	events      chan managerEvent
	disconnects chan error
	done        chan struct{}
	start       sync.Once
	doneOnce    sync.Once
	runOnce     atomic.Bool
	enqueueMu   sync.Mutex
	enqueueOpen bool
	enqueueDone chan struct{}
	enqueueOnce sync.Once
	enqueuers   sync.WaitGroup

	snapshotMu sync.RWMutex
	snapshot   Snapshot
	activeRef  atomic.Pointer[Session]

	// Supervisor exclusively owns these fields after it starts.
	desired        Desired
	desiredGen     uint64
	generationDead bool
	active         managerSlot
	candidate      managerSlot
	attempt        uint64
	draining       map[*Session]uint64
	lastError      string
	retryAt        time.Time
	backoff        time.Duration
	forceReplace   bool
	running        bool
	closing        bool
	reconnects     []reconnectWaiter

	workerCtx    context.Context
	workerCancel context.CancelCauseFunc
	workers      sync.WaitGroup

	beforeCandidateActivation func()
}

func NewManager(opts ManagerOptions) *Manager {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Suppressor == nil {
		opts.Suppressor = diagnostics.NewSuppressor(diagnostics.SuppressorOptions{})
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = defaultManagerDrainTimeout
	}
	if opts.BackoffMin <= 0 {
		opts.BackoffMin = defaultManagerBackoffMin
	}
	if opts.BackoffMax < opts.BackoffMin {
		opts.BackoffMax = defaultManagerBackoffMax
		if opts.BackoffMax < opts.BackoffMin {
			opts.BackoffMax = opts.BackoffMin
		}
	}
	if isNilInterface(opts.Dialer) {
		opts.Dialer = nil
	}
	if isNilInterface(opts.Tickets) {
		opts.Tickets = nil
	}
	if normalized, err := wire.NormalizeV1Limits(opts.Limits); err == nil {
		opts.Limits = normalized
	}
	workerCtx, workerCancel := context.WithCancelCause(context.Background())
	m := &Manager{
		opts: opts, commands: make(chan any, 64), events: make(chan managerEvent, 2048), disconnects: make(chan error, 1), done: make(chan struct{}),
		enqueueOpen: true, enqueueDone: make(chan struct{}),
		draining: make(map[*Session]uint64), backoff: opts.BackoffMin,
		workerCtx: workerCtx, workerCancel: workerCancel,
	}
	m.refreshSnapshot()
	return m
}

func (m *Manager) ensureSupervisor() { m.start.Do(func() { go m.supervise() }) }

func (m *Manager) Run(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	if !m.runOnce.CompareAndSwap(false, true) {
		return errManagerRunRepeated
	}
	m.ensureSupervisor()
	ack := make(chan error, 1)
	if !m.sendCommand(ctx, runCommand{reply: ack}) {
		return errManagerClosed
	}
	select {
	case err := <-ack:
		if err != nil {
			return err
		}
	case <-m.done:
		return errManagerClosed
	case <-ctx.Done():
		m.requestClose(context.Cause(ctx))
		<-m.done
		return context.Cause(ctx)
	}

	select {
	case <-ctx.Done():
		m.requestClose(context.Cause(ctx))
		<-m.done
		return context.Cause(ctx)
	case <-m.done:
		return errManagerClosed
	}
}

func (m *Manager) Apply(desired Desired) uint64 {
	desired.Mode = strings.TrimSpace(desired.Mode)
	desired.ConfiguredURI = wire.TrimRelayURIWhitespace(desired.ConfiguredURI)
	desired.EffectiveURI = wire.TrimRelayURIWhitespace(desired.EffectiveURI)
	m.ensureSupervisor()
	reply := make(chan uint64, 1)
	if !m.beginEnqueue() {
		return m.Snapshot().DesiredGeneration
	}
	select {
	case m.commands <- applyCommand{desired: desired, reply: reply}:
		m.endEnqueue()
	case <-m.enqueueDone:
		m.endEnqueue()
		return m.Snapshot().DesiredGeneration
	case <-m.done:
		m.endEnqueue()
		return m.Snapshot().DesiredGeneration
	}
	select {
	case generation := <-reply:
		return generation
	case <-m.done:
		return m.Snapshot().DesiredGeneration
	}
}

func (m *Manager) OpenStream(ctx context.Context, req agentproxy.RelayRequest) (agentproxy.RelayStream, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	session := m.activeRef.Load()
	if session == nil {
		return nil, errRelayNotAvailable
	}
	if !session.acquireAdmission() {
		return nil, errRelayNotAccepting
	}
	defer session.releaseAdmission()
	return session.OpenStream(ctx, req)
}

func (m *Manager) Snapshot() Snapshot {
	m.snapshotMu.RLock()
	snapshot := m.snapshot
	m.snapshotMu.RUnlock()
	if session := m.activeRef.Load(); session != nil && snapshot.SessionGeneration == session.Generation() {
		snapshot.AcceptingNewStreams = session.acceptsNew()
		snapshot.Streams = session.StreamCount()
		snapshot.RecentErrors = session.RecentErrors()
	} else {
		snapshot.RecentErrors = append([]diagnostics.Event(nil), snapshot.RecentErrors...)
	}
	return snapshot
}

func (m *Manager) Reconnect(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	m.ensureSupervisor()
	reply := make(chan error, 1)
	if !m.sendCommand(ctx, reconnectCommand{ctx: ctx, reply: reply}) {
		return errManagerClosed
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		ack := make(chan struct{}, 1)
		if !m.beginEnqueue() {
			return context.Cause(ctx)
		}
		select {
		case m.commands <- cancelReconnectCommand{reply: reply, cause: context.Cause(ctx), ack: ack}:
			m.endEnqueue()
			select {
			case <-ack:
			case <-m.done:
			}
		case <-m.enqueueDone:
			m.endEnqueue()
		case <-m.done:
			m.endEnqueue()
		}
		return context.Cause(ctx)
	case <-m.done:
		return errManagerClosed
	}
}

func (m *Manager) Drain(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	m.ensureSupervisor()
	reply := make(chan error, 1)
	if !m.sendCommand(ctx, drainCommand{ctx: ctx, reply: reply}) {
		return errManagerClosed
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-m.done:
		return errManagerClosed
	}
}

func (m *Manager) Disconnect(cause error) {
	if cause == nil {
		cause = errSessionClosed
	}
	m.ensureSupervisor()
	if !m.beginEnqueue() {
		return
	}
	defer m.endEnqueue()
	select {
	case m.disconnects <- cause:
	case <-m.enqueueDone:
	case <-m.done:
	default:
	}
}

func (m *Manager) Close(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	m.ensureSupervisor()
	m.requestClose(errManagerClosed)
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (m *Manager) Done() <-chan struct{} { return m.done }

func (m *Manager) sendCommand(ctx context.Context, command any) bool {
	if !m.beginEnqueue() {
		return false
	}
	defer m.endEnqueue()
	select {
	case m.commands <- command:
		return true
	case <-ctx.Done():
		return false
	case <-m.enqueueDone:
		return false
	case <-m.done:
		return false
	}
}

func (m *Manager) requestClose(cause error) {
	if !m.beginEnqueue() {
		return
	}
	defer m.endEnqueue()
	select {
	case m.commands <- closeCommand{cause: cause}:
	case <-m.enqueueDone:
	case <-m.done:
	}
}

func (m *Manager) beginEnqueue() bool {
	m.enqueueMu.Lock()
	defer m.enqueueMu.Unlock()
	if !m.enqueueOpen {
		return false
	}
	m.enqueuers.Add(1)
	return true
}

func (m *Manager) endEnqueue() { m.enqueuers.Done() }

func (m *Manager) closeEnqueueGate() {
	m.enqueueOnce.Do(func() {
		m.enqueueMu.Lock()
		m.enqueueOpen = false
		close(m.enqueueDone)
		m.enqueueMu.Unlock()
	})
	m.enqueuers.Wait()
}

func (m *Manager) supervise() {
	var retryTimer *time.Timer
	var retryC <-chan time.Time
	for !m.closing {
		select {
		case raw := <-m.commands:
			m.handleCommand(raw)
		case event := <-m.events:
			m.handleEvent(event)
		case cause := <-m.disconnects:
			m.handleCommand(disconnectCommand{cause: cause})
		case <-retryC:
			m.retryAt = time.Time{}
			retryC = nil
			m.reconcile()
		}
		if m.closing {
			break
		}
		if m.retryAt.IsZero() {
			if retryTimer != nil && !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
			retryC = nil
		} else {
			delay := m.retryAt.Sub(m.opts.Now())
			if delay < 0 {
				delay = 0
			}
			if retryTimer == nil {
				retryTimer = time.NewTimer(delay)
			} else {
				if !retryTimer.Stop() {
					select {
					case <-retryTimer.C:
					default:
					}
				}
				retryTimer.Reset(delay)
			}
			retryC = retryTimer.C
		}
	}
	if retryTimer != nil {
		retryTimer.Stop()
	}
	m.finalize()
}
