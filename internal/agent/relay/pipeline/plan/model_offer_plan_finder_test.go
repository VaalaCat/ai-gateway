package plan

import (
	"sort"
	"strconv"
	"testing"

	agentapp "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestModelOfferPlanFinderSharesRelayCandidateRules(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func(*testing.T, *cache.Store) *app.UserInfo
		forcedID    uint
		wantKeys    []string
		wantBlocked ModelOfferPlanBlock
		wantRelay   error
	}{
		{
			name: "quota below reserve removes paid candidates",
			prepare: func(t *testing.T, store *cache.Store) *app.UserInfo {
				addPlanChannel(store, 1, "model-a", false, models.ChannelCore{})
				store.SetModelConfig(&models.ModelConfig{ModelName: "model-a", InputPrice: 1})
				store.SetUser(&protocol.SyncedUser{ID: 7, GroupID: 2, Quota: 10})
				loadPlanSettings(store, 100, 10)
				return &app.UserInfo{UserID: 7, TokenID: 17, GroupID: 2}
			},
			wantBlocked: ModelOfferPlanBlockedInsufficientQuota,
			wantRelay:   state.ErrInsufficientQuota,
		},
		{
			name: "byok only with no private candidate fails closed",
			prepare: func(t *testing.T, store *cache.Store) *app.UserInfo {
				addPlanChannel(store, 1, "model-a", true, models.ChannelCore{})
				return &app.UserInfo{UserID: 7, TokenID: 17, GroupID: 2, BYOKOnly: true}
			},
			wantBlocked: ModelOfferPlanBlockedBYOKOnly,
			wantRelay:   state.ErrBYOKOnlyNoChannel,
		},
		{
			name: "byok only retains a visible private candidate",
			prepare: func(t *testing.T, store *cache.Store) *app.UserInfo {
				addPlanChannel(store, 1, "model-a", true, models.ChannelCore{})
				store.OverrideVisiblePrivateChannels(7, []protocol.SyncedPrivateChannel{{
					ChannelCore: models.ChannelCore{ID: 31, Status: consts.StatusEnabled, Weight: 1},
					OwnerID:     7, Models: []string{"model-a"},
				}})
				return &app.UserInfo{UserID: 7, TokenID: 17, GroupID: 2, BYOKOnly: true}
			},
			wantKeys: []string{"model-a:private:31"},
		},
		{
			name: "group token allowlists and forced channel share one intersection",
			prepare: func(t *testing.T, store *cache.Store) *app.UserInfo {
				for id := uint(1); id <= 3; id++ {
					addPlanChannel(store, id, "model-a", true, models.ChannelCore{})
				}
				return &app.UserInfo{
					UserID: 7, TokenID: 17, GroupID: 2,
					GroupAllowedChannelIDs: []uint{1, 2},
					AllowedChannelIDs:      []uint{2, 3},
				}
			},
			forcedID: 2,
			wantKeys: []string{"model-a:admin:2"},
		},
		{
			name: "zero quota keeps free and removes paid",
			prepare: func(t *testing.T, store *cache.Store) *app.UserInfo {
				addPlanChannel(store, 1, "model-a", false, models.ChannelCore{})
				addPlanChannel(store, 2, "model-a", true, models.ChannelCore{})
				store.SetModelConfig(&models.ModelConfig{ModelName: "model-a", InputPrice: 1})
				store.SetUser(&protocol.SyncedUser{ID: 7, GroupID: 2, Quota: 0})
				loadPlanSettings(store, 100, 0)
				return &app.UserInfo{UserID: 7, TokenID: 17, GroupID: 2}
			},
			wantKeys: []string{"model-a:admin:2"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := cache.NewStore(nil, config.AgentCacheConfig{})
			t.Cleanup(store.Close)
			ui := test.prepare(t, store)
			// These parity fixtures model a synchronized, explicitly unpriced
			// model unless the case supplied priced facts itself. A nil config is
			// reserved for the strict missing-facts test below.
			if store.GetModelConfig("model-a") == nil {
				store.SetModelConfig(&models.ModelConfig{ModelName: "model-a"})
			}
			store.RebuildModelIndex()
			finder := NewModelOfferPlanFinder(store)

			got, err := finder.Find(t.Context(), ModelOfferPlanQuery{
				Model: "model-a", UserInfo: ui, ForcedChannelID: test.forcedID,
				InboundProtocols: []llmkit.Protocol{llmkit.ProtocolOpenAIChat},
			})
			require.NoError(t, err)
			require.Equal(t, test.wantKeys, modelOfferCandidateKeys(got.Candidates))
			if test.wantBlocked != "" {
				require.Contains(t, got.Blocked, test.wantBlocked)
			} else {
				require.Empty(t, got.Blocked)
			}

			relayContext := modelOfferRelayContext(store, "model-a", ui, test.forcedID, llmkit.ProtocolOpenAIChat)
			relayErr := NewSolver(nil).Solve(relayContext)
			if test.wantRelay != nil {
				require.ErrorIs(t, relayErr, test.wantRelay)
			} else {
				require.NoError(t, relayErr)
			}
			require.Equal(t, test.wantKeys, attemptCandidateKeys(relayContext.State.Plan.Attempts))
		})
	}
}

