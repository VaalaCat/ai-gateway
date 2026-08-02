package model_marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestRoutingModelFinderFlattensRoutesAndStablyDeduplicatesDestinations(t *testing.T) {
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(3, "z-direct", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("real-b", 0, 1))),
		routingRow(2, "nested", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("real-a", 1, 2), member("real-a", 0, 1))),
		routingRow(1, "a-smart", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("real-b", 0, 1), member("nested", 0, 1), member("real-b", -1, 1))),
	}}
	offers := map[string][]ModelOffer{
		"real-a": {
			routingOffer("real-a", "p:z", OfferKindPlatform, 9, "Zulu"),
			routingOffer("real-a", "p:a", OfferKindPlatform, 1, "Alpha"),
			routingOffer("real-a", "p:a", OfferKindPlatform, 1, "Alpha"),
		},
		"real-b": {routingOffer("real-b", "b:b", OfferKindPrivate, 2, "BYOK")},
	}
	viewer := MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 70, UserID: 7}, GroupIDs: []uint{4, 3},
	}

	got, err := NewRoutingModelFinder(query).Find(context.Background(), viewer, offers)
	require.NoError(t, err)
	require.Equal(t, []string{"a-smart", "nested", "z-direct"}, routingModelNames(got))
	require.Equal(t, []string{"real-a", "real-b"}, got[0].ReachableRealModels)
	require.Equal(t, []string{"real-a", "real-b"}, destinationNames(got[0].FlattenedDestinations))
	require.Equal(t, []string{"p:a", "p:z"}, summaryRefs(got[0].FlattenedDestinations[0].Offers))
	require.Empty(t, got[0].RoutingWarnings)
	require.Equal(t, RoutingModelGuidanceViewReachableRealModels, got[0].Guidance)
	require.Equal(t, 1, query.calls, "routing rows must be batch-loaded once, not once per route/model")
	require.Equal(t, dao.MarketplaceRoutingScope{
		UserID: 7, TokenID: 70, GroupIDs: []uint{4, 3},
	}, query.scopes[0])
}

func TestRoutingModelFinderUsesTokenUserGlobalPrecedenceAndGlobalOnlyRecursion(t *testing.T) {
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(1, "smart", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("global-real", 0, 1))),
		routingRow(2, "smart", models.RoutingScopeUser, 7, 0, true, routingMembers(member("user-real", 0, 1))),
		routingRow(3, "smart", models.RoutingScopeToken, 0, 70, true, routingMembers(member("token-real", 0, 1))),
		routingRow(4, "fallback-user", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("global-real", 0, 1))),
		routingRow(5, "fallback-user", models.RoutingScopeUser, 7, 0, true, routingMembers(member("user-real", 0, 1))),
		routingRow(6, "fallback-user", models.RoutingScopeToken, 0, 70, false, routingMembers(member("token-real", 0, 1))),
		routingRow(7, "fallback-global", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("global-real", 0, 1))),
		routingRow(8, "fallback-global", models.RoutingScopeUser, 7, 0, false, routingMembers(member("user-real", 0, 1))),
		routingRow(9, "outer", models.RoutingScopeToken, 0, 70, true, routingMembers(member("nested", 0, 1))),
		routingRow(10, "nested", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("global-real", 0, 1))),
		routingRow(11, "nested", models.RoutingScopeUser, 7, 0, true, routingMembers(member("user-real", 0, 1))),
		routingRow(12, "other-tenant", models.RoutingScopeUser, 8, 0, true, routingMembers(member("hidden-real", 0, 1))),
	}}
	offers := routingOffers("global-real", "user-real", "token-real", "hidden-real")

	got, err := NewRoutingModelFinder(query).Find(context.Background(), MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 70, UserID: 7}, GroupIDs: []uint{4},
	}, offers)
	require.NoError(t, err)
	require.Equal(t, []string{"fallback-global", "fallback-user", "nested", "outer", "smart"}, routingModelNames(got))
	require.Equal(t, []string{"global-real"}, findRoutingModel(t, got, "fallback-global").ReachableRealModels)
	require.Equal(t, []string{"user-real"}, findRoutingModel(t, got, "fallback-user").ReachableRealModels)
	require.Equal(t, []string{"token-real"}, findRoutingModel(t, got, "smart").ReachableRealModels)
	require.Equal(t, []string{"global-real"}, findRoutingModel(t, got, "outer").ReachableRealModels,
		"nested members resolve only global routes, never same-name user routes")
	require.NotContains(t, routingModelNames(got), "other-tenant")
}

