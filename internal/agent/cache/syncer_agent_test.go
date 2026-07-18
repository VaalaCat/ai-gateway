package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

const testAgentFullSyncContractV1 = "agent_full_sync_v1"

func TestAgentFullSyncRetainsAll501KeysetRows(t *testing.T) {
	firstPage := make([]models.Agent, 500)
	for i := range firstPage {
		firstPage[i] = testSyncedAgentWithID(uint(i+1), fmt.Sprintf("agent-%d", i+1), "snapshot")
	}
	lastPage := []models.Agent{testSyncedAgentWithID(501, "agent-501", "snapshot")}

	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		require.Equal(t, consts.RPCSyncFullSync, call.Method)
		switch callNumber {
		case 1:
			return marshalAgentFullSync(firstPage, protocol.FullSyncResponse{
				Total: 501, HasMore: true, Version: 20,
				LastID: 500, SnapshotMaxID: 501, BaseVersion: 20,
			}), nil
		case 2:
			return marshalAgentFullSync(lastPage, protocol.FullSyncResponse{
				Total: 501, Version: 20,
				LastID: 501, SnapshotMaxID: 501, BaseVersion: 20,
			}), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
	}

	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetAgent(&models.Agent{AgentID: "stale-agent", Name: "stale", Status: 1})
	require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAgent))

	require.Equal(t, 501, syncer.Store.AgentCount())
	require.Equal(t, "snapshot", syncer.Store.GetAgent("agent-1").Name)
	require.Equal(t, "snapshot", syncer.Store.GetAgent("agent-501").Name)
	require.Nil(t, syncer.Store.GetAgent("stale-agent"))
	require.False(t, syncer.agentsDirty.Load())

	requests := client.requests()
	require.Len(t, requests, 2)
	require.Zero(t, requests[0].Request.Page)
	require.Zero(t, requests[0].Request.AfterID)
	require.Zero(t, requests[0].Request.SnapshotMaxID)
	require.Zero(t, requests[0].Request.BaseVersion)
	require.Zero(t, requests[1].Request.Page)
	require.Equal(t, uint(500), requests[1].Request.AfterID)
	require.Equal(t, uint(501), requests[1].Request.SnapshotMaxID)
	require.Equal(t, int64(20), requests[1].Request.BaseVersion)
}

func TestAgentFullSyncReplaysNewerPushOverStaleSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		pushAgent models.Agent
		assert    func(*testing.T, *Store)
	}{
		{
			name:      "update",
			action:    events.ActionUpdate,
			pushAgent: testSyncedAgent("agent-a", "updated-v11"),
			assert: func(t *testing.T, store *Store) {
				require.Equal(t, "updated-v11", store.GetAgent("agent-a").Name)
			},
		},
		{
			name:      "delete",
			action:    events.ActionDelete,
			pushAgent: testSyncedAgent("agent-a", "deleted-v11"),
			assert: func(t *testing.T, store *Store) {
				require.Nil(t, store.GetAgent("agent-a"))
			},
		},
		{
			name:      "create",
			action:    events.ActionCreate,
			pushAgent: testSyncedAgent("agent-a", "created-v11"),
			assert: func(t *testing.T, store *Store) {
				require.Equal(t, "created-v11", store.GetAgent("agent-a").Name)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			syncer := newAgentRouteTestSyncer(client)
			syncer.Store.SetAgent(&models.Agent{AgentID: "agent-a", Name: "live-before-sync", Status: 1})
			client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				require.Equal(t, 1, callNumber)
				require.Equal(t, consts.RPCSyncFullSync, call.Method)
				require.Equal(t, events.EntityAgent, call.Request.Entity)
				require.Zero(t, call.Request.Page)
				require.Zero(t, call.Request.BaseVersion)
				require.NoError(t, syncer.applySyncPush(testAgentPush(test.action, test.pushAgent, 11)))
				return marshalAgentFullSync([]models.Agent{testSyncedAgent("agent-a", "stale-v10")}, protocol.FullSyncResponse{
					Page: 1, Total: 1, Version: 11, BaseVersion: 10,
				}), nil
			}

			require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAgent))
			test.assert(t, syncer.Store)
			require.Equal(t, int64(11), syncer.Store.Version())
		})
	}
}

func TestAgentFullSyncRemovesAgentsAbsentFromCompleteSnapshot(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		return marshalAgentFullSync([]models.Agent{testSyncedAgent("agent-kept", "snapshot")}, protocol.FullSyncResponse{
			Page: 1, Total: 1, Version: 10, BaseVersion: 10,
		}), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetAgent(&models.Agent{AgentID: "agent-stale", Name: "must-be-removed", Status: 1})

	require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAgent))

	require.Nil(t, syncer.Store.GetAgent("agent-stale"))
	require.Equal(t, "snapshot", syncer.Store.GetAgent("agent-kept").Name)
}

