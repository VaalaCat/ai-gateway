package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

func TestAPIFullSyncRetainsAll501KeysetRows(t *testing.T) {
	first := make([]protocol.SyncedAPIService, 500)
	for index := range first {
		first[index] = protocol.SyncedAPIService{ID: uint(index + 1), Slug: fmt.Sprintf("service-%d", index+1), Status: 1}
	}
	last := []protocol.SyncedAPIService{{ID: 501, Slug: "service-501", Status: 1}}
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		require.Equal(t, events.EntityAPIService, call.Request.Entity)
		switch callNumber {
		case 1:
			return marshalAPIFullSync(first, protocol.FullSyncResponse{
				Total: 501, HasMore: true, Version: 20, Keyset: true, LastID: 500,
				SnapshotMaxID: 501, BaseVersion: 20, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
			}), nil
		case 2:
			return marshalAPIFullSync(last, protocol.FullSyncResponse{
				Total: 501, Version: 20, Keyset: true, LastID: 501,
				SnapshotMaxID: 501, BaseVersion: 20, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
			}), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
	}
	syncer := newAgentRouteTestSyncer(client)
	readyAPIIndexExceptServices(t, syncer.Store.APIIndex)

	require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIService))
	require.Equal(t, 501, len(syncer.Store.APIIndex.load().servicesByID))
	_, ok := syncer.Store.APIIndex.load().servicesBySlug["service-501"]
	require.True(t, ok)
	require.NoError(t, syncer.Store.APIIndex.RequireReady())

	requests := client.requests()
	require.Len(t, requests, 2)
	require.Equal(t, uint(500), requests[1].Request.AfterID)
	require.Equal(t, uint(501), requests[1].Request.SnapshotMaxID)
	require.Equal(t, int64(20), requests[1].Request.BaseVersion)
}

func TestAPIFullSyncRejectsInvalidIDsOnEveryPage(t *testing.T) {
	tests := []struct {
		name      string
		responses []json.RawMessage
		wantErr   string
	}{
		{
			name: "final LastID moves backward",
			responses: []json.RawMessage{marshalAPIFullSync(
				[]protocol.SyncedAPIService{{ID: 2, Slug: "two"}},
				protocol.FullSyncResponse{Keyset: true, LastID: 1, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1},
			)},
			wantErr: "last id",
		},
		{
			name: "final LastID exceeds snapshot max",
			responses: []json.RawMessage{marshalAPIFullSync(
				[]protocol.SyncedAPIService{{ID: 2, Slug: "two"}},
				protocol.FullSyncResponse{Keyset: true, LastID: 3, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1},
			)},
			wantErr: "last id",
		},
		{
			name: "item exceeds snapshot window",
			responses: []json.RawMessage{marshalAPIFullSync(
				[]protocol.SyncedAPIService{{ID: 3, Slug: "three"}},
				protocol.FullSyncResponse{Keyset: true, LastID: 2, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1},
			)},
			wantErr: "snapshot window",
		},
		{
			name: "page IDs are not strictly increasing",
			responses: []json.RawMessage{marshalAPIFullSync(
				[]protocol.SyncedAPIService{{ID: 2, Slug: "two"}, {ID: 1, Slug: "one"}},
				protocol.FullSyncResponse{Keyset: true, LastID: 1, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1},
			)},
			wantErr: "strictly increasing",
		},
		{
			name: "continuation item is not after cursor",
			responses: []json.RawMessage{
				marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 1, Slug: "one"}}, protocol.FullSyncResponse{
					Keyset: true, HasMore: true, LastID: 1, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
				}),
				marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 1, Slug: "one-again"}}, protocol.FullSyncResponse{
					Keyset: true, LastID: 1, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
				}),
			},
			wantErr: "after cursor",
		},
		{
			name: "empty page claims more",
			responses: []json.RawMessage{marshalAPIFullSync(
				[]protocol.SyncedAPIService{},
				protocol.FullSyncResponse{Keyset: true, HasMore: true, LastID: 0, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1},
			)},
			wantErr: "empty page",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				return test.responses[callNumber-1], nil
			}
			syncer := newAgentRouteTestSyncer(client)
			index := syncer.Store.APIIndex
			readyAPIIndexExceptServices(t, index)
			require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{{ID: 9, Slug: "old"}}))

			err := syncer.fullSyncEntity(context.Background(), events.EntityAPIService)

			require.ErrorContains(t, err, test.wantErr)
			require.Equal(t, "old", index.load().servicesByID[9].Slug)
			require.ErrorIs(t, index.RequireReady(), ErrAPICacheNotReady)
		})
	}
}

