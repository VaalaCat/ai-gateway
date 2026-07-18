package sync

import (
	"context"
	"encoding/json"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/stretchr/testify/require"
)

func routeObservationRequest(t *testing.T, method string, params any) *jsonrpc.Request {
	t.Helper()
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	return &jsonrpc.Request{JSONRPC: "2.0", Method: method, Params: raw}
}

func TestRouteTelemetryHubUsesAuthenticatedSourceAndCurrentControlSession(t *testing.T) {
	now := time.Unix(60_000, 0)
	h := newControlTestHub(now.Unix(), now.Unix()+1)
	connections := connectivity.NewService("instance-a", connectivity.Sources{}, connectivity.Options{Now: func() time.Time { return now }})
	h.RouteObservations = connections
	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("authenticated-source", oldConn, "127.0.0.1:1")

	digest := protocol.RouteEdgeDigest{Generation: 501, Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "target-a", LastUsedAt: now.Unix()}}}
	require.True(t, h.dispatchControlRequest(context.Background(), oldConn, "authenticated-source", oldGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteDigest, digest)))
	require.Len(t, connections.RouteEdges("authenticated-source"), 1)
	require.Empty(t, connections.RouteEdges("claimed-source"), "the payload cannot choose its source identity")

	newConn := &ws.Conn{}
	newGeneration, _, _ := h.installControlSession("authenticated-source", newConn, "127.0.0.1:2")
	late := protocol.RouteTelemetryBatch{Generation: 501, Events: []protocol.RouteEvent{{TargetAgentID: "late-old-session", ObservedAt: now.Unix() + 1}}}
	h.dispatchControlRequest(context.Background(), oldConn, "authenticated-source", oldGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteTelemetry, late))
	require.Len(t, connections.RouteEdges("authenticated-source"), 1)

	current := protocol.RouteTelemetryBatch{Generation: 501, Events: []protocol.RouteEvent{{TargetAgentID: "current-session", ObservedAt: now.Unix() + 1}}}
	h.dispatchControlRequest(context.Background(), newConn, "authenticated-source", newGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteTelemetry, current))
	require.Len(t, connections.RouteEdges("authenticated-source"), 1, "a new control session rejects events until its digest establishes a process")
	currentDigest := protocol.RouteEdgeDigest{Generation: 502, Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "target-a", LastUsedAt: now.Unix()}}}
	h.dispatchControlRequest(context.Background(), newConn, "authenticated-source", newGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteDigest, currentDigest))
	current.Generation = 502
	h.dispatchControlRequest(context.Background(), newConn, "authenticated-source", newGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteTelemetry, current))
	require.Len(t, connections.RouteEdges("authenticated-source"), 2)
}

func TestRouteDigestHubRejectsMalformedAndMissingServiceWithoutPanic(t *testing.T) {
	h := newControlTestHub(70_000)
	conn := &ws.Conn{}
	generation, _, _ := h.installControlSession("source-a", conn, "127.0.0.1:1")
	require.True(t, h.dispatchControlRequest(context.Background(), conn, "source-a", generation,
		&jsonrpc.Request{JSONRPC: "2.0", Method: consts.RPCAgentRouteDigest, Params: json.RawMessage(`{"generation":"bad"}`)}))

	h.RouteObservations = connectivity.NewService("instance-a", connectivity.Sources{}, connectivity.Options{})
	require.True(t, h.dispatchControlRequest(context.Background(), conn, "source-a", generation,
		&jsonrpc.Request{JSONRPC: "2.0", Method: consts.RPCAgentRouteTelemetry, Params: json.RawMessage(`{"generation":1,"events":[],"source_id":"forged"}`)}))
	require.Empty(t, h.RouteObservations.RouteEdges("source-a"))

	oversized := json.RawMessage(`{"generation":2,"edges":[]}` + strings.Repeat(" ", maxRouteObservationParamsBytes))
	require.True(t, h.dispatchControlRequest(context.Background(), conn, "source-a", generation,
		&jsonrpc.Request{JSONRPC: "2.0", Method: consts.RPCAgentRouteDigest, Params: oversized}))
	require.Empty(t, h.RouteObservations.RouteEdges("source-a"), "oversized params must be rejected before decoding")
}

func TestRouteDigestHubRevalidatesSessionAfterDecodeBarrier(t *testing.T) {
	now := time.Unix(120_000, 0)
	h := newControlTestHub(now.Unix(), now.Unix()+1)
	connections := connectivity.NewService("instance-a", connectivity.Sources{}, connectivity.Options{Now: func() time.Time { return now }})
	h.RouteObservations = connections
	oldConn := &ws.Conn{}
	oldControlGeneration, _, _ := h.installControlSession("source-a", oldConn, "127.0.0.1:1")
	h.dispatchControlRequest(context.Background(), oldConn, "source-a", oldControlGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteDigest, protocol.RouteEdgeDigest{Generation: 801}))

	paused := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce gosync.Once
	h.beforeRouteObservationApply = func() {
		pauseOnce.Do(func() {
			close(paused)
			<-release
		})
	}
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		h.dispatchControlRequest(context.Background(), oldConn, "source-a", oldControlGeneration,
			routeObservationRequest(t, consts.RPCAgentRouteDigest, protocol.RouteEdgeDigest{
				Generation: 801, Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "old", LastUsedAt: now.Unix()}},
			}))
	}()
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("old route digest did not reach the decode/apply barrier")
	}

	newConn := &ws.Conn{}
	newControlGeneration, _, _ := h.installControlSession("source-a", newConn, "127.0.0.1:2")
	h.beforeRouteObservationApply = nil
	h.dispatchControlRequest(context.Background(), newConn, "source-a", newControlGeneration,
		routeObservationRequest(t, consts.RPCAgentRouteDigest, protocol.RouteEdgeDigest{
			Generation: 802, Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "new", LastUsedAt: now.Unix()}},
		}))
	close(release)
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old route digest handler did not resume")
	}
	edges := connections.RouteEdges("source-a")
	require.Len(t, edges, 1)
	require.Equal(t, "new", edges[0].TargetAgentID)
}