func TestAgentFullSyncFailuresKeepLiveStateAndDirty(t *testing.T) {
	tests := []struct {
		name    string
		respond func(*testing.T, *Syncer, context.CancelFunc, agentRouteSyncCall, int) (json.RawMessage, error)
		wantErr string
	}{
		{
			name: "page failure",
			respond: func(t *testing.T, syncer *Syncer, _ context.CancelFunc, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				if callNumber == 1 {
					require.NoError(t, syncer.applySyncPush(testAgentPush(
						events.ActionUpdate, testSyncedAgent("agent-live", "live-v11"), 11,
					)))
					return marshalAgentFullSync([]models.Agent{testSyncedAgent("snapshot-only", "partial")}, protocol.FullSyncResponse{
						Total: 2, HasMore: true, Version: 11,
						LastID: 1, SnapshotMaxID: 2, BaseVersion: 10,
					}), nil
				}
				return nil, errors.New("page two unavailable")
			},
			wantErr: "page two unavailable",
		},
		{
			name: "response decode failure",
			respond: func(t *testing.T, syncer *Syncer, _ context.CancelFunc, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
				require.NoError(t, syncer.applySyncPush(testAgentPush(
					events.ActionUpdate, testSyncedAgent("agent-live", "live-v11"), 11,
				)))
				return json.RawMessage(`{"items":`), nil
			},
			wantErr: "unexpected end of JSON input",
		},
		{
			name: "context cancellation",
			respond: func(t *testing.T, syncer *Syncer, cancel context.CancelFunc, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
				require.NoError(t, syncer.applySyncPush(testAgentPush(
					events.ActionUpdate, testSyncedAgent("agent-live", "live-v11"), 11,
				)))
				cancel()
				return marshalAgentFullSync([]models.Agent{testSyncedAgent("snapshot-only", "cancelled")}, protocol.FullSyncResponse{
					Page: 1, Total: 1, Version: 11, BaseVersion: 10,
				}), nil
			},
			wantErr: context.Canceled.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			syncer := newAgentRouteTestSyncer(client)
			syncer.Store.SetAgent(&models.Agent{AgentID: "agent-live", Name: "before", Status: 1})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				return test.respond(t, syncer, cancel, call, callNumber)
			}

			err := syncer.fullSyncEntity(ctx, events.EntityAgent)
			require.ErrorContains(t, err, test.wantErr)
			require.Equal(t, "live-v11", syncer.Store.GetAgent("agent-live").Name)
			require.Nil(t, syncer.Store.GetAgent("snapshot-only"))
			require.True(t, syncer.agentsDirty.Load())
			requireAgentBuilderCleared(t, syncer)
		})
	}
}

func TestAgentMalformedPushDuringFullSyncAbortsSnapshot(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetAgent(&models.Agent{AgentID: "agent-live", Name: "before", Status: 1})
	var pushErr error
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		pushErr = syncer.applySyncPush(protocol.SyncPushParams{
			Entity: events.EntityAgent, Action: events.ActionUpdate, Data: []byte(`{"agent_id":`), Version: 11,
		})
		return marshalAgentFullSync([]models.Agent{testSyncedAgent("snapshot-only", "must-not-publish")}, protocol.FullSyncResponse{
			Page: 1, Total: 1, Version: 11, BaseVersion: 10,
		}), nil
	}

	err := syncer.fullSyncEntity(context.Background(), events.EntityAgent)
	require.ErrorContains(t, pushErr, "decode agent push")
	require.ErrorContains(t, err, "decode agent push")
	require.Equal(t, "before", syncer.Store.GetAgent("agent-live").Name)
	require.Nil(t, syncer.Store.GetAgent("snapshot-only"))
	require.True(t, syncer.agentsDirty.Load())
	requireAgentBuilderCleared(t, syncer)
}

func TestAgentUnknownPushActionDuringFullSyncAbortsSnapshot(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetAgent(&models.Agent{AgentID: "agent-live", Name: "before", Status: 1})
	var pushErr error
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		pushErr = syncer.applySyncPush(testAgentPush(
			"replace", testSyncedAgent("agent-live", "must-not-apply"), 11,
		))
		return marshalAgentFullSync([]models.Agent{testSyncedAgent("snapshot-only", "must-not-publish")}, protocol.FullSyncResponse{
			Page: 1, Total: 1, Version: 11, BaseVersion: 10,
		}), nil
	}

	err := syncer.fullSyncEntity(context.Background(), events.EntityAgent)
	require.ErrorContains(t, pushErr, `unknown agent push action "replace"`)
	require.ErrorContains(t, err, `unknown agent push action "replace"`)
	require.Equal(t, "before", syncer.Store.GetAgent("agent-live").Name)
	require.Nil(t, syncer.Store.GetAgent("snapshot-only"))
	require.True(t, syncer.agentsDirty.Load())
	requireAgentBuilderCleared(t, syncer)
}

