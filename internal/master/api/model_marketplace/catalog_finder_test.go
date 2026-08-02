package model_marketplace

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestModelCatalogFinderComposesPlannerNeutralOfferMetadata(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		configs: []models.ModelConfig{
			marketplaceModelConfig("hidden", "Hidden Synced", nil),
			marketplaceModelConfig("gpt-4o", "Synced", stringPointer("Overridden")),
			marketplaceModelConfig("empty-only", "Empty", nil),
		},
		channels: []models.Channel{
			marketplacePlatformChannel(1, "blocked", "Blocked", "gpt-4o", `{"chat_completions":"/chat"}`),
			marketplacePlatformChannel(2, "allowed", "Allowed", "gpt-4o", `{"responses":"/responses"}`),
			marketplacePlatformChannel(3, "empty", "Empty", "empty-only", ""),
			marketplacePlatformChannel(4, "hidden", "Hidden", "hidden", `{"chat_completions":"/chat"}`),
		},
		private: []models.PrivateChannel{
			marketplacePrivateChannel(11, 7, "Owned", "gpt-4o", `{"messages":"/messages"}`),
			marketplacePrivateChannel(12, 8, "Shared", "gpt-4o", `{"models":"/models"}`),
			marketplacePrivateChannel(13, 7, "Empty", "empty-only", ""),
			marketplacePrivateChannel(14, 7, "Hidden", "hidden", `{"messages":"/messages"}`),
		},
	}
	finder := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t))
	viewer := MarketplaceViewer{
		UserID: 7, Token: &models.Token{ID: 99, UserID: 7}, AllowedChannelIDs: []uint{2},
		AllowedModels: MarketplaceModelWhitelist{TokenPatterns: []string{"gpt-.*", "empty-.*"}},
	}

	catalog, err := finder.Find(context.Background(), viewer)
	require.NoError(t, err)
	require.Len(t, catalog, 2, "metadata finder must not approximate runtime availability")
	require.Equal(t, "empty-only", catalog[0].ModelName)
	require.Len(t, catalog[0].Offers, 1)
	require.False(t, catalog[0].Offers[0].Available)
	require.Equal(t, "gpt-4o", catalog[1].ModelName)
	require.Equal(t, "Overridden", catalog[1].Metadata.DisplayName)
	require.Zero(t, catalog[1].AvailableOfferCount)
	require.Zero(t, catalog[1].PlatformOfferCount)
	require.Zero(t, catalog[1].PrivateOfferCount)
	require.Equal(t, []ModelOfferKind{OfferKindPlatform, OfferKindPrivate, OfferKindPrivate}, offerKinds(catalog[1].Offers))
	for _, offer := range catalog[1].Offers {
		require.False(t, offer.Available)
	}
	require.Equal(t, 1, query.modelCalls)
	require.Equal(t, 1, query.channelCalls)
	require.Equal(t, 1, query.privateCalls)
}

func TestModelCatalogFinderBYOKOnlySkipsPlatformAndKeepsVisiblePrivateOffers(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		configs:  []models.ModelConfig{marketplaceModelConfig("gpt-4o", "GPT", nil)},
		channels: []models.Channel{marketplacePlatformChannel(2, "platform", "Platform", "gpt-4o", `{"chat_completions":"/chat"}`)},
		private:  []models.PrivateChannel{marketplacePrivateChannel(11, 7, "Owned", "gpt-4o", `{"messages":"/messages"}`)},
	}
	finder := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t))
	viewer := scopedMarketplaceViewer(7, 70)
	viewer.BYOKOnly = true
	catalog, err := finder.Find(context.Background(), viewer)
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	require.Zero(t, catalog[0].PlatformOfferCount)
	require.Zero(t, catalog[0].PrivateOfferCount)
	require.Equal(t, OfferKindPrivate, catalog[0].Offers[0].Kind)
	require.False(t, catalog[0].Offers[0].Available)
	require.Zero(t, query.channelCalls)
}

