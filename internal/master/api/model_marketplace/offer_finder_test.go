package model_marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestPlatformModelOfferFinderUsesPublicDisplayNameAndCurrentVisibleDuplicateSet(t *testing.T) {
	query := &fakeModelMarketplaceQuery{channels: []models.Channel{
		marketplacePlatformChannel(9, "internal-nine", " Cloud ", "gpt-4o", `{"chat_completions":"/v1/chat"}`),
		marketplacePlatformChannel(3, "internal-three", "Cloud", "gpt-4o", `{"chat_completions":"/v1/chat"}`),
		marketplacePlatformChannel(5, "platform-secret-name", "   ", "gpt-4o", `{"chat_completions":"/v1/chat"}`),
	}}
	finder := NewPlatformModelOfferFinder(query, mustTestOfferRefEncoder(t))
	modelsIn := []MarketplaceModel{{ModelName: "gpt-4o"}}

	visible, err := finder.Find(context.Background(), MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 70, UserID: 7}, AllowedChannelIDs: []uint{9, 3, 5},
	}, modelsIn)
	require.NoError(t, err)
	require.Equal(t, []string{"Cloud · 1", "平台来源", "Cloud · 2"}, offerDisplayNames(visible["gpt-4o"]))

	renumbered, err := finder.Find(context.Background(), MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 70, UserID: 7}, AllowedChannelIDs: []uint{9, 5},
	}, modelsIn)
	require.NoError(t, err)
	require.Equal(t, []string{"平台来源", "Cloud"}, offerDisplayNames(renumbered["gpt-4o"]))

	encoded, err := json.Marshal(visible["gpt-4o"])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "internal-nine")
	require.NotContains(t, string(encoded), "internal-three")
	require.NotContains(t, string(encoded), "platform-secret-name")
}

func TestSupportedEndpointParserUsesOnlyNonEmptyEndpointJSONInFixedOrder(t *testing.T) {
	query := &fakeModelMarketplaceQuery{channels: []models.Channel{
		marketplacePlatformChannel(1, "all", "All", "gpt-4o", `{
			"models":"/v1/models",
			"messages":" /v1/messages ",
			"responses":"/v1/responses",
			"chat_completions":"/v1/chat/completions",
			"unknown":"/secret/path"
		}`),
		{
			ChannelCore: models.ChannelCore{
				ID: 2, Name: "legacy-secret", Status: 1, Type: 14,
				SupportedAPITypes: `["chat_completions","responses"]`, Endpoints: `{}`,
			},
			PublicDisplayName: "Legacy", Models: "gpt-4o",
		},
		marketplacePlatformChannel(3, "empty-path", "Empty", "gpt-4o", `{"responses":"  "}`),
	}}
	finder := NewPlatformModelOfferFinder(query, mustTestOfferRefEncoder(t))
	offers, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	require.Len(t, offers["gpt-4o"], 3, "metadata enumeration must not decide runtime availability")
	require.Equal(t, []SupportedEndpoint{
		EndpointChatCompletions,
		EndpointResponses,
		EndpointMessages,
		EndpointModels,
	}, offers["gpt-4o"][0].SupportedEndpoints)
	require.Empty(t, offers["gpt-4o"][1].SupportedEndpoints)
	require.Empty(t, offers["gpt-4o"][2].SupportedEndpoints)

	encoded, err := json.Marshal(offers)
	require.NoError(t, err)
	for _, forbidden := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/models", "/secret/path", "supported_api_types", "type"} {
		require.NotContains(t, string(encoded), forbidden)
	}
}