func TestAgentFullSyncPushBufferOverflowKeepsLiveState(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetAgent(&models.Agent{AgentID: "agent-live", Name: "before", Status: 1})
	var overflowErr error
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		for i := 0; i < 4096; i++ {
			err := syncer.applySyncPush(testAgentPush(
				events.ActionUpdate,
				testSyncedAgent("agent-live", fmt.Sprintf("live-%d", i)),
				int64(11+i),
			))
			require.NoError(t, err, "push %d before capacity", i+1)
		}
		overflowErr = syncer.applySyncPush(testAgentPush(
			events.ActionUpdate, testSyncedAgent("agent-live", "live-4096"), 4107,
		))
		return marshalAgentFullSync([]models.Agent{testSyncedAgent("snapshot-only", "partial")}, protocol.FullSyncResponse{
			Page: 1, Total: 1, Version: 4107, BaseVersion: 10,
		}), nil
	}

	err := syncer.fullSyncEntity(context.Background(), events.EntityAgent)
	require.ErrorContains(t, overflowErr, "agent full sync push buffer overflow")
	require.ErrorContains(t, err, "agent full sync push buffer overflow")
	require.Equal(t, "live-4096", syncer.Store.GetAgent("agent-live").Name)
	require.Nil(t, syncer.Store.GetAgent("snapshot-only"))
	require.Equal(t, int64(4107), syncer.Store.Version())
	require.True(t, syncer.agentsDirty.Load())
	requireAgentBuilderCleared(t, syncer)
}

func TestAgentFullSyncRequiresStableKeysetSnapshotAcrossPages(t *testing.T) {
	tests := []struct {
		name                string
		secondSnapshotMaxID uint
		secondBaseVersion   int64
	}{
		{name: "changed snapshot max", secondSnapshotMaxID: 11, secondBaseVersion: 10},
		{name: "changed base version", secondSnapshotMaxID: 10, secondBaseVersion: 9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				switch callNumber {
				case 1:
					return marshalAgentFullSync(nil, protocol.FullSyncResponse{
						HasMore: true, Version: 11,
						LastID: 5, SnapshotMaxID: 10, BaseVersion: 10,
					}), nil
				case 2:
					require.Zero(t, call.Request.Page)
					require.Equal(t, uint(5), call.Request.AfterID)
					require.Equal(t, uint(10), call.Request.SnapshotMaxID)
					require.Equal(t, int64(10), call.Request.BaseVersion)
					return marshalAgentFullSync(nil, protocol.FullSyncResponse{
						Version: 11, SnapshotMaxID: test.secondSnapshotMaxID, BaseVersion: test.secondBaseVersion,
					}), nil
				default:
					return nil, fmt.Errorf("unexpected call %d", callNumber)
				}
			}
			syncer := newAgentRouteTestSyncer(client)
			err := syncer.fullSyncEntity(context.Background(), events.EntityAgent)
			require.ErrorContains(t, err, "keyset snapshot changed")
			require.True(t, syncer.agentsDirty.Load())
		})
	}
}

func TestAgentFullSyncRejectsInvalidKeysetCursor(t *testing.T) {
	for _, invalidLastID := range []uint{5, 11} {
		t.Run(fmt.Sprintf("last_%d", invalidLastID), func(t *testing.T) {
			client := &agentRouteSyncClient{}
			client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				switch callNumber {
				case 1:
					return marshalAgentFullSync(nil, protocol.FullSyncResponse{
						HasMore: true, Version: 20,
						LastID: 5, SnapshotMaxID: 10, BaseVersion: 20,
					}), nil
				case 2:
					require.Equal(t, uint(5), call.Request.AfterID)
					return marshalAgentFullSync(nil, protocol.FullSyncResponse{
						HasMore: true, Version: 20,
						LastID: invalidLastID, SnapshotMaxID: 10, BaseVersion: 20,
					}), nil
				default:
					return nil, fmt.Errorf("unexpected call %d", callNumber)
				}
			}

			syncer := newAgentRouteTestSyncer(client)
			err := syncer.fullSyncEntity(context.Background(), events.EntityAgent)
			require.ErrorContains(t, err, "cursor made no valid progress")
			require.True(t, syncer.agentsDirty.Load())
		})
	}
}

