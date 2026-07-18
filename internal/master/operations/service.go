package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

var (
	ErrOperationInvalid     = errors.New("operation_invalid")
	ErrSnapshotEpochChanged = errors.New("snapshot_epoch_changed")
	ErrInvalidAgentResponse = errors.New("invalid_agent_response")
	ErrServiceClosed        = errors.New("agent operation service is closed")
	ErrAgentIdentityChanged = errors.New("agent_identity_changed")
)

type AgentFinder interface {
	FindAgent(ctx context.Context, agentID string) (models.Agent, error)
}

type ConnectionAuthorizer interface {
	Authorize(agent models.Agent, operation connectivity.Operation) (connectivity.OperationLease, error)
}

type ControlOperator interface {
	CallSessionContext(ctx context.Context, agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error)
}

type RelayOperator interface {
	Drain(agentID string, generation uint64) error
	Disconnect(agentID string, generation uint64, cause error) error
}

type ProbeOperator interface {
	EnqueueManualSession(ctx context.Context, sourceID string, expectedControlGeneration uint64, scope protocol.ProbeScope) (protocol.ProbeAck, error)
}

type Sources struct {
	Connections ConnectionAuthorizer
	Control     ControlOperator
	Relay       RelayOperator
	Probes      ProbeOperator
	Now         func() time.Time
}

type liveGeneration uint8

const (
	noLiveGeneration liveGeneration = iota
	controlLiveGeneration
	relayLiveGeneration
)

type operationHandler func(context.Context, protocol.OperationRequest, connectivity.OperationLease) (protocol.OperationAck, error)

type registeredOperation struct {
	handle         operationHandler
	liveGeneration liveGeneration
}

type Service struct {
	agents   AgentFinder
	sources  Sources
	handlers map[connectivity.Operation]registeredOperation
	nextID   atomic.Uint64

	lifecycleMu sync.Mutex
	rootCtx     context.Context
	rootCancel  context.CancelCauseFunc
	done        chan struct{}
	active      int
	closing     bool
	doneClosed  bool
}

func NewService(ctx context.Context, agents AgentFinder, sources Sources) *Service {
	if ctx == nil {
		panic("operations.NewService: nil context")
	}
	if sources.Now == nil {
		sources.Now = time.Now
	}
	rootCtx, rootCancel := context.WithCancelCause(ctx)
	service := &Service{agents: agents, sources: sources, rootCtx: rootCtx, rootCancel: rootCancel, done: make(chan struct{})}
	service.handlers = map[connectivity.Operation]registeredOperation{
		connectivity.OperationFullSync:           {handle: service.executeControl, liveGeneration: controlLiveGeneration},
		connectivity.OperationProbe:              {handle: service.executeDefaultProbe, liveGeneration: controlLiveGeneration},
		connectivity.OperationRelayReconnect:     {handle: service.executeControl, liveGeneration: controlLiveGeneration},
		connectivity.OperationRelayDrain:         {handle: service.executeRelayDrain, liveGeneration: relayLiveGeneration},
		connectivity.OperationRelayDisconnect:    {handle: service.executeRelayDisconnect, liveGeneration: relayLiveGeneration},
		connectivity.OperationDirectCircuitReset: {handle: service.executeControl, liveGeneration: controlLiveGeneration},
		connectivity.OperationInterrupt:          {handle: service.executeControl, liveGeneration: controlLiveGeneration},
	}
	return service
}

func (s *Service) Execute(ctx context.Context, req protocol.OperationRequest) (protocol.OperationAck, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return protocol.OperationAck{}, err
	}
	defer finish()
	operation := connectivity.Operation(req.Operation)
	handler, ok := s.handlers[operation]
	if !ok {
		return protocol.OperationAck{}, fmt.Errorf("%w: %q", ErrOperationInvalid, req.Operation)
	}
	req, live, err := s.prepareLive(ctx, req, operation)
	if err != nil {
		return protocol.OperationAck{}, err
	}
	return handler.handle(ctx, req, live)
}