func TestAPIFullSyncAcceptsEmptyFinalContinuationAfterTailDeletion(t *testing.T) {
	responses := []json.RawMessage{
		marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 1, Slug: "one"}}, protocol.FullSyncResponse{
			Keyset: true, HasMore: true, LastID: 1, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
		}),
		marshalAPIFullSync([]protocol.SyncedAPIService{}, protocol.FullSyncResponse{
			Keyset: true, LastID: 0, SnapshotMaxID: 2, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
		}),
	}
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		return responses[callNumber-1], nil
	}
	syncer := newAgentRouteTestSyncer(client)
	readyAPIIndexExceptServices(t, syncer.Store.APIIndex)

	require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIService))
	require.Equal(t, "one", syncer.Store.APIIndex.load().servicesByID[1].Slug)
}

func TestAPIFullSyncReplaysOnlyPushesNewerThanBaseVersion(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	readyAPIIndexExceptServices(t, syncer.Store.APIIndex)
	require.NoError(t, syncer.Store.APIIndex.ReplaceServices([]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Name: "live", Status: 1}}))
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
			protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: "stale-push", Status: 1}, 10)))
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
			protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: "new-push", Status: 1}, 11)))
		return marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Name: "snapshot", Status: 1}}, protocol.FullSyncResponse{
			Total: 1, Version: 11, Keyset: true, LastID: 1, SnapshotMaxID: 1, BaseVersion: 10,
			SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
		}), nil
	}

	require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIService))
	require.Equal(t, "new-push", syncer.Store.APIIndex.load().servicesByID[1].Name)
}

func TestAPIReadyIndexIgnoresOldUpdateDeleteAndMalformedPushes(t *testing.T) {
	tests := []struct {
		name string
		push protocol.SyncPushParams
	}{
		{
			name: "update",
			push: apiPush(t, events.EntityAPIService, events.ActionUpdate,
				protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: "stale"}, 20),
		},
		{
			name: "delete",
			push: apiPush(t, events.EntityAPIService, events.ActionDelete,
				protocol.SyncedAPIService{ID: 1, Slug: "weather"}, 19),
		},
		{
			name: "malformed",
			push: protocol.SyncPushParams{
				Entity: events.EntityAPIService, Action: events.ActionUpdate,
				Data: json.RawMessage(`{"id":`), Version: 20,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
			index := readyAPIIndex(t,
				[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Name: "current", Status: 1}},
				nil, nil, nil, nil,
			)
			syncer.Store.APIIndex = index
			syncer.apiSync.index = index
			syncer.apiSync.versions[events.EntityAPIService] = apiEntityVersionState{
				floor: 20, objects: make(map[uint]apiObjectVersion),
			}
			syncer.Store.AdvanceVersion(20)

			require.NoError(t, syncer.applySyncPush(test.push))
			require.NoError(t, index.RequireReady())
			require.Equal(t, "current", index.load().servicesByID[1].Name)
			require.Equal(t, int64(20), syncer.Store.Version())
		})
	}
}