func TestModelOfferPlanFinderUsesDeterministicExhaustiveRoutingAndModes(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	t.Cleanup(store.Close)
	loadPlanSettings(store, 1, 0) // Finder must not inherit Relay RetryMax truncation.
	store.SetUser(&protocol.SyncedUser{ID: 7, GroupID: 2, Quota: 1_000})
	store.SetTokenRoutings(17, map[string]*protocol.SyncedRouting{
		"route": {
			ID: 101, Name: "route", Scope: "token", TokenID: 17, Enabled: true,
			Members: []protocol.RoutingMember{
				{Ref: "nested", Priority: 10, Weight: 100},
				{Ref: "token-real", Priority: 10, Weight: 1},
			},
		},
	})
	store.SetUserRoutings(7, map[string]*protocol.SyncedRouting{
		"route": {
			ID: 102, Name: "route", Scope: "user", UserID: 7, Enabled: true,
			Members: []protocol.RoutingMember{{Ref: "user-real", Priority: 1, Weight: 1}},
		},
	})
	store.SetGlobalRouting("route", &protocol.SyncedRouting{
		ID: 103, Name: "route", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "global-real", Priority: 1, Weight: 1}},
	})
	store.SetGlobalRouting("nested", &protocol.SyncedRouting{
		ID: 104, Name: "nested", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "nested-real", Priority: 1, Weight: 1}},
	})
	addPlanChannel(store, 1, "nested-real", true, models.ChannelCore{
		PassthroughEnabled: true, SupportedAPITypes: `["chat_completions"]`, Type: consts.ChannelTypeOpenAI,
	})
	addPlanChannel(store, 2, "nested-real", true, models.ChannelCore{UseLegacyAdaptor: true})
	addPlanChannel(store, 3, "token-real", true, models.ChannelCore{})
	store.SetModelConfig(&models.ModelConfig{ModelName: "nested-real"})
	store.SetModelConfig(&models.ModelConfig{ModelName: "token-real"})
	store.RebuildModelIndex()
	ui := &app.UserInfo{UserID: 7, TokenID: 17, GroupID: 2}
	finder := NewModelOfferPlanFinder(store)
	query := ModelOfferPlanQuery{
		Model: "route", UserInfo: ui,
		InboundProtocols: []llmkit.Protocol{llmkit.ProtocolOpenAIChat, llmkit.ProtocolClaude},
	}

	var first ModelOfferPlan
	for attempt := 0; attempt < 20; attempt++ {
		got, err := finder.Find(t.Context(), query)
		require.NoError(t, err)
		if attempt == 0 {
			first = got
		} else {
			require.Equal(t, first, got, "read-only finder must be deterministic")
		}
	}
	require.True(t, first.Routing)
	require.Equal(t, []string{"nested-real", "token-real"}, first.RealModels)
	require.Equal(t, []string{
		"nested-real:admin:1", "nested-real:admin:2", "token-real:admin:3",
	}, modelOfferCandidateKeys(first.Candidates))
	require.Equal(t, state.ModePassthrough, modelOfferMode(first.Candidates[0], llmkit.ProtocolOpenAIChat))
	require.Equal(t, state.ModeNative, modelOfferMode(first.Candidates[0], llmkit.ProtocolClaude))
	require.Equal(t, state.ModeLegacy, modelOfferMode(first.Candidates[1], llmkit.ProtocolOpenAIChat))

	// A high Relay budget exposes the same candidate identity/mode set. The
	// finder remains exhaustive even though the persisted RetryMax is one.
	loadPlanSettings(store, 100, 0)
	relayContext := modelOfferRelayContext(store, "route", ui, 0, llmkit.ProtocolOpenAIChat)
	require.NoError(t, NewSolver(nil).Solve(relayContext))
	require.Equal(t, modelOfferCandidateKeys(first.Candidates), attemptCandidateKeys(relayContext.State.Plan.Attempts))
	require.Equal(t,
		modelOfferModesByKey(first.Candidates, llmkit.ProtocolOpenAIChat),
		attemptModesByKey(relayContext.State.Plan.Attempts),
	)
}

