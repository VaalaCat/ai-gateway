package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type operationSyncer struct {
	calls int
	err   error
}

func (s *operationSyncer) FullSync(context.Context) error {
	s.calls++
	return s.err
}

type operationTunnel struct {
	calls int
	err   error
}

func (t *operationTunnel) Reconnect(context.Context) error {
	t.calls++
	return t.err
}

type operationCircuits struct{ targets []string }

func (c *operationCircuits) ResetCircuit(targetAgentID, _ string) {
	c.targets = append(c.targets, targetAgentID)
}

func operationParams(t *testing.T, operation connectivity.Operation, targetAgentID, requestID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(protocol.OperationRequest{
		AgentID: "agent-a", Operation: string(operation), TargetAgentID: targetAgentID, RequestID: requestID,
		ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
	})
	require.NoError(t, err)
	return raw
}

func TestAgentOperationHandlerDispatchesTypedOperations(t *testing.T) {
	t.Run("full sync", func(t *testing.T) {
		syncer := &operationSyncer{}
		handler := NewAgentOperationHandler(AgentOperationSources{Syncer: syncer})
		out, err := handler.Handle(t.Context(), operationParams(t, connectivity.OperationFullSync, "", ""))
		require.NoError(t, err)
		require.Equal(t, 1, syncer.calls)
		require.Equal(t, "accepted", out.(protocol.OperationAck).State)
	})

	t.Run("relay reconnect", func(t *testing.T) {
		tunnel := &operationTunnel{}
		handler := NewAgentOperationHandler(AgentOperationSources{Tunnel: tunnel})
		_, err := handler.Handle(t.Context(), operationParams(t, connectivity.OperationRelayReconnect, "", ""))
		require.NoError(t, err)
		require.Equal(t, 1, tunnel.calls)
	})

	t.Run("direct circuit reset", func(t *testing.T) {
		circuits := &operationCircuits{}
		handler := NewAgentOperationHandler(AgentOperationSources{Circuits: circuits})
		_, err := handler.Handle(t.Context(), operationParams(t, connectivity.OperationDirectCircuitReset, "agent-b", ""))
		require.NoError(t, err)
		require.Equal(t, []string{"agent-b"}, circuits.targets)
	})

	t.Run("interrupt", func(t *testing.T) {
		registry := inflight.NewRegistry(nil, 0)
		trackedCtx, cancel := context.WithCancelCause(t.Context())
		defer cancel(errors.New("test cleanup"))
		registry.Track(inflight.Meta{ReqID: "request-41", Cancel: func() { cancel(errors.New("interrupted")) }})
		id := registry.Snapshot()[0].ID
		handler := NewAgentOperationHandler(AgentOperationSources{Inflight: registry})
		_, err := handler.Handle(t.Context(), operationParams(t, connectivity.OperationInterrupt, "", jsonID(id)))
		require.NoError(t, err)
		require.ErrorContains(t, context.Cause(trackedCtx), "interrupted")
	})
}

func TestAgentOperationHandlerRejectsInvalidRequestsWithoutDispatch(t *testing.T) {
	syncer := &operationSyncer{}
	tunnel := &operationTunnel{}
	circuits := &operationCircuits{}
	handler := NewAgentOperationHandler(AgentOperationSources{Syncer: syncer, Tunnel: tunnel, Circuits: circuits})

	tests := []struct {
		name   string
		params json.RawMessage
	}{
		{name: "malformed JSON", params: json.RawMessage("{bad")},
		{name: "unknown operation", params: operationParams(t, connectivity.Operation("unknown"), "", "")},
		{name: "master-only relay drain", params: operationParams(t, connectivity.OperationRelayDrain, "", "")},
		{name: "missing circuit target", params: operationParams(t, connectivity.OperationDirectCircuitReset, "", "")},
		{name: "invalid interrupt request id", params: operationParams(t, connectivity.OperationInterrupt, "", "not-a-number")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := handler.Handle(t.Context(), test.params)
			require.Error(t, err)
		})
	}
	require.Zero(t, syncer.calls)
	require.Zero(t, tunnel.calls)
	require.Empty(t, circuits.targets)
}

func TestAgentOperationHandlerPropagatesFailureAndCancellation(t *testing.T) {
	t.Run("operator failure", func(t *testing.T) {
		syncer := &operationSyncer{err: errors.New("full sync failed")}
		handler := NewAgentOperationHandler(AgentOperationSources{Syncer: syncer})
		_, err := handler.Handle(t.Context(), operationParams(t, connectivity.OperationFullSync, "", ""))
		require.ErrorContains(t, err, "full sync failed")
	})

	t.Run("canceled before dispatch", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(errors.New("operation canceled"))
		tunnel := &operationTunnel{}
		handler := NewAgentOperationHandler(AgentOperationSources{Tunnel: tunnel})
		_, err := handler.Handle(ctx, operationParams(t, connectivity.OperationRelayReconnect, "", ""))
		require.ErrorContains(t, err, "operation canceled")
		require.Zero(t, tunnel.calls)
	})
}

func jsonID(id int64) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}
