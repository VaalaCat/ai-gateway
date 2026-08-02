package model_marketplace

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/pricing"
)

// OfferAccuracy states whether a marketplace amount is exact under the
// gateway's own settlement rules or only a local reference for an upstream
// bill. BYOK reference prices never claim to be supplier invoice facts.
type OfferAccuracy string

const (
	AccuracyExact     OfferAccuracy = "exact"
	AccuracyReference OfferAccuracy = "reference"
)

// ModelPrices is the repository's existing model-price unit: USD per one
// million tokens. Values are copied from ModelConfig and retain its float64
// precision; this layer does not invent a second fixed-point unit or rounding.
type ModelPrices struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// OfferBilling contains server-side settlement facts for one already
// re-enumerated offer. It is not an HTTP request DTO.
type OfferBilling struct {
	Identity            ModelOfferIdentity `json:"-"`
	PriceRatio          float64            `json:"-"`
	Free                bool               `json:"-"`
	BYOKMode            string             `json:"-"`
	BYOKServiceFeeRatio float64            `json:"-"`
}

type OfferPricing struct {
	ReferencePrice ModelPrices   `json:"reference_price"`
	GatewayCharge  ModelPrices   `json:"gateway_charge"`
	EstimatedTotal ModelPrices   `json:"estimated_total"`
	Accuracy       OfferAccuracy `json:"accuracy"`
}

type offerPriceCalculator func(ModelPrices, OfferBilling) OfferPricing

var offerPriceCalculators = map[ModelOfferKind]offerPriceCalculator{
	OfferKindPlatform: platformOfferPricing,
	OfferKindPrivate:  privateOfferPricing,
}

// ModelPriceFinder calculates current four-bucket prices for a canonical
// visible offer. Callers must construct OfferBilling from server-side channel
// and Settings facts; no client-controlled source ID or billing factor enters
// this boundary.
type ModelPriceFinder struct{}

func (ModelPriceFinder) Find(
	viewer MarketplaceViewer,
	config *models.ModelConfig,
	offer ModelOffer,
	billing OfferBilling,
) (OfferPricing, error) {
	if err := validateMarketplaceViewer(viewer); err != nil {
		return OfferPricing{}, err
	}
	if config == nil {
		return OfferPricing{}, errors.New("model pricing is required")
	}
	if err := validateRoutingOffer(offer.Identity.ModelName, offer); err != nil {
		return OfferPricing{}, err
	}
	if strings.TrimSpace(config.ModelName) == "" || config.ModelName != offer.Identity.ModelName {
		return OfferPricing{}, fmt.Errorf("model pricing %q does not match offer model %q", config.ModelName, offer.Identity.ModelName)
	}
	prices := pricesFromModelConfig(*config)
	if err := validateModelPrices("model", prices); err != nil {
		return OfferPricing{}, err
	}
	if billing.Identity != offer.Identity {
		return OfferPricing{}, errors.New("marketplace offer billing identity does not match offer identity")
	}
	if err := validateOfferBilling(offer.Kind, billing); err != nil {
		return OfferPricing{}, err
	}
	calculator, ok := offerPriceCalculators[offer.Kind]
	if !ok {
		return OfferPricing{}, fmt.Errorf("unsupported marketplace offer kind %q", offer.Kind)
	}
	result := calculator(prices, billing)
	for _, derived := range []struct {
		name   string
		prices ModelPrices
	}{
		{name: "reference", prices: result.ReferencePrice},
		{name: "gateway charge", prices: result.GatewayCharge},
		{name: "estimated total", prices: result.EstimatedTotal},
	} {
		if err := validateModelPrices(derived.name, derived.prices); err != nil {
			return OfferPricing{}, err
		}
	}
	return result, nil
}

func pricesFromModelConfig(config models.ModelConfig) ModelPrices {
	return ModelPrices{
		Input: config.InputPrice, Output: config.OutputPrice,
		CacheRead: config.CacheReadPrice, CacheWrite: config.CacheWritePrice,
	}
}

func validateModelPrices(label string, prices ModelPrices) error {
	if err := pricing.ValidateModelPrices(
		prices.Input,
		prices.Output,
		prices.CacheRead,
		prices.CacheWrite,
	); err != nil {
		return fmt.Errorf("invalid %s price: %w", label, err)
	}
	return nil
}

func validateOfferBilling(kind ModelOfferKind, billing OfferBilling) error {
	switch kind {
	case OfferKindPlatform:
		if err := pricing.ValidatePlatformPriceRatio(billing.PriceRatio); err != nil {
			return fmt.Errorf("invalid platform price ratio: %w", err)
		}
	case OfferKindPrivate:
		if billing.BYOKMode != consts.BYOKBillingModeFree && billing.BYOKMode != consts.BYOKBillingModeServiceFee {
			return errors.New("invalid BYOK billing mode")
		}
		if billing.BYOKServiceFeeRatio < 0 || billing.BYOKServiceFeeRatio > 1 ||
			math.IsNaN(billing.BYOKServiceFeeRatio) || math.IsInf(billing.BYOKServiceFeeRatio, 0) {
			return errors.New("invalid BYOK service fee ratio")
		}
	default:
		return fmt.Errorf("unsupported marketplace offer kind %q", kind)
	}
	return nil
}

func platformOfferPricing(reference ModelPrices, billing OfferBilling) OfferPricing {
	ratio := billing.PriceRatio
	if ratio == 0 {
		ratio = 1
	}
	gateway := multiplyModelPrices(reference, ratio)
	if billing.Free {
		gateway = ModelPrices{}
	}
	return OfferPricing{
		ReferencePrice: reference, GatewayCharge: gateway,
		EstimatedTotal: gateway, Accuracy: AccuracyExact,
	}
}

func privateOfferPricing(reference ModelPrices, billing OfferBilling) OfferPricing {
	gateway := ModelPrices{}
	if billing.BYOKMode == consts.BYOKBillingModeServiceFee {
		gateway = multiplyModelPrices(reference, billing.BYOKServiceFeeRatio)
	}
	return OfferPricing{
		ReferencePrice: reference, GatewayCharge: gateway,
		EstimatedTotal: addModelPrices(reference, gateway), Accuracy: AccuracyReference,
	}
}

func multiplyModelPrices(prices ModelPrices, ratio float64) ModelPrices {
	return ModelPrices{
		Input: prices.Input * ratio, Output: prices.Output * ratio,
		CacheRead: prices.CacheRead * ratio, CacheWrite: prices.CacheWrite * ratio,
	}
}

func addModelPrices(left, right ModelPrices) ModelPrices {
	return ModelPrices{
		Input: left.Input + right.Input, Output: left.Output + right.Output,
		CacheRead: left.CacheRead + right.CacheRead, CacheWrite: left.CacheWrite + right.CacheWrite,
	}
}