func TestModelOfferPlanFinderExhaustsConvergingRoutingBranches(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	t.Cleanup(store.Close)
	store.SetGlobalRouting("route", &protocol.SyncedRouting{
		ID: 201, Name: "route", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "branch-a", Priority: 20, Weight: 1},
			{Ref: "branch-b", Priority: 10, Weight: 1},
		},
	})
	store.SetGlobalRouting("branch-a", &protocol.SyncedRouting{
		ID: 202, Name: "branch-a", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "shared-model", Priority: 1, Weight: 1}},
	})
	store.SetGlobalRouting("branch-b", &protocol.SyncedRouting{
		ID: 203, Name: "branch-b", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "shared-model", Priority: 20, Weight: 1},
			{Ref: "unique-model", Priority: 10, Weight: 1},
		},
	})
	addPlanChannel(store, 1, "shared-model", true, models.ChannelCore{})
	addPlanChannel(store, 2, "unique-model", true, models.ChannelCore{})
	store.RebuildModelIndex()

	got, err := NewModelOfferPlanFinder(store).Find(t.Context(), ModelOfferPlanQuery{Model: "route"})
	require.NoError(t, err)
	require.True(t, got.Routing)
	require.Equal(t, []string{"shared-model", "unique-model"}, got.RealModels)
	require.Equal(t, []string{
		"shared-model:admin:1",
		"unique-model:admin:2",
	}, modelOfferCandidateKeys(got.Candidates))
}

func TestModelOfferPlanFinderFailsClosedWhenPlannerOrQuotaFactsAreUnavailable(t *testing.T) {
	_, err := NewModelOfferPlanFinder(nil).Find(t.Context(), ModelOfferPlanQuery{Model: "model-a"})
	require.ErrorIs(t, err, ErrModelOfferPlanUnavailable)

	missingConfigStore := cache.NewStore(nil, config.AgentCacheConfig{})
	t.Cleanup(missingConfigStore.Close)
	addPlanChannel(missingConfigStore, 1, "model-a", false, models.ChannelCore{})
	missingConfigStore.SetUser(&protocol.SyncedUser{ID: 7, GroupID: 2, Quota: 1_000})
	missingConfigStore.RebuildModelIndex()
	loadPlanSettings(missingConfigStore, 100, 0)
	missingConfigPlan, missingConfigErr := NewModelOfferPlanFinder(missingConfigStore).Find(
		t.Context(),
		ModelOfferPlanQuery{Model: "model-a", UserInfo: &app.UserInfo{UserID: 7, TokenID: 17}},
	)
	require.ErrorIs(t, missingConfigErr, ErrModelOfferPlanFactsUnavailable)
	require.Empty(t, missingConfigPlan.Candidates)

	store := cache.NewStore(nil, config.AgentCacheConfig{})
	t.Cleanup(store.Close)
	addPlanChannel(store, 1, "model-a", false, models.ChannelCore{})
	store.SetModelConfig(&models.ModelConfig{ModelName: "model-a", InputPrice: 1})
	store.RebuildModelIndex()
	loadPlanSettings(store, 100, 0)

	got, err := NewModelOfferPlanFinder(store).Find(t.Context(), ModelOfferPlanQuery{
		Model: "model-a", UserInfo: &app.UserInfo{UserID: 404, TokenID: 17},
	})
	require.ErrorIs(t, err, ErrModelOfferPlanFactsUnavailable)
	require.Empty(t, got.Candidates)
}

