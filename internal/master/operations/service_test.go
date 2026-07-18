package operations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type operationAgentFinder struct {
	agents map[string]models.Agent
	err    error
}

type operationAgentResult struct {
	agent models.Agent
	err   error
}

type sequenceOperationAgentFinder struct {
	results  []operationAgentResult
	calls    []string
	contexts []context.Context
}

func (f *sequenceOperationAgentFinder) FindAgent(ctx context.Context, agentID string) (models.Agent, error) {
	f.calls = append(f.calls, agentID)
	f.contexts = append(f.contexts, ctx)
	if len(f.results) == 0 {
		return models.Agent{}, errors.New("unexpected third agent lookup")
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result.agent, result.err
}

func (f operationAgentFinder) FindAgent(_ context.Context, agentID string) (models.Agent, error) {
	if f.err != nil {
		return models.Agent{}, f.err
	}
	agent, ok := f.agents[agentID]
	if !ok {
		return models.Agent{}, gorm.ErrRecordNotFound
	}
	return agent, nil
}

type operationAuthorizer struct {
	leases         []connectivity.OperationLease
	err            error
	calls          []connectivity.Operation
	agents         []models.Agent
	rejectDisabled bool
}

func (a *operationAuthorizer) Authorize(agent models.Agent, operation connectivity.Operation) (connectivity.OperationLease, error) {
	a.calls = append(a.calls, operation)
	a.agents = append(a.agents, agent)
	if a.rejectDisabled && agent.Status != consts.StatusEnabled {
		return connectivity.OperationLease{}, &connectivity.OperationDeniedError{
			Operation: operation, DenialCode: connectivity.DenialAgentDisabled,
		}
	}
	if a.err != nil {
		return connectivity.OperationLease{}, a.err
	}
	if len(a.leases) == 0 {
		return connectivity.OperationLease{}, errors.New("missing lease")
	}
	lease := a.leases[0]
	if len(a.leases) > 1 {
		a.leases = a.leases[1:]
	}
	return lease, nil
}

type controlCall struct {
	ctx        context.Context
	agentID    string
	generation uint64
	method     string
	params     any
}

type operationControl struct {
	calls []controlCall
	raw   json.RawMessage
	err   error
}

func (c *operationControl) CallSessionContext(ctx context.Context, agentID string, generation uint64, method string, params any, _ time.Duration) (json.RawMessage, error) {
	c.calls = append(c.calls, controlCall{ctx: ctx, agentID: agentID, generation: generation, method: method, params: params})
	return c.raw, c.err
}

type blockingOperationControl struct {
	started chan struct{}
	done    chan struct{}
}

func (c *blockingOperationControl) CallSessionContext(ctx context.Context, _ string, _ uint64, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
	close(c.started)
	<-ctx.Done()
	close(c.done)
	return nil, context.Cause(ctx)
}

type relayCall struct {
	agentID    string
	generation uint64
	kind       string
}

type operationRelay struct{ calls []relayCall }

func (r *operationRelay) Drain(agentID string, generation uint64) error {
	r.calls = append(r.calls, relayCall{agentID: agentID, generation: generation, kind: "drain"})
	return nil
}

func (r *operationRelay) Disconnect(agentID string, generation uint64, _ error) error {
	r.calls = append(r.calls, relayCall{agentID: agentID, generation: generation, kind: "disconnect"})
	return nil
}

type operationProbes struct {
	calls       []protocol.ProbeScope
	generations []uint64
	ack         protocol.ProbeAck
	err         error
}

func (p *operationProbes) EnqueueManualSession(_ context.Context, _ string, expectedControlGeneration uint64, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	p.calls = append(p.calls, scope)
	p.generations = append(p.generations, expectedControlGeneration)
	return p.ack, p.err
}

func TestExecuteDispatchesEveryOperationThroughLiveGeneration(t *testing.T) {
	operations := []struct {
		operation connectivity.Operation
		control   bool
		relayKind string
	}{
		{operation: connectivity.OperationFullSync, control: true},
		{operation: connectivity.OperationProbe},
		{operation: connectivity.OperationRelayReconnect, control: true},
		{operation: connectivity.OperationRelayDrain, relayKind: "drain"},
		{operation: connectivity.OperationRelayDisconnect, relayKind: "disconnect"},
		{operation: connectivity.OperationDirectCircuitReset, control: true},
		{operation: connectivity.OperationInterrupt, control: true},
	}

	for _, test := range operations {
		t.Run(string(test.operation), func(t *testing.T) {
			lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}
			authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease, lease}}
			control := &operationControl{}
			relay := &operationRelay{}
			probes := &operationProbes{ack: protocol.ProbeAck{ProbeID: "probe-1", State: "queued", SnapshotSeq: 23}}
			service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{
				"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled},
			}}, Sources{Connections: authorizer, Control: control, Relay: relay, Probes: probes, Now: func() time.Time { return time.Unix(0, 99) }})

			req := protocol.OperationRequest{
				AgentID: "agent-a", Operation: string(test.operation), TargetAgentID: "agent-b", RequestID: "41",
				ExpectedEpoch: "master-a", ExpectedControlGeneration: 7, ExpectedRelayGeneration: 11,
			}
			ack, err := service.Execute(t.Context(), req)
			require.NoError(t, err)
			if test.operation == connectivity.OperationProbe {
				require.Equal(t, "queued", ack.State)
			} else {
				require.Equal(t, "accepted", ack.State)
			}
			require.NotEmpty(t, ack.OperationID)
			require.Equal(t, []connectivity.Operation{test.operation, test.operation}, authorizer.calls)

			if test.operation == connectivity.OperationProbe {
				require.Equal(t, "probe-1", ack.OperationID)
				require.Equal(t, uint64(23), ack.SnapshotSeq)
				require.Len(t, probes.calls, 1)
				require.Equal(t, []uint64{7}, probes.generations)
				return
			}
			if test.control {
				require.Len(t, control.calls, 1)
				require.Equal(t, uint64(7), control.calls[0].generation)
				require.Equal(t, consts.RPCAgentOperation, control.calls[0].method)
				require.Equal(t, req, control.calls[0].params)
			}
			if test.relayKind != "" {
				require.Equal(t, []relayCall{{agentID: "agent-a", generation: 11, kind: test.relayKind}}, relay.calls)
			}
		})
	}
}