func TestRoutingModelFinderFiltersTopLevelAliasesByEffectiveModelWhitelistBeforeWalking(t *testing.T) {
	validMembers := routingMembers(member("real", 0, 1))
	malformedMembers := `{must-not-be-parsed`
	tests := []struct {
		name       string
		viewer     MarketplaceViewer
		routes     []models.ModelRouting
		wantRoutes []string
	}{
		{
			name: "token whitelist",
			viewer: func() MarketplaceViewer {
				viewer := scopedMarketplaceViewer(7, 70)
				viewer.AllowedModels.TokenPatterns = []string{"token-route"}
				return viewer
			}(),
			routes: []models.ModelRouting{
				routingRow(1, "token-route", models.RoutingScopeGlobal, 0, 0, true, validMembers),
				routingRow(2, "denied-malformed", models.RoutingScopeGlobal, 0, 0, true, malformedMembers),
			},
			wantRoutes: []string{"token-route"},
		},
		{
			name: "group whitelist",
			viewer: func() MarketplaceViewer {
				viewer := scopedMarketplaceViewer(7, 70)
				viewer.AllowedModels.GroupPatterns = []string{"group-route"}
				return viewer
			}(),
			routes: []models.ModelRouting{
				routingRow(1, "group-route", models.RoutingScopeGlobal, 0, 0, true, validMembers),
				routingRow(2, "denied-malformed", models.RoutingScopeGlobal, 0, 0, true, malformedMembers),
			},
			wantRoutes: []string{"group-route"},
		},
		{
			name: "token and group intersection",
			viewer: func() MarketplaceViewer {
				viewer := scopedMarketplaceViewer(7, 70)
				viewer.AllowedModels.TokenPatterns = []string{"token-only", "both"}
				viewer.AllowedModels.GroupPatterns = []string{"group-only", "both"}
				return viewer
			}(),
			routes: []models.ModelRouting{
				routingRow(1, "both", models.RoutingScopeGlobal, 0, 0, true, validMembers),
				routingRow(2, "token-only", models.RoutingScopeGlobal, 0, 0, true, malformedMembers),
				routingRow(3, "group-only", models.RoutingScopeGlobal, 0, 0, true, malformedMembers),
			},
			wantRoutes: []string{"both"},
		},
		{
			name:   "administrator global is unrestricted",
			viewer: MarketplaceViewer{AdminGlobal: true},
			routes: []models.ModelRouting{
				routingRow(1, "alpha", models.RoutingScopeGlobal, 0, 0, true, validMembers),
				routingRow(2, "zeta", models.RoutingScopeGlobal, 0, 0, true, validMembers),
			},
			wantRoutes: []string{"alpha", "zeta"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &fakeRoutingModelQuery{routings: test.routes}
			got, err := NewRoutingModelFinder(query).Find(context.Background(), test.viewer, routingOffers("real"))
			require.NoError(t, err)
			require.Equal(t, test.wantRoutes, routingModelNames(got))
			require.Equal(t, 1, query.calls)
		})
	}
}

func TestRoutingModelFinderTreatsDisabledGlobalAsRealOnlyWhenVisibleModelExists(t *testing.T) {
	routes := []models.ModelRouting{
		routingRow(1, "root", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("shadow", 0, 1))),
		routingRow(2, "shadow", models.RoutingScopeGlobal, 0, 0, false, routingMembers(member("hidden", 0, 1))),
	}
	tests := []struct {
		name         string
		offers       map[string][]ModelOffer
		wantModels   []string
		wantWarnings []RoutingWarning
	}{
		{
			name: "same-name real with available offer is a destination",
			offers: map[string][]ModelOffer{
				"shadow": {routingOffer("shadow", "p:shadow", OfferKindPlatform, 1, "Platform")},
			},
			wantModels:   []string{"shadow"},
			wantWarnings: []RoutingWarning{},
		},
		{
			name:         "same-name real without available offer reports no visible offer",
			offers:       map[string][]ModelOffer{"shadow": {}},
			wantModels:   []string{},
			wantWarnings: []RoutingWarning{RoutingWarningNoVisibleOffer},
		},
		{
			name:         "disabled route without same-name real stays disabled",
			offers:       map[string][]ModelOffer{},
			wantModels:   []string{},
			wantWarnings: []RoutingWarning{RoutingWarningDisabled},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewRoutingModelFinder(&fakeRoutingModelQuery{routings: routes}).Find(
				context.Background(), scopedMarketplaceViewer(7, 70), test.offers,
			)
			require.NoError(t, err)
			root := findRoutingModel(t, got, "root")
			require.Equal(t, test.wantModels, root.ReachableRealModels)
			require.Equal(t, test.wantWarnings, root.RoutingWarnings)
		})
	}
}