func TestAgentFullSyncRequiresV1ContractAndAcceptsZeroBaseVersion(t *testing.T) {
	tests := []struct {
		name      string
		response  func() json.RawMessage
		wantError string
	}{
		{
			name: "v1 contract with zero base",
			response: func() json.RawMessage {
				return marshalAgentFullSync(nil, protocol.FullSyncResponse{Version: 0, BaseVersion: 0})
			},
		},
		{
			name: "missing keyset flag",
			response: func() json.RawMessage {
				return marshalAgentFullSyncWithContractOnly(nil, protocol.FullSyncResponse{Version: 10, BaseVersion: 10})
			},
			wantError: "requires keyset snapshot",
		},
		{
			name: "missing contract",
			response: func() json.RawMessage {
				return marshalAgentFullSyncWithoutContract(nil, protocol.FullSyncResponse{Page: 1, Version: 10, BaseVersion: 10})
			},
			wantError: "unsupported snapshot contract",
		},
		{
			name: "wrong contract",
			response: func() json.RawMessage {
				raw := marshalAgentFullSyncWithoutContract(nil, protocol.FullSyncResponse{Page: 1, Version: 10, BaseVersion: 10})
				var envelope map[string]any
				require.NoError(t, json.Unmarshal(raw, &envelope))
				envelope["snapshot_contract"] = "agent_full_sync_v2"
				encoded, err := json.Marshal(envelope)
				require.NoError(t, err)
				return encoded
			},
			wantError: "unsupported snapshot contract",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
				return test.response(), nil
			}
			syncer := newAgentRouteTestSyncer(client)
			err := syncer.fullSyncEntity(context.Background(), events.EntityAgent)
			if test.wantError == "" {
				require.NoError(t, err)
				require.False(t, syncer.agentsDirty.Load())
				return
			}
			require.ErrorContains(t, err, test.wantError)
			require.True(t, syncer.agentsDirty.Load())
		})
	}
}

func TestAgentDirtySameVersionCheckRetriesFullSync(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
		if call.Method == consts.RPCSyncGetVersion {
			return marshalGetVersion(55), nil
		}
		response := protocol.FullSyncResponse{Version: 55}
		if call.Request.Entity == events.EntityAgent {
			response.BaseVersion = 55
		}
		return marshalAgentFullSync(nil, response), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetVersion(55)
	syncer.agentsDirty.Store(true)

	syncer.checkVersion(context.Background())

	require.Equal(t, 1, countAgentFullSyncCalls(client.requests()))
	require.False(t, syncer.agentsDirty.Load())
}

func testSyncedAgent(agentID, name string) models.Agent {
	return models.Agent{AgentID: agentID, Name: name, Status: 1}
}

func testSyncedAgentWithID(id uint, agentID, name string) models.Agent {
	agent := testSyncedAgent(agentID, name)
	agent.ID = id
	return agent
}

func testAgentPush(action string, agent models.Agent, version int64) protocol.SyncPushParams {
	data, err := json.Marshal(agent)
	if err != nil {
		panic(err)
	}
	return protocol.SyncPushParams{
		Entity: events.EntityAgent, Action: action, Data: data, Version: version,
	}
}

func marshalAgentFullSync(agents []models.Agent, response protocol.FullSyncResponse) json.RawMessage {
	response.Keyset = true
	return marshalAgentFullSyncWithContractOnly(agents, response)
}

func marshalAgentFullSyncWithContractOnly(agents []models.Agent, response protocol.FullSyncResponse) json.RawMessage {
	raw := marshalAgentFullSyncWithoutContract(agents, response)
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		panic(err)
	}
	envelope["snapshot_contract"] = testAgentFullSyncContractV1
	raw, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return raw
}

func marshalAgentFullSyncWithoutContract(agents []models.Agent, response protocol.FullSyncResponse) json.RawMessage {
	items, err := json.Marshal(agents)
	if err != nil {
		panic(err)
	}
	response.Items = items
	raw, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	return raw
}

func countAgentFullSyncCalls(calls []agentRouteSyncCall) int {
	count := 0
	for _, call := range calls {
		if call.Method == consts.RPCSyncFullSync && call.Request.Entity == events.EntityAgent {
			count++
		}
	}
	return count
}

func requireAgentBuilderCleared(t *testing.T, syncer *Syncer) {
	t.Helper()
	syncer.agentStateMu.Lock()
	defer syncer.agentStateMu.Unlock()
	require.Nil(t, syncer.agentBuilder)
}