func TestAPIPushVersioningIsPerObjectAndEntity(t *testing.T) {
	t.Run("different objects may arrive in descending version order", func(t *testing.T) {
		syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
		index := readyAPIIndex(t, nil, nil, nil, nil, nil)
		syncer.Store.APIIndex = index
		syncer.apiSync.index = index

		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionCreate,
			protocol.SyncedAPIService{ID: 1, Slug: "newer", Status: 1}, 11)))
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionCreate,
			protocol.SyncedAPIService{ID: 2, Slug: "older", Status: 1}, 10)))

		require.Equal(t, "newer", index.load().servicesByID[1].Slug)
		require.Equal(t, "older", index.load().servicesByID[2].Slug)
	})

	t.Run("same object cannot roll back or resurrect after delete", func(t *testing.T) {
		syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
		index := readyAPIIndex(t, nil, nil, nil, nil, nil)
		syncer.Store.APIIndex = index
		syncer.apiSync.index = index

		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionCreate,
			protocol.SyncedAPIService{ID: 1, Slug: "current", Status: 1}, 11)))
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
			protocol.SyncedAPIService{ID: 1, Slug: "stale", Status: 1}, 10)))
		require.Equal(t, "current", index.load().servicesByID[1].Slug)
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionDelete,
			protocol.SyncedAPIService{ID: 1}, 12)))
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionCreate,
			protocol.SyncedAPIService{ID: 1, Slug: "resurrected", Status: 1}, 11)))
		_, exists := index.load().servicesByID[1]
		require.False(t, exists)
	})

	t.Run("another entity full sync version does not suppress equal push", func(t *testing.T) {
		client := &agentRouteSyncClient{}
		client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
			require.Equal(t, events.EntityAPIRole, call.Request.Entity)
			return marshalAPIFullSync([]protocol.SyncedAPIRole{}, protocol.FullSyncResponse{
				Version: 10, BaseVersion: 10, Keyset: true,
				SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
			}), nil
		}
		syncer := newAgentRouteTestSyncer(client)
		index := readyAPIIndex(t, nil, nil, nil, nil, nil)
		syncer.Store.APIIndex = index
		syncer.apiSync.index = index

		require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIRole))
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionCreate,
			protocol.SyncedAPIService{ID: 1, Slug: "equal-version", Status: 1}, 10)))
		require.Equal(t, "equal-version", index.load().servicesByID[1].Slug)
	})

	t.Run("entity full sync base is the floor for unseen objects", func(t *testing.T) {
		client := &agentRouteSyncClient{}
		client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
			require.Equal(t, events.EntityAPIService, call.Request.Entity)
			return marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 1, Slug: "snapshot", Status: 1}}, protocol.FullSyncResponse{
				Version: 10, BaseVersion: 10, Keyset: true, LastID: 1, SnapshotMaxID: 1,
				SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
			}), nil
		}
		syncer := newAgentRouteTestSyncer(client)
		readyAPIIndexExceptServices(t, syncer.Store.APIIndex)

		require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIService))
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionCreate,
			protocol.SyncedAPIService{ID: 2, Slug: "stale", Status: 1}, 10)))
		_, exists := syncer.Store.APIIndex.load().servicesByID[2]
		require.False(t, exists)
	})
}

func TestAPIFullSyncBuffersOldPushWithoutPublishingOrRollingBackSnapshot(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	index := syncer.Store.APIIndex
	readyAPIIndexExceptServices(t, index)
	require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{{
		ID: 1, Slug: "weather", Name: "current", Status: 1,
	}}))
	syncer.Store.AdvanceVersion(12)
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		require.ErrorIs(t, index.RequireReady(), ErrAPICacheNotReady)
		require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
			protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: "stale", Status: 1}, 10)))
		require.ErrorIs(t, index.RequireReady(), ErrAPICacheNotReady)
		require.Equal(t, "current", index.load().servicesByID[1].Name)
		return marshalAPIFullSync([]protocol.SyncedAPIService{{
			ID: 1, Slug: "weather", Name: "snapshot", Status: 1,
		}}, protocol.FullSyncResponse{
			Version: 10, Keyset: true, LastID: 1, SnapshotMaxID: 1, BaseVersion: 10,
			SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
		}), nil
	}

	require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIService))
	require.Equal(t, "snapshot", index.load().servicesByID[1].Name)
	require.Equal(t, int64(12), syncer.Store.Version())
}

func TestAPIFullSyncFailureKeepsOldSnapshotAndMarksEntityNotReady(t *testing.T) {
	tests := []struct {
		name     string
		response protocol.FullSyncResponse
		items    any
		wantErr  string
	}{
		{name: "contract", response: protocol.FullSyncResponse{Keyset: true, SnapshotContract: "future"}, items: []protocol.SyncedAPIService{}, wantErr: "unsupported snapshot contract"},
		{name: "cursor", response: protocol.FullSyncResponse{Keyset: true, HasMore: true, LastID: 0, SnapshotMaxID: 1, SnapshotContract: protocol.APIFullSyncSnapshotContractV1}, items: []protocol.SyncedAPIService{}, wantErr: "empty page"},
		{name: "reference", response: protocol.FullSyncResponse{Keyset: true, LastID: 9, SnapshotMaxID: 9, SnapshotContract: protocol.APIFullSyncSnapshotContractV1}, items: []protocol.SyncedAPIRoute{{ID: 9, ServiceID: 999, BackendID: 1, Slug: "broken"}}, wantErr: "missing service"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			syncer := newAgentRouteTestSyncer(client)
			index := syncer.Store.APIIndex
			require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}}))
			require.NoError(t, index.ReplaceRoutes([]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 1, Slug: "forecast", Status: 1}}))
			require.NoError(t, index.ReplaceUpstreams(nil))
			require.NoError(t, index.ReplaceRoles(nil))
			require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
			client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
				if test.name == "reference" {
					require.Equal(t, events.EntityAPIRoute, call.Request.Entity)
				} else {
					require.Equal(t, events.EntityAPIService, call.Request.Entity)
				}
				return marshalAPIFullSync(test.items, test.response), nil
			}
			entity := events.EntityAPIService
			if test.name == "reference" {
				entity = events.EntityAPIRoute
			}
			err := syncer.fullSyncEntity(context.Background(), entity)
			require.ErrorContains(t, err, test.wantErr)
			require.Equal(t, uint(2), index.load().routesByKey[apiRouteKey{serviceID: 1, slug: "forecast"}].ID)
			require.ErrorIs(t, index.RequireReady(), ErrAPICacheNotReady)
		})
	}
}