func (s *Service) EnqueueProbe(ctx context.Context, req protocol.OperationRequest, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return protocol.ProbeAck{}, err
	}
	defer finish()
	if connectivity.Operation(req.Operation) != connectivity.OperationProbe {
		return protocol.ProbeAck{}, fmt.Errorf("%w: %q", ErrOperationInvalid, req.Operation)
	}
	req, lease, err := s.prepareLive(ctx, req, connectivity.OperationProbe)
	if err != nil {
		return protocol.ProbeAck{}, err
	}
	if s.sources.Probes == nil {
		return protocol.ProbeAck{}, errors.New("probe operator is not available")
	}
	return s.sources.Probes.EnqueueManualSession(ctx, req.AgentID, lease.ControlGeneration, scope)
}

func (s *Service) Authorize(ctx context.Context, req protocol.OperationRequest) error {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	operation := connectivity.Operation(req.Operation)
	if _, ok := s.handlers[operation]; !ok {
		return fmt.Errorf("%w: %q", ErrOperationInvalid, req.Operation)
	}
	_, _, err = s.prepare(ctx, req, operation)
	return err
}

func (s *Service) ForceFullSync(ctx context.Context, req protocol.OperationRequest) (protocol.ForceFullSyncResponse, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return protocol.ForceFullSyncResponse{}, err
	}
	defer finish()
	req.Operation = string(connectivity.OperationFullSync)
	req, lease, err := s.prepareLive(ctx, req, connectivity.OperationFullSync)
	if err != nil {
		return protocol.ForceFullSyncResponse{}, err
	}
	raw, err := s.callPreparedControl(ctx, req.AgentID, lease.ControlGeneration, consts.RPCSyncForceFullSync, nil, 30*time.Second)
	if err != nil {
		return protocol.ForceFullSyncResponse{}, err
	}
	var response protocol.ForceFullSyncResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return protocol.ForceFullSyncResponse{}, fmt.Errorf("%w: decode full sync response: %v", ErrInvalidAgentResponse, err)
	}
	return response, nil
}