func TestModelCatalogFinderAdminGlobalRetainsUnavailableOffersWithoutCountingThem(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		configs:  []models.ModelConfig{marketplaceModelConfig("gpt-4o", "GPT", nil)},
		channels: []models.Channel{marketplacePlatformChannel(2, "platform", "Platform", "gpt-4o", "")},
		private:  []models.PrivateChannel{marketplacePrivateChannel(11, 8, "Private", "gpt-4o", "")},
	}
	finder := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t))
	catalog, err := finder.Find(context.Background(), MarketplaceViewer{AdminGlobal: true})
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	require.Len(t, catalog[0].Offers, 2)
	require.Zero(t, catalog[0].AvailableOfferCount)
	require.Zero(t, catalog[0].PlatformOfferCount)
	require.Zero(t, catalog[0].PrivateOfferCount)
	require.False(t, catalog[0].Offers[0].Available)
	require.False(t, catalog[0].Offers[1].Available)
	require.Equal(t, true, query.privateScopes[0].AdminGlobal)
}

func TestModelCatalogFinderPropagatesEachBatchFailureWithoutContinuing(t *testing.T) {
	t.Run("models", func(t *testing.T) {
		query := &fakeModelMarketplaceQuery{modelErr: errors.New("models failed")}
		_, err := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t)).Find(context.Background(), scopedMarketplaceViewer(7, 70))
		require.ErrorContains(t, err, "models failed")
		require.Zero(t, query.channelCalls)
		require.Zero(t, query.privateCalls)
	})
	t.Run("platform", func(t *testing.T) {
		query := &fakeModelMarketplaceQuery{
			configs:    []models.ModelConfig{marketplaceModelConfig("gpt-4o", "GPT", nil)},
			channelErr: errors.New("platform failed"),
		}
		_, err := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t)).Find(context.Background(), scopedMarketplaceViewer(7, 70))
		require.ErrorContains(t, err, "platform failed")
		require.Zero(t, query.privateCalls)
	})
	t.Run("private", func(t *testing.T) {
		query := &fakeModelMarketplaceQuery{
			configs:    []models.ModelConfig{marketplaceModelConfig("gpt-4o", "GPT", nil)},
			privateErr: errors.New("private failed"),
		}
		_, err := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t)).Find(context.Background(), scopedMarketplaceViewer(7, 70))
		require.ErrorContains(t, err, "private failed")
	})
}

func TestModelCatalogFinderEmptyWhitelistResultStopsBeforeOfferQueries(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		configs: []models.ModelConfig{marketplaceModelConfig("claude-3", "Claude", nil)},
	}
	finder := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t))
	catalog, err := finder.Find(context.Background(), MarketplaceViewer{
		UserID:        7,
		Token:         &models.Token{ID: 70, UserID: 7},
		AllowedModels: MarketplaceModelWhitelist{TokenPatterns: []string{"gpt-.*"}},
	})
	require.NoError(t, err)
	require.Empty(t, catalog)
	require.Zero(t, query.channelCalls)
	require.Zero(t, query.privateCalls)
}

func TestModelCatalogFinderRejectsZeroViewerBeforeAnyDAOQuery(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		configs: []models.ModelConfig{marketplaceModelConfig("gpt-4o", "GPT", nil)},
	}
	finder := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t))

	catalog, err := finder.Find(context.Background(), MarketplaceViewer{})
	require.ErrorContains(t, err, "marketplace viewer")
	require.Nil(t, catalog)
	require.Zero(t, query.modelCalls)
	require.Zero(t, query.channelCalls)
	require.Zero(t, query.privateCalls)
}

func TestModelCatalogFinderRejectsAdminGlobalMixedWithUserScope(t *testing.T) {
	query := &fakeModelMarketplaceQuery{
		configs: []models.ModelConfig{marketplaceModelConfig("gpt-4o", "GPT", nil)},
	}
	finder := NewModelCatalogFinder(query, mustTestOfferRefEncoder(t))

	catalog, err := finder.Find(context.Background(), MarketplaceViewer{AdminGlobal: true, UserID: 7})
	require.ErrorContains(t, err, "admin global")
	require.Nil(t, catalog)
	require.Zero(t, query.modelCalls)
}

func marketplaceModelConfig(modelName, syncedDisplayName string, overrideDisplayName *string) models.ModelConfig {
	return models.ModelConfig{
		ModelName: modelName,
		Status:    1,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{
			DisplayName: syncedDisplayName,
		}),
		MetadataOverride: datatypes.NewJSONType(models.ModelMetadataOverride{
			DisplayName: overrideDisplayName,
		}),
	}
}

func stringPointer(value string) *string { return &value }

func offerKinds(offers []ModelOffer) []ModelOfferKind {
	result := make([]ModelOfferKind, 0, len(offers))
	for _, offer := range offers {
		result = append(result, offer.Kind)
	}
	return result
}
