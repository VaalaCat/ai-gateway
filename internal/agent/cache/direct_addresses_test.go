package cache

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

const (
	testAutoAddressA  = `[{"url":"http://auto-a:8140","tag":"auto-detected"}]`
	testAutoAddressB  = `[{"url":"http://auto-b:8140","tag":"auto-detected"}]`
	testManualAddress = `[{"url":"https://manual.example/v1","tag":"public"}]`
)

func applyTestDirectAddresses(t *testing.T, store *Store, generation, sequence uint64, addresses []protocol.Address) {
	t.Helper()
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID:  "master-a",
		AgentID:           "target",
		SessionGeneration: generation,
		Sequence:          sequence,
		HTTPAddresses:     addresses,
	}))
}

func pushTestAgent(t *testing.T, store *Store, action string, agent models.Agent) {
	t.Helper()
	data, err := json.Marshal(agent)
	require.NoError(t, err)
	store.HandleSyncEvent(events.EntityAgent, action, data)
}

func requireTestAddressEqual(t *testing.T, expected, actual string) {
	t.Helper()
	if expected == "" {
		require.Empty(t, actual)
		return
	}
	require.NotEmpty(t, actual)
	require.JSONEq(t, expected, actual)
}

func TestStoreDirectAddressSessionAppliesNewerUpdateAndTombstone(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "target"})

	store.BeginDirectAddressSession("master-a")
	require.Empty(t, store.GetAgent("target").HTTPAddresses)
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 7, Sequence: 2,
		HTTPAddresses: []protocol.Address{{URL: "http://new:8140", Tag: "auto-detected"}},
	}))
	require.JSONEq(t, `[{"url":"http://new:8140","tag":"auto-detected"}]`, store.GetAgent("target").HTTPAddresses)

	require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 6, Sequence: 99,
		HTTPAddresses: []protocol.Address{},
	}))
	require.NotEmpty(t, store.GetAgent("target").HTTPAddresses)
	require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 7, Sequence: 1,
		HTTPAddresses: []protocol.Address{},
	}))
	require.NotEmpty(t, store.GetAgent("target").HTTPAddresses)

	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 7, Sequence: 3,
		HTTPAddresses: []protocol.Address{},
	}))
	require.Empty(t, store.GetAgent("target").HTTPAddresses)
}

func TestStoreDirectAddressSessionRejectsWrongEpochAndInvalidEnvelope(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "target"})
	store.BeginDirectAddressSession("master-a")

	tests := []protocol.AgentDirectAddressesUpdate{
		{MasterInstanceID: "master-b", AgentID: "target", SessionGeneration: 1, Sequence: 1},
		{MasterInstanceID: "master-a", AgentID: "", SessionGeneration: 1, Sequence: 1},
		{MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 0, Sequence: 1},
		{MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 1, Sequence: 0},
	}
	for _, update := range tests {
		require.False(t, store.ApplyDirectAddressesUpdate(update), "update=%+v", update)
	}
	require.Empty(t, store.GetAgent("target").HTTPAddresses)
}

func TestStoreDirectAddressSessionPreservesManualAndAdvancesHighWater(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	manual := `[{"url":"https://manual.example/v1","tag":"public"}]`
	store.SetAgent(&models.Agent{AgentID: "target", HTTPAddresses: manual})
	store.BeginDirectAddressSession("master-a")

	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 4, Sequence: 5,
		HTTPAddresses: []protocol.Address{{URL: "http://auto:8140", Tag: "auto-detected"}},
	}))
	require.Equal(t, manual, store.GetAgent("target").HTTPAddresses)

	store.SetAgent(&models.Agent{AgentID: "target"})
	require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 3, Sequence: 4,
		HTTPAddresses: []protocol.Address{{URL: "http://stale:8140", Tag: "auto-detected"}},
	}))
	require.JSONEq(t, `[{"url":"http://auto:8140","tag":"auto-detected"}]`, store.GetAgent("target").HTTPAddresses)
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 4, Sequence: 6,
		HTTPAddresses: []protocol.Address{{URL: "http://current:8140", Tag: "auto-detected"}},
	}))
	require.Contains(t, store.GetAgent("target").HTTPAddresses, "http://current:8140")
}