func TestAPIDirtySameVersionCheckRecoversReadiness(t *testing.T) {
	tests := []struct {
		name  string
		dirty func(*testing.T, *Syncer, *agentRouteSyncClient)
	}{
		{
			name: "first sync RPC failure",
			dirty: func(t *testing.T, syncer *Syncer, client *agentRouteSyncClient) {
				client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
					require.Equal(t, events.EntityAPIService, call.Request.Entity)
					return nil, errors.New("temporary API sync failure")
				}
				err := syncer.fullSyncEntity(context.Background(), events.EntityAPIService)
				require.ErrorContains(t, err, "temporary API sync failure")
			},
		},
		{
			name: "push buffer overflow",
			dirty: func(t *testing.T, syncer *Syncer, client *agentRouteSyncClient) {
				client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
					require.Equal(t, events.EntityAPIService, call.Request.Entity)
					for version := int64(56); version < int64(56+maxAPIPushBuffer); version++ {
						require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
							protocol.SyncedAPIService{ID: 1, Slug: "weather"}, version)))
					}
					err := syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
						protocol.SyncedAPIService{ID: 1, Slug: "weather"}, int64(56+maxAPIPushBuffer)))
					require.ErrorIs(t, err, errAPIPushBufferOverflow)
					return emptyAPIFullSyncResponse(55), nil
				}
				err := syncer.fullSyncEntity(context.Background(), events.EntityAPIService)
				require.ErrorIs(t, err, errAPIPushBufferOverflow)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			syncer := newAgentRouteTestSyncer(client)
			syncer.Store.SetVersion(55)
			test.dirty(t, syncer, client)
			require.ErrorIs(t, syncer.Store.APIIndex.RequireReady(), ErrAPICacheNotReady)

			localVersion := syncer.Store.Version()
			client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
				if call.Method == consts.RPCSyncGetVersion {
					return marshalGetVersion(localVersion), nil
				}
				if isTestAPIFullSyncEntity(call.Request.Entity) {
					return emptyAPIFullSyncResponse(localVersion), nil
				}
				return marshalAgentRouteFullSync(nil, protocol.FullSyncResponse{Version: localVersion}), nil
			}

			syncer.checkVersion(context.Background())

			require.NoError(t, syncer.Store.APIIndex.RequireReady())
		})
	}
}

func TestAPIFullSyncOldControlSessionCannotCommit(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	oldSession := syncer.CurrentControlSession()
	client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		syncer.BeginControlSession(&agentRouteSyncClient{})
		return marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 2, Slug: "stale", Status: 1}}, protocol.FullSyncResponse{
			Total: 1, Version: 2, Keyset: true, LastID: 2, SnapshotMaxID: 2, BaseVersion: 1,
			SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
		}), nil
	}

	err := syncer.fullSyncAPIEntityForSession(context.Background(), oldSession, events.EntityAPIService)
	require.ErrorIs(t, err, ErrControlSessionChanged)
	_, exists := syncer.Store.APIIndex.load().servicesByID[2]
	require.False(t, exists)
}

func TestBeginControlSessionNilResetsAPIReadiness(t *testing.T) {
	syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
	index := syncer.Store.APIIndex
	require.NoError(t, index.ReplaceServices(nil))
	require.NoError(t, index.ReplaceRoutes(nil))
	require.NoError(t, index.ReplaceUpstreams(nil))
	require.NoError(t, index.ReplaceRoles(nil))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
	require.NoError(t, index.RequireReady())

	require.Nil(t, syncer.BeginControlSession(nil))
	require.ErrorIs(t, index.RequireReady(), ErrAPICacheNotReady)
}