func TestAdminGlobalOfferFinderRetainsEmptyEndpointsAsUnavailable(t *testing.T) {
	query := &fakeModelMarketplaceQuery{channels: []models.Channel{
		marketplacePlatformChannel(1, "internal", "Public", "gpt-4o", ""),
		{
			ChannelCore:       models.ChannelCore{ID: 2, Name: "disabled", Status: 0, Endpoints: `{"responses":"/responses"}`},
			PublicDisplayName: "Disabled",
			Models:            "gpt-4o",
		},
	}}
	finder := NewPlatformModelOfferFinder(query, mustTestOfferRefEncoder(t))

	offers, err := finder.Find(context.Background(), MarketplaceViewer{AdminGlobal: true}, []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	require.Len(t, offers["gpt-4o"], 2)
	require.False(t, offers["gpt-4o"][0].Available)
	require.Empty(t, offers["gpt-4o"][0].SupportedEndpoints)
	require.False(t, offers["gpt-4o"][1].Available)
	require.Equal(t, []SupportedEndpoint{EndpointResponses}, offers["gpt-4o"][1].SupportedEndpoints)

	preview, err := finder.Find(context.Background(), MarketplaceViewer{UserID: 7, Token: &models.Token{ID: 2, UserID: 7}}, []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	require.Len(t, preview["gpt-4o"], 2, "planner, not channel status or endpoint metadata, decides token-preview availability")
	require.False(t, preview["gpt-4o"][0].Available)
	require.Equal(t, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 2}, preview["gpt-4o"][1].Identity)
	require.False(t, preview["gpt-4o"][1].Available)
	require.Equal(t, []string{"disabled"}, preview["gpt-4o"][1].Facts.DisabledReasons)
}

func TestAdminGlobalPrivateOfferFinderRetainsDisabledSourceAsUnavailable(t *testing.T) {
	disabled := marketplacePrivateChannel(7, 8, "Disabled BYOK", "gpt-4o", `{"messages":"/messages"}`)
	disabled.Status = 0
	query := &fakeModelMarketplaceQuery{private: []models.PrivateChannel{disabled}}
	finder := NewPrivateModelOfferFinder(query, mustTestOfferRefEncoder(t))

	offers, err := finder.Find(context.Background(), MarketplaceViewer{AdminGlobal: true}, []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	require.Len(t, offers["gpt-4o"], 1)
	require.False(t, offers["gpt-4o"][0].Available)
	require.Equal(t, []SupportedEndpoint{EndpointMessages}, offers["gpt-4o"][0].SupportedEndpoints)

	preview, err := finder.Find(context.Background(), MarketplaceViewer{UserID: 7, Token: &models.Token{ID: 2, UserID: 7}}, []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	require.Len(t, preview["gpt-4o"], 1, "planner, not private-channel status metadata, decides token-preview availability")
	require.Equal(t, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 7}, preview["gpt-4o"][0].Identity)
	require.False(t, preview["gpt-4o"][0].Available)
	require.Equal(t, []string{"disabled"}, preview["gpt-4o"][0].Facts.DisabledReasons)
}

func TestPrivateModelOfferFinderMarksOwnedSharedAndUsesExistingUserFacingName(t *testing.T) {
	query := &fakeModelMarketplaceQuery{private: []models.PrivateChannel{
		marketplacePrivateChannel(4, 7, " My Key ", "gpt-4o", `{"responses":"/responses"}`),
		marketplacePrivateChannel(6, 8, "Shared Key", "gpt-4o", `{"messages":"/messages"}`),
		marketplacePrivateChannel(8, 7, "   ", "gpt-4o", `{"models":"/models"}`),
		marketplacePrivateChannel(10, 7, "", "gpt-4o", `{"chat_completions":"/chat"}`),
	}}
	finder := NewPrivateModelOfferFinder(query, mustTestOfferRefEncoder(t))
	offers, err := finder.Find(context.Background(), MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 70, UserID: 7}, GroupIDs: []uint{42},
	}, []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	require.Equal(t, []ModelOfferOwnership{OfferOwned, OfferShared, OfferOwned, OfferOwned}, offerOwnerships(offers["gpt-4o"]))
	require.Equal(t, []string{" My Key ", "Shared Key", "   ", "BYOK 来源"}, offerDisplayNames(offers["gpt-4o"]))
	require.Equal(t, []SupportedEndpoint{EndpointResponses}, offers["gpt-4o"][0].SupportedEndpoints)
	require.Equal(t, dao.MarketplacePrivateChannelScope{UserID: 7, GroupIDs: []uint{42}}, query.privateScopes[0])
}

func TestModelOfferJSONIsAWhitelistWithIdenticalPlatformAndPrivateShape(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		channels: []models.Channel{marketplacePlatformChannel(41, "platform-internal", "Public", "gpt-4o", `{"chat_completions":"/platform-secret-path"}`)},
		private: []models.PrivateChannel{{
			ChannelCore: models.ChannelCore{ID: 41, Name: "private-core-secret", Status: 1, BaseURL: "https://private-secret.example", Endpoints: `{"chat_completions":"/private-secret-path"}`},
			OwnerID:     7, Name: "My BYOK", Status: 1, KeyCipher: []byte("cipher-secret"),
			Models: datatypes.JSONSlice[string]{"gpt-4o"},
		}},
	}
	encoder := mustTestOfferRefEncoder(t)
	platform, err := NewPlatformModelOfferFinder(query, encoder).Find(context.Background(), scopedMarketplaceViewer(7, 70), []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)
	private, err := NewPrivateModelOfferFinder(query, encoder).Find(context.Background(), scopedMarketplaceViewer(7, 70), []MarketplaceModel{{ModelName: "gpt-4o"}})
	require.NoError(t, err)

	require.Len(t, platform["gpt-4o"], 1)
	require.Len(t, private["gpt-4o"], 1)
	require.Equal(t, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 41}, platform["gpt-4o"][0].Identity)
	require.Equal(t, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 41}, private["gpt-4o"][0].Identity)
	require.NotEqual(t, platform["gpt-4o"][0].OfferRef, private["gpt-4o"][0].OfferRef)
	platformJSON := marshalOfferMap(t, platform["gpt-4o"][0])
	privateJSON := marshalOfferMap(t, private["gpt-4o"][0])
	wantKeys := []string{"available", "display_name", "kind", "offer_ref", "ownership", "supported_endpoints"}
	require.Equal(t, wantKeys, sortedJSONKeys(platformJSON))
	require.Equal(t, wantKeys, sortedJSONKeys(privateJSON))

	allJSON, err := json.Marshal([]ModelOffer{platform["gpt-4o"][0], private["gpt-4o"][0]})
	require.NoError(t, err)
	t.Logf("sanitized platform/private offer JSON: %s", allJSON)
	for _, forbidden := range []string{
		"platform-internal", "private-core-secret", "private-secret.example", "cipher-secret",
		"platform-secret-path", "private-secret-path", "identity", "source_id", "owner_id", "priority", "weight", "rule", "sample",
	} {
		require.NotContains(t, string(allJSON), forbidden)
	}
	identityJSON, err := json.Marshal(platform["gpt-4o"][0].Identity)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(identityJSON), "identity internals must also fail closed if marshaled directly")
}