// Break caught: projecting the walker's anonymous max-depth control sentinel
// as a configured member loses the actual ref, priority, and weight.
func TestRoutingModelFinderAllowsDepthFiveAndWarnsAtDepthSix(t *testing.T) {
	routes := []models.ModelRouting{
		routingRow(1, "depth-5", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d5-2", 0, 1))),
		routingRow(2, "d5-2", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d5-3", 0, 1))),
		routingRow(3, "d5-3", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d5-4", 0, 1))),
		routingRow(4, "d5-4", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d5-5", 0, 1))),
		routingRow(5, "d5-5", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("real", 0, 1))),
		routingRow(6, "depth-6", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d6-2", 0, 1))),
		routingRow(7, "d6-2", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d6-3", 0, 1))),
		routingRow(8, "d6-3", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d6-4", 0, 1))),
		routingRow(9, "d6-4", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d6-5", 0, 1))),
		routingRow(10, "d6-5", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d6-6", 0, 1))),
		routingRow(11, "d6-6", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("d6-7", 17, 9))),
		routingRow(12, "d6-7", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("real", 0, 1))),
	}
	got, err := NewRoutingModelFinder(&fakeRoutingModelQuery{routings: routes}).Find(
		context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"real"}, findRoutingModel(t, got, "depth-5").ReachableRealModels)
	require.Empty(t, findRoutingModel(t, got, "depth-5").RoutingWarnings)
	depthSix := findRoutingModel(t, got, "depth-6")
	require.Empty(t, depthSix.ReachableRealModels)
	require.Equal(t, []RoutingWarning{RoutingWarningMaxDepth}, depthSix.RoutingWarnings)

	require.Len(t, depthSix.Facts.Definitions, 6, "max-depth terminal must not create the next routing occurrence")
	deepest := depthSix.Facts.Definitions[5]
	require.Equal(t, "d6-6", deepest.Name)
	require.Equal(t, []RoutingMemberTargetFact{{
		Ref: "d6-7", Priority: 17, Weight: 9, Kind: RoutingMemberTargetInvalid,
	}}, deepest.Members, "max-depth terminal must retain the configured member identity")
	for _, definition := range depthSix.Facts.Definitions {
		require.NotEqual(t, "d6-7", definition.Name, "the truncated routing target must not become an occurrence")
		for _, member := range definition.Members {
			require.NotEmpty(t, member.Ref, "walker control sentinels must not appear as configured members")
		}
	}

	admin := mapAdminRoutingModel(depthSix)
	require.Len(t, admin.Routing.Diagnostics.Definitions, 6)
	terminalMemberJSON, marshalErr := json.Marshal(admin.Routing.Diagnostics.Definitions[5].Members[0])
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{
		"ref":"d6-7","priority":17,"weight":9,"kind":"invalid"
	}`, string(terminalMemberJSON))
	var terminalMember map[string]any
	require.NoError(t, json.Unmarshal(terminalMemberJSON, &terminalMember))
	require.Equal(t, []string{"kind", "priority", "ref", "weight"}, sortedJSONKeys(terminalMember))

	adminPayload, marshalErr := json.Marshal(admin)
	require.NoError(t, marshalErr)
	for _, forbidden := range []string{"key", "cipher", "credential"} {
		require.NotContains(t, strings.ToLower(string(adminPayload)), forbidden)
	}
	ordinaryPayload, marshalErr := json.Marshal(mapUserRoutingModel(depthSix))
	require.NoError(t, marshalErr)
	for _, forbidden := range []string{
		"diagnostics", "occurrence_id", "path", "routing_id", "priority", "weight", "credential",
	} {
		require.NotContains(t, string(ordinaryPayload), forbidden)
	}
}

func TestRoutingModelFinderUsesPathLocalCycleDetectionAndSameNameRuntimeSemantics(t *testing.T) {
	routes := []models.ModelRouting{
		routingRow(1, "cycle-root", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("cycle-a", 0, 1), member("real", 0, 1))),
		routingRow(2, "cycle-a", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("cycle-b", 0, 1))),
		routingRow(3, "cycle-b", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("cycle-a", 0, 1))),
		routingRow(4, "same", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("same", 0, 1))),
		routingRow(5, "off-path", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("nested-same", 0, 1))),
		routingRow(6, "nested-same", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("real", 0, 1))),
		routingRow(7, "shared", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("real", 0, 1))),
		routingRow(8, "two-paths", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("shared", 0, 1), member("shared", 0, 1))),
	}
	offers := routingOffers("real", "same", "nested-same")
	got, err := NewRoutingModelFinder(&fakeRoutingModelQuery{routings: routes}).Find(
		context.Background(), scopedMarketplaceViewer(7, 70), offers,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"real"}, findRoutingModel(t, got, "cycle-root").ReachableRealModels)
	require.Equal(t, []RoutingWarning{RoutingWarningCycle}, findRoutingModel(t, got, "cycle-root").RoutingWarnings)
	require.Equal(t, []string{"same"}, findRoutingModel(t, got, "same").ReachableRealModels,
		"a route revisiting itself may terminate at a same-name real model")
	require.Equal(t, []string{"real"}, findRoutingModel(t, got, "off-path").ReachableRealModels,
		"an off-path route with the same name as a real model still expands as a route")
	require.Equal(t, []string{"real"}, findRoutingModel(t, got, "two-paths").ReachableRealModels,
		"visited state is path-local and destinations are deduplicated")
}

// Break caught: rebuilding administrator member kinds from the global routing
// index loses the walker's path-local same-name decision.
func TestRoutingModelFinderFactsRetainPathLocalMemberTargetsFromWalk(t *testing.T) {
	routes := []models.ModelRouting{
		routingRow(1, "same", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("same", 10, 2))),
		routingRow(2, "outer", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("nested-same", 7, 3))),
		routingRow(3, "nested-same", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("real", 5, 1))),
	}
	got, err := NewRoutingModelFinder(&fakeRoutingModelQuery{routings: routes}).Find(
		context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("same", "nested-same", "real"),
	)
	require.NoError(t, err)

	sameFacts := findRoutingModel(t, got, "same").Facts.Definitions
	require.Len(t, sameFacts, 1)
	require.Equal(t, []RoutingMemberTargetFact{{
		Ref: "same", Priority: 10, Weight: 2,
		Kind: RoutingMemberTargetModel, ModelName: "same",
	}}, sameFacts[0].Members)
	sameAdmin := mapAdminRoutingModel(findRoutingModel(t, got, "same"))
	sameMemberJSON, marshalErr := json.Marshal(sameAdmin.Routing.Diagnostics.Definitions[0].Members[0])
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{
		"ref":"same","priority":10,"weight":2,"kind":"model","model_name":"same"
	}`, string(sameMemberJSON), "path revisit must not emit routing_id")

	outerFacts := findRoutingModel(t, got, "outer").Facts.Definitions
	require.Equal(t, []RoutingDefinitionFact{
		{
			OccurrenceID: "root:2",
			Path:         []RoutingPathStepFact{{Ref: "outer", RoutingID: 2}},
			RoutingID:    2, Name: "outer", Scope: models.RoutingScopeGlobal, Enabled: true,
			Members: []RoutingMemberTargetFact{{
				Ref: "nested-same", Priority: 7, Weight: 3,
				Kind: RoutingMemberTargetRouting, RoutingID: 3,
			}},
		},
		{
			OccurrenceID: "root:2/0:3",
			Path: []RoutingPathStepFact{
				{Ref: "outer", RoutingID: 2},
				{Ref: "nested-same", RoutingID: 3},
			},
			RoutingID: 3, Name: "nested-same", Scope: models.RoutingScopeGlobal, Enabled: true,
			Members: []RoutingMemberTargetFact{{
				Ref: "real", Priority: 5, Weight: 1,
				Kind: RoutingMemberTargetModel, ModelName: "real",
			}},
		},
	}, outerFacts)
	outerAdmin := mapAdminRoutingModel(findRoutingModel(t, got, "outer"))
	outerMemberJSON, marshalErr := json.Marshal(outerAdmin.Routing.Diagnostics.Definitions[0].Members[0])
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{
		"ref":"nested-same","priority":7,"weight":3,"kind":"routing","routing_id":3
	}`, string(outerMemberJSON), "off-path routing must not emit model_name")
}

// Break caught: globally de-duplicating a concrete definition keeps only the
// first path's target facts, even when the shared node makes a different
// path-local decision later in the same root walk.
func TestRoutingModelFinderProjectsEverySharedDefinitionOccurrence(t *testing.T) {
	routes := []models.ModelRouting{
		routingRow(1, "root", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("branch", 20, 1), member("other", 10, 1))),
		routingRow(2, "branch", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("shared", 0, 1))),
		routingRow(3, "other", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("shared", 0, 1))),
		routingRow(4, "shared", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("branch", 0, 1))),
	}
	got, err := NewRoutingModelFinder(&fakeRoutingModelQuery{routings: routes}).Find(
		t.Context(), scopedMarketplaceViewer(7, 70), routingOffers("branch"),
	)
	require.NoError(t, err)

	root := findRoutingModel(t, got, "root")
	sharedFacts := make([]RoutingDefinitionFact, 0, 2)
	for _, definition := range root.Facts.Definitions {
		if definition.Name == "shared" {
			sharedFacts = append(sharedFacts, definition)
		}
	}
	require.GreaterOrEqual(t, len(sharedFacts), 2, "both shared walker occurrences must be retained")
	require.Equal(t, RoutingMemberTargetModel, sharedFacts[0].Members[0].Kind)
	require.Equal(t, "branch", sharedFacts[0].Members[0].ModelName)
	require.Equal(t, RoutingMemberTargetRouting, sharedFacts[1].Members[0].Kind)
	require.Equal(t, uint(2), sharedFacts[1].Members[0].RoutingID)

	type pathStep struct {
		Ref       string `json:"ref"`
		RoutingID uint   `json:"routing_id"`
	}
	type diagnosticMember struct {
		Kind      RoutingMemberTargetKind `json:"kind"`
		ModelName string                  `json:"model_name"`
		RoutingID uint                    `json:"routing_id"`
	}
	type diagnosticDefinition struct {
		Name         string             `json:"name"`
		OccurrenceID string             `json:"occurrence_id"`
		Path         []pathStep         `json:"path"`
		Members      []diagnosticMember `json:"members"`
	}
	var admin struct {
		Routing struct {
			Diagnostics struct {
				Definitions []diagnosticDefinition `json:"definitions"`
			} `json:"diagnostics"`
		} `json:"routing"`
	}
	raw, marshalErr := json.Marshal(mapAdminRoutingModel(root))
	require.NoError(t, marshalErr)
	require.NoError(t, json.Unmarshal(raw, &admin))

	shared := make([]diagnosticDefinition, 0, 2)
	for _, definition := range admin.Routing.Diagnostics.Definitions {
		if definition.Name == "shared" {
			shared = append(shared, definition)
		}
	}
	require.GreaterOrEqual(t, len(shared), 2)
	require.Equal(t, "root:1/0:2/0:4", shared[0].OccurrenceID)
	require.Equal(t, []pathStep{
		{Ref: "root", RoutingID: 1},
		{Ref: "branch", RoutingID: 2},
		{Ref: "shared", RoutingID: 4},
	}, shared[0].Path)
	require.Equal(t, RoutingMemberTargetModel, shared[0].Members[0].Kind)
	require.Equal(t, "branch", shared[0].Members[0].ModelName)
	require.Equal(t, "root:1/1:3/0:4", shared[1].OccurrenceID)
	require.Equal(t, []pathStep{
		{Ref: "root", RoutingID: 1},
		{Ref: "other", RoutingID: 3},
		{Ref: "shared", RoutingID: 4},
	}, shared[1].Path)
	require.Equal(t, RoutingMemberTargetRouting, shared[1].Members[0].Kind)
	require.Equal(t, uint(2), shared[1].Members[0].RoutingID)
}

func TestRoutingModelFinderAggregatesSafeWarningsWithoutLeakingReferences(t *testing.T) {
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(1, "diagnostic", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("secret-disabled", 0, 1), member("secret-missing", 0, 1), member("visible-without-offer", 0, 1))),
		routingRow(2, "secret-disabled", models.RoutingScopeGlobal, 0, 0, false,
			routingMembers(member("hidden-real", 99, 88))),
	}}
	got, err := NewRoutingModelFinder(query).Find(context.Background(), scopedMarketplaceViewer(7, 70), map[string][]ModelOffer{
		"visible-without-offer": {},
	})
	require.NoError(t, err)
	diagnostic := findRoutingModel(t, got, "diagnostic")
	require.Empty(t, diagnostic.FlattenedDestinations)
	require.Equal(t, []RoutingWarning{
		RoutingWarningDisabled, RoutingWarningModelNotFound, RoutingWarningNoVisibleOffer,
	}, diagnostic.RoutingWarnings)
	require.Equal(t, []RoutingMemberTargetKind{
		RoutingMemberTargetRouting, RoutingMemberTargetInvalid, RoutingMemberTargetModel,
	}, []RoutingMemberTargetKind{
		diagnostic.Facts.Definitions[0].Members[0].Kind,
		diagnostic.Facts.Definitions[0].Members[1].Kind,
		diagnostic.Facts.Definitions[0].Members[2].Kind,
	})
	require.Len(t, diagnostic.Facts.Definitions, 2, "disabled routing remains an occurrence")
	require.Equal(t, "secret-disabled", diagnostic.Facts.Definitions[1].Name)
	require.Empty(t, diagnostic.Facts.Definitions[1].Members, "disabled occurrence must not re-walk children")

	raw, err := json.Marshal(diagnostic)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret-disabled")
	require.NotContains(t, string(raw), "secret-missing")
	require.NotContains(t, string(raw), "hidden-real")
	require.NotContains(t, string(raw), "99")
	require.NotContains(t, string(raw), "88")
}

func TestRoutingModelFinderAdminGlobalDiagnosesGlobalDefinitionsOnly(t *testing.T) {
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(1, "enabled-global", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("real", 0, 1))),
		routingRow(2, "disabled-global", models.RoutingScopeGlobal, 0, 0, false, routingMembers(member("real", 0, 1))),
		routingRow(3, "tenant-secret", models.RoutingScopeUser, 9, 0, true, routingMembers(member("real", 0, 1))),
		routingRow(4, "token-secret", models.RoutingScopeToken, 0, 90, true, routingMembers(member("real", 0, 1))),
	}}
	got, err := NewRoutingModelFinder(query).Find(
		context.Background(), MarketplaceViewer{AdminGlobal: true}, routingOffers("real"),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"disabled-global", "enabled-global"}, routingModelNames(got))
	require.Equal(t, []RoutingWarning{RoutingWarningDisabled}, findRoutingModel(t, got, "disabled-global").RoutingWarnings)
	require.Equal(t, dao.MarketplaceRoutingScope{AdminGlobal: true}, query.scopes[0])
}

func TestRoutingModelFinderSerializesOnlySafeRoutingAndOfferSummaryFields(t *testing.T) {
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(41, "safe-route", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("real", 123, 456))),
	}}
	got, err := NewRoutingModelFinder(query).Find(
		context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"),
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	raw, err := json.Marshal(got[0])
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, []string{
		"display_name", "flattened_destinations", "guidance", "model_name", "reachable_real_models", "routing_warnings",
	}, sortedJSONKeys(payload))
	require.NotContains(t, payload, "pricing")
	require.NotContains(t, payload, "performance")
	require.NotContains(t, payload, "status")
	require.NotContains(t, payload, "status_history")
	require.NotContains(t, payload, "trend")
	require.NotContains(t, payload, "usage")
	require.NotContains(t, payload, "scope")
	require.NotContains(t, payload, "priority")
	require.NotContains(t, payload, "weight")
	require.NotContains(t, payload, "rule_id")

	destination := payload["flattened_destinations"].([]any)[0].(map[string]any)
	require.Equal(t, []string{"model_name", "offers"}, sortedJSONKeys(destination))
	summary := destination["offers"].([]any)[0].(map[string]any)
	require.Equal(t, []string{
		"available", "display_name", "kind", "offer_ref", "ownership", "supported_endpoints",
	}, sortedJSONKeys(summary))
	require.Equal(t, string(RoutingModelGuidanceViewReachableRealModels), payload["guidance"])
	require.NotContains(t, string(raw), "123")
	require.NotContains(t, string(raw), "456")
	require.NotContains(t, string(raw), "source_id")
}

func TestRoutingModelFinderFailsClosedForInvalidViewerBeforeQuery(t *testing.T) {
	tests := []struct {
		name   string
		viewer MarketplaceViewer
	}{
		{name: "zero", viewer: MarketplaceViewer{}},
		{name: "admin with user", viewer: MarketplaceViewer{AdminGlobal: true, UserID: 7}},
		{name: "admin with token", viewer: MarketplaceViewer{AdminGlobal: true, Token: &models.Token{ID: 70}}},
		{name: "admin with group", viewer: MarketplaceViewer{AdminGlobal: true, GroupIDs: []uint{4}}},
		{name: "admin with channel rules", viewer: MarketplaceViewer{AdminGlobal: true, AllowedChannelIDs: []uint{1}}},
		{name: "admin with model rules", viewer: MarketplaceViewer{
			AdminGlobal: true, AllowedModels: MarketplaceModelWhitelist{TokenPatterns: []string{"gpt-.*"}},
		}},
		{name: "admin byok only", viewer: MarketplaceViewer{AdminGlobal: true, BYOKOnly: true}},
		{name: "zero token ID", viewer: MarketplaceViewer{Token: &models.Token{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &fakeRoutingModelQuery{}
			got, err := NewRoutingModelFinder(query).Find(context.Background(), tt.viewer, routingOffers("real"))
			require.ErrorContains(t, err, "marketplace viewer")
			require.Nil(t, got)
			require.Zero(t, query.calls)
		})
	}
}

func TestRoutingModelFinderPropagatesQueryAndInvalidMemberErrors(t *testing.T) {
	t.Run("nil query", func(t *testing.T) {
		got, err := NewRoutingModelFinder(nil).Find(context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"))
		require.ErrorContains(t, err, "routing model query")
		require.Nil(t, got)
	})
	t.Run("query error", func(t *testing.T) {
		query := &fakeRoutingModelQuery{err: errors.New("routing database unavailable")}
		got, err := NewRoutingModelFinder(query).Find(context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"))
		require.ErrorContains(t, err, "routing database unavailable")
		require.Nil(t, got)
		require.Equal(t, 1, query.calls)
	})
	t.Run("invalid JSON", func(t *testing.T) {
		query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
			routingRow(1, "broken", models.RoutingScopeGlobal, 0, 0, true, `{not-json`),
		}}
		got, err := NewRoutingModelFinder(query).Find(context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"))
		require.ErrorContains(t, err, "invalid routing members")
		require.Nil(t, got)
	})
	t.Run("empty members", func(t *testing.T) {
		query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
			routingRow(1, "broken", models.RoutingScopeGlobal, 0, 0, true, `[]`),
		}}
		got, err := NewRoutingModelFinder(query).Find(context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"))
		require.ErrorContains(t, err, "members are empty")
		require.Nil(t, got)
	})
	t.Run("empty ref", func(t *testing.T) {
		query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
			routingRow(1, "broken", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("", 0, 1))),
		}}
		got, err := NewRoutingModelFinder(query).Find(context.Background(), scopedMarketplaceViewer(7, 70), routingOffers("real"))
		require.ErrorContains(t, err, "empty routing member ref")
		require.Nil(t, got)
	})
}

func TestRoutingModelFinderRejectsOfferIdentityOutsideItsModelBucket(t *testing.T) {
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(1, "route", models.RoutingScopeGlobal, 0, 0, true, routingMembers(member("real", 0, 1))),
	}}
	offer := routingOffer("other-model", "p:a", OfferKindPlatform, 1, "Platform")
	got, err := NewRoutingModelFinder(query).Find(context.Background(), scopedMarketplaceViewer(7, 70), map[string][]ModelOffer{
		"real": {offer},
	})
	require.ErrorContains(t, err, "offer identity")
	require.Nil(t, got)
}

func TestRoutingModelFinderEnforcesGlobalBidirectionalOfferUniqueness(t *testing.T) {
	finder := NewRoutingModelFinder(&fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(1, "route", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("real-a", 0, 1), member("real-b", 0, 1))),
	}})
	viewer := scopedMarketplaceViewer(7, 70)
	baseA := routingOffer("real-a", "p:shared", OfferKindPlatform, 1, "Platform A")
	baseB := routingOffer("real-b", "p:other", OfferKindPlatform, 2, "Platform B")

	tests := []struct {
		name       string
		offers     map[string][]ModelOffer
		wantErr    string
		wantModels []string
		wantRefs   []string
	}{
		{
			name: "cross-model same ref",
			offers: map[string][]ModelOffer{
				"real-a": {baseA},
				"real-b": {func() ModelOffer { offer := baseB; offer.OfferRef = baseA.OfferRef; return offer }()},
			},
			wantErr: "offer reference",
		},
		{
			name: "same-model different-source same ref",
			offers: map[string][]ModelOffer{
				"real-a": {baseA, func() ModelOffer {
					offer := baseA
					offer.Identity.SourceID = 99
					return offer
				}()},
			},
			wantErr: "offer reference",
		},
		{
			name: "same identity with multiple refs",
			offers: map[string][]ModelOffer{
				"real-a": {baseA, func() ModelOffer { offer := baseA; offer.OfferRef = "p:other-ref"; return offer }()},
			},
			wantErr: "offer identity",
		},
		{
			name: "same identity and ref with different public summary",
			offers: map[string][]ModelOffer{
				"real-a": {baseA, func() ModelOffer { offer := baseA; offer.DisplayName = "Changed"; return offer }()},
			},
			wantErr: "offer reference",
		},
		{
			name: "exact duplicates are stable and deduplicated",
			offers: map[string][]ModelOffer{
				"real-b": {baseB, baseB},
				"real-a": {baseA, baseA},
			},
			wantModels: []string{"real-a", "real-b"},
			wantRefs:   []string{"p:shared"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := finder.Find(context.Background(), viewer, test.offers)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.Nil(t, got)

				reversed, reversedErr := finder.Find(context.Background(), viewer, reverseRoutingOffers(test.offers))
				require.ErrorContains(t, reversedErr, test.wantErr)
				require.Nil(t, reversed, "reversing every offer slice must not change conflict handling")
				return
			}
			require.NoError(t, err)
			route := findRoutingModel(t, got, "route")
			require.Equal(t, test.wantModels, route.ReachableRealModels)
			require.Equal(t, test.wantRefs, summaryRefs(route.FlattenedDestinations[0].Offers))
		})
	}

	forward := map[string][]ModelOffer{"real-a": {baseA, baseA}, "real-b": {baseB, baseB}}
	reverse := map[string][]ModelOffer{"real-b": {baseB, baseB}, "real-a": {baseA, baseA}}
	forwardResult, err := finder.Find(context.Background(), viewer, forward)
	require.NoError(t, err)
	reverseResult, err := finder.Find(context.Background(), viewer, reverse)
	require.NoError(t, err)
	require.Equal(t, forwardResult, reverseResult, "map insertion and slice duplication order must not affect output")
}

type fakeRoutingModelQuery struct {
	routings []models.ModelRouting
	err      error
	calls    int
	scopes   []dao.MarketplaceRoutingScope
}

func (q *fakeRoutingModelQuery) ListMarketplaceRoutings(
	_ context.Context,
	scope dao.MarketplaceRoutingScope,
) ([]models.ModelRouting, error) {
	q.calls++
	q.scopes = append(q.scopes, scope)
	return append([]models.ModelRouting(nil), q.routings...), q.err
}

func routingRow(id uint, name, scope string, userID, tokenID uint, enabled bool, members string) models.ModelRouting {
	return models.ModelRouting{
		ID: id, Name: name, Scope: scope, UserID: userID, TokenID: tokenID, Enabled: enabled, Members: members,
	}
}

func member(ref string, priority, weight int) models.RoutingMember {
	return models.RoutingMember{Ref: ref, Priority: priority, Weight: weight}
}

func routingMembers(members ...models.RoutingMember) string {
	raw, err := json.Marshal(members)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func routingOffer(modelName, ref string, kind ModelOfferKind, sourceID uint, displayName string) ModelOffer {
	ownership := OfferPlatform
	if kind == OfferKindPrivate {
		ownership = OfferOwned
	}
	return ModelOffer{
		OfferRef: ref, Kind: kind, DisplayName: displayName, Ownership: ownership, Available: true,
		SupportedEndpoints: []SupportedEndpoint{EndpointChatCompletions},
		Identity:           ModelOfferIdentity{ModelName: modelName, Kind: kind, SourceID: sourceID},
	}
}

func routingOffers(modelNames ...string) map[string][]ModelOffer {
	result := make(map[string][]ModelOffer, len(modelNames))
	for i, modelName := range modelNames {
		result[modelName] = []ModelOffer{routingOffer(modelName, "p:"+modelName, OfferKindPlatform, uint(i+1), "Platform")}
	}
	return result
}

func routingModelNames(rows []RoutingModel) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.ModelName)
	}
	return result
}

func destinationNames(rows []FlattenedDestination) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.ModelName)
	}
	return result
}

func summaryRefs(rows []ModelOfferSummary) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.OfferRef)
	}
	return result
}

func findRoutingModel(t *testing.T, rows []RoutingModel, name string) RoutingModel {
	t.Helper()
	for _, row := range rows {
		if row.ModelName == name {
			return row
		}
	}
	t.Fatalf("routing model %q not found in %v", name, routingModelNames(rows))
	return RoutingModel{}
}

func scopedMarketplaceViewer(userID, tokenID uint) MarketplaceViewer {
	return MarketplaceViewer{UserID: userID, Token: &models.Token{ID: tokenID, UserID: userID}}
}

func reverseRoutingOffers(offersByModel map[string][]ModelOffer) map[string][]ModelOffer {
	reversed := make(map[string][]ModelOffer, len(offersByModel))
	for modelName, offers := range offersByModel {
		reversed[modelName] = append([]ModelOffer(nil), offers...)
		slices.Reverse(reversed[modelName])
	}
	return reversed
}