func TestControlSessionChangesCannotBecomeVisibleBeforeAPIReadinessClears(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*Syncer, *ControlSession) <-chan struct{}
		changed func(*ControlSession, *ControlSession) bool
	}{
		{
			name: "begin replacement",
			change: func(syncer *Syncer, _ *ControlSession) <-chan struct{} {
				done := make(chan struct{})
				go func() {
					syncer.BeginControlSession(&agentRouteSyncClient{})
					close(done)
				}()
				return done
			},
			changed: func(current, original *ControlSession) bool { return current != original },
		},
		{
			name: "end current",
			change: func(syncer *Syncer, original *ControlSession) <-chan struct{} {
				done := make(chan struct{})
				go func() {
					syncer.EndControlSession(original)
					close(done)
				}()
				return done
			},
			changed: func(current, _ *ControlSession) bool { return current == nil },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
			index := syncer.Store.APIIndex
			require.NoError(t, index.ReplaceServices(nil))
			require.NoError(t, index.ReplaceRoutes(nil))
			require.NoError(t, index.ReplaceUpstreams(nil))
			require.NoError(t, index.ReplaceRoles(nil))
			require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
			original := syncer.CurrentControlSession()
			syncer.apiSync.stateMu.Lock()
			changeDone := test.change(syncer, original)
			observed := make(chan struct{})
			go func() {
				for {
					if test.changed(syncer.CurrentControlSession(), original) {
						close(observed)
						return
					}
				}
			}()

			observedEarly := false
			select {
			case <-observed:
				observedEarly = true
			case <-time.After(50 * time.Millisecond):
			}
			syncer.apiSync.stateMu.Unlock()
			<-changeDone
			<-observed

			require.False(t, observedEarly, "control session mutation was visible while API readiness was still ready")
			require.ErrorIs(t, index.RequireReady(), ErrAPICacheNotReady)
		})
	}
}

func TestAPIFullSyncMarksFiveEmptyEntitiesReadyInOrder(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
		return emptyAPIFullSyncResponse(5), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	for _, entity := range apiFullCacheEntities {
		require.NoError(t, syncer.fullSyncEntity(context.Background(), entity))
	}
	require.NoError(t, syncer.Store.APIIndex.RequireReady())
	requests := client.requests()
	require.Len(t, requests, len(apiFullCacheEntities))
	for index, entity := range apiFullCacheEntities {
		require.Equal(t, entity, requests[index].Request.Entity)
		require.Equal(t, protocol.FullSyncMaxPageSize, requests[index].Request.PageSize)
	}
}

func TestAPIFullSyncReplaysCreateAndDeleteOverSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		action string
		push   protocol.SyncedAPIService
		want   map[uint]string
	}{
		{name: "create", action: events.ActionCreate, push: protocol.SyncedAPIService{ID: 2, Slug: "late", Name: "created", Status: 1}, want: map[uint]string{1: "snapshot", 2: "created"}},
		{name: "delete", action: events.ActionDelete, push: protocol.SyncedAPIService{ID: 1, Slug: "weather", Status: 1}, want: map[uint]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			syncer := newAgentRouteTestSyncer(client)
			client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
				require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, test.action, test.push, 11)))
				return marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Name: "snapshot", Status: 1}}, protocol.FullSyncResponse{
					Version: 11, Keyset: true, LastID: 1, SnapshotMaxID: 1, BaseVersion: 10,
					SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
				}), nil
			}
			require.NoError(t, syncer.fullSyncEntity(context.Background(), events.EntityAPIService))
			actual := make(map[uint]string)
			for id, service := range syncer.Store.APIIndex.load().servicesByID {
				actual[id] = service.Name
			}
			require.Equal(t, test.want, actual)
		})
	}
}

