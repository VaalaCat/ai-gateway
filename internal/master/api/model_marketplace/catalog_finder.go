package model_marketplace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// MarketplaceModel is the real-model catalog DTO composed in Task 3. Routing
// models, pricing, performance, status history, and HTTP transport are added by
// later tasks rather than leaking into this boundary early.
type MarketplaceModel struct {
	ModelName           string               `json:"model_name"`
	Metadata            models.ModelMetadata `json:"metadata"`
	AvailableOfferCount int                  `json:"available_offer_count"`
	PlatformOfferCount  int                  `json:"platform_offer_count"`
	PrivateOfferCount   int                  `json:"private_offer_count"`
	Offers              []ModelOffer         `json:"offers"`
	// Config carries the already-batched pricing facts into the HTTP composer.
	// It is never part of a response DTO.
	Config models.ModelConfig `json:"-"`
}

type ModelCatalogFinder struct {
	query          dao.ModelMarketplaceQuery
	platformOffers PlatformModelOfferFinder
	privateOffers  PrivateModelOfferFinder
}

func NewModelCatalogFinder(query dao.ModelMarketplaceQuery, encoder OfferRefEncoder) ModelCatalogFinder {
	return ModelCatalogFinder{
		query:          query,
		platformOffers: NewPlatformModelOfferFinder(query, encoder),
		privateOffers:  NewPrivateModelOfferFinder(query, encoder),
	}
}

func (f ModelCatalogFinder) Find(ctx context.Context, viewer MarketplaceViewer) ([]MarketplaceModel, error) {
	if err := validateMarketplaceViewer(viewer); err != nil {
		return nil, err
	}
	if f.query == nil {
		return nil, errors.New("model catalog query is required")
	}
	configs, err := f.query.ListEnabledMarketplaceModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("find marketplace models: %w", err)
	}
	modelsIn := marketplaceModelsFromConfigs(configs, viewer)
	if len(modelsIn) == 0 {
		return []MarketplaceModel{}, nil
	}

	platformOffers, err := f.platformOffers.Find(ctx, viewer, modelsIn)
	if err != nil {
		return nil, err
	}
	privateOffers, err := f.privateOffers.Find(ctx, viewer, modelsIn)
	if err != nil {
		return nil, err
	}
	return composeMarketplaceCatalog(modelsIn, platformOffers, privateOffers), nil
}

func marketplaceModelsFromConfigs(configs []models.ModelConfig, viewer MarketplaceViewer) []MarketplaceModel {
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].ModelName < configs[j].ModelName
	})
	result := make([]MarketplaceModel, 0, len(configs))
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		modelName := strings.TrimSpace(config.ModelName)
		if config.Status != consts.StatusEnabled || modelName == "" || !viewer.AllowedModels.Allows(modelName) {
			continue
		}
		if _, duplicate := seen[modelName]; duplicate {
			continue
		}
		seen[modelName] = struct{}{}
		result = append(result, MarketplaceModel{
			ModelName: modelName,
			Metadata:  config.EffectiveMetadata(),
			Offers:    []ModelOffer{},
			Config:    config,
		})
	}
	return result
}

func composeMarketplaceCatalog(
	modelsIn []MarketplaceModel,
	platformByModel map[string][]ModelOffer,
	privateByModel map[string][]ModelOffer,
) []MarketplaceModel {
	result := make([]MarketplaceModel, 0, len(modelsIn))
	for _, marketplaceModel := range modelsIn {
		platformOffers := platformByModel[marketplaceModel.ModelName]
		privateOffers := privateByModel[marketplaceModel.ModelName]
		marketplaceModel.Offers = make([]ModelOffer, 0, len(platformOffers)+len(privateOffers))
		marketplaceModel.Offers = append(marketplaceModel.Offers, platformOffers...)
		marketplaceModel.Offers = append(marketplaceModel.Offers, privateOffers...)
		result = append(result, marketplaceModel)
	}
	return result
}