func TestOfferFindersUseModelNameInPlatformAndPrivateIdentity(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		channels: []models.Channel{
			marketplacePlatformChannel(41, "platform", "Platform", "gpt-4o,claude-3", `{"chat_completions":"/chat"}`),
		},
		private: []models.PrivateChannel{{
			ChannelCore: models.ChannelCore{ID: 41, Status: 1, Endpoints: `{"messages":"/messages"}`},
			OwnerID:     7, Name: "Private", Status: 1,
			Models: datatypes.JSONSlice[string]{"gpt-4o", "claude-3"},
		}},
	}
	modelsIn := []MarketplaceModel{{ModelName: "gpt-4o"}, {ModelName: "claude-3"}}
	encoder := mustTestOfferRefEncoder(t)

	platform, err := NewPlatformModelOfferFinder(query, encoder).Find(context.Background(), scopedMarketplaceViewer(7, 70), modelsIn)
	require.NoError(t, err)
	private, err := NewPrivateModelOfferFinder(query, encoder).Find(context.Background(), scopedMarketplaceViewer(7, 70), modelsIn)
	require.NoError(t, err)

	require.NotEqual(t, platform["gpt-4o"][0].OfferRef, platform["claude-3"][0].OfferRef)
	require.NotEqual(t, private["gpt-4o"][0].OfferRef, private["claude-3"][0].OfferRef)
	require.Equal(t, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 41}, platform["gpt-4o"][0].Identity)
	require.Equal(t, ModelOfferIdentity{ModelName: "claude-3", Kind: OfferKindPlatform, SourceID: 41}, platform["claude-3"][0].Identity)
	require.Equal(t, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 41}, private["gpt-4o"][0].Identity)
	require.Equal(t, ModelOfferIdentity{ModelName: "claude-3", Kind: OfferKindPrivate, SourceID: 41}, private["claude-3"][0].Identity)
}