func TestAPIMalformedUnknownAndOverflowPushAbortPublication(t *testing.T) {
	tests := []struct {
		name    string
		pushAll func(*testing.T, *Syncer) error
		wantErr string
	}{
		{
			name: "decode",
			pushAll: func(_ *testing.T, syncer *Syncer) error {
				return syncer.applySyncPush(protocol.SyncPushParams{Entity: events.EntityAPIService, Action: events.ActionUpdate, Data: json.RawMessage(`{"id":`), Version: 11})
			},
			wantErr: "decode api_service push",
		},
		{
			name: "unknown action",
			pushAll: func(t *testing.T, syncer *Syncer) error {
				return syncer.applySyncPush(apiPush(t, events.EntityAPIService, "replace", protocol.SyncedAPIService{ID: 1, Slug: "weather"}, 11))
			},
			wantErr: "unknown API push action",
		},
		{
			name: "overflow",
			pushAll: func(t *testing.T, syncer *Syncer) error {
				for index := 0; index < maxAPIPushBuffer; index++ {
					require.NoError(t, syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
						protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: fmt.Sprintf("push-%d", index)}, int64(index+11))))
				}
				return syncer.applySyncPush(apiPush(t, events.EntityAPIService, events.ActionUpdate,
					protocol.SyncedAPIService{ID: 1, Slug: "weather", Name: "overflow"}, int64(maxAPIPushBuffer+11)))
			},
			wantErr: "push buffer overflow",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			syncer := newAgentRouteTestSyncer(client)
			require.NoError(t, syncer.Store.APIIndex.ReplaceServices([]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Name: "old", Status: 1}}))
			var pushErr error
			client.respond = func(_ context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
				pushErr = test.pushAll(t, syncer)
				return marshalAPIFullSync([]protocol.SyncedAPIService{{ID: 2, Slug: "partial", Status: 1}}, protocol.FullSyncResponse{
					Version: 20, Keyset: true, LastID: 2, SnapshotMaxID: 2, BaseVersion: 10,
					SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
				}), nil
			}
			err := syncer.fullSyncEntity(context.Background(), events.EntityAPIService)
			require.ErrorContains(t, pushErr, test.wantErr)
			require.ErrorContains(t, err, test.wantErr)
			_, partial := syncer.Store.APIIndex.load().servicesByID[2]
			require.False(t, partial)
			require.ErrorIs(t, syncer.Store.APIIndex.RequireReady(), ErrAPICacheNotReady)
		})
	}
}

func TestAPIIndexConcurrentReadersObserveCompleteSnapshots(t *testing.T) {
	index := readyAPIIndex(t, []protocol.SyncedAPIService{{ID: 1, Slug: "one"}}, nil, nil, nil, nil)
	var failed atomic.Bool
	var group conc.WaitGroup
	group.Go(func() {
		for iteration := 0; iteration < 1000; iteration++ {
			id := uint(iteration%2 + 1)
			slug := fmt.Sprintf("service-%d", id)
			if err := index.ReplaceServices([]protocol.SyncedAPIService{{ID: id, Slug: slug}}); err != nil {
				failed.Store(true)
				return
			}
		}
	})
	for reader := 0; reader < 8; reader++ {
		group.Go(func() {
			for iteration := 0; iteration < 1000; iteration++ {
				snapshot := index.load()
				if len(snapshot.servicesByID) != 1 || len(snapshot.servicesBySlug) != 1 {
					failed.Store(true)
					return
				}
				for id, service := range snapshot.servicesByID {
					if snapshot.servicesBySlug[service.Slug].ID != id {
						failed.Store(true)
						return
					}
				}
			}
		})
	}
	group.Wait()
	require.False(t, failed.Load())
}

func readyAPIIndexExceptServices(t *testing.T, index *APIIndex) {
	t.Helper()
	require.NoError(t, index.ReplaceRoutes(nil))
	require.NoError(t, index.ReplaceUpstreams(nil))
	require.NoError(t, index.ReplaceRoles(nil))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
}

func marshalAPIFullSync(items any, response protocol.FullSyncResponse) json.RawMessage {
	encodedItems, _ := json.Marshal(items)
	response.Items = encodedItems
	encodedResponse, _ := json.Marshal(response)
	return encodedResponse
}

func apiPush(t *testing.T, entity, action string, value any, version int64) protocol.SyncPushParams {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return protocol.SyncPushParams{Entity: entity, Action: action, Data: data, Version: version}
}

func isTestAPIFullSyncEntity(entity string) bool {
	for _, candidate := range apiFullCacheEntities {
		if entity == candidate {
			return true
		}
	}
	return false
}

func emptyAPIFullSyncResponse(version int64) json.RawMessage {
	return marshalAPIFullSync([]any{}, protocol.FullSyncResponse{
		Version: version, Keyset: true, SnapshotContract: protocol.APIFullSyncSnapshotContractV1,
	})
}