func TestExecuteRejectsStaleExpectedLease(t *testing.T) {
	tests := []struct {
		name string
		req  protocol.OperationRequest
		want error
	}{
		{name: "epoch", req: protocol.OperationRequest{ExpectedEpoch: "master-old", ExpectedControlGeneration: 7}, want: ErrSnapshotEpochChanged},
		{name: "control generation", req: protocol.OperationRequest{ExpectedEpoch: "master-a", ExpectedControlGeneration: 6}, want: connectivity.ErrConnectionGenerationChanged},
		{name: "relay generation", req: protocol.OperationRequest{ExpectedEpoch: "master-a", ExpectedControlGeneration: 7, ExpectedRelayGeneration: 10}, want: connectivity.ErrConnectionGenerationChanged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}}}
			control := &operationControl{}
			service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}, Sources{
				Connections: authorizer, Control: control,
			})
			test.req.AgentID = "agent-a"
			test.req.Operation = string(connectivity.OperationFullSync)
			_, err := service.Execute(t.Context(), test.req)
			require.ErrorIs(t, err, test.want)
			require.Empty(t, control.calls)
		})
	}
}

func TestExecuteRejectsReplacementSessionImmediatelyBeforeMutation(t *testing.T) {
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{
		{SnapshotEpoch: "master-a", ControlGeneration: 7},
		{SnapshotEpoch: "master-a", ControlGeneration: 8},
	}}
	control := &operationControl{}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}, Sources{
		Connections: authorizer, Control: control,
	})

	_, err := service.Execute(t.Context(), protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(connectivity.OperationInterrupt), ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
	})
	require.ErrorIs(t, err, connectivity.ErrConnectionGenerationChanged)
	require.Empty(t, control.calls)
}