func TestStoreDirectAddressSessionResetClearsAutoOnly(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	manual := `[{"url":"https://manual.example/v1","tag":"public"}]`
	store.SetAgent(&models.Agent{AgentID: "auto"})
	store.SetAgent(&models.Agent{AgentID: "manual", HTTPAddresses: manual})

	store.BeginDirectAddressSession("master-a")
	require.Empty(t, store.GetAgent("auto").HTTPAddresses)
	require.Equal(t, manual, store.GetAgent("manual").HTTPAddresses)

	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "auto", SessionGeneration: 1, Sequence: 8,
		HTTPAddresses: []protocol.Address{{URL: "http://fresh:8140", Tag: "auto-detected"}},
	}))
	store.BeginDirectAddressSession("master-a")
	require.Empty(t, store.GetAgent("auto").HTTPAddresses)
	require.Equal(t, manual, store.GetAgent("manual").HTTPAddresses)
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "auto", SessionGeneration: 2, Sequence: 1,
		HTTPAddresses: []protocol.Address{{URL: "http://replacement:8140", Tag: "auto-detected"}},
	}))
}

func TestStoreDirectAddressHighWaterFollowsAgentLifetime(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "target"})
	store.BeginDirectAddressSession("master-a")
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 1, Sequence: 1,
		HTTPAddresses: []protocol.Address{{URL: "http://first:8140", Tag: "auto-detected"}},
	}))
	require.Contains(t, store.directAddressLatest, "target")

	store.DeleteAgent("target")
	require.NotContains(t, store.directAddressLatest, "target")
	require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 1, Sequence: 2,
		HTTPAddresses: []protocol.Address{{URL: "http://unknown:8140", Tag: "auto-detected"}},
	}))
	require.NotContains(t, store.directAddressLatest, "target")

	store.SetAgent(&models.Agent{AgentID: "target"})
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 2, Sequence: 3,
		HTTPAddresses: []protocol.Address{{URL: "http://recreated:8140", Tag: "auto-detected"}},
	}))
}

func TestStoreDirectAddressUnknownDeleteChurnRemainsBounded(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	store.BeginDirectAddressSession("master-a")

	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "staged", SessionGeneration: 1, Sequence: 1,
		HTTPAddresses: []protocol.Address{{URL: "http://staged:8140", Tag: "auto-detected"}},
	}))
	store.DeleteAgent("staged")

	for i := 0; i < 10_000; i++ {
		agentID := fmt.Sprintf("unknown-%d", i)
		require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
			MasterInstanceID: "master-a", AgentID: agentID, SessionGeneration: 1, Sequence: uint64(i + 2),
			HTTPAddresses: []protocol.Address{{URL: "http://unknown:8140", Tag: "auto-detected"}},
		}))
		store.DeleteAgent(agentID)
	}

	require.Empty(t, store.directAddressOverlays)
	require.Empty(t, store.directAddressLatest)

	store.SetAgent(&models.Agent{AgentID: "created", Name: "created"})
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "created", SessionGeneration: 2, Sequence: 20_000,
		HTTPAddresses: []protocol.Address{{URL: "http://created:8140", Tag: "auto-detected"}},
	}))
	require.Contains(t, store.GetAgent("created").HTTPAddresses, "http://created:8140")
}

func TestStoreDirectAddressOverlaySurvivesAgentSyncPush(t *testing.T) {
	tests := []struct {
		name            string
		databaseAddress string
	}{
		{name: "empty database address", databaseAddress: ""},
		{name: "empty database array", databaseAddress: "[]"},
		{name: "null database address", databaseAddress: "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.SetAgent(&models.Agent{AgentID: "target", Name: "before"})
			store.BeginDirectAddressSession("master-a")
			applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}})

			pushTestAgent(t, store, events.ActionUpdate, models.Agent{
				AgentID:       "target",
				Name:          "after",
				HTTPAddresses: tt.databaseAddress,
			})

			got := store.GetAgent("target")
			require.NotNil(t, got)
			require.Equal(t, "after", got.Name)
			requireTestAddressEqual(t, testAutoAddressA, got.HTTPAddresses)
		})
	}
}