func TestHMACOfferRefEncoderIsStableNamespacedOpaqueAndVerifiable(t *testing.T) {
	encoder := mustTestOfferRefEncoder(t)
	platformIdentity := ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 424242}
	platform, err := encoder.Encode(platformIdentity)
	require.NoError(t, err)
	sameSecretEncoder := mustTestOfferRefEncoder(t)
	again, err := sameSecretEncoder.Encode(platformIdentity)
	require.NoError(t, err)
	privateIdentity := ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 424242}
	private, err := encoder.Encode(privateIdentity)
	require.NoError(t, err)

	require.Equal(t, platform, again)
	require.True(t, strings.HasPrefix(platform, "p:"))
	require.True(t, strings.HasPrefix(private, "b:"))
	require.NotEqual(t, platform, private)
	require.NotContains(t, platform, "424242")
	require.NotContains(t, private, "424242")
	require.True(t, encoder.Matches(platform, platformIdentity))
	require.False(t, encoder.Matches(platform, ModelOfferIdentity{ModelName: "gpt-4.1", Kind: OfferKindPlatform, SourceID: 424242}))
	require.False(t, encoder.Matches(platform, ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 424243}))
	require.False(t, encoder.Matches(platform, privateIdentity))

	other, err := NewHMACOfferRefEncoder("another-jwt-secret-with-at-least-32-bytes")
	require.NoError(t, err)
	otherRef, err := other.Encode(platformIdentity)
	require.NoError(t, err)
	require.NotEqual(t, platform, otherRef)
	require.False(t, other.Matches(platform, platformIdentity))
}

func TestHMACOfferRefEncoderRejectsMissingSecretAndUnknownNamespace(t *testing.T) {
	_, err := NewHMACOfferRefEncoder("   ")
	require.Error(t, err)

	encoder := mustTestOfferRefEncoder(t)
	invalid := ModelOfferIdentity{ModelName: "gpt-4o", Kind: ModelOfferKind("unknown"), SourceID: 1}
	_, err = encoder.Encode(invalid)
	require.Error(t, err)
	require.False(t, encoder.Matches("p:anything", invalid))

	incomplete := ModelOfferIdentity{Kind: OfferKindPlatform, SourceID: 1}
	_, err = encoder.Encode(incomplete)
	require.Error(t, err)
	require.False(t, encoder.Matches("p:anything", incomplete))
}

func TestOfferFindersApplyWhitelistChannelAndBYOKOnlyBeforeReturningOffers(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		channels: []models.Channel{
			marketplacePlatformChannel(1, "one", "One", "gpt-4o,hidden", `{"chat_completions":"/chat"}`),
			marketplacePlatformChannel(2, "two", "Two", "gpt-4o", `{"chat_completions":"/chat"}`),
		},
		private: []models.PrivateChannel{
			marketplacePrivateChannel(3, 7, "Private", "gpt-4o", `{"chat_completions":"/chat"}`),
		},
	}
	modelsIn := []MarketplaceModel{{ModelName: "gpt-4o"}, {ModelName: "hidden"}}
	viewer := MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 70, UserID: 7}, AllowedChannelIDs: []uint{2}, BYOKOnly: true,
		AllowedModels: MarketplaceModelWhitelist{TokenPatterns: []string{"gpt-.*"}},
	}

	platform, err := NewPlatformModelOfferFinder(query, mustTestOfferRefEncoder(t)).Find(context.Background(), viewer, modelsIn)
	require.NoError(t, err)
	require.Empty(t, platform)
	require.Zero(t, query.channelCalls, "BYOKOnly must stop before reading platform channels")

	private, err := NewPrivateModelOfferFinder(query, mustTestOfferRefEncoder(t)).Find(context.Background(), viewer, modelsIn)
	require.NoError(t, err)
	require.Len(t, private["gpt-4o"], 1)
	require.Empty(t, private["hidden"])
}