func TestEnqueueProbeRejectsReplacementControlBeforeScheduler(t *testing.T) {
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{
		{SnapshotEpoch: "master-a", ControlGeneration: 7},
		{SnapshotEpoch: "master-a", ControlGeneration: 8},
	}}
	probes := &operationProbes{}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {
		AgentID: "agent-a", Status: consts.StatusEnabled,
	}}}, Sources{Connections: authorizer, Probes: probes})

	_, err := service.EnqueueProbe(t.Context(), protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(connectivity.OperationProbe), ExpectedEpoch: "master-a",
	}, protocol.ProbeScope{Kind: "all_enabled"})
	require.ErrorIs(t, err, connectivity.ErrConnectionGenerationChanged)
	require.Empty(t, probes.calls)
}

func TestExecuteRejectsUnknownOperationBeforeAgentLookup(t *testing.T) {
	service := NewService(t.Context(), operationAgentFinder{err: errors.New("must not query")}, Sources{})
	_, err := service.Execute(t.Context(), protocol.OperationRequest{AgentID: "agent-a", Operation: "unknown"})
	require.ErrorIs(t, err, ErrOperationInvalid)
}

func TestExecutePropagatesCancellationWithoutDispatch(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("request canceled"))
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{{SnapshotEpoch: "master-a", ControlGeneration: 7}}}
	control := &operationControl{}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}, Sources{
		Connections: authorizer, Control: control,
	})

	_, err := service.Execute(ctx, protocol.OperationRequest{AgentID: "agent-a", Operation: string(connectivity.OperationFullSync), ExpectedEpoch: "master-a"})
	require.ErrorContains(t, err, "request canceled")
	require.Empty(t, authorizer.calls)
	require.Empty(t, control.calls)
}

func TestEnqueueProbeReturnsFullProbeAck(t *testing.T) {
	lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7}
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease, lease}}
	probes := &operationProbes{ack: protocol.ProbeAck{
		ProbeID: "probe-9", ProbeGeneration: 4, Scope: protocol.ProbeScope{Kind: "tag", Tag: "wan"},
		State: "queued", TargetTotal: 3, SnapshotSeq: 31,
	}}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}, Sources{
		Connections: authorizer, Probes: probes,
	})
	scope := protocol.ProbeScope{Kind: "tag", Tag: "wan"}
	ack, err := service.EnqueueProbe(t.Context(), protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(connectivity.OperationProbe), ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
	}, scope)
	require.NoError(t, err)
	require.Equal(t, probes.ack, ack)
	require.Equal(t, []protocol.ProbeScope{scope}, probes.calls)
	require.Equal(t, []uint64{7}, probes.generations)
}

func TestEnqueueProbeReplacementAfterLiveAuthorizeStaysBoundToLease(t *testing.T) {
	lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7}
	probes := &operationProbes{err: connectivity.ErrConnectionGenerationChanged}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {
		AgentID: "agent-a", Status: consts.StatusEnabled,
	}}}, Sources{
		Connections: &operationAuthorizer{leases: []connectivity.OperationLease{lease, lease}},
		Probes:      probes,
	})

	_, err := service.EnqueueProbe(t.Context(), protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(connectivity.OperationProbe), ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
	}, protocol.ProbeScope{Kind: "all_enabled"})
	require.ErrorIs(t, err, connectivity.ErrConnectionGenerationChanged)
	require.Equal(t, []uint64{7}, probes.generations)
}

func TestForceFullSyncUsesSharedLiveGenerationGuardAndTypedResponse(t *testing.T) {
	lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7}
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease, lease}}
	control := &operationControl{raw: json.RawMessage(`{"version":19,"duration_ms":31}`)}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {
		AgentID: "agent-a", Status: consts.StatusEnabled,
	}}}, Sources{Connections: authorizer, Control: control})

	response, err := service.ForceFullSync(t.Context(), protocol.OperationRequest{AgentID: "agent-a"})
	require.NoError(t, err)
	require.Equal(t, protocol.ForceFullSyncResponse{Version: 19, DurationMs: 31}, response)
	require.Equal(t, []connectivity.Operation{connectivity.OperationFullSync, connectivity.OperationFullSync}, authorizer.calls)
	require.Equal(t, consts.RPCSyncForceFullSync, control.calls[0].method)
	require.Nil(t, control.calls[0].params)
}