func TestStoreDirectAddressOverlaySurvivesFullSync(t *testing.T) {
	tests := []struct {
		name            string
		overlay         []protocol.Address
		databaseAddress string
		wantAddress     string
	}{
		{
			name:            "tombstone beats empty database address",
			overlay:         []protocol.Address{},
			databaseAddress: "",
			wantAddress:     "",
		},
		{
			name:            "new auto address beats empty database array",
			overlay:         []protocol.Address{{URL: "http://auto-b:8140", Tag: "auto-detected"}},
			databaseAddress: "[]",
			wantAddress:     testAutoAddressB,
		},
		{
			name:            "new auto address beats null database address",
			overlay:         []protocol.Address{{URL: "http://auto-b:8140", Tag: "auto-detected"}},
			databaseAddress: "null",
			wantAddress:     testAutoAddressB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.SetAgent(&models.Agent{AgentID: "target", Name: "before"})
			store.BeginDirectAddressSession("master-a")
			applyTestDirectAddresses(t, store, 1, 1, tt.overlay)

			store.LoadAgents([]models.Agent{{
				AgentID:       "target",
				Name:          "after",
				HTTPAddresses: tt.databaseAddress,
			}})

			got := store.GetAgent("target")
			require.NotNil(t, got)
			require.Equal(t, "after", got.Name)
			requireTestAddressEqual(t, tt.wantAddress, got.HTTPAddresses)
		})
	}
}

func TestStoreDirectAddressOverlayWaitsForAuthoritativeAgent(t *testing.T) {
	tests := []struct {
		name        string
		overlay     []protocol.Address
		loadAgent   func(*testing.T, *Store)
		wantAddress string
	}{
		{
			name:    "auto address before SetAgent",
			overlay: []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}},
			loadAgent: func(_ *testing.T, store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "after"})
			},
			wantAddress: testAutoAddressA,
		},
		{
			name:    "tombstone before LoadAgents",
			overlay: []protocol.Address{},
			loadAgent: func(_ *testing.T, store *Store) {
				store.LoadAgents([]models.Agent{{AgentID: "target", Name: "after"}})
			},
			wantAddress: "",
		},
		{
			name:    "auto address before manual sync push",
			overlay: []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}},
			loadAgent: func(t *testing.T, store *Store) {
				pushTestAgent(t, store, events.ActionUpdate, models.Agent{
					AgentID:       "target",
					Name:          "after",
					HTTPAddresses: testManualAddress,
				})
			},
			wantAddress: testManualAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.BeginDirectAddressSession("master-a")
			applyTestDirectAddresses(t, store, 1, 1, tt.overlay)
			require.Nil(t, store.GetAgent("target"))

			tt.loadAgent(t, store)

			got := store.GetAgent("target")
			require.NotNil(t, got)
			require.Equal(t, "after", got.Name)
			requireTestAddressEqual(t, tt.wantAddress, got.HTTPAddresses)
			require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
				MasterInstanceID:  "master-a",
				AgentID:           "target",
				SessionGeneration: 1,
				Sequence:          1,
				HTTPAddresses:     []protocol.Address{{URL: "http://stale:8140", Tag: "auto-detected"}},
			}))
			requireTestAddressEqual(t, tt.wantAddress, store.GetAgent("target").HTTPAddresses)
			if tt.wantAddress == testManualAddress {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "manual-removed"})
				requireTestAddressEqual(t, testAutoAddressA, store.GetAgent("target").HTTPAddresses)
			}
		})
	}
}

func TestStoreManualDirectAddressAlwaysWins(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*testing.T, *Store)
	}{
		{
			name: "runtime auto address",
			apply: func(t *testing.T, store *Store) {
				applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}})
			},
		},
		{
			name: "runtime tombstone",
			apply: func(t *testing.T, store *Store) {
				applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{})
			},
		},
		{
			name: "epoch reset",
			apply: func(t *testing.T, store *Store) {
				applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}})
				store.BeginDirectAddressSession("master-b")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.SetAgent(&models.Agent{AgentID: "target", HTTPAddresses: testManualAddress})
			store.BeginDirectAddressSession("master-a")

			tt.apply(t, store)

			require.JSONEq(t, testManualAddress, store.GetAgent("target").HTTPAddresses)
		})
	}
}

