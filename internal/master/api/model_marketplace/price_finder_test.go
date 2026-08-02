package model_marketplace

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestModelPriceFinderCalculatesPlatformFourBucketPrices(t *testing.T) {
	prices := models.ModelConfig{
		ModelName: "gpt-4o", InputPrice: 10, OutputPrice: 20,
		CacheReadPrice: 3, CacheWritePrice: 4,
	}
	tests := []struct {
		name    string
		billing OfferBilling
		want    ModelPrices
	}{
		{name: "ratio one", billing: OfferBilling{PriceRatio: 1}, want: ModelPrices{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4}},
		{name: "ratio zero normalizes to one", billing: OfferBilling{PriceRatio: 0}, want: ModelPrices{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4}},
		{name: "discount applies to every bucket", billing: OfferBilling{PriceRatio: 0.5}, want: ModelPrices{Input: 5, Output: 10, CacheRead: 1.5, CacheWrite: 2}},
		{name: "free clears every bucket", billing: OfferBilling{PriceRatio: 2, Free: true}, want: ModelPrices{}},
		{name: "canonical upper boundary", billing: OfferBilling{PriceRatio: 1000}, want: ModelPrices{Input: 10_000, Output: 20_000, CacheRead: 3_000, CacheWrite: 4_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offer := pricedOffer(OfferKindPlatform)
			got, err := (ModelPriceFinder{}).Find(scopedMarketplaceViewer(7, 70), &prices, offer, boundOfferBilling(offer, tt.billing))
			require.NoError(t, err)
			assertModelPricesNear(t, pricesFromModelConfig(prices), got.ReferencePrice)
			assertModelPricesNear(t, tt.want, got.GatewayCharge)
			assertModelPricesNear(t, tt.want, got.EstimatedTotal)
			require.Equal(t, AccuracyExact, got.Accuracy)
		})
	}
}

func TestModelPriceFinderCalculatesPrivateReferencePrices(t *testing.T) {
	prices := models.ModelConfig{
		ModelName: "gpt-4o", InputPrice: 10, OutputPrice: 20,
		CacheReadPrice: 3, CacheWritePrice: 4,
	}
	tests := []struct {
		name        string
		billing     OfferBilling
		wantGateway ModelPrices
		wantTotal   ModelPrices
	}{
		{
			name: "free mode retains reference total", billing: OfferBilling{BYOKMode: consts.BYOKBillingModeFree},
			wantGateway: ModelPrices{}, wantTotal: ModelPrices{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4},
		},
		{
			name: "service fee adds every gateway bucket", billing: OfferBilling{BYOKMode: consts.BYOKBillingModeServiceFee, BYOKServiceFeeRatio: 0.1},
			wantGateway: ModelPrices{Input: 1, Output: 2, CacheRead: 0.3, CacheWrite: 0.4},
			wantTotal:   ModelPrices{Input: 11, Output: 22, CacheRead: 3.3, CacheWrite: 4.4},
		},
		{
			name: "zero service fee keeps reference total", billing: OfferBilling{BYOKMode: consts.BYOKBillingModeServiceFee, BYOKServiceFeeRatio: 0},
			wantGateway: ModelPrices{}, wantTotal: ModelPrices{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offer := pricedOffer(OfferKindPrivate)
			got, err := (ModelPriceFinder{}).Find(scopedMarketplaceViewer(7, 70), &prices, offer, boundOfferBilling(offer, tt.billing))
			require.NoError(t, err)
			assertModelPricesNear(t, pricesFromModelConfig(prices), got.ReferencePrice)
			assertModelPricesNear(t, tt.wantGateway, got.GatewayCharge)
			assertModelPricesNear(t, tt.wantTotal, got.EstimatedTotal)
			require.Equal(t, AccuracyReference, got.Accuracy)
		})
	}
}

func TestModelPriceFinderRejectsMissingOrInvalidPricingInputs(t *testing.T) {
	finder := ModelPriceFinder{}
	config := &models.ModelConfig{ModelName: "gpt-4o", InputPrice: 1}
	tests := []struct {
		name    string
		config  *models.ModelConfig
		offer   ModelOffer
		billing OfferBilling
		wantErr string
	}{
		{name: "missing model config", config: nil, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: 1}, wantErr: "model pricing is required"},
		{name: "model identity mismatch", config: config, offer: pricedOfferForModel(OfferKindPlatform, "other"), billing: OfferBilling{PriceRatio: 1}, wantErr: "does not match"},
		{name: "negative platform ratio", config: config, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: -1}, wantErr: "price ratio"},
		{name: "platform ratio above canonical maximum", config: config, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: 1000.1}, wantErr: "price ratio"},
		{name: "platform ratio nan", config: config, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: math.NaN()}, wantErr: "price ratio"},
		{name: "platform ratio infinity", config: config, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: math.Inf(1)}, wantErr: "price ratio"},
		{name: "unknown byok mode", config: config, offer: pricedOffer(OfferKindPrivate), billing: OfferBilling{BYOKMode: "unknown"}, wantErr: "BYOK billing mode"},
		{name: "out of range byok fee", config: config, offer: pricedOffer(OfferKindPrivate), billing: OfferBilling{BYOKMode: consts.BYOKBillingModeServiceFee, BYOKServiceFeeRatio: 1.1}, wantErr: "service fee ratio"},
		{name: "incomplete offer identity", config: config, offer: ModelOffer{OfferRef: "broken", Kind: OfferKindPlatform}, billing: OfferBilling{PriceRatio: 1}, wantErr: "offer identity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := finder.Find(scopedMarketplaceViewer(7, 70), tt.config, tt.offer, boundOfferBilling(tt.offer, tt.billing))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestModelPriceFinderRejectsBillingForDifferentOfferIdentity(t *testing.T) {
	config := &models.ModelConfig{ModelName: "gpt-4o", InputPrice: 1}
	offer := pricedOffer(OfferKindPlatform)
	tests := []struct {
		name     string
		identity ModelOfferIdentity
	}{
		{name: "wrong model", identity: ModelOfferIdentity{ModelName: "gpt-4.1", Kind: OfferKindPlatform, SourceID: 9}},
		{name: "wrong source", identity: ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 10}},
		{name: "wrong kind and source", identity: ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 10}},
		{name: "same numeric source across kinds", identity: ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			billing := OfferBilling{Identity: tt.identity, PriceRatio: 1}
			_, err := (ModelPriceFinder{}).Find(scopedMarketplaceViewer(7, 70), config, offer, billing)
			require.ErrorContains(t, err, "billing identity")
		})
	}
}