func TestInterruptUsesSharedLiveGenerationGuardAndTypedResponse(t *testing.T) {
	lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7}
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease, lease}}
	control := &operationControl{raw: json.RawMessage(`{"interrupted":true}`)}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {
		AgentID: "agent-a", Status: consts.StatusEnabled,
	}}}, Sources{Connections: authorizer, Control: control})

	interrupted, err := service.Interrupt(t.Context(), protocol.OperationRequest{AgentID: "agent-a", RequestID: "41"})
	require.NoError(t, err)
	require.True(t, interrupted)
	require.Equal(t, []connectivity.Operation{connectivity.OperationInterrupt, connectivity.OperationInterrupt}, authorizer.calls)
	require.Equal(t, consts.RPCAgentInterrupt, control.calls[0].method)
	require.Equal(t, map[string]any{"id": int64(41)}, control.calls[0].params)
}

func TestAuthorizeChecksAgentAndInitialLeaseWithoutDispatch(t *testing.T) {
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{{SnapshotEpoch: "master-a", ControlGeneration: 7}}}
	control := &operationControl{}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {
		AgentID: "agent-a", Status: consts.StatusEnabled,
	}}}, Sources{Connections: authorizer, Control: control})

	err := service.Authorize(t.Context(), protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(connectivity.OperationFullSync), ExpectedEpoch: "master-a",
	})
	require.NoError(t, err)
	require.Equal(t, []connectivity.Operation{connectivity.OperationFullSync}, authorizer.calls)
	require.Empty(t, control.calls)
}

func TestServiceCancelIsIdempotentAndCloseJoinsActiveOperation(t *testing.T) {
	lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7}
	control := &blockingOperationControl{started: make(chan struct{}), done: make(chan struct{})}
	service := NewService(t.Context(), operationAgentFinder{agents: map[string]models.Agent{"agent-a": {
		AgentID: "agent-a", Status: consts.StatusEnabled,
	}}}, Sources{Connections: &operationAuthorizer{leases: []connectivity.OperationLease{lease, lease}}, Control: control})
	operationDone := make(chan error, 1)
	go func() {
		_, err := service.Execute(t.Context(), protocol.OperationRequest{
			AgentID: "agent-a", Operation: string(connectivity.OperationFullSync), ExpectedEpoch: "master-a",
		})
		operationDone <- err
	}()
	<-control.started

	service.Cancel()
	service.Cancel()
	select {
	case <-control.done:
	case <-time.After(time.Second):
		t.Fatal("active operation did not receive service cancellation")
	}
	require.Error(t, <-operationDone)
	require.NoError(t, service.Close(t.Context()))
	select {
	case <-service.Done():
	default:
		t.Fatal("service Done remained open after Close")
	}
}

func TestServiceRejectsOperationAfterCancel(t *testing.T) {
	service := NewService(t.Context(), operationAgentFinder{}, Sources{})
	service.Cancel()
	_, err := service.Execute(t.Context(), protocol.OperationRequest{Operation: string(connectivity.OperationFullSync)})
	require.ErrorIs(t, err, ErrServiceClosed)
	require.NoError(t, service.Close(t.Context()))
}

func operationCases() []connectivity.Operation {
	return []connectivity.Operation{
		connectivity.OperationFullSync,
		connectivity.OperationProbe,
		connectivity.OperationRelayReconnect,
		connectivity.OperationRelayDrain,
		connectivity.OperationRelayDisconnect,
		connectivity.OperationDirectCircuitReset,
		connectivity.OperationInterrupt,
	}
}

func executeOperationForLiveAgentTest(t *testing.T, finder AgentFinder, authorizer *operationAuthorizer, operation connectivity.Operation) (*operationControl, *operationRelay, *operationProbes, error) {
	t.Helper()
	control := &operationControl{}
	relay := &operationRelay{}
	probes := &operationProbes{ack: protocol.ProbeAck{ProbeID: "probe-1", State: "queued"}}
	service := NewService(t.Context(), finder, Sources{
		Connections: authorizer, Control: control, Relay: relay, Probes: probes,
	})
	_, err := service.Execute(t.Context(), protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(operation), TargetAgentID: "agent-b", RequestID: "41",
		ExpectedEpoch: "master-a", ExpectedControlGeneration: 7, ExpectedRelayGeneration: 11,
	})
	return control, relay, probes, err
}

func requireNoOperationMutation(t *testing.T, control *operationControl, relay *operationRelay, probes *operationProbes) {
	t.Helper()
	require.Empty(t, control.calls)
	require.Empty(t, relay.calls)
	require.Empty(t, probes.calls)
}