func TestStoreConfiguredAddressTagDoesNotDetermineOwnership(t *testing.T) {
	configured := `[{
		"url":"https://configured.example/direct",
		"tag":"auto-detected"
	}]`
	tests := []struct {
		name  string
		apply func(*testing.T, *Store)
	}{
		{
			name: "SetAgent",
			apply: func(_ *testing.T, store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", HTTPAddresses: configured})
			},
		},
		{
			name: "full sync",
			apply: func(_ *testing.T, store *Store) {
				store.LoadAgents([]models.Agent{{AgentID: "target", HTTPAddresses: configured}})
			},
		},
		{
			name: "sync push",
			apply: func(t *testing.T, store *Store) {
				pushTestAgent(t, store, events.ActionUpdate, models.Agent{AgentID: "target", HTTPAddresses: configured})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.BeginDirectAddressSession("master-a")
			tt.apply(t, store)
			requireTestAddressEqual(t, configured, store.GetAgent("target").HTTPAddresses)

			applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{{URL: "http://runtime:8140", Tag: "auto-detected"}})
			requireTestAddressEqual(t, configured, store.GetAgent("target").HTTPAddresses)
			applyTestDirectAddresses(t, store, 1, 2, nil)
			requireTestAddressEqual(t, configured, store.GetAgent("target").HTTPAddresses)

			store.BeginDirectAddressSession("master-b")
			requireTestAddressEqual(t, configured, store.GetAgent("target").HTTPAddresses)
		})
	}
}

func TestStoreLoadAgentsReplacesConfiguredSnapshotAndPrunesOrphanOverlay(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "removed", Name: "old"})
	store.SetAgent(&models.Agent{AgentID: "kept", Name: "before"})
	store.BeginDirectAddressSession("master-a")
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "removed", SessionGeneration: 1, Sequence: 1,
		HTTPAddresses: []protocol.Address{{URL: "http://removed:8140", Tag: "auto-detected"}},
	}))
	require.True(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "staged-only", SessionGeneration: 2, Sequence: 2,
		HTTPAddresses: []protocol.Address{{URL: "http://staged:8140", Tag: "auto-detected"}},
	}))

	store.LoadAgents([]models.Agent{{AgentID: "kept", Name: "after"}, {AgentID: "created", Name: "new"}})

	require.Nil(t, store.GetAgent("removed"))
	require.Nil(t, store.configuredAgents["removed"])
	require.NotContains(t, store.directAddressOverlays, "removed")
	require.NotContains(t, store.directAddressLatest, "removed")
	require.NotContains(t, store.directAddressOverlays, "staged-only")
	require.NotContains(t, store.directAddressLatest, "staged-only")
	require.Equal(t, "after", store.GetAgent("kept").Name)
	require.Equal(t, "new", store.GetAgent("created").Name)
	require.False(t, store.ApplyDirectAddressesUpdate(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "removed", SessionGeneration: 1, Sequence: 3,
		HTTPAddresses: []protocol.Address{{URL: "http://late:8140", Tag: "auto-detected"}},
	}))
}

func TestStoreManualAddressRemovalRevealsCurrentAutoOverlay(t *testing.T) {
	tests := []struct {
		name   string
		remove func(*testing.T, *Store)
	}{
		{
			name: "SetAgent",
			remove: func(_ *testing.T, store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "after"})
			},
		},
		{
			name: "sync push",
			remove: func(t *testing.T, store *Store) {
				pushTestAgent(t, store, events.ActionUpdate, models.Agent{AgentID: "target", Name: "after"})
			},
		},
		{
			name: "full sync",
			remove: func(_ *testing.T, store *Store) {
				store.LoadAgents([]models.Agent{{AgentID: "target", Name: "after"}})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.SetAgent(&models.Agent{AgentID: "target", Name: "before", HTTPAddresses: testManualAddress})
			store.BeginDirectAddressSession("master-a")
			applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}})
			require.JSONEq(t, testManualAddress, store.GetAgent("target").HTTPAddresses)

			tt.remove(t, store)

			got := store.GetAgent("target")
			require.NotNil(t, got)
			require.Equal(t, "after", got.Name)
			requireTestAddressEqual(t, testAutoAddressA, got.HTTPAddresses)
		})
	}
}