func TestModelPriceFinderRejectsNonFiniteInputsAndDerivedPrices(t *testing.T) {
	tests := []struct {
		name    string
		config  models.ModelConfig
		offer   ModelOffer
		billing OfferBilling
		wantErr string
	}{
		{name: "negative model price", config: models.ModelConfig{ModelName: "gpt-4o", InputPrice: -1}, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: 1}, wantErr: "model price"},
		{name: "nan model price", config: models.ModelConfig{ModelName: "gpt-4o", OutputPrice: math.NaN()}, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: 1}, wantErr: "model price"},
		{name: "infinite model price", config: models.ModelConfig{ModelName: "gpt-4o", CacheReadPrice: math.Inf(1)}, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: 1}, wantErr: "model price"},
		{name: "platform multiplication becomes infinite", config: models.ModelConfig{ModelName: "gpt-4o", InputPrice: math.MaxFloat64}, offer: pricedOffer(OfferKindPlatform), billing: OfferBilling{PriceRatio: 2}, wantErr: "gateway charge"},
		{name: "private total addition becomes infinite", config: models.ModelConfig{ModelName: "gpt-4o", InputPrice: math.MaxFloat64}, offer: pricedOffer(OfferKindPrivate), billing: OfferBilling{BYOKMode: consts.BYOKBillingModeServiceFee, BYOKServiceFeeRatio: 0.1}, wantErr: "estimated total"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (ModelPriceFinder{}).Find(
				scopedMarketplaceViewer(7, 70), &tt.config, tt.offer, boundOfferBilling(tt.offer, tt.billing),
			)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestModelPriceFinderKeepsConfiguredZeroPrices(t *testing.T) {
	config := &models.ModelConfig{ModelName: "free-to-price-model"}
	for _, kind := range []ModelOfferKind{OfferKindPlatform, OfferKindPrivate} {
		offer := pricedOfferForModel(kind, config.ModelName)
		billing := OfferBilling{PriceRatio: 1}
		if kind == OfferKindPrivate {
			billing = OfferBilling{BYOKMode: consts.BYOKBillingModeServiceFee, BYOKServiceFeeRatio: 0.1}
		}
		got, err := (ModelPriceFinder{}).Find(scopedMarketplaceViewer(7, 70), config, offer, boundOfferBilling(offer, billing))
		require.NoError(t, err)
		require.Equal(t, ModelPrices{}, got.ReferencePrice)
		require.Equal(t, ModelPrices{}, got.GatewayCharge)
		require.Equal(t, ModelPrices{}, got.EstimatedTotal)
	}
}

func TestModelPriceFinderRejectsInvalidViewerBeforePricing(t *testing.T) {
	offer := pricedOffer(OfferKindPlatform)
	_, err := (ModelPriceFinder{}).Find(
		MarketplaceViewer{},
		&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 1},
		offer,
		boundOfferBilling(offer, OfferBilling{PriceRatio: 1}),
	)
	require.ErrorContains(t, err, "marketplace viewer")
}

func TestOfferBillingJSONNeverExposesIdentityOrSettlementFacts(t *testing.T) {
	offer := pricedOffer(OfferKindPrivate)
	payload, err := json.Marshal(boundOfferBilling(offer, OfferBilling{
		BYOKMode: consts.BYOKBillingModeServiceFee, BYOKServiceFeeRatio: 0.1,
	}))
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(payload))
}

func pricedOffer(kind ModelOfferKind) ModelOffer {
	return pricedOfferForModel(kind, "gpt-4o")
}

func pricedOfferForModel(kind ModelOfferKind, modelName string) ModelOffer {
	return ModelOffer{
		OfferRef: "offer-ref", Kind: kind, Available: true,
		SupportedEndpoints: []SupportedEndpoint{EndpointChatCompletions},
		Identity:           ModelOfferIdentity{ModelName: modelName, Kind: kind, SourceID: 9},
	}
}

func boundOfferBilling(offer ModelOffer, billing OfferBilling) OfferBilling {
	billing.Identity = offer.Identity
	return billing
}

func assertModelPricesNear(t *testing.T, want, got ModelPrices) {
	t.Helper()
	require.InDelta(t, want.Input, got.Input, 1e-12, "input")
	require.InDelta(t, want.Output, got.Output, 1e-12, "output")
	require.InDelta(t, want.CacheRead, got.CacheRead, 1e-12, "cache_read")
	require.InDelta(t, want.CacheWrite, got.CacheWrite, 1e-12, "cache_write")
}