func TestOfferFindersPropagateDAOAndEncoderFailures(t *testing.T) {
	t.Run("platform dao", func(t *testing.T) {
		finder := NewPlatformModelOfferFinder(&fakeModelMarketplaceQuery{channelErr: errors.New("channel read failed")}, mustTestOfferRefEncoder(t))
		_, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []MarketplaceModel{{ModelName: "gpt-4o"}})
		require.ErrorContains(t, err, "channel read failed")
	})
	t.Run("private dao", func(t *testing.T) {
		finder := NewPrivateModelOfferFinder(&fakeModelMarketplaceQuery{privateErr: errors.New("private read failed")}, mustTestOfferRefEncoder(t))
		_, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []MarketplaceModel{{ModelName: "gpt-4o"}})
		require.ErrorContains(t, err, "private read failed")
	})
	t.Run("encoder", func(t *testing.T) {
		query := &fakeModelMarketplaceQuery{channels: []models.Channel{
			marketplacePlatformChannel(1, "one", "One", "gpt-4o", `{"chat_completions":"/chat"}`),
		}}
		finder := NewPlatformModelOfferFinder(query, failingOfferRefEncoder{})
		_, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []MarketplaceModel{{ModelName: "gpt-4o"}})
		require.ErrorContains(t, err, "encode failed")
	})
}

func TestOfferFindersRejectZeroViewerBeforeAnyDAOQuery(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		channels: []models.Channel{marketplacePlatformChannel(1, "platform", "Platform", "gpt-4o", `{"chat_completions":"/chat"}`)},
		private:  []models.PrivateChannel{marketplacePrivateChannel(2, 7, "Private", "gpt-4o", `{"messages":"/messages"}`)},
	}
	modelsIn := []MarketplaceModel{{ModelName: "gpt-4o"}}
	encoder := mustTestOfferRefEncoder(t)

	platform, err := NewPlatformModelOfferFinder(query, encoder).Find(context.Background(), MarketplaceViewer{}, modelsIn)
	require.ErrorContains(t, err, "marketplace viewer")
	require.Nil(t, platform)
	require.Zero(t, query.channelCalls)

	private, err := NewPrivateModelOfferFinder(query, encoder).Find(context.Background(), MarketplaceViewer{}, modelsIn)
	require.ErrorContains(t, err, "marketplace viewer")
	require.Nil(t, private)
	require.Zero(t, query.privateCalls)
	require.Empty(t, query.privateScopes)
}

func TestMarketplaceFindersRejectNonCanonicalScopedViewersBeforeEveryDAO(t *testing.T) {
	invalid := []struct {
		name   string
		viewer MarketplaceViewer
	}{
		{name: "user without token", viewer: MarketplaceViewer{UserID: 7}},
		{name: "token owner mismatch", viewer: MarketplaceViewer{
			UserID: 7, Token: &models.Token{ID: 70, UserID: 8},
		}},
	}
	modelsIn := []MarketplaceModel{{ModelName: "gpt-4o"}}
	encoder := mustTestOfferRefEncoder(t)

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			catalogQuery := &fakeModelMarketplaceQuery{}
			catalog, err := NewModelCatalogFinder(catalogQuery, encoder).Find(context.Background(), test.viewer)
			require.ErrorContains(t, err, "marketplace viewer")
			require.Nil(t, catalog)
			require.Zero(t, catalogQuery.modelCalls)

			platformQuery := &fakeModelMarketplaceQuery{}
			platform, err := NewPlatformModelOfferFinder(platformQuery, encoder).Find(context.Background(), test.viewer, modelsIn)
			require.ErrorContains(t, err, "marketplace viewer")
			require.Nil(t, platform)
			require.Zero(t, platformQuery.channelCalls)

			privateQuery := &fakeModelMarketplaceQuery{}
			private, err := NewPrivateModelOfferFinder(privateQuery, encoder).Find(context.Background(), test.viewer, modelsIn)
			require.ErrorContains(t, err, "marketplace viewer")
			require.Nil(t, private)
			require.Zero(t, privateQuery.privateCalls)

			routingQuery := &fakeRoutingModelQuery{}
			routing, err := NewRoutingModelFinder(routingQuery).Find(context.Background(), test.viewer, routingOffers("gpt-4o"))
			require.ErrorContains(t, err, "marketplace viewer")
			require.Nil(t, routing)
			require.Zero(t, routingQuery.calls)
		})
	}
}