func TestStoreDirectAddressStateFollowsDeleteAndRecreateLifetime(t *testing.T) {
	tests := []struct {
		name     string
		recreate func(*testing.T, *Store)
	}{
		{
			name: "direct Store methods",
			recreate: func(_ *testing.T, store *Store) {
				store.DeleteAgent("target")
				store.SetAgent(&models.Agent{AgentID: "target", Name: "recreated"})
			},
		},
		{
			name: "sync delete and create",
			recreate: func(t *testing.T, store *Store) {
				pushTestAgent(t, store, events.ActionDelete, models.Agent{AgentID: "target"})
				pushTestAgent(t, store, events.ActionCreate, models.Agent{AgentID: "target", Name: "recreated"})
			},
		},
		{
			name: "delete then full sync",
			recreate: func(_ *testing.T, store *Store) {
				store.DeleteAgent("target")
				store.LoadAgents([]models.Agent{{AgentID: "target", Name: "recreated"}})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			store.SetAgent(&models.Agent{AgentID: "target"})
			store.BeginDirectAddressSession("master-a")
			applyTestDirectAddresses(t, store, 5, 9, []protocol.Address{{URL: "http://auto-a:8140", Tag: "auto-detected"}})

			tt.recreate(t, store)

			recreated := store.GetAgent("target")
			require.NotNil(t, recreated)
			require.Equal(t, "recreated", recreated.Name)
			require.Empty(t, recreated.HTTPAddresses)
			applyTestDirectAddresses(t, store, 1, 1, []protocol.Address{{URL: "http://auto-b:8140", Tag: "auto-detected"}})
			require.JSONEq(t, testAutoAddressB, store.GetAgent("target").HTTPAddresses)
		})
	}
}

func TestStoreAgentSnapshotsHaveCopyOwnership(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store)
	}{
		{
			name: "SetAgent input",
			mutate: func(store *Store) {
				input := &models.Agent{AgentID: "target", Name: "original", HTTPAddresses: testManualAddress, Status: consts.StatusEnabled, Tags: "edge"}
				store.SetAgent(input)
				input.Name = "mutated"
				input.HTTPAddresses = ""
			},
		},
		{
			name: "LoadAgents input",
			mutate: func(store *Store) {
				input := []models.Agent{{AgentID: "target", Name: "original", HTTPAddresses: testManualAddress, Status: consts.StatusEnabled, Tags: "edge"}}
				store.LoadAgents(input)
				input[0].Name = "mutated"
				input[0].HTTPAddresses = ""
			},
		},
		{
			name: "GetAgent output",
			mutate: func(store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "original", HTTPAddresses: testManualAddress, Status: consts.StatusEnabled, Tags: "edge"})
				output := store.GetAgent("target")
				output.Name = "mutated"
				output.HTTPAddresses = ""
			},
		},
		{
			name: "GetAllAgents output",
			mutate: func(store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "original", HTTPAddresses: testManualAddress, Status: consts.StatusEnabled, Tags: "edge"})
				output := store.GetAllAgents()
				require.Len(t, output, 1)
				output[0].Name = "mutated"
				output[0].HTTPAddresses = ""
			},
		},
		{
			name: "GetAgentsByTag output",
			mutate: func(store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "original", HTTPAddresses: testManualAddress, Status: consts.StatusEnabled, Tags: "edge"})
				output := store.GetAgentsByTag("edge")
				require.Len(t, output, 1)
				output[0].Name = "mutated"
				output[0].HTTPAddresses = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(nil, config.AgentCacheConfig{})
			tt.mutate(store)

			got := store.GetAgent("target")
			require.NotNil(t, got)
			require.Equal(t, "original", got.Name)
			require.JSONEq(t, testManualAddress, got.HTTPAddresses)
		})
	}
}