func TestExecuteRechecksDisabledAgentBeforeEveryMutation(t *testing.T) {
	for _, operation := range operationCases() {
		t.Run(string(operation), func(t *testing.T) {
			finder := &sequenceOperationAgentFinder{results: []operationAgentResult{
				{agent: models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}},
				{agent: models.Agent{AgentID: "agent-a", Status: consts.StatusDisabled}},
			}}
			lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}
			authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease}, rejectDisabled: true}

			control, relay, probes, err := executeOperationForLiveAgentTest(t, finder, authorizer, operation)
			var denied *connectivity.OperationDeniedError
			require.ErrorAs(t, err, &denied)
			require.Equal(t, connectivity.DenialAgentDisabled, denied.DenialCode)
			require.Equal(t, []string{"agent-a", "agent-a"}, finder.calls)
			require.Len(t, finder.contexts, 2)
			require.Equal(t, finder.contexts[0], finder.contexts[1])
			require.Equal(t, []int{consts.StatusEnabled, consts.StatusDisabled}, []int{
				authorizer.agents[0].Status, authorizer.agents[1].Status,
			})
			requireNoOperationMutation(t, control, relay, probes)
		})
	}
}

func TestExecutePropagatesSecondAgentLookupErrorWithoutMutation(t *testing.T) {
	lookupErr := errors.New("live agent lookup failed")
	for _, operation := range operationCases() {
		t.Run(string(operation), func(t *testing.T) {
			finder := &sequenceOperationAgentFinder{results: []operationAgentResult{
				{agent: models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}},
				{err: lookupErr},
			}}
			lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}
			authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease}}

			control, relay, probes, err := executeOperationForLiveAgentTest(t, finder, authorizer, operation)
			require.ErrorIs(t, err, lookupErr)
			require.Len(t, authorizer.calls, 1)
			requireNoOperationMutation(t, control, relay, probes)
		})
	}
}

func TestExecuteRejectsChangedAgentIdentityBeforeLiveAuthorize(t *testing.T) {
	finder := &sequenceOperationAgentFinder{results: []operationAgentResult{
		{agent: models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}},
		{agent: models.Agent{AgentID: "agent-b", Status: consts.StatusEnabled}},
	}}
	lease := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}
	authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{lease}}

	control, relay, probes, err := executeOperationForLiveAgentTest(t, finder, authorizer, connectivity.OperationFullSync)
	require.ErrorIs(t, err, ErrAgentIdentityChanged)
	require.Len(t, authorizer.calls, 1)
	requireNoOperationMutation(t, control, relay, probes)
}

func TestExecuteLiveAgentRecheckKeepsGenerationCASAndUsesExactlyTwoLookups(t *testing.T) {
	tests := []struct {
		name      string
		liveLease connectivity.OperationLease
		wantErr   error
	}{
		{name: "same generation mutates", liveLease: connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}},
		{name: "changed generation rejects", liveLease: connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 8, RelayGeneration: 12}, wantErr: connectivity.ErrConnectionGenerationChanged},
	}
	for _, test := range tests {
		for _, operation := range operationCases() {
			t.Run(test.name+"/"+string(operation), func(t *testing.T) {
				finder := &sequenceOperationAgentFinder{results: []operationAgentResult{
					{agent: models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}},
					{agent: models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}},
				}}
				initial := connectivity.OperationLease{SnapshotEpoch: "master-a", ControlGeneration: 7, RelayGeneration: 11}
				authorizer := &operationAuthorizer{leases: []connectivity.OperationLease{initial, test.liveLease}}

				control, relay, probes, err := executeOperationForLiveAgentTest(t, finder, authorizer, operation)
				require.Equal(t, []string{"agent-a", "agent-a"}, finder.calls)
				require.Len(t, authorizer.calls, 2)
				if test.wantErr != nil {
					require.ErrorIs(t, err, test.wantErr)
					requireNoOperationMutation(t, control, relay, probes)
					return
				}
				require.NoError(t, err)
				require.Equal(t, 1, len(control.calls)+len(relay.calls)+len(probes.calls))
			})
		}
	}
}