func TestMarketplaceFindersAcceptCanonicalSystemTokenViewer(t *testing.T) {
	viewer := MarketplaceViewer{Token: &models.Token{ID: 70, UserID: 0}}
	query := &fakeRoutingModelQuery{}

	got, err := NewRoutingModelFinder(query).Find(context.Background(), viewer, map[string][]ModelOffer{})
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 1, query.calls)
	require.Equal(t, dao.MarketplaceRoutingScope{TokenID: 70}, query.scopes[0])
}

type fakeModelMarketplaceQuery struct {
	configs       []models.ModelConfig
	channels      []models.Channel
	private       []models.PrivateChannel
	modelErr      error
	channelErr    error
	privateErr    error
	modelCalls    int
	channelCalls  int
	privateCalls  int
	privateScopes []dao.MarketplacePrivateChannelScope
}

func (q *fakeModelMarketplaceQuery) ListEnabledMarketplaceModels(context.Context) ([]models.ModelConfig, error) {
	q.modelCalls++
	return append([]models.ModelConfig(nil), q.configs...), q.modelErr
}

func (q *fakeModelMarketplaceQuery) ListMarketplaceChannels(context.Context) ([]models.Channel, error) {
	q.channelCalls++
	return append([]models.Channel(nil), q.channels...), q.channelErr
}

func (q *fakeModelMarketplaceQuery) ListMarketplacePrivateChannels(_ context.Context, scope dao.MarketplacePrivateChannelScope) ([]models.PrivateChannel, error) {
	q.privateCalls++
	q.privateScopes = append(q.privateScopes, scope)
	return append([]models.PrivateChannel(nil), q.private...), q.privateErr
}

func (q *fakeModelMarketplaceQuery) ListMarketplaceRoutings(
	context.Context,
	dao.MarketplaceRoutingScope,
) ([]models.ModelRouting, error) {
	return []models.ModelRouting{}, nil
}

type failingOfferRefEncoder struct{}

func (failingOfferRefEncoder) Encode(ModelOfferIdentity) (string, error) {
	return "", errors.New("encode failed")
}

func (failingOfferRefEncoder) Matches(string, ModelOfferIdentity) bool { return false }

func mustTestOfferRefEncoder(t *testing.T) OfferRefEncoder {
	t.Helper()
	encoder, err := NewHMACOfferRefEncoder("marketplace-test-jwt-secret-at-least-32-bytes")
	require.NoError(t, err)
	return encoder
}

func marketplacePlatformChannel(id uint, internalName, publicName, modelNames, endpoints string) models.Channel {
	return models.Channel{
		ChannelCore:       models.ChannelCore{ID: id, Name: internalName, Status: 1, Endpoints: endpoints},
		PublicDisplayName: publicName,
		Models:            modelNames,
	}
}

func marketplacePrivateChannel(id, ownerID uint, name, modelName, endpoints string) models.PrivateChannel {
	return models.PrivateChannel{
		ChannelCore: models.ChannelCore{ID: id, Status: 1, Endpoints: endpoints},
		OwnerID:     ownerID,
		Name:        name,
		Status:      1,
		Models:      datatypes.JSONSlice[string]{modelName},
	}
}

func offerDisplayNames(offers []ModelOffer) []string {
	result := make([]string, 0, len(offers))
	for _, offer := range offers {
		result = append(result, offer.DisplayName)
	}
	return result
}

func offerOwnerships(offers []ModelOffer) []ModelOfferOwnership {
	result := make([]ModelOfferOwnership, 0, len(offers))
	for _, offer := range offers {
		result = append(result, offer.Ownership)
	}
	return result
}

func marshalOfferMap(t *testing.T, offer ModelOffer) map[string]any {
	t.Helper()
	raw, err := json.Marshal(offer)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func sortedJSONKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