func addPlanChannel(store *cache.Store, id uint, model string, free bool, core models.ChannelCore) {
	core.ID = id
	core.Status = consts.StatusEnabled
	if core.Weight == 0 {
		core.Weight = 1
	}
	store.SetChannel(&models.Channel{ChannelCore: core, Models: model, Free: free})
}

func loadPlanSettings(store *cache.Store, retryMax int, reserve int64) {
	store.LoadSettings([]models.Setting{
		{Key: "retry_max_channels", Value: stringInt(retryMax)},
		{Key: consts.SettingKeyMinQuotaReserve, Value: stringInt64(reserve)},
		{Key: consts.SettingKeyBYOKBillingMode, Value: consts.BYOKBillingModeServiceFee},
	})
}

func modelOfferRelayContext(
	store *cache.Store,
	model string,
	ui *app.UserInfo,
	forcedID uint,
	protocol llmkit.Protocol,
) *state.RelayContext {
	agentApplication := agentapp.NewDefaultAgentApplication(
		store, nil, zap.NewNop(), &config.AgentRuntimeConfig{}, nil,
	)
	rctx := newTestRelayContext(agentApplication.GetCache(), model, ui, forcedID)
	rctx.Input.InboundProto = protocol
	return rctx
}

func modelOfferCandidateKeys(candidates []ModelOfferCandidate) []string {
	var keys []string
	for _, candidate := range candidates {
		keys = append(keys, candidate.RealModel+":"+string(candidate.Source)+":"+stringUint(candidate.SourceID))
	}
	sort.Strings(keys)
	return keys
}

func attemptCandidateKeys(attempts []state.Attempt) []string {
	var keys []string
	for _, attempt := range attempts {
		keys = append(keys, attempt.RealModel+":"+string(attempt.Source)+":"+stringUint(attempt.SourceID))
	}
	sort.Strings(keys)
	return keys
}

func modelOfferMode(candidate ModelOfferCandidate, protocol llmkit.Protocol) state.RelayMode {
	for _, mode := range candidate.Modes {
		if mode.Protocol == protocol {
			return mode.Mode
		}
	}
	return ""
}

func modelOfferModesByKey(
	candidates []ModelOfferCandidate,
	protocol llmkit.Protocol,
) map[string]state.RelayMode {
	modes := make(map[string]state.RelayMode, len(candidates))
	for _, candidate := range candidates {
		key := candidate.RealModel + ":" + string(candidate.Source) + ":" + stringUint(candidate.SourceID)
		modes[key] = modelOfferMode(candidate, protocol)
	}
	return modes
}

func attemptModesByKey(attempts []state.Attempt) map[string]state.RelayMode {
	modes := make(map[string]state.RelayMode, len(attempts))
	for _, attempt := range attempts {
		key := attempt.RealModel + ":" + string(attempt.Source) + ":" + stringUint(attempt.SourceID)
		modes[key] = attempt.Mode
	}
	return modes
}

func stringInt(value int) string     { return strconv.Itoa(value) }
func stringInt64(value int64) string { return strconv.FormatInt(value, 10) }
func stringUint(value uint) string   { return strconv.FormatUint(uint64(value), 10) }