func (s *Service) Interrupt(ctx context.Context, req protocol.OperationRequest) (bool, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	req.Operation = string(connectivity.OperationInterrupt)
	id, err := strconv.ParseInt(req.RequestID, 10, 64)
	if err != nil || id <= 0 {
		return false, fmt.Errorf("%w: request_id must be a positive integer", ErrOperationInvalid)
	}
	req, lease, err := s.prepareLive(ctx, req, connectivity.OperationInterrupt)
	if err != nil {
		return false, err
	}
	raw, err := s.callPreparedControl(ctx, req.AgentID, lease.ControlGeneration, consts.RPCAgentInterrupt, map[string]any{"id": id}, 10*time.Second)
	if err != nil {
		return false, err
	}
	var response struct {
		Interrupted bool `json:"interrupted"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, fmt.Errorf("%w: decode interrupt response: %v", ErrInvalidAgentResponse, err)
	}
	return response.Interrupted, nil
}

func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("agent operation service: nil context")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, nil, err
	}
	s.lifecycleMu.Lock()
	if s.closing || context.Cause(s.rootCtx) != nil {
		s.lifecycleMu.Unlock()
		return nil, nil, ErrServiceClosed
	}
	s.active++
	rootCtx := s.rootCtx
	s.lifecycleMu.Unlock()

	callCtx, cancel := context.WithCancelCause(ctx)
	stopRoot := context.AfterFunc(rootCtx, func() { cancel(context.Cause(rootCtx)) })
	finish := func() {
		stopRoot()
		cancel(context.Canceled)
		s.finishCall()
	}
	return callCtx, finish, nil
}

func (s *Service) finishCall() {
	s.lifecycleMu.Lock()
	s.active--
	closeDone := s.markDoneLocked()
	s.lifecycleMu.Unlock()
	if closeDone {
		close(s.done)
	}
}

func (s *Service) Cancel() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.closing = true
	closeDone := s.markDoneLocked()
	s.lifecycleMu.Unlock()
	s.rootCancel(ErrServiceClosed)
	if closeDone {
		close(s.done)
	}
}

func (s *Service) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("agent operation service: nil close context")
	}
	s.Cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Service) Done() <-chan struct{} {
	if s == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.done
}

func (s *Service) markDoneLocked() bool {
	if !s.closing || s.active != 0 || s.doneClosed {
		return false
	}
	s.doneClosed = true
	return true
}

func (s *Service) prepareLive(ctx context.Context, req protocol.OperationRequest, operation connectivity.Operation) (protocol.OperationRequest, connectivity.OperationLease, error) {
	agent, lease, err := s.prepare(ctx, req, operation)
	if err != nil {
		return protocol.OperationRequest{}, connectivity.OperationLease{}, err
	}
	req.AgentID = agent.AgentID
	req.Operation = string(operation)
	if err := context.Cause(ctx); err != nil {
		return protocol.OperationRequest{}, connectivity.OperationLease{}, err
	}
	liveAgent, err := s.agents.FindAgent(ctx, req.AgentID)
	if err != nil {
		return protocol.OperationRequest{}, connectivity.OperationLease{}, err
	}
	if liveAgent.AgentID == "" || liveAgent.AgentID != agent.AgentID {
		return protocol.OperationRequest{}, connectivity.OperationLease{}, fmt.Errorf(
			"%w: expected agent %q, got %q", ErrAgentIdentityChanged, agent.AgentID, liveAgent.AgentID,
		)
	}
	live, err := s.liveLease(ctx, liveAgent, req, operation, lease)
	if err != nil {
		return protocol.OperationRequest{}, connectivity.OperationLease{}, err
	}
	return req, live, nil
}

func (s *Service) prepare(ctx context.Context, req protocol.OperationRequest, operation connectivity.Operation) (models.Agent, connectivity.OperationLease, error) {
	if ctx == nil {
		return models.Agent{}, connectivity.OperationLease{}, errors.New("agent operation: nil context")
	}
	if err := context.Cause(ctx); err != nil {
		return models.Agent{}, connectivity.OperationLease{}, err
	}
	if s.agents == nil {
		return models.Agent{}, connectivity.OperationLease{}, errors.New("agent finder is not available")
	}
	agent, err := s.agents.FindAgent(ctx, req.AgentID)
	if err != nil {
		return models.Agent{}, connectivity.OperationLease{}, err
	}
	if s.sources.Connections == nil {
		return models.Agent{}, connectivity.OperationLease{}, errors.New("connection authorizer is not available")
	}
	lease, err := s.sources.Connections.Authorize(agent, operation)
	if err != nil {
		return models.Agent{}, connectivity.OperationLease{}, err
	}
	if err := validateExpectedLease(req, lease); err != nil {
		return models.Agent{}, connectivity.OperationLease{}, err
	}
	return agent, lease, nil
}

func (s *Service) liveLease(ctx context.Context, agent models.Agent, req protocol.OperationRequest, operation connectivity.Operation, previous connectivity.OperationLease) (connectivity.OperationLease, error) {
	if err := context.Cause(ctx); err != nil {
		return connectivity.OperationLease{}, err
	}
	live, err := s.sources.Connections.Authorize(agent, operation)
	if err != nil {
		return connectivity.OperationLease{}, err
	}
	if err := validateExpectedLease(req, live); err != nil {
		return connectivity.OperationLease{}, err
	}
	if previous.SnapshotEpoch != live.SnapshotEpoch {
		return connectivity.OperationLease{}, ErrSnapshotEpochChanged
	}
	registered := s.handlers[operation]
	if registered.liveGeneration == controlLiveGeneration && previous.ControlGeneration != live.ControlGeneration {
		return connectivity.OperationLease{}, connectivity.ErrConnectionGenerationChanged
	}
	if registered.liveGeneration == relayLiveGeneration && previous.RelayGeneration != live.RelayGeneration {
		return connectivity.OperationLease{}, connectivity.ErrConnectionGenerationChanged
	}
	return live, nil
}

func validateExpectedLease(req protocol.OperationRequest, lease connectivity.OperationLease) error {
	if req.ExpectedEpoch != "" && req.ExpectedEpoch != lease.SnapshotEpoch {
		return ErrSnapshotEpochChanged
	}
	if req.ExpectedControlGeneration != 0 && req.ExpectedControlGeneration != lease.ControlGeneration {
		return connectivity.ErrConnectionGenerationChanged
	}
	if req.ExpectedRelayGeneration != 0 && req.ExpectedRelayGeneration != lease.RelayGeneration {
		return connectivity.ErrConnectionGenerationChanged
	}
	return nil
}

func (s *Service) executeControl(ctx context.Context, req protocol.OperationRequest, lease connectivity.OperationLease) (protocol.OperationAck, error) {
	if _, err := s.callPreparedControl(ctx, req.AgentID, lease.ControlGeneration, consts.RPCAgentOperation, req, operationTimeout(connectivity.Operation(req.Operation))); err != nil {
		return protocol.OperationAck{}, err
	}
	return s.acceptedAck(), nil
}

func (s *Service) callPreparedControl(ctx context.Context, agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if s.sources.Control == nil {
		return nil, errors.New("control operator is not available")
	}
	return s.sources.Control.CallSessionContext(ctx, agentID, generation, method, params, timeout)
}

func (s *Service) executeDefaultProbe(ctx context.Context, req protocol.OperationRequest, lease connectivity.OperationLease) (protocol.OperationAck, error) {
	if s.sources.Probes == nil {
		return protocol.OperationAck{}, errors.New("probe operator is not available")
	}
	ack, err := s.sources.Probes.EnqueueManualSession(ctx, req.AgentID, lease.ControlGeneration, protocol.ProbeScope{Kind: "all_enabled"})
	if err != nil {
		return protocol.OperationAck{}, err
	}
	return protocol.OperationAck{OperationID: ack.ProbeID, State: ack.State, SnapshotSeq: ack.SnapshotSeq}, nil
}

func (s *Service) executeRelayDrain(_ context.Context, req protocol.OperationRequest, lease connectivity.OperationLease) (protocol.OperationAck, error) {
	if s.sources.Relay == nil {
		return protocol.OperationAck{}, errors.New("relay operator is not available")
	}
	if err := s.sources.Relay.Drain(req.AgentID, lease.RelayGeneration); err != nil {
		return protocol.OperationAck{}, err
	}
	return s.acceptedAck(), nil
}

func (s *Service) executeRelayDisconnect(_ context.Context, req protocol.OperationRequest, lease connectivity.OperationLease) (protocol.OperationAck, error) {
	if s.sources.Relay == nil {
		return protocol.OperationAck{}, errors.New("relay operator is not available")
	}
	if err := s.sources.Relay.Disconnect(req.AgentID, lease.RelayGeneration, errors.New("agent operation requested relay disconnect")); err != nil {
		return protocol.OperationAck{}, err
	}
	return s.acceptedAck(), nil
}

func (s *Service) acceptedAck() protocol.OperationAck {
	sequence := s.nextID.Add(1)
	return protocol.OperationAck{
		OperationID: fmt.Sprintf("operation-%d-%d", s.sources.Now().UnixNano(), sequence),
		State:       "accepted",
	}
}

func operationTimeout(operation connectivity.Operation) time.Duration {
	if operation == connectivity.OperationFullSync || operation == connectivity.OperationRelayReconnect {
		return 30 * time.Second
	}
	return 10 * time.Second
}
