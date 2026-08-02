package model

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/pricing"
)

type modelPriceUpdateWriter func(*PricingValues, float64)

var modelPriceUpdateWriters = map[string]modelPriceUpdateWriter{
	"input_price":       func(prices *PricingValues, value float64) { prices.InputPrice = value },
	"output_price":      func(prices *PricingValues, value float64) { prices.OutputPrice = value },
	"cache_read_price":  func(prices *PricingValues, value float64) { prices.CacheReadPrice = value },
	"cache_write_price": func(prices *PricingValues, value float64) { prices.CacheWritePrice = value },
}

func validateModelPriceBuckets(prices PricingValues) error {
	return pricing.ValidateModelPrices(
		prices.InputPrice,
		prices.OutputPrice,
		prices.CacheReadPrice,
		prices.CacheWritePrice,
	)
}

func modelPriceBucketsAfterUpdates(existing models.ModelConfig, updates map[string]any) PricingValues {
	prices := PricingValues{
		InputPrice: existing.InputPrice, OutputPrice: existing.OutputPrice,
		CacheReadPrice: existing.CacheReadPrice, CacheWritePrice: existing.CacheWritePrice,
	}
	for field, write := range modelPriceUpdateWriters {
		value, present := updates[field]
		if !present {
			continue
		}
		write(&prices, value.(float64))
	}
	return prices
}

func validatePricingUpdates(updates []PricingUpdate) error {
	for _, update := range updates {
		prices := PricingValues{
			InputPrice: update.InputPrice, OutputPrice: update.OutputPrice,
			CacheReadPrice: update.CacheReadPrice, CacheWritePrice: update.CacheWritePrice,
		}
		if err := validateModelPriceBuckets(prices); err != nil {
			return fmt.Errorf("model %d: %w", update.ModelID, err)
		}
	}
	return nil
}
